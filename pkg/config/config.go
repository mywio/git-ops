package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Token          string
	Users          []string
	Topic          string
	TargetDir      string
	Interval       time.Duration
	GlobalHooksDir string
	DryRun         bool
	SecretsDir     string // Directory to look for secret files
}

func LoadConfig() Config {
	interval, _ := time.ParseDuration(os.Getenv("SYNC_INTERVAL"))
	if interval == 0 {
		interval = 5 * time.Minute
	}

	usersStr := os.Getenv("GITHUB_USERS") // Expect comma-separated: "user1,org2,user3"
	users := strings.Split(usersStr, ",")
	for i := range users {
		users[i] = strings.TrimSpace(users[i])
	}

	return Config{
		Token:          os.Getenv("GITHUB_TOKEN"),
		Users:          users,
		Topic:          os.Getenv("TOPIC_FILTER"),
		TargetDir:      os.Getenv("TARGET_DIR"),
		Interval:       interval,
		DryRun:         os.Getenv("DRY_RUN") == "true",
		GlobalHooksDir: os.Getenv("GLOBAL_HOOKS_DIR"),
		SecretsDir:     os.Getenv("SECRETS_DIR"),
	}
}

// ConfigMap is a sectioned configuration map keyed by plugin name (or "core").
// Values are YAML-friendly scalars or nested maps/lists.
type ConfigMap map[string]map[string]any

// LoadConfigFile loads a YAML config file from disk.
// Returns an empty map if the file does not exist or is empty.
func LoadConfigFile(path string) (ConfigMap, error) {
	if path == "" {
		return ConfigMap{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ConfigMap{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return ConfigMap{}, nil
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	return normalizeConfigMap(raw), nil
}

// LoadConfigMapFromEnv builds a sectioned config map from environment variables.
// This allows config-file values to override env values without losing defaults.
func LoadConfigMapFromEnv() ConfigMap {
	cfg := ConfigMap{
		"core": {
			"token":            os.Getenv("GITHUB_TOKEN"),
			"users":            os.Getenv("GITHUB_USERS"),
			"topic":            os.Getenv("TOPIC_FILTER"),
			"target_dir":       os.Getenv("TARGET_DIR"),
			"interval":         os.Getenv("SYNC_INTERVAL"),
			"dry_run":          os.Getenv("DRY_RUN"),
			"global_hooks_dir": os.Getenv("GLOBAL_HOOKS_DIR"),
			"secrets_dir":      os.Getenv("SECRETS_DIR"),
			"plugins_dir":      os.Getenv("PLUGINS_DIR"),
		},
		"pushover": {
			"token": os.Getenv("NOTIFY_PUSHOVER_TOKEN"),
			"user":  os.Getenv("NOTIFY_PUSHOVER_USER"),
		},
		"webhook": {
			"url": os.Getenv("NOTIFY_WEBHOOK_URL"),
		},
		"webhook_trigger": {
			"port":  os.Getenv("WEBHOOK_PORT"),
			"token": os.Getenv("WEBHOOK_TOKEN"),
		},
		"mcp": {
			"api_key":    os.Getenv("MCP_API_KEY"),
			"target_dir": os.Getenv("TARGET_DIR"),
		},
		"google_secret_manager": {
			"project_id": os.Getenv("GOOGLE_CLOUD_PROJECT"),
		},
	}
	if v := os.Getenv("NOTIFY_PUSHOVER_EVENTS"); v != "" {
		cfg["pushover"]["subscribe"] = v
	}
	if v := os.Getenv("NOTIFY_WEBHOOK_EVENTS"); v != "" {
		cfg["webhook"]["subscribe"] = v
	}
	return cfg
}

// LoadConfigFromMap builds a core Config from a map.
// Supported keys (yaml): token, users, topic, target_dir, interval, dry_run, global_hooks_dir, secrets_dir.
func LoadConfigFromMap(m map[string]any) Config {
	cfg := Config{}

	if v, ok := getString(m, "token", "github_token"); ok {
		cfg.Token = v
	}
	if v, ok := getStringSlice(m, "users", "github_users"); ok {
		cfg.Users = v
	}
	if v, ok := getString(m, "topic", "topic_filter"); ok {
		cfg.Topic = v
	}
	if v, ok := getString(m, "target_dir"); ok {
		cfg.TargetDir = v
	}
	if v, ok := getDuration(m, "interval", "sync_interval"); ok {
		cfg.Interval = v
	}
	if v, ok := getBool(m, "dry_run"); ok {
		cfg.DryRun = v
	}
	if v, ok := getString(m, "global_hooks_dir"); ok {
		cfg.GlobalHooksDir = v
	}
	if v, ok := getString(m, "secrets_dir"); ok {
		cfg.SecretsDir = v
	}

	if cfg.Interval == 0 {
		cfg.Interval = 5 * time.Minute
	}

	return cfg
}

// MergeConfig uses primary values when set, otherwise falls back.
func MergeConfig(primary, fallback Config) Config {
	out := primary
	if out.Token == "" {
		out.Token = fallback.Token
	}
	if len(out.Users) == 0 {
		out.Users = fallback.Users
	}
	if out.Topic == "" {
		out.Topic = fallback.Topic
	}
	if out.TargetDir == "" {
		out.TargetDir = fallback.TargetDir
	}
	if out.Interval == 0 {
		out.Interval = fallback.Interval
	}
	if out.GlobalHooksDir == "" {
		out.GlobalHooksDir = fallback.GlobalHooksDir
	}
	if out.SecretsDir == "" {
		out.SecretsDir = fallback.SecretsDir
	}
	if !out.DryRun && fallback.DryRun {
		out.DryRun = true
	}
	return out
}

// MergeConfigMap merges primary over fallback (primary wins).
func MergeConfigMap(primary, fallback ConfigMap) ConfigMap {
	out := cloneConfigMap(fallback)
	for section, vals := range primary {
		if len(vals) == 0 {
			continue
		}
		merged := map[string]any{}
		if existing, ok := out[section]; ok {
			for k, v := range existing {
				merged[k] = v
			}
		}
		for k, v := range vals {
			merged[k] = v
		}
		out[section] = merged
	}
	return out
}

func cloneConfigMap(src ConfigMap) ConfigMap {
	dst := ConfigMap{}
	for section, vals := range src {
		sectionCopy := map[string]any{}
		for k, v := range vals {
			sectionCopy[k] = v
		}
		dst[section] = sectionCopy
	}
	return dst
}

func normalizeConfigMap(raw map[string]any) ConfigMap {
	out := ConfigMap{}
	for key, value := range raw {
		if m := normalizeStringMap(value); m != nil {
			out[key] = m
		}
	}
	return out
}

func normalizeStringMap(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range t {
			out[k] = normalizeValue(v)
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for k, v := range t {
			ks, ok := k.(string)
			if !ok {
				continue
			}
			out[ks] = normalizeValue(v)
		}
		return out
	default:
		return nil
	}
}

func normalizeValue(v any) any {
	switch t := v.(type) {
	case map[string]any, map[any]any:
		return normalizeStringMap(t)
	case []any:
		out := make([]any, 0, len(t))
		for _, item := range t {
			out = append(out, normalizeValue(item))
		}
		return out
	default:
		return v
	}
}

func getString(m map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch t := v.(type) {
			case string:
				return t, true
			default:
				return strings.TrimSpace(fmt.Sprint(t)), true
			}
		}
	}
	return "", false
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func getBool(m map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch t := v.(type) {
			case bool:
				return t, true
			case string:
				return strings.EqualFold(strings.TrimSpace(t), "true"), true
			case int:
				return t != 0, true
			case int64:
				return t != 0, true
			case float64:
				return t != 0, true
			}
		}
	}
	return false, false
}

func getDuration(m map[string]any, keys ...string) (time.Duration, bool) {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch t := v.(type) {
			case time.Duration:
				return t, true
			case string:
				d, err := time.ParseDuration(strings.TrimSpace(t))
				if err == nil {
					return d, true
				}
			case int:
				return time.Duration(t) * time.Second, true
			case int64:
				return time.Duration(t) * time.Second, true
			case float64:
				return time.Duration(t) * time.Second, true
			}
		}
	}
	return 0, false
}

func getStringSlice(m map[string]any, keys ...string) ([]string, bool) {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch t := v.(type) {
			case []any:
				out := make([]string, 0, len(t))
				for _, item := range t {
					out = append(out, strings.TrimSpace(toString(item)))
				}
				return out, true
			case []string:
				return t, true
			case string:
				parts := strings.Split(t, ",")
				for i := range parts {
					parts[i] = strings.TrimSpace(parts[i])
				}
				return parts, true
			}
		}
	}
	return nil, false
}
