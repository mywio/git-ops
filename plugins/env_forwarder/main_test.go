package main

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
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

	res, err := p.Execute("get_secrets", map[string]interface{}{})
	assert.NoError(t, err)

	secrets, ok := res.(map[string]string)
	assert.True(t, ok)
	assert.Equal(t, "bar", secrets["FOO"])
	assert.Equal(t, "abc123", secrets["APP_TOKEN"])
	_, exists := secrets["MISSING"]
	assert.False(t, exists)
}

func TestEnvForwarderPlugin_DisabledWithoutConfig(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mgr := core.NewModuleManager(logger)

	p := &EnvForwarderPlugin{}
	err := p.Init(context.Background(), logger, mgr)
	assert.NoError(t, err)

	res, err := p.Execute("get_secrets", map[string]interface{}{})
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

	_, err = p.Execute("nope", map[string]interface{}{})
	assert.Error(t, err)
}
