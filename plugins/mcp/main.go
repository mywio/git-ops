package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Plugin interface (assume this matches your core's expected export)
type Plugin interface {
	Init(config map[string]string) error
	Start() error
	Stop() error
}

// MCPPlugin struct
type MCPPlugin struct {
	port      string
	targetDir string
	apiKey    string
}

// Exported for plugin loading (core loads symbol "MCPPlugin")
var MCP = &MCPPlugin{}

// Init loads config from env or passed map
func (p *MCPPlugin) Init(config map[string]string) error {
	p.port = os.Getenv("MCP_PORT")
	if p.port == "" {
		p.port = "8081"
	}
	p.targetDir = os.Getenv("TARGET_DIR")
	if p.targetDir == "" {
		p.targetDir = "/opt/stacks"
	}
	p.apiKey = os.Getenv("MCP_API_KEY")
	log.Printf("MCP Plugin Initialized: Port=%s, TargetDir=%s, Auth=%t", p.port, p.targetDir, p.apiKey != "")
	return nil
}

// Start runs the HTTP server in a goroutine
func (p *MCPPlugin) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/setup", authMiddleware(p.apiKey, p.handleSetup))
	mux.HandleFunc("/mcp/stacks", authMiddleware(p.apiKey, p.handleStacks))
	mux.HandleFunc("/mcp/services/", authMiddleware(p.apiKey, p.handleServices)) // /mcp/services/:repo
	mux.HandleFunc("/mcp/logs/", authMiddleware(p.apiKey, p.handleLogs))         // /mcp/logs/:repo/:service?lines=100&since=1h
	mux.HandleFunc("/mcp/health/", authMiddleware(p.apiKey, p.handleHealth))     // /mcp/health/:repo/:service

	go func() {
		log.Printf("MCP Server starting on :%s", p.port)
		if err := http.ListenAndServe(":"+p.port, mux); err != nil {
			log.Fatalf("MCP Server failed: %v", err)
		}
	}()
	return nil
}

// Stop (placeholder, since server runs in bg)
func (p *MCPPlugin) Stop() error {
	return nil
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
		"topics":       "Use 'ghops-remove' for cleanup.",
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
	p := &MCPPlugin{}
	p.Init(nil)
	p.Start()
	select {} // Block for testing
}
