package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPPlugin(t *testing.T) {
	// Verify Plugin variable implements interface
	var _ core.Plugin = Plugin

	assert.Equal(t, "mcp", Plugin.Name())

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()

	// Use ModuleManager as a dummy registry
	mgr := core.NewModuleManager(logger)

	err := Plugin.Init(ctx, logger, mgr)
	assert.NoError(t, err)

	err = Plugin.Start(ctx)
	assert.NoError(t, err)

	caps := Plugin.Capabilities()
	assert.Contains(t, caps, core.CapabilityMCP)
	assert.Contains(t, caps, core.CapabilityAPI)

	status := Plugin.Status()
	assert.Equal(t, core.StatusHealthy, status)

	err = Plugin.Stop(ctx)
	assert.NoError(t, err)
}

func TestListStacksReturnsOwnerRepoPaths(t *testing.T) {
	targetDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(targetDir, "acme", "api"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(targetDir, "acme", "web"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(targetDir, "other", "worker"), 0755))

	stacks, err := listStacks(targetDir)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"acme/api",
		"acme/web",
		"other/worker",
	}, stacks)
}

func TestHandleStacksListsActualStacks(t *testing.T) {
	targetDir := t.TempDir()
	stackDir := filepath.Join(targetDir, "acme", "api")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	modTime := time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(stackDir, modTime, modTime))

	plugin := &MCPPlugin{
		targetDir: targetDir,
		deployments: map[string]deploymentInfo{
			"acme/api": {
				FullName:  "acme/api",
				Owner:     "acme",
				Repo:      "api",
				Status:    "deploy_success",
				UpdatedAt: modTime,
			},
		},
		wg: &sync.WaitGroup{},
	}

	req := httptest.NewRequest(http.MethodGet, "/mcp/stacks", nil)
	resp := httptest.NewRecorder()

	plugin.handleStacks(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)

	var stacks []map[string]any
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &stacks))
	require.Len(t, stacks, 1)
	assert.Equal(t, "acme/api", stacks[0]["repo"])
	assert.Equal(t, modTime.Local().Format(time.RFC3339), stacks[0]["lastSync"])
	assert.Equal(t, "deploy_success", stacks[0]["deployStatus"])
}

func TestHandleLogsSupportsOwnerRepoStackNames(t *testing.T) {
	dockerDir := buildFakeDocker(t)
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dockerDir+string(os.PathListSeparator)+originalPath)

	targetDir := t.TempDir()
	stackDir := filepath.Join(targetDir, "acme", "api")
	require.NoError(t, os.MkdirAll(stackDir, 0755))

	expectedComposeFile := filepath.Join(stackDir, "docker-compose.yml")
	t.Setenv("FAKE_DOCKER_EXPECT_COMPOSE_FILE", expectedComposeFile)
	t.Setenv("FAKE_DOCKER_EXPECT_LOG_SERVICE", "web")
	t.Setenv("FAKE_DOCKER_EXPECT_LOG_TAIL", "12")
	t.Setenv("FAKE_DOCKER_LOG_OUTPUT", "line-1\nline-2\n")

	plugin := &MCPPlugin{
		targetDir: targetDir,
		wg:        &sync.WaitGroup{},
	}

	req := httptest.NewRequest(http.MethodGet, "/mcp/logs/acme/api/web?lines=12", nil)
	resp := httptest.NewRecorder()

	plugin.handleLogs(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	assert.Equal(t, "line-1\nline-2\n", body["logs"])
}

func TestHandleHealthSupportsOwnerRepoStackNames(t *testing.T) {
	dockerDir := buildFakeDocker(t)
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dockerDir+string(os.PathListSeparator)+originalPath)

	targetDir := t.TempDir()
	stackDir := filepath.Join(targetDir, "acme", "api")
	require.NoError(t, os.MkdirAll(stackDir, 0755))

	expectedComposeFile := filepath.Join(stackDir, "docker-compose.yml")
	t.Setenv("FAKE_DOCKER_EXPECT_PS_SERVICE", "web")
	t.Setenv("FAKE_DOCKER_COMPOSE_PS_OUTPUT", `[{"Name":"acme-api-web-1","Service":"web"}]`)
	t.Setenv("FAKE_DOCKER_EXPECT_CONTAINER", "acme-api-web-1")
	t.Setenv("FAKE_DOCKER_HEALTH_OUTPUT", `{"Status":"healthy"}`)
	t.Setenv("FAKE_DOCKER_EXPECT_COMPOSE_FILE", expectedComposeFile)

	plugin := &MCPPlugin{
		targetDir: targetDir,
		wg:        &sync.WaitGroup{},
	}

	req := httptest.NewRequest(http.MethodGet, "/mcp/health/acme/api/web", nil)
	resp := httptest.NewRecorder()

	plugin.handleHealth(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	assert.Equal(t, "healthy", body["Status"])
}

func TestHandleLogsRejectsMalformedOwnerRepoPath(t *testing.T) {
	dockerDir := buildFakeDocker(t)
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dockerDir+string(os.PathListSeparator)+originalPath)

	targetDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(targetDir, "acme", "api"), 0755))

	plugin := &MCPPlugin{
		targetDir: targetDir,
		wg:        &sync.WaitGroup{},
	}

	req := httptest.NewRequest(http.MethodGet, "/mcp/logs/acme/api/extra/web", nil)
	resp := httptest.NewRecorder()

	plugin.handleLogs(resp, req)

	require.Equal(t, http.StatusInternalServerError, resp.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "format:")
}

func TestHandleHealthRejectsMalformedOwnerRepoPath(t *testing.T) {
	dockerDir := buildFakeDocker(t)
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dockerDir+string(os.PathListSeparator)+originalPath)

	targetDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(targetDir, "acme", "api"), 0755))

	plugin := &MCPPlugin{
		targetDir: targetDir,
		wg:        &sync.WaitGroup{},
	}

	req := httptest.NewRequest(http.MethodGet, "/mcp/health/acme/api/extra/web", nil)
	resp := httptest.NewRecorder()

	plugin.handleHealth(resp, req)

	require.Equal(t, http.StatusInternalServerError, resp.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "format:")
}

func TestHandleServicesRejectsMalformedOwnerRepoPath(t *testing.T) {
	dockerDir := buildFakeDocker(t)
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dockerDir+string(os.PathListSeparator)+originalPath)

	targetDir := t.TempDir()
	stackDir := filepath.Join(targetDir, "acme", "api")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	t.Setenv("FAKE_DOCKER_EXPECT_COMPOSE_FILE", filepath.Join(stackDir, "docker-compose.yml"))

	plugin := &MCPPlugin{
		targetDir: targetDir,
		wg:        &sync.WaitGroup{},
	}

	req := httptest.NewRequest(http.MethodGet, "/mcp/services/acme/api/extra", nil)
	resp := httptest.NewRecorder()

	plugin.handleServices(resp, req)

	require.Equal(t, http.StatusInternalServerError, resp.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "format:")
}

func TestHandleServicesSupportsComposePsLineDelimitedJSON(t *testing.T) {
	dockerDir := buildFakeDocker(t)
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dockerDir+string(os.PathListSeparator)+originalPath)

	targetDir := t.TempDir()
	stackDir := filepath.Join(targetDir, "acme", "api")
	require.NoError(t, os.MkdirAll(stackDir, 0755))

	t.Setenv("FAKE_DOCKER_EXPECT_COMPOSE_FILE", filepath.Join(stackDir, "docker-compose.yml"))
	t.Setenv("FAKE_DOCKER_COMPOSE_PS_OUTPUT", "{\"Name\":\"web\"}\n{\"Name\":\"db\"}\n")

	plugin := &MCPPlugin{
		targetDir: targetDir,
		wg:        &sync.WaitGroup{},
	}

	req := httptest.NewRequest(http.MethodGet, "/mcp/services/acme/api", nil)
	resp := httptest.NewRecorder()

	plugin.handleServices(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)

	var services []map[string]any
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &services))
	require.Len(t, services, 2)
	assert.Equal(t, "web", services[0]["Name"])
	assert.Equal(t, "db", services[1]["Name"])
}

func buildFakeDocker(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	src := []byte(`package main

import (
	"fmt"
	"os"
	"strings"
)

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		fail("missing command")
	}

	switch os.Args[1] {
	case "compose":
		runCompose()
	case "inspect":
		runInspect()
	default:
		fail("unexpected command: " + strings.Join(os.Args[1:], " "))
	}
}

func runCompose() {
	args := os.Args[2:]
	if len(args) < 4 || args[0] != "-f" {
		fail("expected compose -f <file> ...")
	}

	composeFile := args[1]
	if expected := os.Getenv("FAKE_DOCKER_EXPECT_COMPOSE_FILE"); expected != "" && composeFile != expected {
		fail("unexpected compose file: " + composeFile)
	}

	switch args[2] {
	case "ps":
		if len(args) < 5 || args[3] != "--format" || args[4] != "json" {
			fail("expected compose ps --format json")
		}
		if expectService := os.Getenv("FAKE_DOCKER_EXPECT_PS_SERVICE"); expectService != "" {
			if len(args) != 6 || args[5] != expectService {
				fail("unexpected compose ps service: " + strings.Join(args[5:], " "))
			}
		}
		if output := os.Getenv("FAKE_DOCKER_COMPOSE_PS_OUTPUT"); output != "" {
			fmt.Print(output)
			return
		}
		fmt.Print("[{\"Name\":\"web\"}]\n")
	case "logs":
		expectService := os.Getenv("FAKE_DOCKER_EXPECT_LOG_SERVICE")
		if expectService == "" {
			fail("missing FAKE_DOCKER_EXPECT_LOG_SERVICE")
		}
		if idx := len(args) - 1; idx < 0 || args[idx] != expectService {
			fail("unexpected log service: " + args[len(args)-1])
		}
		if len(args) < 5 || args[3] != "--tail" {
			fail("expected logs --tail <lines>")
		}
		if expectTail := os.Getenv("FAKE_DOCKER_EXPECT_LOG_TAIL"); expectTail != "" && args[4] != expectTail {
			fail("unexpected log tail: " + args[4])
		}
		if output := os.Getenv("FAKE_DOCKER_LOG_OUTPUT"); output != "" {
			fmt.Print(output)
			return
		}
		fmt.Print("logs\n")
	default:
		fail("unexpected compose subcommand: " + args[2])
	}
}

func runInspect() {
	args := os.Args[2:]
	if len(args) != 3 || args[0] != "--format" || args[1] != "{{json .State.Health}}" {
		fail("expected docker inspect --format {{json .State.Health}} <container>")
	}
	expectContainer := os.Getenv("FAKE_DOCKER_EXPECT_CONTAINER")
	if expectContainer == "" {
		fail("missing FAKE_DOCKER_EXPECT_CONTAINER")
	}
	if args[2] != expectContainer {
		fail("unexpected container: " + args[2])
	}
	if output := os.Getenv("FAKE_DOCKER_HEALTH_OUTPUT"); output != "" {
		fmt.Print(output)
		return
	}
	fmt.Print("{\"Status\":\"healthy\"}")
}
`)

	srcPath := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(srcPath, src, 0644))

	exeName := "docker"
	if runtime.GOOS == "windows" {
		exeName = "docker.exe"
	}
	exePath := filepath.Join(dir, exeName)
	cmd := exec.Command("go", "build", "-o", exePath, srcPath)
	cmd.Env = append(os.Environ(), "GO111MODULE=off")
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "build fake docker: %s", string(output))
	return dir
}
