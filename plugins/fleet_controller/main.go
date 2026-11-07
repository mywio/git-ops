package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mywio/git-ops/pkg/core"
	"github.com/mywio/git-ops/pkg/fleetapi"
)

var Plugin core.Plugin = &FleetControllerPlugin{}

type FleetControllerPlugin struct {
	cfg       fleetConfig
	logger    *slog.Logger
	registry  core.PluginRegistry
	store     *fleetStore
	ca        *certificateAuthority
	server    *http.Server
	dryRun    bool
	mu        sync.RWMutex
	healthErr error
}

func (p *FleetControllerPlugin) Name() string { return "fleet_controller" }
func (p *FleetControllerPlugin) Description() string {
	return "Central outbound-mTLS agent fleet and persistent runner task controller"
}
func (p *FleetControllerPlugin) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityListAgents, core.CapabilityManageAgents}
}

func (p *FleetControllerPlugin) Init(_ context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger, p.registry = logger, registry
	var section map[string]any
	if registry != nil {
		config := registry.GetConfig()
		section = config["fleet_controller"]
		if coreCfg := config["core"]; coreCfg != nil {
			p.dryRun, _ = coreCfg["dry_run"].(bool)
		}
	}
	cfg, err := decodeFleetConfig(section)
	if err != nil {
		return err
	}
	p.cfg = cfg
	if registry != nil {
		for _, desc := range fleetEventTypes() {
			if err := registry.RegisterEventType(desc); err != nil {
				return err
			}
		}
	}
	if !cfg.Enabled {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.StateDB), 0o750); err != nil {
		return err
	}
	p.store, err = openFleetStore(cfg.StateDB)
	if err != nil {
		return fmt.Errorf("open fleet state: %w", err)
	}
	p.ca, err = loadCertificateAuthority(cfg.AgentCACertFile, cfg.AgentCAKeyFile)
	if err != nil {
		_ = p.store.close()
		return err
	}
	return nil
}

func fleetEventTypes() []core.EventTypeDesc {
	return []core.EventTypeDesc{
		{Name: "fleet_agent_enrolled", Description: "A fleet agent enrolled"},
		{Name: "fleet_agent_revoked", Description: "A fleet agent was revoked"},
		{Name: "fleet_agent_heartbeat", Description: "A fleet agent reported inventory"},
		{Name: "fleet_task_requested", Description: "A typed fleet task was queued"},
		{Name: "fleet_task_completed", Description: "A typed fleet task completed"},
	}
}

func (p *FleetControllerPlugin) Start(context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	cert, err := tls.LoadX509KeyPair(p.cfg.TLSCertFile, p.cfg.TLSKeyFile)
	if err != nil {
		p.setHealth(err)
		return nil
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(p.ca.certPEM) {
		p.setHealth(fmt.Errorf("load agent CA trust"))
		return nil
	}
	mux := http.NewServeMux()
	p.registerAgentRoutes(mux)
	p.server = &http.Server{
		Addr: p.cfg.ListenAddr, Handler: mux,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 35 * time.Second, IdleTimeout: 90 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert},
			ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots},
	}
	go func() {
		listener, err := tls.Listen("tcp", p.cfg.ListenAddr, p.server.TLSConfig)
		if err != nil {
			p.setHealth(err)
			return
		}
		if err := p.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			p.setHealth(err)
		}
	}()
	return nil
}

func (p *FleetControllerPlugin) Stop(ctx context.Context) error {
	if p.server != nil {
		_ = p.server.Shutdown(ctx)
	}
	if p.store != nil {
		return p.store.close()
	}
	return nil
}

func (p *FleetControllerPlugin) Status() core.ServiceStatus {
	if !p.cfg.Enabled {
		return core.StatusDegraded
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.healthErr != nil {
		return core.StatusUnhealthy
	}
	return core.StatusHealthy
}

func (p *FleetControllerPlugin) setHealth(err error) {
	p.mu.Lock()
	p.healthErr = err
	p.mu.Unlock()
	if p.logger != nil {
		p.logger.Error("fleet controller unhealthy", "error", err)
	}
}

func (p *FleetControllerPlugin) Config() any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	health := ""
	if p.healthErr != nil {
		health = p.healthErr.Error()
	}
	return map[string]any{
		"enabled": p.cfg.Enabled, "listen_addr": p.cfg.ListenAddr, "public_url": p.cfg.PublicURL,
		"state_db": p.cfg.StateDB, "enrollment_token_ttl": p.cfg.EnrollmentTokenTTL.String(),
		"heartbeat_interval": p.cfg.HeartbeatInterval.String(), "offline_after": p.cfg.OfflineAfter.String(),
		"pools": p.cfg.Pools, "health_error": health,
	}
}

func (p *FleetControllerPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	if !p.cfg.Enabled || p.store == nil {
		return nil, fmt.Errorf("fleet controller is disabled")
	}
	switch action {
	case "list_agents":
		return p.store.listAgents(ctx, p.cfg.OfflineAfter)
	case "create_enrollment_token":
		pool := stringParam(params, "pool")
		if _, ok := p.cfg.pool(pool); !ok {
			return nil, fmt.Errorf("unknown fleet pool %q", pool)
		}
		token, err := randomToken(32)
		if err != nil {
			return nil, err
		}
		expires := time.Now().UTC().Add(p.cfg.EnrollmentTokenTTL)
		if err := p.store.createEnrollment(ctx, token, pool, expires); err != nil {
			return nil, err
		}
		return map[string]any{"token": token, "pool": pool, "expires_at": expires, "controller_url": p.cfg.PublicURL}, nil
	case "revoke_agent":
		id := stringParam(params, "agent_id")
		if id == "" {
			return nil, fmt.Errorf("agent_id is required")
		}
		if err := p.store.revoke(ctx, id); err != nil {
			return nil, err
		}
		p.publish(ctx, "fleet_agent_revoked", map[string]any{"agent_id": id, "requested_by": stringParam(params, "requested_by")})
		return map[string]any{"revoked": true}, nil
	case "queue_agent_task":
		if p.dryRun {
			return nil, fmt.Errorf("fleet mutations are disabled by core.dry_run")
		}
		agentID := stringParam(params, "agent_id")
		taskType := stringParam(params, "task_type")
		if agentID == "" || !strings.HasPrefix(taskType, "runner.") {
			return nil, fmt.Errorf("agent_id and a runner.* task_type are required")
		}
		if !p.store.agentAllowed(ctx, agentID) {
			return nil, fmt.Errorf("agent is unknown or revoked")
		}
		payload, _ := params["payload"].(map[string]any)
		task := fleetapi.Task{ID: uuid.NewString(), Type: taskType, Generation: time.Now().UnixNano(),
			ExpiresAt: time.Now().UTC().Add(30 * time.Minute), Payload: payload}
		if err := p.store.queueTask(ctx, task, agentID); err != nil {
			return nil, err
		}
		p.publish(ctx, "fleet_task_requested", map[string]any{"agent_id": agentID, "task_id": task.ID, "task_type": task.Type})
		return task, nil
	default:
		return nil, fmt.Errorf("unsupported fleet action %q", action)
	}
}

func (p *FleetControllerPlugin) publish(ctx context.Context, event string, details map[string]any) {
	if p.registry != nil {
		p.registry.Publish(ctx, core.InternalEvent{Type: core.EventTypeName(event), Source: p.Name(), Details: details})
	}
}

func randomToken(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func stringParam(params map[string]interface{}, key string) string {
	if params == nil {
		return ""
	}
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}
