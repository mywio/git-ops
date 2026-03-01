package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/config"
	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
)

func TestPushoverIntegration_FromEnv(t *testing.T) {
	token := os.Getenv("NOTIFY_PUSHOVER_TOKEN")
	user := os.Getenv("NOTIFY_PUSHOVER_USER")
	if token == "" || user == "" {
		t.Skip("NOTIFY_PUSHOVER_TOKEN/NOTIFY_PUSHOVER_USER not set; skipping Pushover integration test")
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	mgr := core.NewModuleManager(logger)
	mgr.SetConfig(config.LoadConfigMapFromEnv())

	p := &PushoverNotifier{}
	err := p.Init(context.Background(), logger, mgr)
	assert.NoError(t, err)
	assert.True(t, p.enabled)
	assert.Equal(t, token, p.token.Value)
	assert.Equal(t, user, p.user)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err = p.send(ctx, core.InternalEvent{
		Type:   "notify_integration_test",
		Source: "test",
		Repo:   "repo",
		String: "hello from integration test",
		Details: map[string]interface{}{
			"key": "value",
		},
	})
	assert.NoError(t, err)
}
