package main

import (
	"fmt"
	"strconv"
	"strings"
)

func normalizeLocalDeploySection(section map[string]any) (map[string]any, error) {
	normalized := make(map[string]any, len(section))
	for key, value := range section {
		if text, ok := value.(string); ok {
			text = strings.TrimSpace(text)
			if text == "" && knownLocalDeployConfigKey(key) {
				continue
			}
			switch key {
			case "enabled", "require_systemd":
				parsed, err := strconv.ParseBool(text)
				if err != nil {
					return nil, fmt.Errorf("local_deploy.%s: %w", key, err)
				}
				value = parsed
			case "global_concurrency", "checkout_retention", "execution_retention", "successful_retention":
				parsed, err := strconv.Atoi(text)
				if err != nil {
					return nil, fmt.Errorf("local_deploy.%s: %w", key, err)
				}
				value = parsed
			case "allowed_environment":
				value = strings.Split(text, ",")
			}
		}
		normalized[key] = value
	}
	return normalized, nil
}

func knownLocalDeployConfigKey(key string) bool {
	switch key {
	case "enabled", "require_systemd", "state_dir", "cache_dir", "log_dir",
		"global_concurrency", "checkout_retention", "execution_retention",
		"successful_retention", "max_log_size", "allowed_environment":
		return true
	default:
		return false
	}
}
