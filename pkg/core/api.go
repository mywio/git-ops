package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type pluginInfo struct {
	Name         string       `json:"name"`
	Description  string       `json:"description,omitempty"`
	Capabilities []Capability `json:"capabilities,omitempty"`
	Status       ServiceStatus `json:"status,omitempty"`
	Config       any          `json:"config,omitempty"`
}

func (m *ModuleManager) registerCoreRoutes() {
	m.mux.HandleFunc("/api/plugins", m.handlePlugins)
	m.mux.HandleFunc("/api/plugins/", m.handlePlugin)
}

func (m *ModuleManager) handlePlugins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	includeConfig := strings.EqualFold(r.URL.Query().Get("include_config"), "true")
	plugins := m.ListPlugins()
	out := make([]pluginInfo, 0, len(plugins))
	for _, p := range plugins {
		out = append(out, buildPluginInfo(p, includeConfig))
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *ModuleManager) handlePlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	name = strings.Trim(name, "/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "plugin name required"})
		return
	}
	plug, err := m.GetPlugin(name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, buildPluginInfo(plug, true))
}

func buildPluginInfo(plug Plugin, includeConfig bool) pluginInfo {
	info := pluginInfo{
		Name:         plug.Name(),
		Description:  plug.Description(),
		Capabilities: plug.Capabilities(),
		Status:       plug.Status(),
	}
	if includeConfig {
		if cfg, ok := plug.(ConfigProvider); ok {
			info.Config = cfg.Config()
		}
	}
	return info
}

func (m *ModuleManager) startHTTPServer() {
	m.serverOnce.Do(func() {
		addr := m.httpAddr()
		if addr == "" {
			return
		}
		m.server = &http.Server{
			Addr:    addr,
			Handler: m.mux,
		}
		m.logger.Info("HTTP server starting", "addr", addr)
		go func() {
			if err := m.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				m.logger.Error("HTTP server failed", "error", err)
			}
		}()
	})
}

func (m *ModuleManager) httpAddr() string {
	cfg := m.GetConfig()
	coreSection, ok := cfg["core"]
	if !ok {
		return ""
	}
	if v, ok := coreSection["http_addr"]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
