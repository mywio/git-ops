package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

//go:embed docs/*
//go:embed docs/**/*
var docsFS embed.FS

// MCPPlugin struct implements core.Plugin
type MCPPlugin struct {
	logger    *slog.Logger
	port      string
	targetDir string
	apiKey    string
	mux       *http.ServeMux
	wg        *sync.WaitGroup

	deployMu    sync.RWMutex
	deployments map[string]deploymentInfo
}

type mcpConfig struct {
	TargetDir string `yaml:"target_dir"`
	APIKey    string `yaml:"api_key"`
}

type deploymentInfo struct {
	FullName  string    `json:"full_name"`
	Owner     string    `json:"owner"`
	Repo      string    `json:"repo"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	Duration  string    `json:"duration,omitempty"`
	Source    string    `json:"source,omitempty"`
}
// Exported for plugin loading (core loads symbol "MCPPlugin" or similar)
var Plugin = &MCPPlugin{}

// Name returns the plugin name
func (p *MCPPlugin) Name() string {
	return "mcp"
}

// Init initializes the plugin with context, logger, and registry
func (p *MCPPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger
	if p.wg == nil {
		p.wg = &sync.WaitGroup{}
	}
	if p.deployments == nil {
		p.deployments = make(map[string]deploymentInfo)
	}

	if registry != nil {
		cfg := registry.GetConfig()
		if section, ok := cfg["mcp"]; ok {
			var mcfg mcpConfig
			if err := core.DecodeConfigSection(section, &mcfg); err != nil {
				p.logger.Warn("Invalid mcp config", "error", err)
			}
			p.targetDir = mcfg.TargetDir
			p.apiKey = mcfg.APIKey
		}
		p.mux = registry.GetMuxServer()
		registry.Subscribe("deploy_*", p.handleDeployEvent)
	} else {
		p.mux = http.NewServeMux()
	}
	if p.targetDir == "" {
		p.targetDir = "/opt/stacks"
	}

	p.logger.Info("MCP Plugin Initialized", "Port", p.port, "TargetDir", p.targetDir, "Auth", p.apiKey != "")
	return nil
}

// Start starts the plugin services
func (p *MCPPlugin) Start(ctx context.Context) error {
	//mux := http.NewServeMux()
	p.mux.HandleFunc("/mcp/setup", authMiddleware(p.apiKey, p.handleSetup))
	p.mux.HandleFunc("/mcp/stacks", authMiddleware(p.apiKey, p.handleStacks))
	p.mux.HandleFunc("/mcp/deployments", authMiddleware(p.apiKey, p.handleDeployments))
	p.mux.HandleFunc("/mcp/services/", authMiddleware(p.apiKey, p.handleServices)) // /mcp/services/:repo
	p.mux.HandleFunc("/mcp/logs/", authMiddleware(p.apiKey, p.handleLogs))         // /mcp/logs/:repo/:service?lines=100&since=1h
	p.mux.HandleFunc("/mcp/health/", authMiddleware(p.apiKey, p.handleHealth))     // /mcp/health/:repo/:service

	if docsSub, err := fs.Sub(docsFS, "docs"); err == nil {
		fileServer := http.FileServer(http.FS(docsSub))
		p.mux.HandleFunc("/mcp/docs", authMiddleware(p.apiKey, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/mcp/docs/", http.StatusMovedPermanently)
		}))
		p.mux.HandleFunc("/mcp/docs/", authMiddleware(p.apiKey, func(w http.ResponseWriter, r *http.Request) {
			http.StripPrefix("/mcp/docs/", fileServer).ServeHTTP(w, r)
		}))
	} else {
		p.logger.Warn("MCP docs not available", "error", err)
	}
	return nil
}

// Stop stops the plugin services
func (p *MCPPlugin) Stop(ctx context.Context) error {
	p.wg.Wait()
	p.logger.Info("MCP Server stopped")
	return nil
}

// Description returns a description of the plugin
func (p *MCPPlugin) Description() string {
	return "Model Context Protocol Plugin for deploying and debugging multiple Docker Compose stacks for LLM and AI applications"
}

// Capabilities returns the capabilities of the plugin
func (p *MCPPlugin) Capabilities() []core.Capability {
	// Assuming core.Capability is defined; return empty or specific if known
	return []core.Capability{core.CapabilityMCP, core.CapabilityAPI}
}

// Status returns the current status of the plugin
func (p *MCPPlugin) Status() core.ServiceStatus {
	// Assuming core.ServiceStatus has a State field; adjust as needed
	return core.StatusHealthy
}

// Execute executes an action with parameters
func (p *MCPPlugin) Execute(action string, params map[string]interface{}) (interface{}, error) {
	// This plugin is HTTP-based, so Execute might not be applicable; return not supported
	return nil, errors.New("execute not supported for MCP plugin")
}

// Auth middleware
func authMiddleware(key string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if key != "" && r.Header.Get("X-API-Key") != key {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// handleSetup - Current feature: returns setup instructions
func (p *MCPPlugin) handleSetup(w http.ResponseWriter, r *http.Request) {
	p.wg.Add(1)
	defer p.wg.Done()

	setup := map[string]string{
		"instructions": "To setup a repo: Add topic 'homelab-server-1', place docker-compose.yml at root, hooks in .deploy/pre and .deploy/post.",
		"topics":       "Use 'git-ops-remove' for cleanup.",
		"secrets":      "Fetched via plugins at runtime.",
	}
	jsonResponse(w, setup)
}

// handleStacks - New: list deployed repos/stacks
func (p *MCPPlugin) handleStacks(w http.ResponseWriter, r *http.Request) {
	p.wg.Add(1)
	defer p.wg.Done()

	repos, err := listDirs(p.targetDir)
	if err != nil {
		jsonError(w, err)
		return
	}
	stacks := []map[string]interface{}{}
	for _, repo := range repos {
		lastSync, _ := os.Stat(filepath.Join(p.targetDir, repo)) // Approx last reconcile
		entry := map[string]interface{}{
			"repo":     repo,
			"lastSync": lastSync.ModTime().Format(time.RFC3339),
			"status":   "deployed", // Could enhance with more checks
		}
		if info, ok := p.getDeploymentInfo(repo); ok {
			entry["lastDeploy"] = info.UpdatedAt.Format(time.RFC3339)
			entry["deployStatus"] = info.Status
		}
		stacks = append(stacks, entry)
	}
	jsonResponse(w, stacks)
}

func (p *MCPPlugin) handleDeployments(w http.ResponseWriter, r *http.Request) {
	p.wg.Add(1)
	defer p.wg.Done()

	p.deployMu.RLock()
	entries := make([]deploymentInfo, 0, len(p.deployments))
	for _, info := range p.deployments {
		entries = append(entries, info)
	}
	p.deployMu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})

	jsonResponse(w, entries)
}

// handleServices - New: list services for a repo
func (p *MCPPlugin) handleServices(w http.ResponseWriter, r *http.Request) {
	p.wg.Add(1)
	defer p.wg.Done()

	repo := strings.TrimPrefix(r.URL.Path, "/mcp/services/")
	if repo == "" {
		jsonError(w, errors.New("repo required"))
		return
	}
	output, err := dockerComposeExec(p.targetDir, repo, "ps", "--format", "json")
	if err != nil {
		jsonError(w, err)
		return
	}
	// Parse JSON from compose ps (array of service objects)
	var services []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &services); err != nil {
		jsonError(w, err)
		return
	}
	jsonResponse(w, services)
}

// handleLogs - New: get logs for service
func (p *MCPPlugin) handleLogs(w http.ResponseWriter, r *http.Request) {
	p.wg.Add(1)
	defer p.wg.Done()

	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/mcp/logs/"), "/", 2)
	if len(parts) != 2 {
		jsonError(w, errors.New("format: /logs/:repo/:service"))
		return
	}
	repo, service := parts[0], parts[1]
	lines := r.URL.Query().Get("lines")
	if lines == "" {
		lines = "100"
	}
	since := r.URL.Query().Get("since")
	args := []string{"logs", "--tail", lines}
	if since != "" {
		args = append(args, "--since", since)
	}
	args = append(args, service)
	output, err := dockerComposeExec(p.targetDir, repo, args...)
	if err != nil {
		jsonError(w, err)
		return
	}
	jsonResponse(w, map[string]string{"logs": output})
}

// handleHealth - New: health status for service
func (p *MCPPlugin) handleHealth(w http.ResponseWriter, r *http.Request) {
	p.wg.Add(1)
	defer p.wg.Done()

	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/mcp/health/"), "/", 2)
	if len(parts) != 2 {
		jsonError(w, errors.New("format: /health/:repo/:service"))
		return
	}
	repo, service := parts[0], parts[1]
	// Use docker inspect for health
	cmd := exec.Command("docker", "inspect", "--format", "{{json .State.Health}}", fmt.Sprintf("%s_%s_1", repo, service)) // Assume default container name
	output, err := cmd.Output()
	if err != nil {
		jsonError(w, err)
		return
	}
	var health map[string]interface{}
	if err := json.Unmarshal(output, &health); err != nil {
		jsonError(w, err)
		return
	}
	jsonResponse(w, health)
}

func (p *MCPPlugin) handleDeployEvent(ctx context.Context, event core.InternalEvent) {
	owner, _ := event.Details["owner"].(string)
	repo, _ := event.Details["repo"].(string)
	fullName := ""
	if owner != "" && repo != "" {
		fullName = fmt.Sprintf("%s/%s", owner, repo)
	} else if v, ok := event.Details["full_name"].(string); ok {
		fullName = v
	}
	if fullName == "" {
		return
	}

	info := deploymentInfo{
		FullName:  fullName,
		Owner:     owner,
		Repo:      repo,
		Status:    string(event.Type),
		Message:   event.String,
		UpdatedAt: event.Timestamp,
		Source:    event.Source,
	}
	if v, ok := event.Details["duration"].(string); ok {
		info.Duration = v
	}

	p.deployMu.Lock()
	p.deployments[fullName] = info
	p.deployMu.Unlock()
}

func (p *MCPPlugin) getDeploymentInfo(repo string) (deploymentInfo, bool) {
	p.deployMu.RLock()
	defer p.deployMu.RUnlock()
	for _, info := range p.deployments {
		if info.Repo == repo || strings.HasSuffix(info.FullName, "/"+repo) {
			return info, true
		}
	}
	return deploymentInfo{}, false
}

// Helpers
func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func listDirs(dir string) ([]string, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, f := range files {
		if f.IsDir() {
			dirs = append(dirs, f.Name())
		}
	}
	return dirs, nil
}

func dockerComposeExec(targetDir, repo string, args ...string) (string, error) {
	stackDir := filepath.Join(targetDir, repo)
	cmd := exec.Command("docker", append([]string{"compose", "-f", filepath.Join(stackDir, "docker-compose.yml")}, args...)...)
	cmd.Dir = stackDir
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// Main (for standalone testing; ignored in plugin mode)
func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	p := &MCPPlugin{}
	ctx := context.Background()
	p.Init(ctx, logger, nil) // nil registry for testing
	p.Start(ctx)
	select {} // Block for testing
}
