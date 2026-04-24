package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/mywio/git-ops/pkg/core"
)

const (
	uiMethodNotAllowed = "Method not allowed"
	headerContentType  = "Content-Type"
)

func (p *UIPlugin) registerRoutes() {
	if p.mux == nil {
		return
	}

	// API Routes (prefix with /api/ui to avoid core conflicts)
	p.mux.HandleFunc("/api/ui/deployments", p.requireAuth(p.handleDeployments))
	p.mux.HandleFunc("/api/ui/logs", p.requireAuth(p.handleLogs))
	p.mux.HandleFunc("/api/ui/system/info", p.requireAuth(p.handleSystemInfo))
	p.mux.HandleFunc("/api/ui/stacks/action", p.requireAuth(p.handleStackAction))
	p.mux.HandleFunc("/", p.requireAuth(p.handleRootRedirect))
	p.mux.HandleFunc("/ui", p.requireAuth(p.handleUIRootRedirect))
	p.mux.HandleFunc("/ui/", p.requireAuth(p.handleFrontend))
}

func (p *UIPlugin) handleDeployments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, uiMethodNotAllowed, http.StatusMethodNotAllowed)
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
		http.Error(w, uiMethodNotAllowed, http.StatusMethodNotAllowed)
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
		http.Error(w, uiMethodNotAllowed, http.StatusMethodNotAllowed)
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

	if logStr, logChan, ok := parseLogsResult(res); ok {
		if logChan == nil {
			writeJSON(w, http.StatusOK, map[string]string{"logs": logStr})
			return
		}
		p.streamLogChannel(w, r, logChan)
		return
	}
	http.Error(w, "Plugin returned unsupported log format", http.StatusInternalServerError)
}

type stackActionRequest struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Action string `json:"action"`
}

func (p *UIPlugin) handleStackAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, uiMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}
	if p.registry == nil {
		http.Error(w, "Plugin registry unavailable", http.StatusInternalServerError)
		return
	}

	var req stackActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	req.Owner = strings.TrimSpace(req.Owner)
	req.Repo = strings.TrimSpace(req.Repo)
	req.Action = strings.TrimSpace(req.Action)
	if req.Owner == "" || req.Repo == "" || req.Action == "" {
		http.Error(w, "owner, repo, and action are required", http.StatusBadRequest)
		return
	}

	eventType, ok := stackActionEventType(req.Action)
	if !ok {
		http.Error(w, "unsupported stack action", http.StatusBadRequest)
		return
	}

	details := map[string]interface{}{
		"owner": req.Owner,
		"repo":  req.Repo,
	}
	if user := p.authenticatedUser(r); user != "" {
		details["requested_by"] = user
	}

	p.registry.Publish(r.Context(), core.InternalEvent{
		Type:    core.EventTypeName(eventType),
		Source:  "ui",
		Repo:    req.Repo,
		Message: fmt.Sprintf("Stack action %s requested for %s/%s", req.Action, req.Owner, req.Repo),
		Details: details,
	})

	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func stackActionEventType(action string) (string, bool) {
	switch action {
	case "start_stack":
		return "stack_start_requested", true
	case "stop_stack":
		return "stack_stop_requested", true
	case "restart_stack":
		return "stack_restart_requested", true
	case "disable_stack":
		return "stack_disable_requested", true
	case "enable_stack":
		return "stack_enable_requested", true
	case "reconcile_stack":
		return "reconcile_stack", true
	default:
		return "", false
	}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set(headerContentType, "application/json")
	w.WriteHeader(status)
	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			slog.Default().Error("failed to encode UI JSON response", "error", err)
		}
	}
}

func (p *UIPlugin) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p.cfg.DisableAuth {
			next(w, r)
			return
		}
		if p.authenticatedUser(r) == "" {
			http.Error(w, "missing authenticated user header", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (p *UIPlugin) authenticatedUser(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(p.cfg.AuthHeader))
}

func (p *UIPlugin) handleRootRedirect(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui/system", http.StatusTemporaryRedirect)
}

func (p *UIPlugin) handleUIRootRedirect(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ui" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui/system", http.StatusTemporaryRedirect)
}

func (p *UIPlugin) handleFrontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, uiMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	dist, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		p.logger.Error("Failed to load frontend/dist from embedded FS", "error", err)
		http.Error(w, "UI assets unavailable", http.StatusInternalServerError)
		return
	}

	requested := strings.TrimPrefix(path.Clean(r.URL.Path), "/ui/")
	if requested == "." || requested == "/" {
		requested = ""
	}
	if requested != "" {
		if stat, err := fs.Stat(dist, requested); err == nil && !stat.IsDir() {
			http.StripPrefix("/ui/", http.FileServer(http.FS(dist))).ServeHTTP(w, r)
			return
		}
	}

	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		p.logger.Error("Failed to read embedded UI index", "error", err)
		http.Error(w, "UI assets unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set(headerContentType, "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(index); err != nil {
		p.logger.Debug("failed to write UI index response", "error", err)
	}
}

func parseLogsResult(res interface{}) (string, <-chan string, bool) {
	if logChan, ok := res.(<-chan string); ok {
		return "", logChan, true
	}
	if logStr, ok := res.(string); ok {
		return logStr, nil, true
	}
	return "", nil, false
}

func (p *UIPlugin) streamLogChannel(w http.ResponseWriter, r *http.Request, logChan <-chan string) {
	w.Header().Set(headerContentType, "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-logChan:
			if !ok {
				if _, err := fmt.Fprintf(w, "event: close\ndata: \n\n"); err != nil {
					slog.Default().Debug("failed to write SSE close event", "error", err)
				}
				flusher.Flush()
				return
			}
			safeLine := strings.ReplaceAll(line, "\n", "\\n")
			if _, err := fmt.Fprintf(w, "data: %s\n\n", safeLine); err != nil {
				slog.Default().Debug("failed to write SSE log event", "error", err)
				return
			}
			flusher.Flush()
		}
	}
}
