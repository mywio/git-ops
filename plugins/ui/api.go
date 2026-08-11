package main

import (
	"context"
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

type authenticatedUserContextKey struct{}

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

	deployers := p.registry.GetPluginsWithCapability(core.CapabilityListDeployments)
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

	systems := p.registry.GetPluginsWithCapability(core.CapabilitySystemInfo)
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

	deployers := p.registry.GetPluginsWithCapability(core.CapabilityStreamLogs)
	if len(deployers) == 0 {
		http.Error(w, "No log streaming plugins available", http.StatusNotFound)
		return
	}

	params := map[string]interface{}{
		"owner":     owner,
		"repo":      repo,
		"container": container,
		"lines":     lines,
	}

	var lastErr error
	for _, deployer := range deployers {
		res, err := deployer.Execute(r.Context(), "stream_logs", params)
		if err != nil {
			lastErr = err
			p.logger.Debug("Failed to stream logs from plugin", "plugin", deployer.Name(), "error", err)
			continue
		}

		if logStr, logChan, ok := parseLogsResult(res); ok {
			if logChan == nil {
				writeJSON(w, http.StatusOK, map[string]string{"logs": logStr})
				return
			}
			p.streamLogChannel(w, r, logChan)
			return
		}
		lastErr = fmt.Errorf("plugin %s returned unsupported log format", deployer.Name())
	}

	if lastErr != nil {
		p.logger.Error("Failed to stream logs from deployer plugins", "error", lastErr)
		http.Error(w, lastErr.Error(), http.StatusInternalServerError)
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
	if err := core.ValidateStackIdentity(req.Owner, req.Repo); err != nil {
		http.Error(w, "invalid owner or repo", http.StatusBadRequest)
		return
	}

	capability, ok := stackActionCapability(req.Action)
	if !ok {
		http.Error(w, "unsupported stack action", http.StatusBadRequest)
		return
	}

	plugins := p.registry.GetPluginsWithCapability(capability)
	if len(plugins) == 0 {
		http.Error(w, fmt.Sprintf("no plugin supports %s", req.Action), http.StatusNotFound)
		return
	}

	params := map[string]interface{}{
		"owner": req.Owner,
		"repo":  req.Repo,
	}
	if user := p.authenticatedUser(r); user != "" {
		params["requested_by"] = user
	}

	var lastErr error
	for _, plugin := range plugins {
		if _, err := plugin.Execute(r.Context(), req.Action, params); err != nil {
			lastErr = err
			p.logger.Debug("Failed to execute stack action plugin", "plugin", plugin.Name(), "action", req.Action, "error", err)
			continue
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
		return
	}

	p.logger.Error("Failed to execute stack action", "action", req.Action, "error", lastErr)
	http.Error(w, lastErr.Error(), http.StatusInternalServerError)
}

func stackActionCapability(action string) (core.Capability, bool) {
	switch action {
	case string(core.CapabilityStartStack):
		return core.CapabilityStartStack, true
	case string(core.CapabilityStopStack):
		return core.CapabilityStopStack, true
	case string(core.CapabilityRestartStack):
		return core.CapabilityRestartStack, true
	case string(core.CapabilityDisableStack):
		return core.CapabilityDisableStack, true
	case string(core.CapabilityEnableStack):
		return core.CapabilityEnableStack, true
	case string(core.CapabilityReconcileStack):
		return core.CapabilityReconcileStack, true
	case string(core.CapabilityRefreshStackImages):
		return core.CapabilityRefreshStackImages, true
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

		user, err := p.verifyAuthenticatedUser(r)
		if err != nil {
			p.logger.Warn("UI authentication rejected", "error", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), authenticatedUserContextKey{}, user)
		next(w, r.WithContext(ctx))
	}
}

func (p *UIPlugin) authenticatedUser(r *http.Request) string {
	if r == nil {
		return ""
	}
	user, _ := r.Context().Value(authenticatedUserContextKey{}).(string)
	return strings.TrimSpace(user)
}

func (p *UIPlugin) verifyAuthenticatedUser(r *http.Request) (string, error) {
	if verifyURL := strings.TrimSpace(p.cfg.AuthVerifyURL); verifyURL != "" {
		return p.verifyWithAuthEndpoint(r, verifyURL)
	}
	if !p.cfg.TrustAuthHeader {
		return "", fmt.Errorf("ui auth_verify_url is not configured and trust_auth_header is false")
	}

	user := strings.TrimSpace(r.Header.Get(p.cfg.AuthHeader))
	if user == "" {
		return "", fmt.Errorf("missing authenticated user header")
	}
	return user, nil
}

func (p *UIPlugin) verifyWithAuthEndpoint(r *http.Request, verifyURL string) (string, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, verifyURL, nil)
	if err != nil {
		return "", fmt.Errorf("create auth verification request: %w", err)
	}
	req.Header.Set("Cookie", r.Header.Get("Cookie"))
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	req.Header.Set("X-Forwarded-Method", r.Method)
	req.Header.Set("X-Forwarded-Uri", r.URL.RequestURI())
	req.Header.Set("X-Forwarded-Host", r.Host)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("verify session with auth endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("auth endpoint returned %s", resp.Status)
	}
	user := strings.TrimSpace(resp.Header.Get(p.cfg.AuthHeader))
	if user == "" {
		return "", fmt.Errorf("auth endpoint response missing %s", p.cfg.AuthHeader)
	}
	return user, nil
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
