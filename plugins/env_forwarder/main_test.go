package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvForwarderPlugin_AllowsKeysAndPrefixes(t *testing.T) {
	t.Setenv("FOO", "bar")
	t.Setenv("APP_TOKEN", "abc123")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mgr := core.NewModuleManager(logger)
	mgr.SetConfig(map[string]map[string]any{
		"env_forwarder": {
			"keys":     []string{"FOO", "MISSING"},
			"prefixes": []string{"APP_"},
		},
	})

	p := &EnvForwarderPlugin{}
	err := p.Init(context.Background(), logger, mgr)
	assert.NoError(t, err)

	res, err := p.Execute(context.Background(), "get_secrets", map[string]interface{}{})
	assert.NoError(t, err)

	secrets, ok := res.(map[string]string)
	assert.True(t, ok)
	assert.Equal(t, "bar", secrets["FOO"])
	assert.Equal(t, "abc123", secrets["APP_TOKEN"])
	_, exists := secrets["MISSING"]
	assert.False(t, exists)

	statsRes, err := p.Execute(context.Background(), "get_stats", map[string]interface{}{})
	assert.NoError(t, err)
	stats, ok := statsRes.(envForwarderStatsView)
	assert.True(t, ok)
	assert.Equal(t, 2, stats.ConfiguredKeys)
	assert.Equal(t, 1, stats.ConfiguredPrefixes)
	assert.Equal(t, 2, stats.ForwardedTotal)
	assert.Equal(t, 1, stats.ForwardedFromKeys)
	assert.Equal(t, 1, stats.ForwardedFromPrefixes)
	assert.Equal(t, 1, stats.PrefixMatches["APP_"])
	assert.Equal(t, 1, stats.PrefixForwarded["APP_"])
	assert.Contains(t, stats.MissingKeys, "MISSING")
}

func TestEnvForwarderPlugin_DisabledWithoutConfig(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mgr := core.NewModuleManager(logger)

	p := &EnvForwarderPlugin{}
	err := p.Init(context.Background(), logger, mgr)
	assert.NoError(t, err)

	res, err := p.Execute(context.Background(), "get_secrets", map[string]interface{}{})
	assert.NoError(t, err)

	secrets, ok := res.(map[string]string)
	assert.True(t, ok)
	assert.Len(t, secrets, 0)
}

func TestEnvForwarderPlugin_UnknownAction(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	p := &EnvForwarderPlugin{}
	err := p.Init(context.Background(), logger, nil)
	assert.NoError(t, err)

	_, err = p.Execute(context.Background(), "nope", map[string]interface{}{})
	assert.Error(t, err)
}

func TestEnvForwarderPlugin_PersistsSnapshotAcrossRestart(t *testing.T) {
	stateDir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	buildManager := func() *core.ModuleManager {
		mgr := core.NewModuleManager(logger)
		mgr.SetConfig(map[string]map[string]any{
			"core": {
				"target_dir": stateDir,
			},
			"env_forwarder": {
				"keys":     []string{"FOO"},
				"prefixes": []string{"APP_"},
			},
		})
		return mgr
	}

	t.Setenv("FOO", "bar")
	t.Setenv("APP_TOKEN", "abc123")

	first := &EnvForwarderPlugin{}
	require.NoError(t, first.Init(context.Background(), logger, buildManager()))

	firstRes, err := first.Execute(context.Background(), "get_secrets", map[string]interface{}{})
	require.NoError(t, err)
	firstSecrets := firstRes.(map[string]string)
	assert.Equal(t, "bar", firstSecrets["FOO"])
	assert.Equal(t, "abc123", firstSecrets["APP_TOKEN"])

	require.NoError(t, os.Unsetenv("FOO"))
	require.NoError(t, os.Unsetenv("APP_TOKEN"))

	second := &EnvForwarderPlugin{}
	require.NoError(t, second.Init(context.Background(), logger, buildManager()))

	secondRes, err := second.Execute(context.Background(), "get_secrets", map[string]interface{}{})
	require.NoError(t, err)
	secondSecrets := secondRes.(map[string]string)
	assert.Equal(t, "bar", secondSecrets["FOO"])
	assert.Equal(t, "abc123", secondSecrets["APP_TOKEN"])

	data, err := os.ReadFile(filepath.Join(stateDir, ".git-ops", "env_forwarder_snapshot.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "\"FOO\":\"bar\"")
	assert.Contains(t, string(data), "\"APP_TOKEN\":\"abc123\"")
}
