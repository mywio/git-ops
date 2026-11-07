package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validRecipe = `version: 1
checkout:
  submodules: recursive
  lfs: false
phases:
  build:
    - command: make
      args: [build]
      working_directory: .
      timeout: 30m
  deploy:
    - command: make
      args: [deploy]
      timeout: 10m
  verify:
    - command: make
      args: [verify]
      timeout: 2m
  rollback:
    - command: make
      args: [rollback]
      timeout: 10m
health:
  compose:
    files: [compose.yaml]
    project_name: my-app
    timeout: 2m
    interval: 5s
    allow_completed: [migrate]
`

func TestParseDeployRecipeStrictAndVersioned(t *testing.T) {
	recipe, err := parseDeployRecipe([]byte(validRecipe))
	require.NoError(t, err)
	assert.Equal(t, 1, recipe.Version)
	assert.Equal(t, 30*time.Minute, recipe.Phases.Build[0].Timeout.Duration)
	assert.Equal(t, "recursive", recipe.Checkout.Submodules)

	for name, replacement := range map[string]string{
		"unknown field": strings.Replace(validRecipe, "version: 1", "version: 1\nunknown: true", 1),
		"wrong version": strings.Replace(validRecipe, "version: 1", "version: 2", 1),
		"empty build":   strings.Replace(validRecipe, "  build:\n    - command: make\n      args: [build]\n      working_directory: .\n      timeout: 30m", "  build: []", 1),
		"traversal":     strings.Replace(validRecipe, "working_directory: .", "working_directory: ../outside", 1),
		"absolute":      strings.Replace(validRecipe, "working_directory: .", "working_directory: /tmp", 1),
		"bad duration":  strings.Replace(validRecipe, "timeout: 30m", "timeout: forever", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseDeployRecipe([]byte(replacement))
			require.Error(t, err)
		})
	}
}

func TestRecipeAllowsOmittedVerifyAndHealth(t *testing.T) {
	data := `version: 1
phases:
  build:
    - {command: make, args: [build], timeout: 1m}
  deploy:
    - {command: make, args: [deploy], timeout: 1m}
  rollback:
    - {command: make, args: [rollback], timeout: 1m}
`
	recipe, err := parseDeployRecipe([]byte(data))
	require.NoError(t, err)
	assert.Equal(t, "unverified", verificationLabel(recipe))
}

func TestEnvironmentIsAllowlistedAndLogSecretsAreRedacted(t *testing.T) {
	t.Setenv("LOCAL_DEPLOY_ALLOWED_TEST", "top-secret-value")
	t.Setenv("LOCAL_DEPLOY_BLOCKED_TEST", "must-not-pass")
	environment, secrets := recipeEnvironment([]string{"LOCAL_DEPLOY_ALLOWED_TEST"}, map[string]string{
		"GHOPS_OWNER": "acme",
	})
	assert.ElementsMatch(t, []string{"GHOPS_OWNER=acme", "LOCAL_DEPLOY_ALLOWED_TEST=top-secret-value"}, environment)

	var output bytes.Buffer
	writer := newBoundedLogWriter(&output, 1024, secrets)
	_, err := writer.Write([]byte("token=top-secret-value"))
	require.NoError(t, err)
	assert.Equal(t, "token=[REDACTED]", output.String())
	assert.NotContains(t, strings.Join(environment, "\n"), "LOCAL_DEPLOY_BLOCKED_TEST")
}

func TestContainedWorkingDirectoryRejectsSymlinkEscape(t *testing.T) {
	checkout := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(checkout, "escape")))

	_, err := containedWorkingDirectory(checkout, "escape")

	require.ErrorContains(t, err, "escapes checkout")
}

func TestDecodeLocalDeployConfigAcceptsEnvironmentStrings(t *testing.T) {
	root := t.TempDir()
	cfg, err := decodeLocalDeployConfig(map[string]any{
		"enabled": "true", "require_systemd": "false",
		"state_dir": filepath.Join(root, "state"), "cache_dir": filepath.Join(root, "cache"),
		"log_dir": filepath.Join(root, "logs"), "global_concurrency": "2",
		"max_log_size": "1MiB", "allowed_environment": "PATH, APP_TOKEN",
	})
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	assert.False(t, cfg.RequireSystemd)
	assert.Equal(t, 2, cfg.GlobalConcurrency)
	assert.Equal(t, int64(1<<20), cfg.MaxLogSize)
	assert.Equal(t, []string{"APP_TOKEN", "PATH"}, cfg.AllowedEnvironment)
}

func TestDecodeLocalDeployConfigRejectsUnknownFields(t *testing.T) {
	_, err := decodeLocalDeployConfig(map[string]any{"mystery": true})
	require.Error(t, err)
}

func TestComposeHealthAcceptsRunningAndAllowedCompletedServices(t *testing.T) {
	containers := []composeHealthContainer{
		{Service: "api", State: "running", Health: "healthy"},
		{Service: "migrate", State: "exited", ExitCode: 0},
	}
	assert.Empty(t, composeHealthProblem([]string{"api", "migrate"}, containers, map[string]bool{"migrate": true}))
	assert.Contains(t, composeHealthProblem([]string{"api", "migrate"}, containers, nil), "migrate")
}

func TestRunRecipeStepDoesNotInheritEnvironment(t *testing.T) {
	checkout := t.TempDir()
	t.Setenv("LOCAL_DEPLOY_BLOCKED_TEST", "must-not-pass")
	var output bytes.Buffer
	step := recipeStep{Command: "env", WorkingDirectory: ".", Timeout: recipeDuration{time.Second}}

	err := runRecipeStep(context.Background(), checkout, step, []string{"GHOPS_OWNER=acme"}, &output)

	require.NoError(t, err)
	assert.Contains(t, output.String(), "GHOPS_OWNER=acme")
	assert.NotContains(t, output.String(), "LOCAL_DEPLOY_BLOCKED_TEST")
}
func newPipelineTestPlugin(t *testing.T) (*LocalDeployPlugin, context.CancelFunc) {
	t.Helper()
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := defaultLocalDeployConfig()
	cfg.Enabled = true
	cfg.RequireSystemd = false
	cfg.StateDir = filepath.Join(root, "state")
	cfg.CacheDir = filepath.Join(root, "cache")
	cfg.LogDir = filepath.Join(root, "logs")
	plugin := &LocalDeployPlugin{
		cfg: cfg, ctx: ctx, cancel: cancel, sem: make(chan struct{}, 1),
		repositories: make(map[string]*repositoryController),
	}
	return plugin, cancel
}

func writeTestCheckout(t *testing.T, plugin *LocalDeployPlugin, sha, buildCommand, deployCommand, rollbackCommand string) string {
	t.Helper()
	checkout := plugin.checkoutPath("acme", "api", sha)
	require.NoError(t, os.MkdirAll(filepath.Join(checkout, ".git"), 0o750))
	recipe := fmt.Sprintf(`version: 1
phases:
  build:
    - {command: %s, timeout: 1m}
  deploy:
    - {command: %s, timeout: 1m}
  rollback:
    - {command: %s, args: [rollback.marker], timeout: 1m}
`, buildCommand, deployCommand, rollbackCommand)
	require.NoError(t, os.MkdirAll(filepath.Join(checkout, ".ghops"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, ".ghops", "deploy.yaml"), []byte(recipe), 0o600))
	return checkout
}

func waitForController(t *testing.T, controller *repositoryController) repositoryState {
	t.Helper()
	require.Eventually(t, func() bool {
		controller.mu.Lock()
		defer controller.mu.Unlock()
		return !controller.running
	}, 5*time.Second, 10*time.Millisecond)
	return controller.snapshot()
}

func TestBuildFailureDoesNotRollback(t *testing.T) {
	plugin, cancel := newPipelineTestPlugin(t)
	defer cancel()
	sha := "1111111111111111111111111111111111111111"
	checkout := writeTestCheckout(t, plugin, sha, "false", "true", "touch")

	require.NoError(t, plugin.queue(revisionRequest{Owner: "acme", Repo: "api", SHA: sha, Trigger: "test"}))
	state := waitForController(t, plugin.controller("acme", "api"))

	require.Len(t, state.History, 1)
	assert.Equal(t, "build", string(state.History[0].Phase))
	assert.Empty(t, state.History[0].RollbackStatus)
	assert.NoFileExists(t, filepath.Join(checkout, "rollback.marker"))
}

func TestDeployFailureRollsBackPreviousSuccessfulRevision(t *testing.T) {
	plugin, cancel := newPipelineTestPlugin(t)
	defer cancel()
	previous := "2222222222222222222222222222222222222222"
	candidate := "3333333333333333333333333333333333333333"
	previousCheckout := writeTestCheckout(t, plugin, previous, "true", "true", "touch")
	writeTestCheckout(t, plugin, candidate, "true", "false", "touch")
	controller := plugin.controller("acme", "api")
	controller.mu.Lock()
	controller.state.CurrentSHA = previous
	controller.state.Successful = []successfulRevision{{SHA: previous}}
	controller.mu.Unlock()

	require.NoError(t, plugin.queue(revisionRequest{Owner: "acme", Repo: "api", SHA: candidate, Trigger: "test"}))
	state := waitForController(t, controller)

	require.Len(t, state.History, 1)
	assert.Equal(t, previous, state.CurrentSHA)
	assert.Equal(t, "succeeded", state.History[0].RollbackStatus)
	assert.FileExists(t, filepath.Join(previousCheckout, "rollback.marker"))
}
func TestQueueCoalescesToNewestPendingRevision(t *testing.T) {
	plugin, cancel := newPipelineTestPlugin(t)
	defer cancel()
	controller := plugin.controller("acme", "api")
	controller.mu.Lock()
	controller.running = true
	controller.mu.Unlock()
	first := "4444444444444444444444444444444444444444"
	newest := "5555555555555555555555555555555555555555"

	require.NoError(t, plugin.queue(revisionRequest{Owner: "acme", Repo: "api", SHA: first, Trigger: "push"}))
	require.NoError(t, plugin.queue(revisionRequest{Owner: "acme", Repo: "api", SHA: newest, Trigger: "push"}))
	require.NoError(t, plugin.queue(revisionRequest{Owner: "acme", Repo: "api", SHA: newest, Trigger: "periodic"}))

	state := controller.snapshot()
	require.NotNil(t, state.Pending)
	assert.Equal(t, newest, state.Pending.SHA)
}

func TestStartMarksInterruptedExecutionFailed(t *testing.T) {
	plugin, cancel := newPipelineTestPlugin(t)
	defer cancel()
	sha := "6666666666666666666666666666666666666666"
	state := repositoryState{
		Owner: "acme", Repo: "api",
		Active: &deploymentExecution{
			ID: "interrupted", SHA: sha, Status: core.ExecutionStatusRunning,
			Phase: core.ExecutionStageDeploy, StartedAt: time.Now().Add(-time.Minute),
		},
	}
	require.NoError(t, persistRepositoryState(plugin.cfg.StateDir, state))

	require.NoError(t, plugin.Start(context.Background()))
	recovered := plugin.controller("acme", "api").snapshot()

	assert.Nil(t, recovered.Active)
	require.Len(t, recovered.History, 1)
	assert.Equal(t, core.ExecutionStatusFailed, recovered.History[0].Status)
	assert.Equal(t, "execution interrupted by process restart", recovered.History[0].Error)
}
