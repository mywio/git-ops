package utils

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ExecuteHooks runs all executable scripts in a specific directory in lexical order.
//
// Each script receives its own timeout budget derived from the parent context.
func ExecuteHooks(ctx context.Context, dir string, env []string, logger *slog.Logger, timeout time.Duration) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil // No hooks dir, that's fine
	}
	if err != nil {
		return fmt.Errorf("read hooks dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sh") {
			continue
		}

		scriptPath := filepath.Join(dir, entry.Name())
		logger.Info("Running hook", "script", entry.Name())

		hookCtx := ctx
		cancel := func() {
			// No timeout was configured, so there is no derived context to cancel.
		}
		if timeout > 0 {
			hookCtx, cancel = context.WithTimeout(ctx, timeout)
		}

		cmd := exec.CommandContext(hookCtx, scriptPath)
		cmd.Env = append(os.Environ(), env...) // Pass custom env vars
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		err := cmd.Run()
		cancel()
		if err != nil {
			if errors := hookCtx.Err(); errors != nil {
				return fmt.Errorf("hook %s failed: %w", entry.Name(), errors)
			}
			return fmt.Errorf("hook %s failed: %w", entry.Name(), err)
		}
	}
	return nil
}
