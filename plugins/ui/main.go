package main

import (
	"context"
	"embed"
	"log/slog"
	"net/http"
	"strings"

	"github.com/mywio/git-ops/pkg/core"
)

//go:embed frontend/dist
var frontendFS embed.FS

type UIPlugin struct {
	mux        *http.ServeMux
	logger     *slog.Logger
	registry   core.PluginRegistry
	httpClient *http.Client
	cfg        uiConfig
}

var Plugin = &UIPlugin{}

type uiConfig struct {
	DisableAuth     bool   `yaml:"disable_auth"`
	AuthHeader      string `yaml:"auth_header"`
	AuthVerifyURL   string `yaml:"auth_verify_url"`
	TrustAuthHeader bool   `yaml:"trust_auth_header"`
}

type uiConfigView struct {
	DisableAuth       bool   `json:"disable_auth" yaml:"disable_auth"`
	AuthHeader        string `json:"auth_header" yaml:"auth_header"`
	AuthVerifyEnabled bool   `json:"auth_verify_enabled" yaml:"auth_verify_enabled"`
	TrustAuthHeader   bool   `json:"trust_auth_header" yaml:"trust_auth_header"`
}

func (p *UIPlugin) Name() string {
	return "ui"
}

func (p *UIPlugin) Description() string {
	return "Web Dashboard UI"
}

func (p *UIPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger
	p.registry = registry
	p.cfg = defaultUIConfig()
	if registry != nil {
		if section, ok := registry.GetConfig()["ui"]; ok {
			if err := core.DecodeConfigSection(section, &p.cfg); err != nil {
				return err
			}
		}
	}
	p.cfg.AuthHeader = normalizeHeaderName(p.cfg.AuthHeader)
	if registry != nil {
		p.mux = registry.GetMuxServer()
		p.httpClient = registry.GetHTTPClient()
		p.registerRoutes()
	}
	if p.httpClient == nil {
		p.httpClient = http.DefaultClient
	}
	return nil
}

func (p *UIPlugin) Start(ctx context.Context) error {
	p.logger.Info("UI Plugin Started")
	return nil
}

func (p *UIPlugin) Stop(ctx context.Context) error {
	return nil
}

func (p *UIPlugin) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityUI, core.CapabilityAPI}
}

func (p *UIPlugin) Status() core.ServiceStatus {
	return core.StatusHealthy
}

func (p *UIPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	return nil, nil
}

func (p *UIPlugin) Config() any {
	return uiConfigView{
		DisableAuth:       p.cfg.DisableAuth,
		AuthHeader:        p.cfg.AuthHeader,
		AuthVerifyEnabled: strings.TrimSpace(p.cfg.AuthVerifyURL) != "",
		TrustAuthHeader:   p.cfg.TrustAuthHeader,
	}
}

func defaultUIConfig() uiConfig {
	return uiConfig{
		AuthHeader: "X-Auth-Request-User",
	}
}

func normalizeHeaderName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return defaultUIConfig().AuthHeader
	}
	return http.CanonicalHeaderKey(name)
}
