package main

import (
	"bytes"
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

const actionsManifestPath = ".ghops/actions.yaml"

type actionsManifest struct {
	Version     int                    `yaml:"version" json:"version"`
	Deployments []actionDeploymentSpec `yaml:"deployments" json:"deployments"`
}

type actionDeploymentSpec struct {
	ID           string   `yaml:"id" json:"id"`
	Name         string   `yaml:"name" json:"name"`
	Workflow     string   `yaml:"workflow" json:"workflow"`
	Environment  string   `yaml:"environment" json:"environment"`
	RunnerLabels []string `yaml:"runner_labels" json:"runner_labels"`
}

func parseActionsManifest(data []byte) (actionsManifest, error) {
	var manifest actionsManifest
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("decode %s: %w", actionsManifestPath, err)
	}
	if manifest.Version != 1 {
		return manifest, fmt.Errorf("unsupported actions manifest version %d", manifest.Version)
	}
	if len(manifest.Deployments) == 0 {
		return manifest, fmt.Errorf("actions manifest requires deployments")
	}
	ids := map[string]bool{}
	for i := range manifest.Deployments {
		deployment := &manifest.Deployments[i]
		deployment.ID, deployment.Name, deployment.Workflow = strings.TrimSpace(deployment.ID), strings.TrimSpace(deployment.Name), strings.TrimSpace(deployment.Workflow)
		if deployment.ID == "" || ids[deployment.ID] {
			return manifest, fmt.Errorf("deployment ids must be non-empty and unique")
		}
		ids[deployment.ID] = true
		clean := path.Clean(deployment.Workflow)
		if clean != deployment.Workflow || !strings.HasPrefix(clean, ".github/workflows/") ||
			(!strings.HasSuffix(clean, ".yml") && !strings.HasSuffix(clean, ".yaml")) {
			return manifest, fmt.Errorf("deployment %s has invalid workflow path", deployment.ID)
		}
		deployment.RunnerLabels = cleanStrings(deployment.RunnerLabels)
		if len(deployment.RunnerLabels) == 0 {
			return manifest, fmt.Errorf("deployment %s requires runner_labels", deployment.ID)
		}
	}
	return manifest, nil
}

func cleanStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
