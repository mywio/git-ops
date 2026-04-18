package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

type AuditPlugin struct {
	logger         *slog.Logger
	registry       core.PluginRegistry
	store          AuditStore
	retentionCount int
}

// Plugin is the exported symbol for dynamic loading
var Plugin = &AuditPlugin{}

func (p *AuditPlugin) Name() string {
	return "audit"
}

func (p *AuditPlugin) Description() string {
	return "Subscribes to all events and keeps a record of them"
}

func (p *AuditPlugin) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityAudit}
}

func (p *AuditPlugin) Status() core.ServiceStatus {
	return core.StatusHealthy
}

func (p *AuditPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger
	p.registry = registry

	config := registry.GetConfig()
	auditCfg, ok := config["audit"]

	storageType := "memory"
	dbPath := "data/audit.db"
	retentionCount := 1000

	if ok {
		if s, ok := auditCfg["storage"].(string); ok && s != "" {
			storageType = s
		}
		if d, ok := auditCfg["db_path"].(string); ok && d != "" {
			dbPath = d
		}
		if r, ok := auditCfg["retention_count"].(int); ok {
			retentionCount = r
		} else if r, ok := auditCfg["retention_count"].(float64); ok {
			retentionCount = int(r)
		}
	}
	p.retentionCount = retentionCount

	if storageType == "sqlite" {
		p.logger.Info("Initializing sqlite audit store", "db_path", dbPath)
		s, err := newSQLiteStore(dbPath)
		if err != nil {
			return fmt.Errorf("failed to initialize sqlite store: %w", err)
		}
		p.store = s
	} else {
		p.logger.Info("Initializing memory audit store")
		p.store = newMemoryStore()
	}

	return nil
}

func (p *AuditPlugin) Start(ctx context.Context) error {
	p.logger.Info("Starting audit plugin and subscribing to all events")
	p.registry.Subscribe("*", p.handleEvent)
	return nil
}

func (p *AuditPlugin) Stop(ctx context.Context) error {
	p.logger.Info("Stopping audit plugin")
	if p.store != nil {
		return p.store.Close()
	}
	return nil
}

func (p *AuditPlugin) handleEvent(ctx context.Context, event core.InternalEvent) {
	if p.store == nil {
		return
	}
	if err := p.store.Save(event); err != nil {
		p.logger.Error("Failed to save audit event", "error", err)
	}

	if p.retentionCount > 0 {
		if err := p.store.Cleanup(p.retentionCount); err != nil {
			p.logger.Error("Failed to cleanup audit store", "error", err)
		}
	}
}

func (p *AuditPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	if action != "last_events" {
		return nil, fmt.Errorf("unknown action: %s", action)
	}

	query := parseAuditQueryParams(params)

	events, err := p.store.GetLastEvents(query.filter, query.limit, query.offset, query.order, query.since, query.until)
	if err != nil {
		return nil, fmt.Errorf("failed to get events: %w", err)
	}

	return events, nil
}

type auditQueryParams struct {
	limit  int
	offset int
	order  string
	filter map[string]any
	since  *time.Time
	until  *time.Time
}

func parseAuditQueryParams(params map[string]interface{}) auditQueryParams {
	query := auditQueryParams{
		limit: 100,
		order: "desc",
	}
	if params == nil {
		return query
	}
	query.limit = intParam(params, "limit", query.limit)
	query.offset = intParam(params, "offset", query.offset)
	query.order = stringParam(params, "order", query.order)
	query.filter = filterParam(params["filter"])
	query.since = timeParam(params["since"])
	query.until = timeParam(params["until"])
	return query
}

func intParam(params map[string]interface{}, key string, fallback int) int {
	if value, ok := params[key].(int); ok {
		return value
	}
	if value, ok := params[key].(float64); ok {
		return int(value)
	}
	return fallback
}

func stringParam(params map[string]interface{}, key, fallback string) string {
	if value, ok := params[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func filterParam(raw interface{}) map[string]any {
	if filter, ok := raw.(map[string]any); ok {
		return cloneAuditFilter(filter)
	}
	if filter, ok := raw.(map[string]string); ok {
		out := make(map[string]any, len(filter))
		for key, value := range filter {
			out[key] = value
		}
		return out
	}
	return nil
}

func timeParam(raw interface{}) *time.Time {
	if value, ok := raw.(time.Time); ok {
		return &value
	}
	if value, ok := raw.(string); ok && value != "" {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return &parsed
		}
	}
	return nil
}

func cloneAuditFilter(filter map[string]any) map[string]any {
	if len(filter) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(filter))
	for key, value := range filter {
		cloned[key] = value
	}

	return cloned
}
