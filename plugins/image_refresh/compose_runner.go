package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mywio/git-ops/pkg/core"
)

type composeExecutionEnv struct {
	ComposeEnv     []string
	RuntimeFileEnv []string
	Cleanup        func()
}

type composeRunStatus string

const (
	composeRunStatusNoUpdate         composeRunStatus = "no_update"
	composeRunStatusUpdated          composeRunStatus = "updated"
	composeRunStatusRetryableFailure composeRunStatus = "retryable_failure"
	composeRunStatusTerminalFailure  composeRunStatus = "terminal_failure"
)

type composeRunResult struct {
	Status  composeRunStatus
	Err     error
	Updated bool
}

var runImageRefreshComposeCommand = func(stackPath string, composeEnv, runtimeFileEnv []string, args ...string) ([]byte, error) {
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = stackPath
	cmd.Env = append(os.Environ(), composeEnv...)
	cmd.Env = append(cmd.Env, runtimeFileEnv...)
	return cmd.CombinedOutput()
}

var inspectDockerImageIdentity = func(imageRef string) (string, error) {
	cmd := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", imageRef)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func runImageRefreshAttempt(ctx context.Context, registry core.PluginRegistry, logger *slog.Logger, req refreshJobRequest) composeRunResult {
	execEnv, err := prepareImageRefreshComposeEnvironment(ctx, registry, req.Owner, req.Repo, logger)
	if err != nil {
		return composeRunResult{Status: composeRunStatusTerminalFailure, Err: err}
	}
	defer execEnv.Cleanup()

	if err := runImageRefreshPreflight(req.Key.StackPath, execEnv.RuntimeFileEnv); err != nil {
		return composeRunResult{Status: composeRunStatusTerminalFailure, Err: err}
	}

	updated, err := detectUpdatedImages(req.Key.StackPath, execEnv)
	if err != nil {
		return composeRunResult{Status: composeRunStatusRetryableFailure, Err: err}
	}
	if !updated {
		return composeRunResult{Status: composeRunStatusNoUpdate}
	}

	if err := runComposeUp(req.Key.StackPath, execEnv); err != nil {
		return composeRunResult{Status: composeRunStatusTerminalFailure, Err: err, Updated: true}
	}
	return composeRunResult{Status: composeRunStatusUpdated, Updated: true}
}

func prepareImageRefreshComposeEnvironment(ctx context.Context, registry core.PluginRegistry, owner, repo string, logger *slog.Logger) (composeExecutionEnv, error) {
	_ = logger

	composeEnv, secretSources, err := collectImageRefreshSecrets(ctx, registry, owner, repo, logger)
	if err != nil {
		return composeExecutionEnv{}, err
	}

	runtimeFiles, err := collectImageRefreshRuntimeFiles(ctx, registry, owner, repo, logger, secretSources)
	if err != nil {
		return composeExecutionEnv{}, err
	}

	runtimeFileEnv := []string{}
	cleanup := func() {}
	if len(runtimeFiles) > 0 {
		runtimeFileEnv, cleanup, err = materializeImageRefreshRuntimeFiles(runtimeFiles)
		if err != nil {
			return composeExecutionEnv{}, err
		}
	}

	return composeExecutionEnv{ComposeEnv: composeEnv, RuntimeFileEnv: runtimeFileEnv, Cleanup: cleanup}, nil
}

func collectLocalImageIdentities(stackPath string, execEnv composeExecutionEnv) (map[string]string, error) {
	output, err := runImageRefreshComposeCommand(stackPath, execEnv.ComposeEnv, execEnv.RuntimeFileEnv, "config", "--images")
	if err != nil {
		return nil, err
	}

	identities := map[string]string{}
	for _, line := range strings.Split(string(output), "\n") {
		imageRef := strings.TrimSpace(line)
		if imageRef == "" {
			continue
		}
		identity, err := inspectDockerImageIdentity(imageRef)
		if err != nil {
			return nil, err
		}
		identities[imageRef] = identity
	}
	return identities, nil
}

func runComposePull(stackPath string, execEnv composeExecutionEnv) error {
	_, err := runImageRefreshComposeCommand(stackPath, execEnv.ComposeEnv, execEnv.RuntimeFileEnv, "pull")
	return err
}

func runComposeUp(stackPath string, execEnv composeExecutionEnv) error {
	_, err := runImageRefreshComposeCommand(stackPath, execEnv.ComposeEnv, execEnv.RuntimeFileEnv, "up", "-d", "--remove-orphans")
	return err
}

func detectUpdatedImages(stackPath string, execEnv composeExecutionEnv) (bool, error) {
	before, err := collectLocalImageIdentities(stackPath, execEnv)
	if err != nil {
		return false, err
	}
	if err := runComposePull(stackPath, execEnv); err != nil {
		return false, err
	}
	after, err := collectLocalImageIdentities(stackPath, execEnv)
	if err != nil {
		return false, err
	}
	return imageIdentitySetsChanged(before, after), nil
}

func imageIdentitySetsChanged(before, after map[string]string) bool {
	if len(before) != len(after) {
		return true
	}
	for ref, beforeID := range before {
		if after[ref] != beforeID {
			return true
		}
	}
	return false
}

func collectImageRefreshSecrets(ctx context.Context, registry core.PluginRegistry, owner, repo string, logger *slog.Logger) ([]string, map[string]string, error) {
	_ = logger

	secretPlugins := []core.Plugin{}
	if registry != nil {
		secretPlugins = registry.GetPluginsWithCapability(core.CapabilitySecrets)
	}

	secretValues := map[string]string{}
	secretSources := map[string]string{}
	for _, plugin := range secretPlugins {
		result, err := plugin.Execute(ctx, "get_secrets", map[string]interface{}{"owner": owner, "repo": repo})
		if err != nil {
			return nil, nil, err
		}
		secrets, ok := result.(map[string]string)
		if !ok {
			continue
		}
		for key, value := range secrets {
			if _, exists := secretValues[key]; exists {
				continue
			}
			secretValues[key] = value
			secretSources[key] = plugin.Name()
		}
	}

	keys := make([]string, 0, len(secretValues))
	for key := range secretValues {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	composeEnv := make([]string, 0, len(keys))
	for _, key := range keys {
		composeEnv = append(composeEnv, fmt.Sprintf("%s=%s", key, secretValues[key]))
	}
	return composeEnv, secretSources, nil
}

func collectImageRefreshRuntimeFiles(ctx context.Context, registry core.PluginRegistry, owner, repo string, logger *slog.Logger, existingSources map[string]string) ([]core.RuntimeFile, error) {
	_ = logger

	runtimePlugins := []core.Plugin{}
	if registry != nil {
		runtimePlugins = registry.GetPluginsWithCapability(core.CapabilityRuntimeFiles)
	}

	files := make([]core.RuntimeFile, 0)
	runtimeSources := map[string]string{}
	for _, plugin := range runtimePlugins {
		result, err := plugin.Execute(ctx, "get_runtime_files", map[string]interface{}{"owner": owner, "repo": repo})
		if err != nil {
			return nil, err
		}
		runtimeFiles, ok := result.([]core.RuntimeFile)
		if !ok {
			return nil, fmt.Errorf("plugin %s returned unexpected runtime file payload", plugin.Name())
		}
		for _, file := range runtimeFiles {
			key := strings.TrimSpace(file.EnvKey)
			if key == "" || strings.Contains(key, "=") {
				return nil, fmt.Errorf("plugin %s returned invalid env key %q", plugin.Name(), key)
			}
			if _, exists := existingSources[key]; exists {
				continue
			}
			if _, exists := runtimeSources[key]; exists {
				continue
			}
			file.EnvKey = key
			files = append(files, file)
			runtimeSources[key] = plugin.Name()
		}
	}
	return files, nil
}

func materializeImageRefreshRuntimeFiles(files []core.RuntimeFile) ([]string, func(), error) {
	runtimeDir, err := os.MkdirTemp("", "gitops-image-refresh-runtime-*")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() {
		_ = os.RemoveAll(runtimeDir)
	}

	envToPath := map[string]string{}
	for index, file := range files {
		filename := strings.TrimSpace(file.Filename)
		if filename == "" {
			filename = fmt.Sprintf("runtime_file_%d", index)
		}
		filename = filepath.Base(filename)
		if filename == "" || filename == "." {
			cleanup()
			return nil, func() {}, fmt.Errorf("invalid runtime file name for %s", file.EnvKey)
		}
		targetPath := filepath.Join(runtimeDir, fmt.Sprintf("%02d_%s", index, filename))
		mode := os.FileMode(file.Mode & 0o777)
		if mode == 0 {
			mode = 0o600
		}
		if err := os.WriteFile(targetPath, file.Content, mode); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		envToPath[file.EnvKey] = targetPath
	}

	keys := make([]string, 0, len(envToPath))
	for key := range envToPath {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, fmt.Sprintf("%s=%s", key, envToPath[key]))
	}
	return env, cleanup, nil
}

func runImageRefreshPreflight(stackPath string, runtimeFileEnv []string) error {
	composePath := filepath.Join(stackPath, "docker-compose.yml")
	info, err := os.Stat(composePath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("compose file missing: %s", composePath)
	}
	for _, entry := range runtimeFileEnv {
		_, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("invalid runtime file env %q", entry)
		}
		if _, err := os.Stat(value); err != nil {
			return err
		}
	}
	return nil
}
