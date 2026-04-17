package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

type mockPlugin struct {
	MockModule
	capabilities []Capability
	status       ServiceStatus
}

func (m *mockPlugin) Description() string { return "mock plugin" }
func (m *mockPlugin) Capabilities() []Capability {
	return m.capabilities
}
func (m *mockPlugin) Status() ServiceStatus { return m.status }
func (m *mockPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	return nil, nil
}

func TestModuleManager(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
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

func TestModuleManagerStartLogsActivePlugins(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuffer, nil))
	mgr := NewModuleManager(logger)
	plug := &mockPlugin{
		MockModule:   MockModule{name: "test-plugin"},
		capabilities: []Capability{CapabilityAPI, CapabilityNotifier},
		status:       StatusHealthy,
	}
	mgr.Register(plug)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	decoder := json.NewDecoder(bytes.NewReader(logBuffer.Bytes()))
	found := false
	for decoder.More() {
		var entry map[string]any
		require.NoError(t, decoder.Decode(&entry))
		if entry["msg"] != "Active plugin" {
			continue
		}

		assert.Equal(t, "test-plugin", entry["name"])
		assert.Equal(t, string(StatusHealthy), entry["status"])
		assert.Equal(t, []any{string(CapabilityAPI), string(CapabilityNotifier)}, entry["capabilities"])
		found = true
	}

	assert.True(t, found, "expected active plugin log entry")
}

func TestModuleManagerBrokerIsInstanceScoped(t *testing.T) {
	mgr1 := NewModuleManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	mgr2 := NewModuleManager(slog.New(slog.NewTextHandler(io.Discard, nil)))

	var fired1 int32
	var fired2 int32

	mgr1.Subscribe("test_event", func(_ context.Context, _ InternalEvent) {
		atomic.AddInt32(&fired1, 1)
	})
	mgr2.Subscribe("test_event", func(_ context.Context, _ InternalEvent) {
		atomic.AddInt32(&fired2, 1)
	})

	mgr1.Publish(context.Background(), InternalEvent{Type: "test_event"})
	time.Sleep(50 * time.Millisecond)

	assert.EqualValues(t, 1, atomic.LoadInt32(&fired1))
	assert.EqualValues(t, 0, atomic.LoadInt32(&fired2), "mgr2 should not receive mgr1 events")
}
