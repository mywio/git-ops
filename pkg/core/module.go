package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"plugin"
	"strings"
)

type Capability string
type ServiceStatus string

const (
	StatusHealthy   ServiceStatus = "HEALTHY"
	StatusUnhealthy ServiceStatus = "UNHEALTHY"
	StatusUnknown   ServiceStatus = "UNKNOWN"
)

// PluginRegistry allows modules to query for other plugins/capabilities.
type PluginRegistry interface {
	GetPlugin(name string) (Plugin, error)
	GetPluginsWithCapability(cap Capability) []Plugin
}

type Module interface {
	Name() string
	// Init receives a PluginRegistry for dependency injection/discovery
	Init(ctx context.Context, logger *slog.Logger, registry PluginRegistry) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type Plugin interface {
	Module
	Description() string
	Capabilities() []Capability
	Status() ServiceStatus
	// Execute provides a generic entry point for plugin actions
	Execute(action string, params map[string]interface{}) (interface{}, error)
}

type ModuleManager struct {
	modules []Module
	logger  *slog.Logger
}

func NewModuleManager(logger *slog.Logger) *ModuleManager {
	return &ModuleManager{
		modules: []Module{},
		logger:  logger,
	}
}

func (m *ModuleManager) Register(mod Module) {
	m.modules = append(m.modules, mod)
}

// GetPlugin implements PluginRegistry
func (m *ModuleManager) GetPlugin(name string) (Plugin, error) {
	for _, mod := range m.modules {
		if mod.Name() == name {
			if plug, ok := mod.(Plugin); ok {
				return plug, nil
			}
			return nil, fmt.Errorf("module %s is not a plugin", name)
		}
	}
	return nil, fmt.Errorf("plugin %s not found", name)
}

// GetPluginsWithCapability implements PluginRegistry
func (m *ModuleManager) GetPluginsWithCapability(cap Capability) []Plugin {
	var results []Plugin
	for _, mod := range m.modules {
		if plug, ok := mod.(Plugin); ok {
			for _, c := range plug.Capabilities() {
				if c == cap {
					results = append(results, plug)
					break
				}
			}
		}
	}
	return results
}

func (m *ModuleManager) LoadPlugins(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			m.logger.Warn("Plugins directory not found", "dir", dir)
			return nil
		}
		return fmt.Errorf("failed to read plugins dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".so") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		m.logger.Info("Loading plugin", "path", path)

		p, err := plugin.Open(path)
		if err != nil {
			m.logger.Error("Failed to open plugin", "path", path, "error", err)
			continue
		}

		sym, err := p.Lookup("Plugin")
		if err != nil {
			m.logger.Error("Plugin symbol not found", "path", path, "error", err)
			continue
		}

		plug, ok := sym.(Plugin)
		if !ok {
			m.logger.Error("Plugin has wrong type (must implement core.Plugin)", "path", path)
			continue
		}

		m.Register(plug)
		m.logger.Info("Plugin loaded successfully", "name", plug.Name())
	}
	return nil
}

func (m *ModuleManager) Init(ctx context.Context) error {
	for _, mod := range m.modules {
		// Pass 'm' (ModuleManager) as the PluginRegistry
		if err := mod.Init(ctx, m.logger.With("module", mod.Name()), m); err != nil {
			return fmt.Errorf("failed to init module %s: %w", mod.Name(), err)
		}
	}
	return nil
}

func (m *ModuleManager) Start(ctx context.Context) {
	for _, mod := range m.modules {
		go func(mod Module) {
			m.logger.Info("Starting module", "module", mod.Name())
			if err := mod.Start(ctx); err != nil {
				m.logger.Error("Module failed", "module", mod.Name(), "error", err)
			}
		}(mod)
	}
}

func (m *ModuleManager) Stop(ctx context.Context) {
	for i := len(m.modules) - 1; i >= 0; i-- {
		mod := m.modules[i]
		m.logger.Info("Stopping module", "module", mod.Name())
		if err := mod.Stop(ctx); err != nil {
			m.logger.Error("Error stopping module", "module", mod.Name(), "error", err)
		}
	}
}
