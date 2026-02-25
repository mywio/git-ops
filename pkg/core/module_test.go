package core

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type MockModule struct {
	name        string
	initCalled  bool
	startCalled bool
	stopCalled  bool
}

func (m *MockModule) Name() string { return m.name }
func (m *MockModule) Init(ctx context.Context, l *slog.Logger, r PluginRegistry) error {
	m.initCalled = true
	return nil
}
func (m *MockModule) Start(ctx context.Context) error {
	m.startCalled = true
	return nil
}
func (m *MockModule) Stop(ctx context.Context) error {
	m.stopCalled = true
	return nil
}

func TestModuleManager(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mgr := NewModuleManager(logger)

	// Mock Module
	mock := &MockModule{name: "mock"}
	mgr.Register(mock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := mgr.Init(ctx)
	assert.NoError(t, err)
	assert.True(t, mock.initCalled)

	mgr.Start(ctx)
	// Wait a bit for goroutine
	time.Sleep(100 * time.Millisecond)
	assert.True(t, mock.startCalled)

	mgr.Stop(ctx)
	assert.True(t, mock.stopCalled)
}
