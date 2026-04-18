package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	mcpProtocolVersion = "2024-11-05"
	mcpDockerFormatArg = "--format"
	mcpDockerJSONArg   = "json"
)

// ── JSON-RPC 2.0 wire types ───────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"` // nil when absent (notification)
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// ── MCP types ─────────────────────────────────────────────────────────────────

type mcpTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// ── Tool registry ─────────────────────────────────────────────────────────────

func (p *MCPPlugin) mcpTools() []mcpTool {
	return []mcpTool{
		{
			Name:        "list_stacks",
			Description: "List all deployed git-ops stacks with their last sync time and deployment status.",
			InputSchema: jsonObject(nil, nil),
		},
		{
			Name:        "list_deployments",
			Description: "List recent deployment events with status, timing, and source.",
			InputSchema: jsonObject(nil, nil),
		},
		{
			Name:        "get_services",
			Description: "List running Docker Compose services for a specific stack.",
			InputSchema: jsonObject(map[string]interface{}{
				"repo": prop("string", "Repository name (e.g. myorg/my-app or just my-app)"),
			}, []string{"repo"}),
		},
		{
			Name:        "get_logs",
			Description: "Get container logs for a specific service in a stack.",
			InputSchema: jsonObject(map[string]interface{}{
				"repo":    prop("string", "Repository name"),
				"service": prop("string", "Docker Compose service name"),
				"lines":   prop("string", "Number of log lines to return (default: 100)"),
				"since":   prop("string", "Return logs since this duration (e.g. 1h, 30m)"),
			}, []string{"repo", "service"}),
		},
		{
			Name:        "get_health",
			Description: "Get Docker health check status for a service in a stack.",
			InputSchema: jsonObject(map[string]interface{}{
				"repo":    prop("string", "Repository name"),
				"service": prop("string", "Docker Compose service name"),
			}, []string{"repo", "service"}),
		},
		{
			Name:        "get_setup_info",
			Description: "Get instructions for adding a new repository to git-ops.",
			InputSchema: jsonObject(nil, nil),
		},
	}
}

// ── MCP HTTP handler ──────────────────────────────────────────────────────────

func (p *MCPPlugin) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMCPError(w, nil, -32700, "Parse error", err.Error())
		return
	}

	if req.JSONRPC != "2.0" {
		writeMCPError(w, req.ID, -32600, "Invalid Request", "jsonrpc must be \"2.0\"")
		return
	}

	// Notifications have no id — server MUST NOT reply.
	if req.ID == nil && strings.HasPrefix(req.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		writeMCPResult(w, req.ID, map[string]interface{}{
			"protocolVersion": mcpProtocolVersion,
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "git-ops",
				"version": "1.0.0",
			},
			"instructions": "Use these tools to inspect and manage Docker Compose stacks deployed by git-ops. Start with list_stacks to see what is running.",
		})

	case "ping":
		writeMCPResult(w, req.ID, map[string]interface{}{})

	case "tools/list":
		writeMCPResult(w, req.ID, map[string]interface{}{
			"tools": p.mcpTools(),
		})

	case "tools/call":
		p.handleToolCall(w, r.Context(), req)

	default:
		writeMCPError(w, req.ID, -32601, "Method not found", req.Method)
	}
}

// ── Tool dispatch ─────────────────────────────────────────────────────────────

func (p *MCPPlugin) handleToolCall(w http.ResponseWriter, ctx context.Context, req rpcRequest) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeMCPError(w, req.ID, -32602, "Invalid params", err.Error())
		return
	}

	result, toolErr := p.dispatchTool(ctx, params.Name, params.Arguments)
	if toolErr != nil {
		writeMCPResult(w, req.ID, mcpToolResult{
			Content: []mcpContent{{Type: "text", Text: toolErr.Error()}},
			IsError: true,
		})
		return
	}
	writeMCPResult(w, req.ID, result)
}

func (p *MCPPlugin) dispatchTool(_ context.Context, name string, args map[string]interface{}) (mcpToolResult, error) {
	switch name {
	case "list_stacks":
		return p.dispatchListStacks()

	case "list_deployments":
		return p.dispatchListDeployments()

	case "get_services":
		return p.dispatchGetServices(toolStringArg(args, "repo"))

	case "get_logs":
		return p.dispatchGetLogs(args)

	case "get_health":
		return p.dispatchGetHealth(args)

	case "get_setup_info":
		return textResult(map[string]string{
			"instructions": "Add one of the configured TOPIC_FILTER topics to your GitHub repo and place docker-compose.yml at the root.",
			"hooks":        "Add optional hook scripts in .deploy/pre/ (run before compose up) and .deploy/post/ (run after).",
			"removal":      "Add the 'git-ops-remove' topic or archive the repository to trigger teardown.",
			"secrets":      "Secrets are injected at runtime by configured plugins (Google Secret Manager, env_forwarder, file_forwarder).",
		})

	default:
		return mcpToolResult{}, fmt.Errorf("unknown tool: %s", name)
	}
}

type mcpStackEntry struct {
	Repo         string `json:"repo"`
	LastSync     string `json:"last_sync,omitempty"`
	DeployStatus string `json:"deploy_status,omitempty"`
	LastDeploy   string `json:"last_deploy,omitempty"`
}

func toolStringArg(args map[string]interface{}, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func (p *MCPPlugin) dispatchListStacks() (mcpToolResult, error) {
	repos, err := listStacks(p.targetDir)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("list stacks: %w", err)
	}
	entries := make([]mcpStackEntry, 0, len(repos))
	for _, repo := range repos {
		entries = append(entries, p.stackEntry(repo))
	}
	return textResult(entries)
}

func (p *MCPPlugin) stackEntry(repo string) mcpStackEntry {
	entry := mcpStackEntry{Repo: repo}
	if fi, err := os.Stat(filepath.Join(p.targetDir, filepath.FromSlash(repo))); err == nil {
		entry.LastSync = fi.ModTime().Format(time.RFC3339)
	}
	if info, ok := p.getDeploymentInfo(repo); ok {
		entry.DeployStatus = info.Status
		entry.LastDeploy = info.UpdatedAt.Format(time.RFC3339)
	}
	return entry
}

func (p *MCPPlugin) dispatchListDeployments() (mcpToolResult, error) {
	p.deployMu.RLock()
	entries := make([]deploymentInfo, 0, len(p.deployments))
	for _, info := range p.deployments {
		entries = append(entries, info)
	}
	p.deployMu.RUnlock()
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})
	return textResult(entries)
}

func (p *MCPPlugin) dispatchGetServices(repo string) (mcpToolResult, error) {
	if repo == "" {
		return mcpToolResult{}, fmt.Errorf("repo is required")
	}
	output, err := dockerComposeExec(p.targetDir, repo, "ps", mcpDockerFormatArg, mcpDockerJSONArg)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("docker compose ps: %w", err)
	}
	return textResultOrRawJSONOutput(output)
}

func (p *MCPPlugin) dispatchGetLogs(args map[string]interface{}) (mcpToolResult, error) {
	repo := toolStringArg(args, "repo")
	service := toolStringArg(args, "service")
	if repo == "" || service == "" {
		return mcpToolResult{}, fmt.Errorf("repo and service are required")
	}
	dockerArgs := []string{"logs", "--tail", logLineLimit(toolStringArg(args, "lines"))}
	if since := toolStringArg(args, "since"); since != "" {
		dockerArgs = append(dockerArgs, "--since", since)
	}
	dockerArgs = append(dockerArgs, service)
	output, err := dockerComposeExec(p.targetDir, repo, dockerArgs...)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("docker compose logs: %w", err)
	}
	return mcpToolResult{Content: []mcpContent{{Type: "text", Text: output}}}, nil
}

func logLineLimit(lines string) string {
	if strings.TrimSpace(lines) == "" {
		return "100"
	}
	return lines
}

func (p *MCPPlugin) dispatchGetHealth(args map[string]interface{}) (mcpToolResult, error) {
	repo := toolStringArg(args, "repo")
	service := toolStringArg(args, "service")
	if repo == "" || service == "" {
		return mcpToolResult{}, fmt.Errorf("repo and service are required")
	}
	output, err := dockerComposeExec(p.targetDir, repo, "ps", mcpDockerFormatArg, mcpDockerJSONArg, service)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("docker compose ps: %w", err)
	}
	containers, ok := parseHealthContainers(output)
	if !ok {
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: output}}}, nil
	}
	if len(containers) == 0 {
		return mcpToolResult{}, fmt.Errorf("service %q not found in stack %q", service, repo)
	}
	return textResult(containers)
}

func textResultOrRawJSONOutput(output string) (mcpToolResult, error) {
	var services []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &services); err != nil {
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: output}}}, nil
	}
	return textResult(services)
}

func parseHealthContainers(output string) ([]map[string]interface{}, bool) {
	var containers []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &containers); err == nil {
		return containers, true
	}

	var single map[string]interface{}
	if err := json.Unmarshal([]byte(output), &single); err != nil {
		return nil, false
	}
	return []map[string]interface{}{single}, true
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func textResult(data interface{}) (mcpToolResult, error) {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return mcpToolResult{}, err
	}
	return mcpToolResult{Content: []mcpContent{{Type: "text", Text: string(b)}}}, nil
}

func writeMCPResult(w http.ResponseWriter, id json.RawMessage, result interface{}) {
	writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeMCPError(w http.ResponseWriter, id json.RawMessage, code int, message, data string) {
	writeRPC(w, rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message, Data: data},
	})
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Default().Error("failed to encode MCP RPC response", "error", err)
	}
}

// jsonObject builds a JSON Schema object node.
func jsonObject(properties map[string]interface{}, required []string) map[string]interface{} {
	schema := map[string]interface{}{"type": "object"}
	if len(properties) > 0 {
		schema["properties"] = properties
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func prop(typ, description string) map[string]interface{} {
	return map[string]interface{}{"type": typ, "description": description}
}
