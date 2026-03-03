package main

import (
	"context"
	"fmt"
	"log/slog"

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

	limit := 100
	offset := 0
	order := "desc"
	var filter map[string]any

	if params != nil {
		if l, ok := params["limit"].(int); ok {
			limit = l
		} else if l, ok := params["limit"].(float64); ok {
			limit = int(l)
		}

		if o, ok := params["offset"].(int); ok {
			offset = o
		} else if o, ok := params["offset"].(float64); ok {
			offset = int(o)
		}

		if o, ok := params["order"].(string); ok && o != "" {
			order = o
		}

		if f, ok := params["filter"].(map[string]any); ok {
			filter = f
		}
	}

	events, err := p.store.GetLastEvents(filter, limit, offset, order)
	if err != nil {
		return nil, fmt.Errorf("failed to get events: %w", err)
	}

	return events, nil
}
