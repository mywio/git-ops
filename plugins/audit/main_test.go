package main

import (
	"context"
	"database/sql"
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
	defer func() { require.NoError(t, store.Close()) }()

	runStoreTests(t, store)
}

func TestSQLiteStore(t *testing.T) {
	dbPath := "test_audit.db"
	_ = os.Remove(dbPath)
	defer func() { _ = os.Remove(dbPath) }()

	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()

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
	res, err := store.GetLastEvents(nil, 10, 0, "desc", nil, nil)
	require.NoError(t, err)
	require.Len(t, res, 3)
	assert.Equal(t, core.EventTypeName("reconcile_start"), res[0].Type)
	assert.Equal(t, core.EventTypeName("deploy_start"), res[2].Type)

	// Test asc order
	resAsc, err := store.GetLastEvents(nil, 10, 0, "asc", nil, nil)
	require.NoError(t, err)
	require.Len(t, resAsc, 3)
	assert.Equal(t, core.EventTypeName("deploy_start"), resAsc[0].Type)
	assert.Equal(t, core.EventTypeName("reconcile_start"), resAsc[2].Type)

	// Test filter
	filter := map[string]any{"repo": "repo1"}
	resFilter, err := store.GetLastEvents(filter, 10, 0, "desc", nil, nil)
	require.NoError(t, err)
	require.Len(t, resFilter, 2)
	assert.Equal(t, core.EventTypeName("deploy_success"), resFilter[0].Type)

	// Test Limit and Offset
	resLim, err := store.GetLastEvents(nil, 2, 1, "desc", nil, nil)
	require.NoError(t, err)
	require.Len(t, resLim, 2)
	assert.Equal(t, core.EventTypeName("deploy_success"), resLim[0].Type)
	assert.Equal(t, core.EventTypeName("deploy_start"), resLim[1].Type)

	// Test since/until filters
	since := now.Add(-7 * time.Minute)
	until := now.Add(-2 * time.Minute)
	resSince, err := store.GetLastEvents(nil, 10, 0, "desc", &since, &until)
	require.NoError(t, err)
	require.Len(t, resSince, 1)
	assert.Equal(t, core.EventTypeName("deploy_success"), resSince[0].Type)

	// Test Cleanup
	err = store.Cleanup(2)
	require.NoError(t, err)

	resClean, err := store.GetLastEvents(nil, 10, 0, "desc", nil, nil)
	require.NoError(t, err)
	require.Len(t, resClean, 2)
	// The oldest one (deploy_start) should be deleted
	assert.Equal(t, core.EventTypeName("reconcile_start"), resClean[0].Type)
	assert.Equal(t, core.EventTypeName("deploy_success"), resClean[1].Type)
}

func TestStoreSupportsExecutionAwareQueries(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) AuditStore
	}{
		{
			name: "memory",
			setup: func(t *testing.T) AuditStore {
				t.Helper()
				return newMemoryStore()
			},
		},
		{
			name: "sqlite",
			setup: func(t *testing.T) AuditStore {
				t.Helper()
				dbPath := "test_audit_execution_filters.db"
				_ = os.Remove(dbPath)
				t.Cleanup(func() {
					_ = os.Remove(dbPath)
				})

				store, err := newSQLiteStore(dbPath)
				require.NoError(t, err)
				return store
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := tc.setup(t)
			defer func() { require.NoError(t, store.Close()) }()

			now := time.Now().UTC()
			events := []core.InternalEvent{
				{
					Type:      core.EventTypeExecution,
					Timestamp: now.Add(-3 * time.Minute),
					Source:    "reconciler",
					Repo:      "api",
					Details: map[string]any{
						"execution_id":  "exec-1",
						"full_name":     "acme/api",
						"stage":         string(core.ExecutionStageFetch),
						"status":        string(core.ExecutionStatusRunning),
						"last_error":    "fetch interrupted",
						"failure_class": "",
					},
				},
				{
					Type:      core.EventTypeExecution,
					Timestamp: now.Add(-2 * time.Minute),
					Source:    "reconciler",
					Repo:      "api",
					Details: map[string]any{
						"execution_id":  "exec-1",
						"full_name":     "acme/api",
						"stage":         string(core.ExecutionStageHooks),
						"status":        string(core.ExecutionStatusFailed),
						"last_error":    "hook failed",
						"failure_class": string(core.FailureClassValidation),
					},
				},
				{
					Type:      core.EventTypeExecution,
					Timestamp: now.Add(-1 * time.Minute),
					Source:    "reconciler",
					Repo:      "web",
					Details: map[string]any{
						"execution_id":  "exec-2",
						"full_name":     "acme/web",
						"stage":         string(core.ExecutionStageComplete),
						"status":        string(core.ExecutionStatusSucceeded),
						"last_error":    "",
						"failure_class": "",
					},
				},
			}

			for _, event := range events {
				err := store.Save(event)
				require.NoError(t, err)
			}

			resByExecution, err := store.GetLastEvents(map[string]any{"execution_id": "exec-1"}, 10, 0, "desc", nil, nil)
			require.NoError(t, err)
			require.Len(t, resByExecution, 2)

			resByStatus, err := store.GetLastEvents(map[string]any{"status": string(core.ExecutionStatusFailed)}, 10, 0, "desc", nil, nil)
			require.NoError(t, err)
			require.Len(t, resByStatus, 1)
			assert.Equal(t, "exec-1", resByStatus[0].Details["execution_id"])

			resByStage, err := store.GetLastEvents(map[string]any{"stage": string(core.ExecutionStageComplete)}, 10, 0, "desc", nil, nil)
			require.NoError(t, err)
			require.Len(t, resByStage, 1)
			assert.Equal(t, "acme/web", resByStage[0].Details["full_name"])

			resByFullName, err := store.GetLastEvents(map[string]any{"full_name": "acme/api"}, 10, 0, "desc", nil, nil)
			require.NoError(t, err)
			require.Len(t, resByFullName, 2)

			resByFailureClass, err := store.GetLastEvents(map[string]any{"failure_class": string(core.FailureClassValidation)}, 10, 0, "desc", nil, nil)
			require.NoError(t, err)
			require.Len(t, resByFailureClass, 1)
			assert.Equal(t, "hook failed", resByFailureClass[0].Details["last_error"])
		})
	}
}

type mockRegistry struct {
	config map[string]map[string]any
	subs   map[string]core.Listener
}

func (m *mockRegistry) GetPlugin(name string) (core.Plugin, error)                 { return nil, nil }
func (m *mockRegistry) GetPluginsWithCapability(cap core.Capability) []core.Plugin { return nil }
func (m *mockRegistry) ListPlugins() []core.Plugin                                 { return nil }
func (m *mockRegistry) RegisterEventType(desc core.EventTypeDesc) error            { return nil }
func (m *mockRegistry) Publish(ctx context.Context, event core.InternalEvent)      {}
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

func TestAuditPluginExecuteLastEventsSupportsExecutionFilters(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	registry := &mockRegistry{
		config: map[string]map[string]any{
			"audit": {
				"storage":         "memory",
				"retention_count": 10,
			},
		},
	}

	p := &AuditPlugin{}
	err := p.Init(context.Background(), logger, registry)
	require.NoError(t, err)

	err = p.Start(context.Background())
	require.NoError(t, err)
	handler := registry.subs["*"]

	handler(context.Background(), core.InternalEvent{
		Type:      core.EventTypeExecution,
		Timestamp: time.Now().UTC(),
		Source:    "reconciler",
		Repo:      "api",
		Details: map[string]any{
			"execution_id":  "exec-9",
			"full_name":     "acme/api",
			"stage":         string(core.ExecutionStageHooks),
			"status":        string(core.ExecutionStatusFailed),
			"last_error":    "hook failed",
			"failure_class": string(core.FailureClassValidation),
		},
	})

	handler(context.Background(), core.InternalEvent{
		Type:      core.EventTypeExecution,
		Timestamp: time.Now().UTC().Add(-1 * time.Minute),
		Source:    "reconciler",
		Repo:      "web",
		Details: map[string]any{
			"execution_id": "exec-10",
			"full_name":    "acme/web",
			"stage":        string(core.ExecutionStageComplete),
			"status":       string(core.ExecutionStatusSucceeded),
		},
	})

	res, err := p.Execute(context.Background(), "last_events", map[string]interface{}{
		"limit": 10,
		"filter": map[string]any{
			"execution_id":  "exec-9",
			"failure_class": string(core.FailureClassValidation),
		},
	})
	require.NoError(t, err)

	events := res.([]core.InternalEvent)
	require.Len(t, events, 1)
	assert.Equal(t, "acme/api", events[0].Details["full_name"])
	assert.Equal(t, "hook failed", events[0].Details["last_error"])
}

func TestSQLiteStoreSupportsExecutionFiltersForLegacyJSONRows(t *testing.T) {
	dbPath := "test_audit_legacy_execution_filters.db"
	_ = os.Remove(dbPath)
	defer func() { _ = os.Remove(dbPath) }()

	legacyDB, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)

	_, err = legacyDB.Exec(`
		CREATE TABLE audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			timestamp DATETIME NOT NULL,
			source TEXT NOT NULL,
			repo TEXT,
			details TEXT,
			string_val TEXT
		)
	`)
	require.NoError(t, err)

	details := `{"execution_id":"legacy-exec","full_name":"acme/legacy","stage":"hooks","status":"failed","last_error":"legacy hook failed","failure_class":"validation"}`
	_, err = legacyDB.Exec(`
		INSERT INTO audit_events (type, timestamp, source, repo, details, string_val)
		VALUES (?, ?, ?, ?, ?, ?)
	`, string(core.EventTypeExecution), time.Now().UTC(), "reconciler", "legacy", details, "")
	require.NoError(t, err)

	otherDetails := `{"execution_id":"other-exec","full_name":"acme/other","stage":"complete","status":"succeeded","last_error":"","failure_class":""}`
	_, err = legacyDB.Exec(`
		INSERT INTO audit_events (type, timestamp, source, repo, details, string_val)
		VALUES (?, ?, ?, ?, ?, ?)
	`, string(core.EventTypeExecution), time.Now().UTC().Add(-1*time.Minute), "reconciler", "other", otherDetails, "")
	require.NoError(t, err)

	require.NoError(t, legacyDB.Close())

	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()

	events, err := store.GetLastEvents(map[string]any{"execution_id": "legacy-exec"}, 10, 0, "desc", nil, nil)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "acme/legacy", events[0].Details["full_name"])
	assert.Equal(t, "legacy hook failed", events[0].Details["last_error"])
}

func TestSQLiteStoreBackfillIsIdempotentAcrossReopen(t *testing.T) {
	dbPath := "test_audit_backfill_idempotent.db"
	_ = os.Remove(dbPath)
	defer func() { _ = os.Remove(dbPath) }()

	legacyDB, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)

	_, err = legacyDB.Exec(`
		CREATE TABLE audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			timestamp DATETIME NOT NULL,
			source TEXT NOT NULL,
			repo TEXT,
			details TEXT,
			string_val TEXT
		);

		CREATE TABLE audit_event_updates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id INTEGER NOT NULL
		);

		CREATE TRIGGER audit_events_update_trigger
		AFTER UPDATE ON audit_events
		BEGIN
			INSERT INTO audit_event_updates (event_id) VALUES (NEW.id);
		END;
	`)
	require.NoError(t, err)

	legacyRows := []struct {
		eventType string
		repo      string
		details   string
	}{
		{
			eventType: string(core.EventTypeExecution),
			repo:      "api",
			details:   `{"execution_id":"exec-succeeded","full_name":"acme/api","stage":"complete","status":"succeeded","last_error":"","failure_class":""}`,
		},
		{
			eventType: string(core.EventTypeExecution),
			repo:      "worker",
			details:   `{"execution_id":"exec-running","full_name":"acme/worker","stage":"fetch","status":"running","last_error":"","failure_class":""}`,
		},
		{
			eventType: "deploy_start",
			repo:      "repo1",
			details:   `{"request_id":"req-1"}`,
		},
	}

	for index, row := range legacyRows {
		_, err = legacyDB.Exec(`
			INSERT INTO audit_events (type, timestamp, source, repo, details, string_val)
			VALUES (?, ?, ?, ?, ?, ?)
		`, row.eventType, time.Now().UTC().Add(time.Duration(index)*time.Minute), "test", row.repo, row.details, "")
		require.NoError(t, err)
	}

	require.NoError(t, legacyDB.Close())

	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	firstReopenUpdates := auditUpdateCount(t, dbPath)
	require.Equal(t, 2, firstReopenUpdates)

	store, err = newSQLiteStore(dbPath)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	secondReopenUpdates := auditUpdateCount(t, dbPath)
	assert.Equal(t, firstReopenUpdates, secondReopenUpdates)
}

func auditUpdateCount(t *testing.T, dbPath string) int {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM audit_event_updates`).Scan(&count)
	require.NoError(t, err)

	return count
}
