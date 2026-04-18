package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mywio/git-ops/pkg/core"
)

var (
	errComposeFileMissing          = errors.New("compose file missing")
	errTargetDirNotWritable        = errors.New("target directory not writable")
	errRuntimeFilesNotMaterialized = errors.New("runtime files not materialized")
)

const wrapPathErrorFormat = "%w: %s: %w"

var localComposeFilenames = []string{"compose.yaml", "docker-compose.yml"}

type composeCommandError struct {
	args   []string
	output string
	err    error
}

func (e *composeCommandError) Error() string {
	command := strings.Join(append([]string{"docker", "compose"}, e.args...), " ")
	if output := strings.TrimSpace(e.output); output != "" {
		return fmt.Sprintf("%s failed: %v: %s", command, e.err, output)
	}
	return fmt.Sprintf("%s failed: %v", command, e.err)
}

func (e *composeCommandError) Unwrap() error {
	return e.err
}

var executeComposeCommand = func(repoLocalPath string, cmdEnv, runtimeFileEnv []string, args ...string) error {
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = repoLocalPath

	if len(cmdEnv) > 0 || len(runtimeFileEnv) > 0 {
		cmd.Env = append(os.Environ(), cmdEnv...)
		cmd.Env = append(cmd.Env, runtimeFileEnv...)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &composeCommandError{
			args:   append([]string(nil), args...),
			output: string(output),
			err:    err,
		}
	}
	return nil
}

func runComposePreflight(repoLocalPath string, runtimeFileEnv []string) error {
	// Verify a compose file exists; Docker Compose discovers it by name at runtime.
	if _, err := localComposeFilePath(repoLocalPath); err != nil {
		return err
	}

	if err := ensureDirectoryWritable(repoLocalPath); err != nil {
		return err
	}
	if err := validateRuntimeFileEnv(runtimeFileEnv); err != nil {
		return err
	}
	return nil
}

func localComposeFilePath(repoLocalPath string) (string, error) {
	for _, filename := range localComposeFilenames {
		composePath := filepath.Join(repoLocalPath, filename)
		info, err := os.Stat(composePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf(wrapPathErrorFormat, errComposeFileMissing, composePath, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("%w: %s is a directory", errComposeFileMissing, composePath)
		}
		return composePath, nil
	}

	return "", fmt.Errorf("%w: %s", errComposeFileMissing, filepath.Join(repoLocalPath, localComposeFilenames[0]))
}

func ensureDirectoryWritable(dir string) error {
	file, err := os.CreateTemp(dir, ".preflight-*")
	if err != nil {
		return fmt.Errorf(wrapPathErrorFormat, errTargetDirNotWritable, dir, err)
	}
	path := file.Name()
	if closeErr := file.Close(); closeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf(wrapPathErrorFormat, errTargetDirNotWritable, dir, closeErr)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf(wrapPathErrorFormat, errTargetDirNotWritable, dir, err)
	}
	return nil
}

func validateRuntimeFileEnv(runtimeFileEnv []string) error {
	for _, entry := range runtimeFileEnv {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			return fmt.Errorf("%w: invalid runtime file env %q", errRuntimeFilesNotMaterialized, entry)
		}

		info, err := os.Stat(value)
		if err != nil {
			return fmt.Errorf(wrapPathErrorFormat, errRuntimeFilesNotMaterialized, key, err)
		}
		if info.IsDir() {
			return fmt.Errorf("%w: %s points to directory %s", errRuntimeFilesNotMaterialized, key, value)
		}
	}
	return nil
}

func classifyFailure(err error, stage core.ExecutionStage) core.FailureClass {
	if err == nil {
		return ""
	}

	var composeErr *composeCommandError
	if errors.As(err, &composeErr) && looksLikeComposeDependencyFailure(composeErr.output) {
		return core.FailureClassDependency
	}

	switch {
	case errors.Is(err, errComposeFileMissing),
		errors.Is(err, errTargetDirNotWritable),
		errors.Is(err, errRuntimeFilesNotMaterialized):
		return core.FailureClassValidation
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return core.FailureClassTransient
	case errors.Is(err, exec.ErrNotFound):
		return core.FailureClassDependency
	}

	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return core.FailureClassDependency
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		switch stage {
		case core.ExecutionStageComposeUp, core.ExecutionStageComposeDown:
			return core.FailureClassPermanent
		}
	}

	return core.FailureClassUnknown
}

func looksLikeComposeDependencyFailure(output string) bool {
	text := strings.ToLower(strings.TrimSpace(output))
	if text == "" {
		return false
	}

	switch {
	case strings.Contains(text, "compose") && strings.Contains(text, "not a docker command"):
		return true
	case strings.Contains(text, "unknown command") && strings.Contains(text, "compose"):
		return true
	case strings.Contains(text, "docker-compose plugin") && strings.Contains(text, "not found"):
		return true
	}

	return false
}
