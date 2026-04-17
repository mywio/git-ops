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
	"path"
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
	// MCP protocol endpoint (JSON-RPC 2.0, used by Claude Code)
	p.mux.HandleFunc("/mcp", authMiddleware(p.apiKey, p.handleMCP))

	// Legacy REST endpoints
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
func (p *MCPPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	// This plugin is HTTP-based, so Execute might not be applicable; return not supported
	return nil, errors.New("execute not supported for MCP plugin")
}

type mcpConfigView struct {
	TargetDir   string      `json:"target_dir"`
	APIKey      core.Secret `json:"api_key"`
	AuthEnabled bool        `json:"auth_enabled"`
}

func (p *MCPPlugin) Config() any {
	return mcpConfigView{
		TargetDir:   p.targetDir,
		APIKey:      core.NewSecret(p.apiKey),
		AuthEnabled: p.apiKey != "",
	}
}

// authMiddleware accepts Authorization: Bearer <token> (used by Claude MCP)
// or X-API-Key: <token> (legacy REST clients).
func authMiddleware(key string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if key == "" {
			next(w, r)
			return
		}
		if bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); bearer == key {
			next(w, r)
			return
		}
		if r.Header.Get("X-API-Key") == key {
			next(w, r)
			return
		}
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
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

	repos, err := listStacks(p.targetDir)
	if err != nil {
		jsonError(w, err)
		return
	}
	stacks := []map[string]interface{}{}
	for _, repo := range repos {
		lastSync, _ := os.Stat(filepath.Join(p.targetDir, filepath.FromSlash(repo))) // Approx last reconcile
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

	repo, err := parseStackPath(r.URL.Path, "/mcp/services/")
	if err != nil {
		jsonError(w, err)
		return
	}
	output, err := dockerComposeExec(p.targetDir, repo, "ps", "--format", "json")
	if err != nil {
		jsonError(w, err)
		return
	}
	services, err := parseComposeServicesOutput(output)
	if err != nil {
		jsonError(w, err)
		return
	}
	jsonResponse(w, services)
}

// handleLogs - New: get logs for service
func (p *MCPPlugin) handleLogs(w http.ResponseWriter, r *http.Request) {
	p.wg.Add(1)
	defer p.wg.Done()

	repo, service, err := parseStackServicePath(r.URL.Path, "/mcp/logs/")
	if err != nil {
		jsonError(w, err)
		return
	}
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

	repo, service, err := parseStackServicePath(r.URL.Path, "/mcp/health/")
	if err != nil {
		jsonError(w, err)
		return
	}
	discoveryOutput, err := dockerComposeExec(p.targetDir, repo, "ps", "--format", "json", service)
	if err != nil {
		jsonError(w, err)
		return
	}
	containers, err := parseComposeServicesOutput(discoveryOutput)
	if err != nil {
		jsonError(w, err)
		return
	}
	if len(containers) == 0 {
		jsonError(w, fmt.Errorf("service %q not found in stack %q", service, repo))
		return
	}
	containerRef := composeContainerRef(containers[0])
	if containerRef == "" {
		jsonError(w, fmt.Errorf("service %q not found in stack %q", service, repo))
		return
	}
	// Use docker inspect for health on the discovered container reference.
	cmd := exec.Command("docker", "inspect", "--format", "{{json .State.Health}}", containerRef)
	healthOutput, err := cmd.Output()
	if err != nil {
		jsonError(w, err)
		return
	}
	var health map[string]interface{}
	if err := json.Unmarshal(healthOutput, &health); err != nil {
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
		Message:   event.Message,
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
		if info.FullName == repo || info.Repo == repo || strings.HasSuffix(info.FullName, "/"+repo) {
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

// parseStackServicePath expects exactly {owner}/{repo}/{service} after prefix.
func parseStackServicePath(rawPath, prefix string) (stack string, service string, err error) {
	trimmed := strings.TrimPrefix(rawPath, prefix)
	if trimmed == rawPath || trimmed == "" {
		return "", "", fmt.Errorf("format: %s{owner}/{repo}/{service}", prefix)
	}

	parts := strings.Split(trimmed, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", fmt.Errorf("format: %s{owner}/{repo}/{service}", prefix)
	}

	stack = path.Join(parts[0], parts[1])
	service = parts[2]
	return stack, service, nil
}

func parseComposeServicesOutput(output string) ([]map[string]interface{}, error) {
	var services []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &services); err == nil {
		return services, nil
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var service map[string]interface{}
		if err := json.Unmarshal([]byte(line), &service); err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	if len(services) == 0 {
		return nil, errors.New("invalid compose ps output")
	}
	return services, nil
}

func parseStackPath(rawPath, prefix string) (stack string, err error) {
	trimmed := strings.TrimPrefix(rawPath, prefix)
	if trimmed == rawPath || trimmed == "" {
		return "", fmt.Errorf("format: %s{owner}/{repo}", prefix)
	}

	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("format: %s{owner}/{repo}", prefix)
	}
	return trimmed, nil
}

func composeContainerRef(container map[string]interface{}) string {
	for _, key := range []string{"Name", "ID", "ContainerID"} {
		if value, ok := container[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func listStacks(dir string) ([]string, error) {
	owners, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var stacks []string
	for _, owner := range owners {
		if !owner.IsDir() {
			continue
		}

		repos, err := os.ReadDir(filepath.Join(dir, owner.Name()))
		if err != nil {
			return nil, err
		}
		for _, repo := range repos {
			if repo.IsDir() {
				stacks = append(stacks, path.Join(owner.Name(), repo.Name()))
			}
		}
	}
	sort.Strings(stacks)
	return stacks, nil
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
