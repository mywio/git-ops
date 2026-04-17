package main

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/google/go-github/v57/github"
	"github.com/mywio/git-ops/pkg/core"
	"gopkg.in/yaml.v3"
)

type composeEnvPersistenceRisk struct {
	Service string
	Key     string
	Reason  string
}

var composeEnvReferencePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)[^}]*\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

func scanComposeEnvPersistenceRisks(composeContent string, composeEnv []string) ([]composeEnvPersistenceRisk, error) {
	forwardedKeys := forwardedComposeEnvKeys(composeEnv)
	if len(forwardedKeys) == 0 || strings.TrimSpace(composeContent) == "" {
		return nil, nil
	}

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(composeContent), &root); err != nil {
		return nil, fmt.Errorf("parse compose yaml: %w", err)
	}

	doc := firstContentNode(&root)
	if doc == nil || doc.Kind != yaml.MappingNode {
		return nil, nil
	}

	servicesNode := mappingValue(doc, "services")
	if servicesNode == nil || servicesNode.Kind != yaml.MappingNode {
		return nil, nil
	}

	riskSet := map[string]composeEnvPersistenceRisk{}
	for i := 0; i+1 < len(servicesNode.Content); i += 2 {
		serviceName := strings.TrimSpace(servicesNode.Content[i].Value)
		serviceNode := servicesNode.Content[i+1]
		if serviceName == "" || serviceNode == nil {
			continue
		}

		allRefs := collectComposeEnvRefs(serviceNode)
		if len(allRefs) == 0 {
			continue
		}
		safeRefs := collectServiceRuntimeEnvRefs(serviceNode)

		for _, key := range forwardedKeys {
			if _, referenced := allRefs[key]; !referenced {
				continue
			}
			if _, safe := safeRefs[key]; safe {
				continue
			}
			riskKey := serviceName + "\x00" + key
			riskSet[riskKey] = composeEnvPersistenceRisk{
				Service: serviceName,
				Key:     key,
				Reason:  "referenced in compose but not mapped into the service runtime environment",
			}
		}
	}

	if len(riskSet) == 0 {
		return nil, nil
	}

	risks := make([]composeEnvPersistenceRisk, 0, len(riskSet))
	for _, risk := range riskSet {
		risks = append(risks, risk)
	}
	sort.Slice(risks, func(i, j int) bool {
		if risks[i].Service == risks[j].Service {
			return risks[i].Key < risks[j].Key
		}
		return risks[i].Service < risks[j].Service
	})
	return risks, nil
}

func forwardedComposeEnvKeys(composeEnv []string) []string {
	keys := make([]string, 0, len(composeEnv))
	for _, entry := range composeEnv {
		key, _, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if ok && key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func collectComposeEnvRefs(node *yaml.Node) map[string]struct{} {
	refs := map[string]struct{}{}
	collectScalarEnvRefs(node, refs)
	return refs
}

func collectScalarEnvRefs(node *yaml.Node, refs map[string]struct{}) {
	if node == nil {
		return
	}

	if node.Kind == yaml.ScalarNode && node.Tag != "!!null" {
		for _, match := range composeEnvReferencePattern.FindAllStringSubmatch(node.Value, -1) {
			key := strings.TrimSpace(match[1])
			if key == "" {
				key = strings.TrimSpace(match[2])
			}
			if key != "" {
				refs[key] = struct{}{}
			}
		}
	}

	for _, child := range node.Content {
		collectScalarEnvRefs(child, refs)
	}
}

func collectServiceRuntimeEnvRefs(serviceNode *yaml.Node) map[string]struct{} {
	refs := map[string]struct{}{}
	envNode := mappingValue(serviceNode, "environment")
	if envNode == nil {
		return refs
	}

	switch envNode.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(envNode.Content); i += 2 {
			envKey := strings.TrimSpace(envNode.Content[i].Value)
			envValue := envNode.Content[i+1]
			if envKey == "" {
				continue
			}
			if envValue == nil || envValue.Tag == "!!null" || strings.TrimSpace(envValue.Value) == "" {
				refs[envKey] = struct{}{}
			}
			collectScalarEnvRefs(envValue, refs)
		}
	case yaml.SequenceNode:
		for _, item := range envNode.Content {
			entry := strings.TrimSpace(item.Value)
			if entry == "" {
				continue
			}
			if !strings.Contains(entry, "=") {
				refs[entry] = struct{}{}
				continue
			}
			_, rhs, _ := strings.Cut(entry, "=")
			for _, match := range composeEnvReferencePattern.FindAllStringSubmatch(rhs, -1) {
				key := strings.TrimSpace(match[1])
				if key == "" {
					key = strings.TrimSpace(match[2])
				}
				if key != "" {
					refs[key] = struct{}{}
				}
			}
		}
	}

	return refs
}

func firstContentNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0]
	}
	return node
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func (r *Reconciler) warnOnComposeEnvPersistenceRisks(ctx context.Context, repo *github.Repository, composeContent string, composeEnv []string, logger *slog.Logger) {
	if repo == nil || repo.Owner == nil || repo.Owner.Login == nil || repo.Name == nil {
		return
	}

	risks, err := scanComposeEnvPersistenceRisks(composeContent, composeEnv)
	if err != nil {
		logger.Warn("Failed to scan compose env persistence risks", "error", err)
		return
	}
	if len(risks) == 0 {
		return
	}

	services := make([]string, 0, len(risks))
	keys := make([]string, 0, len(risks))
	serviceSeen := map[string]struct{}{}
	keySeen := map[string]struct{}{}
	findings := make([]map[string]any, 0, len(risks))
	for _, risk := range risks {
		findings = append(findings, map[string]any{
			"service": risk.Service,
			"key":     risk.Key,
			"reason":  risk.Reason,
		})
		if _, ok := serviceSeen[risk.Service]; !ok {
			serviceSeen[risk.Service] = struct{}{}
			services = append(services, risk.Service)
		}
		if _, ok := keySeen[risk.Key]; !ok {
			keySeen[risk.Key] = struct{}{}
			keys = append(keys, risk.Key)
		}
	}
	sort.Strings(services)
	sort.Strings(keys)

	fullName := fmt.Sprintf("%s/%s", *repo.Owner.Login, *repo.Name)
	message := fmt.Sprintf("Compose env persistence risk detected for %s: %d finding(s) across %d service(s)", fullName, len(risks), len(services))
	logger.Warn(message, "services", services, "keys", keys)
	r.publish(ctx, core.InternalEvent{
		Type:   "notify_compose_env_persistence_risk",
		Source: "reconciler",
		Repo:   *repo.Name,
		Message: message,
		Details: map[string]any{
			"owner":      *repo.Owner.Login,
			"repo":       *repo.Name,
			"full_name":  fullName,
			"services":   services,
			"keys":       keys,
			"risk_count": len(risks),
			"findings":   findings,
		},
	})
}
