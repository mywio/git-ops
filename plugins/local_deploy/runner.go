package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type boundedLogWriter struct {
	mu        sync.Mutex
	writer    io.Writer
	remaining int64
	truncated bool
	secrets   [][]byte
}

func newBoundedLogWriter(writer io.Writer, limit int64, secretValues []string) *boundedLogWriter {
	secrets := make([][]byte, 0, len(secretValues))
	for _, value := range secretValues {
		if len(value) >= 4 {
			secrets = append(secrets, []byte(value))
		}
	}
	return &boundedLogWriter{writer: writer, remaining: limit, secrets: secrets}
}

func (writer *boundedLogWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	originalLength := len(data)
	for _, secret := range writer.secrets {
		data = bytes.ReplaceAll(data, secret, []byte("[REDACTED]"))
	}
	if writer.remaining <= 0 {
		if !writer.truncated {
			writer.truncated = true
			_, _ = writer.writer.Write([]byte("\n--- log truncated at configured maximum size ---\n"))
		}
		return originalLength, nil
	}
	if int64(len(data)) > writer.remaining {
		data = data[:writer.remaining]
	}
	written, err := writer.writer.Write(data)
	writer.remaining -= int64(written)
	return originalLength, err
}

func recipeEnvironment(allowed []string, metadata map[string]string) ([]string, []string) {
	values := make(map[string]string, len(allowed)+len(metadata))
	secrets := make([]string, 0, len(allowed))
	for _, name := range allowed {
		if value, ok := os.LookupEnv(name); ok {
			values[name] = value
			secrets = append(secrets, value)
		}
	}
	for name, value := range metadata {
		values[name] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment, secrets
}

func runRecipeStep(ctx context.Context, checkout string, step recipeStep, environment []string, output io.Writer) error {
	workingDirectory, err := containedWorkingDirectory(checkout, step.WorkingDirectory)
	if err != nil {
		return err
	}
	executable, err := exec.LookPath(step.Command)
	if err != nil {
		return fmt.Errorf("required executable %q is unavailable: %w", step.Command, err)
	}

	stepContext, cancel := context.WithTimeout(ctx, step.Timeout.Duration)
	defer cancel()
	cmd := exec.Command(executable, step.Args...)
	cmd.Dir = workingDirectory
	cmd.Env = environment
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	_, _ = fmt.Fprintf(output, "$ %s\n", strings.Join(append([]string{step.Command}, step.Args...), " "))
	if err := cmd.Start(); err != nil {
		return err
	}
	wait := make(chan error, 1)
	go func() {
		wait <- cmd.Wait()
	}()

	select {
	case err := <-wait:
		if err != nil {
			return fmt.Errorf("command %q failed: %w", step.Command, err)
		}
		return nil
	case <-stepContext.Done():
		terminateProcessGroup(cmd.Process.Pid, wait)
		if errors.Is(stepContext.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("command %q timed out after %s", step.Command, step.Timeout.Duration)
		}
		return stepContext.Err()
	}
}

func terminateProcessGroup(pid int, wait <-chan error) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-wait: //nolint:errcheck // The caller returns the context cancellation error.
		return
	case <-timer.C:
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-wait //nolint:errcheck // A nonzero exit is expected after terminating the process group.
	}
}

func containedWorkingDirectory(checkout, relative string) (string, error) {
	if err := validateRelativePath(relative); err != nil {
		return "", err
	}
	root, err := filepath.Abs(checkout)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(root, filepath.Clean(relative))
	info, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("working directory %q: %w", relative, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory %q is not a directory", relative)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("working directory escapes checkout")
	}
	return resolvedCandidate, nil
}
