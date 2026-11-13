package config

import (
	"strings"
	"testing"
)

func TestLoadConfigMapFromEnvParsesUITrustAuthHeader(t *testing.T) {
	t.Setenv("UI_TRUST_AUTH_HEADER", "true")

	cfg := LoadConfigMapFromEnv()

	value, ok := cfg["ui"]["trust_auth_header"].(bool)
	if !ok || !value {
		t.Fatalf("expected boolean true, got %#v", cfg["ui"]["trust_auth_header"])
	}
}

func TestLoadConfigMapFromEnvOmitsEmptyUITrustAuthHeader(t *testing.T) {
	t.Setenv("UI_TRUST_AUTH_HEADER", "")

	cfg := LoadConfigMapFromEnv()

	if _, ok := cfg["ui"]["trust_auth_header"]; ok {
		t.Fatal("expected empty UI_TRUST_AUTH_HEADER to be omitted")
	}
}

func FuzzSplitAndTrim(f *testing.F) {
	for _, seed := range []string{
		"",
		"alpha",
		"alpha,beta",
		" alpha , beta ,, gamma ",
		",,,",
		"one, two,three , four",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		out := splitAndTrim(input)
		for _, entry := range out {
			if entry == "" {
				t.Fatalf("splitAndTrim returned empty entry for %q", input)
			}
			if entry != strings.TrimSpace(entry) {
				t.Fatalf("splitAndTrim returned untrimmed entry %q for %q", entry, input)
			}
		}
	})
}

func FuzzLoadConfigFromMap(f *testing.F) {
	f.Add("token", "user1,user2", "topic1,topic2", "/tmp/stacks", "5m", "30s", "true")
	f.Add("", "", "", "", "", "", "")
	f.Add("tok", " user ", " topic ", ".", "not-a-duration", "also-bad", "false")

	f.Fuzz(func(t *testing.T, token, users, topics, targetDir, interval, hookTimeout, dryRun string) {
		cfg := LoadConfigFromMap(map[string]any{
			"token":        token,
			"users":        users,
			"topic":        topics,
			"target_dir":   targetDir,
			"interval":     interval,
			"hook_timeout": hookTimeout,
			"dry_run":      dryRun,
		})

		if cfg.Interval <= 0 {
			t.Fatalf("expected positive interval, got %s", cfg.Interval)
		}
		if cfg.HookTimeout <= 0 {
			t.Fatalf("expected positive hook timeout, got %s", cfg.HookTimeout)
		}
		for _, user := range cfg.Users {
			if user != strings.TrimSpace(user) {
				t.Fatalf("expected trimmed user entry, got %q", user)
			}
		}
		for _, topic := range cfg.Topics {
			if topic != strings.TrimSpace(topic) {
				t.Fatalf("expected trimmed topic entry, got %q", topic)
			}
		}
	})
}
