package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

// MCPPlugin struct implements core.Plugin
type MCPPlugin struct {
	port      string
	targetDir string
	apiKey    string
	logger    *slog.Logger
	server    *http.Server
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

	// Load config from env (or could use registry if it provides config)
	p.port = os.Getenv("MCP_PORT")
	if p.port == "" {
		p.port = "8081"
	}
	p.targetDir = os.Getenv("TARGET_DIR")
	if p.targetDir == "" {
		p.targetDir = "/opt/stacks"
	}
	p.apiKey = os.Getenv("MCP_API_KEY")

	p.logger.Info("MCP Plugin Initialized", "Port", p.port, "TargetDir", p.targetDir, "Auth", p.apiKey != "")
	return nil
}

// Start starts the plugin services
func (p *MCPPlugin) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/setup", authMiddleware(p.apiKey, p.handleSetup))
	mux.HandleFunc("/mcp/stacks", authMiddleware(p.apiKey, p.handleStacks))
	mux.HandleFunc("/mcp/services/", authMiddleware(p.apiKey, p.handleServices)) // /mcp/services/:repo
	mux.HandleFunc("/mcp/logs/", authMiddleware(p.apiKey, p.handleLogs))         // /mcp/logs/:repo/:service?lines=100&since=1h
	mux.HandleFunc("/mcp/health/", authMiddleware(p.apiKey, p.handleHealth))     // /mcp/health/:repo/:service

	p.server = &http.Server{
		Addr:    ":" + p.port,
		Handler: mux,
	}

	go func() {
		p.logger.Info("MCP Server starting", "port", p.port)
		if err := p.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.logger.Error("MCP Server failed", "error", err)
		}
	}()

	return nil
}

// Stop stops the plugin services
func (p *MCPPlugin) Stop(ctx context.Context) error {
	if p.server != nil {
		if err := p.server.Shutdown(ctx); err != nil {
			p.logger.Error("MCP Server shutdown failed", "error", err)
			return err
		}
		p.logger.Info("MCP Server stopped")
	}
	return nil
}

// Description returns a description of the plugin
func (p *MCPPlugin) Description() string {
	return "Model Context Protocol Plugin for deploying and debugging multiple Docker Compose stacks for LLM and AI applications"
}

// Capabilities returns the capabilities of the plugin
func (p *MCPPlugin) Capabilities() []core.Capability {
	// Assuming core.Capability is defined; return empty or specific if known
	return []core.Capability{}
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
	setup := map[string]string{
		"instructions": "To setup a repo: Add topic 'homelab-server-1', place docker-compose.yml at root, hooks in .deploy/pre and .deploy/post.",
		"topics":       "Use 'git-ops-remove' for cleanup.",
		"secrets":      "Fetched via plugins at runtime.",
	}
	jsonResponse(w, setup)
}

// handleStacks - New: list deployed repos/stacks
func (p *MCPPlugin) handleStacks(w http.ResponseWriter, r *http.Request) {
	repos, err := listDirs(p.targetDir)
	if err != nil {
		jsonError(w, err)
		return
	}
	stacks := []map[string]string{}
	for _, repo := range repos {
		lastSync, _ := os.Stat(filepath.Join(p.targetDir, repo)) // Approx last reconcile
		stacks = append(stacks, map[string]string{
			"repo":     repo,
			"lastSync": lastSync.ModTime().Format(time.RFC3339),
			"status":   "deployed", // Could enhance with more checks
		})
	}
	jsonResponse(w, stacks)
}

// handleServices - New: list services for a repo
func (p *MCPPlugin) handleServices(w http.ResponseWriter, r *http.Request) {
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
