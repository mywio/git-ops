package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/config"
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

func TestLoadPluginsLoadsAllowedPlugin(t *testing.T) {
	var logBuffer bytes.Buffer
	mgr := NewModuleManager(slog.New(slog.NewJSONHandler(&logBuffer, nil)))
	mgr.SetConfig(map[string]map[string]any{
		"core": {"plugins": []string{"alpha"}},
	})

	dir := t.TempDir()
	writeFakePluginFile(t, dir, "alpha")

	require.NoError(t, mgr.LoadPlugins(dir))
	assertLogContains(t, logBuffer.Bytes(), "Plugin allowlist active")
	assertLogContains(t, logBuffer.Bytes(), "Loading plugin")
	assertLogFieldValue(t, logBuffer.Bytes(), "path", filepath.Join(dir, "alpha.so"))
}

func TestLoadPluginsSkipsPluginNotInAllowlist(t *testing.T) {
	var logBuffer bytes.Buffer
	mgr := NewModuleManager(slog.New(slog.NewJSONHandler(&logBuffer, nil)))
	mgr.SetConfig(map[string]map[string]any{
		"core": {"plugins": []string{"beta"}},
	})

	dir := t.TempDir()
	writeFakePluginFile(t, dir, "alpha")

	require.NoError(t, mgr.LoadPlugins(dir))
	assert.Empty(t, mgr.ListPlugins())
	assertLogContains(t, logBuffer.Bytes(), "Skipping plugin (not in allowlist)")
}

func TestLoadPluginsLoadsAllWhenAllowlistEmpty(t *testing.T) {
	var logBuffer bytes.Buffer
	mgr := NewModuleManager(slog.New(slog.NewJSONHandler(&logBuffer, nil)))
	mgr.SetConfig(map[string]map[string]any{
		"core": {"plugins": []string{}},
	})

	dir := t.TempDir()
	writeFakePluginFile(t, dir, "alpha")
	writeFakePluginFile(t, dir, "beta")

	require.NoError(t, mgr.LoadPlugins(dir))
	assertLogFieldValue(t, logBuffer.Bytes(), "path", filepath.Join(dir, "alpha.so"))
	assertLogFieldValue(t, logBuffer.Bytes(), "path", filepath.Join(dir, "beta.so"))
}

func TestLoadPluginsParsesAllowlistFromEnv(t *testing.T) {
	t.Setenv("PLUGINS_ALLOW", "alpha, beta")

	var logBuffer bytes.Buffer
	mgr := NewModuleManager(slog.New(slog.NewJSONHandler(&logBuffer, nil)))
	mgr.SetConfig(config.LoadConfigMapFromEnv())

	dir := t.TempDir()
	writeFakePluginFile(t, dir, "alpha")
	writeFakePluginFile(t, dir, "beta")
	writeFakePluginFile(t, dir, "gamma")

	require.NoError(t, mgr.LoadPlugins(dir))
	assertLogContains(t, logBuffer.Bytes(), "Plugin allowlist active")
	assertLogFieldValue(t, logBuffer.Bytes(), "path", filepath.Join(dir, "alpha.so"))
	assertLogFieldValue(t, logBuffer.Bytes(), "path", filepath.Join(dir, "beta.so"))
	assertLogFieldValue(t, logBuffer.Bytes(), "plugin", "gamma")
}

func assertLogContains(t *testing.T, data []byte, msg string) {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var entry map[string]any
		require.NoError(t, decoder.Decode(&entry))
		if entry["msg"] == msg {
			return
		}
	}

	t.Fatalf("log message %q not found", msg)
}

func assertLogFieldValue(t *testing.T, data []byte, key string, value any) {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var entry map[string]any
		require.NoError(t, decoder.Decode(&entry))
		if entry[key] == value {
			return
		}
	}

	t.Fatalf("log field %q with value %v not found", key, value)
}

func writeFakePluginFile(t *testing.T, dir, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".so"), []byte("not a plugin"), 0o644))
}

func TestPluginFromValueSupportsPointerToPluginPointer(t *testing.T) {
	plug := &mockPlugin{
		MockModule:   MockModule{name: "pointer-plugin"},
		capabilities: []Capability{CapabilityAPI},
		status:       StatusHealthy,
	}
	exported := plug

	resolved, ok := pluginFromValue(reflect.ValueOf(&exported))
	require.True(t, ok)
	require.NotNil(t, resolved)
	assert.Equal(t, "pointer-plugin", resolved.Name())
}
