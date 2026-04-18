package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/mywio/git-ops/pkg/core"
)

func (p *UIPlugin) registerRoutes() {
	if p.mux == nil {
		return
	}

	// API Routes (prefix with /api/ui to avoid core conflicts)
	p.mux.HandleFunc("/api/ui/deployments", p.handleDeployments)
	p.mux.HandleFunc("/api/ui/logs", p.handleLogs)
	p.mux.HandleFunc("/api/ui/system/info", p.handleSystemInfo)

	// In the future this is where we will mount the Vite static SPA on "/"
}

func (p *UIPlugin) handleDeployments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	deployers := p.registry.GetPluginsWithCapability(core.CapabilityDeployer)
	var allDeployments []interface{}

	for _, deployer := range deployers {
		res, err := deployer.Execute(r.Context(), "list_deployments", nil)
		if err != nil {
			p.logger.Error("Failed to list deployments from plugin", "plugin", deployer.Name(), "error", err)
			continue
		}

		if list, ok := res.([]interface{}); ok {
			allDeployments = append(allDeployments, list...)
		} else if listMap, ok := res.([]map[string]interface{}); ok {
			for _, m := range listMap {
				allDeployments = append(allDeployments, m)
			}
		}
	}

	writeJSON(w, http.StatusOK, allDeployments)
}

func (p *UIPlugin) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	systems := p.registry.GetPluginsWithCapability(core.CapabilitySystem)
	info := make(map[string]interface{})

	for _, sys := range systems {
		res, err := sys.Execute(r.Context(), "system_info", nil)
		if err != nil {
			p.logger.Error("Failed to get system info from plugin", "plugin", sys.Name(), "error", err)
			continue
		}
		info[sys.Name()] = res
	}

	writeJSON(w, http.StatusOK, info)
}

func (p *UIPlugin) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// query params: ?owner=org&repo=app&lines=100 or ?container=name&lines=100
	owner := r.URL.Query().Get("owner")
	repo := r.URL.Query().Get("repo")
	container := r.URL.Query().Get("container")
	lines := r.URL.Query().Get("lines")
	if lines == "" {
		lines = "100"
	}

	if container == "" && (owner == "" || repo == "") {
		http.Error(w, "container or owner and repo are required", http.StatusBadRequest)
		return
	}

	// This is a simple SSE implementation for streaming logs.
	// Since HTTP requests execute synchronously, streaming via a plugin Execute method needs
	// a channel or an io.Reader. For simplicity in the Execute abstraction, the plugin might
	// return an io.ReadCloser (like stdout pipe) or a channel of strings.

	deployers := p.registry.GetPluginsWithCapability(core.CapabilityDeployer)
	if len(deployers) == 0 {
		http.Error(w, "No deployer plugins available", http.StatusNotFound)
		return
	}

	deployer := deployers[0] // pick the first one for now
	params := map[string]interface{}{
		"owner":     owner,
		"repo":      repo,
		"container": container,
		"lines":     lines,
	}

	res, err := deployer.Execute(r.Context(), "stream_logs", params)
	if err != nil {
		p.logger.Error("Failed to stream logs from plugin", "plugin", deployer.Name(), "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// We expect the plugin to return a channel of strings
	logChan, ok := res.(<-chan string)
	if !ok {
		// Fallback: it might just be a string block natively if tailing is not supported
		if logStr, ok := res.(string); ok {
			writeJSON(w, http.StatusOK, map[string]string{"logs": logStr})
			return
		}
		http.Error(w, "Plugin returned unsupported log format", http.StatusInternalServerError)
		return
	}

	// Setup SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Stream from channel to HTTP response Writer
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-logChan:
			if !ok {
				// channel closed
				if _, err := fmt.Fprintf(w, "event: close\ndata: \n\n"); err != nil {
					slog.Default().Debug("failed to write SSE close event", "error", err)
				}
				flusher.Flush()
				return
			}
			// Escape newlines for SSE format
			safeLine := strings.ReplaceAll(line, "\n", "\\n")
			if _, err := fmt.Fprintf(w, "data: %s\n\n", safeLine); err != nil {
				slog.Default().Debug("failed to write SSE log event", "error", err)
				return
			}
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			slog.Default().Error("failed to encode UI JSON response", "error", err)
		}
	}
}
