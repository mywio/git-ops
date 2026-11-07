package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var fullCommitSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

const checkoutTimeout = 10 * time.Minute

func (plugin *LocalDeployPlugin) prepareCheckout(ctx context.Context, request revisionRequest, output io.Writer) (string, *deployRecipe, error) {
	if !fullCommitSHA.MatchString(request.SHA) {
		return "", nil, fmt.Errorf("commit SHA must contain exactly 40 hexadecimal characters")
	}
	mirror := filepath.Join(plugin.cfg.CacheDir, "mirrors", request.Owner, request.Repo+".git")
	checkout := plugin.checkoutPath(request.Owner, request.Repo, request.SHA)
	if _, err := os.Stat(filepath.Join(checkout, ".git")); err == nil {
		recipe, err := readRecipe(checkout)
		return checkout, recipe, err
	}
	if err := os.MkdirAll(filepath.Dir(mirror), 0o750); err != nil {
		return "", nil, err
	}
	if _, err := os.Stat(mirror); os.IsNotExist(err) {
		remote := fmt.Sprintf("https://github.com/%s/%s.git", request.Owner, request.Repo)
		if err := runGit(ctx, output, "", "clone", "--mirror", remote, mirror); err != nil {
			return "", nil, fmt.Errorf("create mirror: %w", err)
		}
	} else if err != nil {
		return "", nil, err
	} else if err := runGit(ctx, output, "", "--git-dir", mirror, "fetch", "--prune", "origin", "+refs/heads/*:refs/heads/*"); err != nil {
		return "", nil, fmt.Errorf("update mirror: %w", err)
	}
	if err := runGit(ctx, output, "", "--git-dir", mirror, "cat-file", "-e", request.SHA+"^{commit}"); err != nil {
		if fetchErr := runGit(ctx, output, "", "--git-dir", mirror, "fetch", "origin", request.SHA); fetchErr != nil {
			return "", nil, fmt.Errorf("fetch exact revision %s: %w", request.SHA, fetchErr)
		}
	}

	if err := os.MkdirAll(filepath.Dir(checkout), 0o750); err != nil {
		return "", nil, err
	}
	if err := os.RemoveAll(checkout); err != nil {
		return "", nil, err
	}
	if err := runGit(ctx, output, "", "clone", "--no-checkout", "--shared", mirror, checkout); err != nil {
		return "", nil, fmt.Errorf("create revision checkout: %w", err)
	}
	if err := runGit(ctx, output, checkout, "checkout", "--detach", request.SHA); err != nil {
		return "", nil, fmt.Errorf("checkout revision: %w", err)
	}

	recipe, err := readRecipe(checkout)
	if err != nil {
		return "", nil, err
	}
	if recipe.Checkout.Submodules == "recursive" {
		if err := runGit(ctx, output, checkout, "submodule", "update", "--init", "--recursive"); err != nil {
			return "", nil, fmt.Errorf("update submodules: %w", err)
		}
	}
	if recipe.Checkout.LFS {
		if _, err := exec.LookPath("git-lfs"); err != nil {
			return "", nil, fmt.Errorf("recipe requests LFS but git-lfs is unavailable")
		}
		if err := runGit(ctx, output, checkout, "lfs", "pull"); err != nil {
			return "", nil, fmt.Errorf("pull Git LFS objects: %w", err)
		}
	}
	return checkout, recipe, nil
}

func readRecipe(checkout string) (*deployRecipe, error) {
	data, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(recipePath)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", recipePath, err)
	}
	return parseDeployRecipe(data)
}

func runGit(parent context.Context, output io.Writer, directory string, args ...string) error {
	ctx, cancel := context.WithTimeout(parent, checkoutTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = directory
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("git command timed out: %w", ctx.Err())
		}
		return err
	}
	return nil
}

func (plugin *LocalDeployPlugin) checkoutPath(owner, repo, sha string) string {
	return filepath.Join(plugin.cfg.CacheDir, "checkouts", owner, repo, strings.ToLower(sha))
}

type composeHealthContainer struct {
	Service  string `json:"Service"`
	State    string `json:"State"`
	Health   string `json:"Health"`
	ExitCode int    `json:"ExitCode"`
}

func runComposeHealth(ctx context.Context, checkout string, health *composeHealthRecipe, output io.Writer) error {
	if health == nil {
		return nil
	}
	healthContext, cancel := context.WithTimeout(ctx, health.Timeout.Duration)
	defer cancel()
	args := composeArgs(health)
	configArgs := append(append([]string{}, args...), "config", "--services")
	configOutput, err := runCaptured(healthContext, checkout, "docker", configArgs...)
	if err != nil {
		return fmt.Errorf("enumerate compose services: %w", err)
	}
	expected := nonEmptyLines(string(configOutput))
	if len(expected) == 0 {
		return fmt.Errorf("compose health configuration contains no services")
	}
	allowed := make(map[string]bool, len(health.AllowCompleted))
	for _, service := range health.AllowCompleted {
		allowed[service] = true
	}

	deadline := time.Now().Add(health.Timeout.Duration)
	var lastProblem string
	for {
		psArgs := append(append([]string{}, args...), "ps", "-a", "--format", "json")
		psOutput, commandErr := runCaptured(healthContext, checkout, "docker", psArgs...)
		if commandErr == nil {
			containers, parseErr := parseComposeHealthOutput(psOutput)
			if parseErr == nil {
				if problem := composeHealthProblem(expected, containers, allowed); problem == "" {
					_, _ = fmt.Fprintln(output, "Compose health checks passed")
					return nil
				} else {
					lastProblem = problem
				}
			} else {
				lastProblem = parseErr.Error()
			}
		} else {
			lastProblem = commandErr.Error()
		}
		if time.Now().Add(health.Interval.Duration).After(deadline) {
			return fmt.Errorf("compose health timed out after %s: %s", health.Timeout.Duration, lastProblem)
		}
		timer := time.NewTimer(health.Interval.Duration)
		select {
		case <-healthContext.Done():
			timer.Stop()
			return fmt.Errorf("compose health timed out after %s: %s", health.Timeout.Duration, lastProblem)
		case <-timer.C:
		}
	}
}

func composeArgs(health *composeHealthRecipe) []string {
	args := []string{"compose"}
	for _, file := range health.Files {
		args = append(args, "-f", file)
	}
	if health.ProjectName != "" {
		args = append(args, "-p", health.ProjectName)
	}
	return args
}

func runCaptured(ctx context.Context, directory, command string, args ...string) ([]byte, error) {
	executable, err := exec.LookPath(command)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = directory
	return cmd.CombinedOutput()
}

func nonEmptyLines(value string) []string {
	var result []string
	scanner := bufio.NewScanner(strings.NewReader(value))
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func parseComposeHealthOutput(data []byte) ([]composeHealthContainer, error) {
	var array []composeHealthContainer
	if err := json.Unmarshal(data, &array); err == nil {
		return array, nil
	}
	var result []composeHealthContainer
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		var container composeHealthContainer
		if err := json.Unmarshal(scanner.Bytes(), &container); err != nil {
			return nil, fmt.Errorf("parse docker compose ps output: %w", err)
		}
		result = append(result, container)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func composeHealthProblem(expected []string, containers []composeHealthContainer, allowed map[string]bool) string {
	byService := make(map[string]composeHealthContainer, len(containers))
	for _, container := range containers {
		byService[container.Service] = container
	}
	for _, service := range expected {
		container, ok := byService[service]
		if !ok {
			return "service " + service + " is missing"
		}
		state := strings.ToLower(container.State)
		health := strings.ToLower(container.Health)
		if state == "exited" && allowed[service] && container.ExitCode == 0 {
			continue
		}
		if state != "running" {
			return "service " + service + " is " + state
		}
		if health == "unhealthy" {
			return "service " + service + " is unhealthy"
		}
	}
	return ""
}

func resolveRemoteHEAD(ctx context.Context, owner, repo string) (string, error) {
	remote := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	output, err := runCaptured(ctx, "", "git", "ls-remote", remote, "HEAD")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(output))
	if len(fields) < 1 || !fullCommitSHA.MatchString(fields[0]) {
		return "", fmt.Errorf("git ls-remote returned an invalid HEAD")
	}
	return fields[0], nil
}

func tailLog(path string, lines int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	all := strings.Split(string(data), "\n")
	if lines > 0 && len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n"), nil
}

func parseLines(value any) int {
	raw, _ := value.(string)
	lines, err := strconv.Atoi(raw)
	if err != nil || lines < 1 || lines > 10000 {
		return 100
	}
	return lines
}
