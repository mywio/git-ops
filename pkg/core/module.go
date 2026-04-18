package core

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"plugin"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// PluginRegistry provides shared runtime services to modules during Init and
// later execution.
//
// ModuleManager implements this interface and passes itself to each module so
// plugins can discover other plugins, access configuration, publish and
// subscribe to events, and register HTTP handlers.
type PluginRegistry interface {
	// GetPlugin returns a plugin by its registered name.
	GetPlugin(name string) (Plugin, error)
	// GetPluginsWithCapability returns all plugins advertising the given capability.
	GetPluginsWithCapability(cap Capability) []Plugin
	// ListPlugins returns all registered plugins in registration order.
	ListPlugins() []Plugin
	// RegisterEventType declares an event type for validation and discoverability.
	RegisterEventType(desc EventTypeDesc) error
	// Publish delivers an event to matching subscribers on this manager's bus.
	Publish(ctx context.Context, event InternalEvent)
	// GetMuxServer returns the shared HTTP mux used for core and plugin routes.
	GetMuxServer() *http.ServeMux
	// Subscribe registers a listener for an exact event type or wildcard pattern.
	Subscribe(pattern string, handler Listener)
	// GetHTTPClient returns the shared HTTP client for outbound requests.
	GetHTTPClient() *http.Client
	// GetConfig returns the sectioned configuration map visible to plugins.
	GetConfig() map[string]map[string]any
}

// Module is the base lifecycle contract for components managed by ModuleManager.
//
// Core modules and plugins implement this interface. Init is called before
// Start, and Stop is called during shutdown in reverse registration order.
type Module interface {
	// Name returns the stable module name used for registration and logging.
	Name() string
	// Init prepares the module and receives the shared PluginRegistry.
	Init(ctx context.Context, logger *slog.Logger, registry PluginRegistry) error
	// Start begins the module's runtime work.
	Start(ctx context.Context) error
	// Stop shuts the module down and releases owned resources.
	Stop(ctx context.Context) error
}

// Plugin extends Module with metadata, health, and an action-based execution
// entry point.
//
// Dynamically loaded plugins implement this interface and expose an exported
// symbol named Plugin that resolves to a Plugin value.
type Plugin interface {
	Module
	// Description returns a human-readable summary of the plugin.
	Description() string
	// Capabilities returns the plugin capabilities it provides to the system.
	Capabilities() []Capability
	// Status reports the current plugin health for APIs and status pages.
	Status() ServiceStatus
	// Execute runs a plugin-specific action with free-form parameters.
	Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error)
}

// ConfigProvider allows plugins to expose a UI-safe view of their configuration.
// Use core.Secret for any sensitive values to avoid leaking secrets.
type ConfigProvider interface {
	Config() any
}

// ModuleManager owns registered modules, shared configuration, the shared HTTP
// server, and the instance-scoped event bus.
type ModuleManager struct {
	modules []Module
	logger  *slog.Logger
	mux     *http.ServeMux
	server  *http.Server

	httpClient *http.Client
	configMu   sync.RWMutex
	config     map[string]map[string]any
	serverOnce sync.Once

	subscribers   map[string][]Listener
	subscribersMu sync.RWMutex
	eventTypes    map[EventTypeName]EventTypeDesc
	eventTypesMu  sync.RWMutex
}

func (m *ModuleManager) RegisterEventType(desc EventTypeDesc) error {
	m.eventTypesMu.Lock()
	defer m.eventTypesMu.Unlock()

	if _, exists := m.eventTypes[desc.Name]; exists {
		return fmt.Errorf("event type %s already registered", desc.Name)
	}
	m.eventTypes[desc.Name] = desc
	return nil
}

func (m *ModuleManager) Publish(ctx context.Context, event InternalEvent) {
	if ctx == nil {
		ctx = context.Background()
	}
	event.Timestamp = time.Now()

	m.eventTypesMu.RLock()
	desc, ok := m.eventTypes[event.Type]
	m.eventTypesMu.RUnlock()
	if ok {
		for field, spec := range desc.PayloadSpec {
			if spec.Required {
				if _, has := event.Details[field]; !has {
					m.logger.Warn("Published event missing required field", "event_type", event.Type, "field", field)
				}
			}
		}
	}

	m.subscribersMu.RLock()
	defer m.subscribersMu.RUnlock()

	for pattern, listeners := range m.subscribers {
		if matchesPattern(string(event.Type), pattern) {
			for _, listener := range listeners {
				go listener(ctx, event)
			}
		}
	}
}

func (m *ModuleManager) GetMuxServer() *http.ServeMux {
	return m.mux
}

// NewModuleManager creates a ModuleManager with an initialized HTTP mux, event
// bus state, and default HTTP client.
func NewModuleManager(logger *slog.Logger) *ModuleManager {
	mgr := &ModuleManager{
		modules: []Module{},
		logger:  logger,
		mux:     http.NewServeMux(),
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		config:      map[string]map[string]any{},
		subscribers: map[string][]Listener{},
		eventTypes:  map[EventTypeName]EventTypeDesc{},
	}
	mgr.registerCoreRoutes()
	return mgr
}

func (m *ModuleManager) Subscribe(pattern string, handler Listener) {
	m.subscribersMu.Lock()
	defer m.subscribersMu.Unlock()
	m.subscribers[pattern] = append(m.subscribers[pattern], handler)
}

func (m *ModuleManager) GetHTTPClient() *http.Client {
	if m.httpClient != nil {
		return m.httpClient
	}
	return http.DefaultClient
}

func (m *ModuleManager) SetHTTPClient(client *http.Client) {
	if client == nil {
		m.httpClient = http.DefaultClient
		return
	}
	m.httpClient = client
}

func (m *ModuleManager) GetConfig() map[string]map[string]any {
	m.configMu.RLock()
	defer m.configMu.RUnlock()
	return cloneConfigMap(m.config)
}

func (m *ModuleManager) SetConfig(cfg map[string]map[string]any) {
	m.configMu.Lock()
	defer m.configMu.Unlock()
	m.config = cloneConfigMap(cfg)
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

// ListPlugins returns all registered plugins in registration order.
func (m *ModuleManager) ListPlugins() []Plugin {
	results := make([]Plugin, 0, len(m.modules))
	for _, mod := range m.modules {
		if plug, ok := mod.(Plugin); ok {
			results = append(results, plug)
		}
	}
	return results
}

// LoadPlugins loads plugins from a directory and registers them with the module manager.
func (m *ModuleManager) LoadPlugins(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			m.logger.Warn("Plugins directory not found", "dir", dir)
			return nil
		}
		return fmt.Errorf("failed to read plugins dir: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	allowlist := m.pluginAllowlist()
	allowset := make(map[string]struct{}, len(allowlist))
	if len(allowlist) > 0 {
		for _, name := range allowlist {
			allowset[name] = struct{}{}
		}
		m.logger.Info("Plugin allowlist active", "allowed", allowlist)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".so") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".so")
		if len(allowset) > 0 {
			if _, ok := allowset[name]; !ok {
				m.logger.Info("Skipping plugin (not in allowlist)", "plugin", name, "allowlist", allowlist)
				continue
			}
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

		plug, ok := resolvePluginSymbol(sym)
		if !ok || plug == nil {
			m.logger.Error("Plugin has wrong type (must implement core.Plugin)", "path", path)
			continue
		}

		m.Register(plug)
		m.logger.Info("Plugin loaded successfully", "name", plug.Name())
	}
	return nil
}

func (m *ModuleManager) pluginAllowlist() []string {
	m.configMu.RLock()
	defer m.configMu.RUnlock()

	coreCfg, ok := m.config["core"]
	if !ok {
		return nil
	}

	raw, ok := coreCfg["plugins"]
	if !ok || raw == nil {
		return nil
	}

	switch v := raw.(type) {
	case string:
		return nonEmptyStrings(strings.Split(v, ","))
	case []string:
		return nonEmptyStrings(v)
	case []any:
		values := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				values = append(values, s)
			}
		}
		return nonEmptyStrings(values)
	default:
		return nil
	}
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func resolvePluginSymbol(sym any) (Plugin, bool) {
	if plug, ok := sym.(Plugin); ok {
		return plug, true
	}
	if plugPtr, ok := sym.(*Plugin); ok {
		if plugPtr == nil || *plugPtr == nil {
			return nil, false
		}
		return *plugPtr, true
	}

	// plugin.Lookup returns a pointer to the exported variable. Unwrap pointers
	// until we find a value that implements Plugin.
	val := reflect.ValueOf(sym)
	for val.IsValid() && val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return nil, false
		}
		val = val.Elem()
		if !val.IsValid() {
			return nil, false
		}
		if val.CanInterface() {
			if plug, ok := val.Interface().(Plugin); ok {
				return plug, true
			}
		}
	}

	if val.IsValid() && val.CanInterface() {
		if plug, ok := val.Interface().(Plugin); ok {
			return plug, true
		}
	}

	return nil, false
}

// Init initializes all registered modules in registration order.
func (m *ModuleManager) Init(ctx context.Context) error {
	for _, mod := range m.modules {
		if err := mod.Init(ctx, m.logger.With("module", mod.Name()), m); err != nil {
			return fmt.Errorf("failed to init module %s: %w", mod.Name(), err)
		}
	}
	return nil
}

// Start starts the shared HTTP server and then starts all registered modules.
func (m *ModuleManager) Start(ctx context.Context) {
	m.startHTTPServer()
	for _, plug := range m.ListPlugins() {
		m.logger.Info("Active plugin",
			"name", plug.Name(),
			"capabilities", plug.Capabilities(),
			"status", plug.Status(),
		)
	}
	for _, mod := range m.modules {
		go func(mod Module) {
			m.logger.Info("Starting module", "module", mod.Name())
			if err := mod.Start(ctx); err != nil {
				m.logger.Error("Module failed", "module", mod.Name(), "error", err)
			}
		}(mod)
	}
}

// Stop stops all modules in reverse registration order and then shuts down the
// shared HTTP server.
func (m *ModuleManager) Stop(ctx context.Context) {
	for i := len(m.modules) - 1; i >= 0; i-- {
		mod := m.modules[i]
		m.logger.Info("Stopping module", "module", mod.Name())
		if err := mod.Stop(ctx); err != nil {
			m.logger.Error("Error stopping module", "module", mod.Name(), "error", err)
		}
	}
	if m.server != nil {
		if err := m.server.Shutdown(ctx); err != nil {
			m.logger.Error("HTTP server shutdown failed", "error", err)
		}
	}
}

func matchesPattern(eventType, pattern string) bool {
	if pattern == eventType {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(eventType, prefix)
	}
	return false
}

// cloneConfigMap creates a deep copy of a configuration map.
func cloneConfigMap(src map[string]map[string]any) map[string]map[string]any {
	if len(src) == 0 {
		return map[string]map[string]any{}
	}
	dst := make(map[string]map[string]any, len(src))
	for section, values := range src {
		sectionCopy := make(map[string]any, len(values))
		for k, v := range values {
			sectionCopy[k] = v
		}
		dst[section] = sectionCopy
	}
	return dst
}
