package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStore(t *testing.T) {
	store := newMemoryStore()
	defer store.Close()

	runStoreTests(t, store)
}

func TestSQLiteStore(t *testing.T) {
	dbPath := "test_audit.db"
	os.Remove(dbPath)
	defer os.Remove(dbPath)

	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	runStoreTests(t, store)
}

func runStoreTests(t *testing.T, store AuditStore) {
	now := time.Now()

	events := []core.InternalEvent{
		{Type: "deploy_start", Timestamp: now.Add(-10 * time.Minute), Source: "github", Repo: "repo1"},
		{Type: "deploy_success", Timestamp: now.Add(-5 * time.Minute), Source: "github", Repo: "repo1"},
		{Type: "reconcile_start", Timestamp: now.Add(-1 * time.Minute), Source: "timer", Repo: "repo2"},
	}

	for _, ev := range events {
		err := store.Save(ev)
		require.NoError(t, err)
	}

	// Test GetLastEvents default (no filter, desc order)
	res, err := store.GetLastEvents(nil, 10, 0, "desc")
	require.NoError(t, err)
	require.Len(t, res, 3)
	assert.Equal(t, core.EventTypeName("reconcile_start"), res[0].Type)
	assert.Equal(t, core.EventTypeName("deploy_start"), res[2].Type)

	// Test asc order
	resAsc, err := store.GetLastEvents(nil, 10, 0, "asc")
	require.NoError(t, err)
	require.Len(t, resAsc, 3)
	assert.Equal(t, core.EventTypeName("deploy_start"), resAsc[0].Type)
	assert.Equal(t, core.EventTypeName("reconcile_start"), resAsc[2].Type)

	// Test filter
	filter := map[string]any{"repo": "repo1"}
	resFilter, err := store.GetLastEvents(filter, 10, 0, "desc")
	require.NoError(t, err)
	require.Len(t, resFilter, 2)
	assert.Equal(t, core.EventTypeName("deploy_success"), resFilter[0].Type)

	// Test Limit and Offset
	resLim, err := store.GetLastEvents(nil, 2, 1, "desc")
	require.NoError(t, err)
	require.Len(t, resLim, 2)
	assert.Equal(t, core.EventTypeName("deploy_success"), resLim[0].Type)
	assert.Equal(t, core.EventTypeName("deploy_start"), resLim[1].Type)

	// Test Cleanup
	err = store.Cleanup(2)
	require.NoError(t, err)

	resClean, err := store.GetLastEvents(nil, 10, 0, "desc")
	require.NoError(t, err)
	require.Len(t, resClean, 2)
	// The oldest one (deploy_start) should be deleted
	assert.Equal(t, core.EventTypeName("reconcile_start"), resClean[0].Type)
	assert.Equal(t, core.EventTypeName("deploy_success"), resClean[1].Type)
}

type mockRegistry struct {
	config map[string]map[string]any
	subs   map[string]core.Listener
}

func (m *mockRegistry) GetPlugin(name string) (core.Plugin, error)                 { return nil, nil }
func (m *mockRegistry) GetPluginsWithCapability(cap core.Capability) []core.Plugin { return nil }
func (m *mockRegistry) ListPlugins() []core.Plugin                                 { return nil }
func (m *mockRegistry) RegisterEventType(desc core.EventTypeDesc) error            { return nil }
func (m *mockRegistry) GetMuxServer() *http.ServeMux                               { return nil }
func (m *mockRegistry) Subscribe(pattern string, handler core.Listener) {
	if m.subs == nil {
		m.subs = make(map[string]core.Listener)
	}
	m.subs[pattern] = handler
}
func (m *mockRegistry) GetHTTPClient() *http.Client { return nil }
func (m *mockRegistry) GetConfig() map[string]map[string]any {
	return m.config
}

func TestAuditPlugin(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	registry := &mockRegistry{
		config: map[string]map[string]any{
			"audit": {
				"storage":         "memory",
				"retention_count": 2,
			},
		},
	}

	p := &AuditPlugin{}
	err := p.Init(context.Background(), logger, registry)
	require.NoError(t, err)

	err = p.Start(context.Background())
	require.NoError(t, err)
	require.NotNil(t, registry.subs["*"])

	handler := registry.subs["*"]

	// Publish 3 events, retention is 2
	handler(context.Background(), core.InternalEvent{Type: "e1", Timestamp: time.Now().Add(-2 * time.Minute)})
	handler(context.Background(), core.InternalEvent{Type: "e2", Timestamp: time.Now().Add(-1 * time.Minute)})
	handler(context.Background(), core.InternalEvent{Type: "e3", Timestamp: time.Now()})

	// Check Execute
	res, err := p.Execute(context.Background(), "last_events", map[string]interface{}{
		"limit": 10,
	})
	require.NoError(t, err)

	events := res.([]core.InternalEvent)
	require.Len(t, events, 2)

	// Default order is desc, so newest first.
	// e1 should be deleted.
	assert.Equal(t, core.EventTypeName("e3"), events[0].Type)
	assert.Equal(t, core.EventTypeName("e2"), events[1].Type)
}
