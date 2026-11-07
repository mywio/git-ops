package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func checkDockerAccess() error {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("docker is unavailable: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, docker, "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		return fmt.Errorf("docker access is unavailable: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.CommandContext(ctx, docker, "compose", "version").CombinedOutput(); err != nil {
		return fmt.Errorf("docker compose is unavailable: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}
