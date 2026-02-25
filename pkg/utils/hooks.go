package utils

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExecuteHooks runs all executable scripts in a specific directory (lexical order)
func ExecuteHooks(dir string, env []string, logger *slog.Logger) error {
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

		cmd := exec.Command(scriptPath)
		cmd.Env = append(os.Environ(), env...) // Pass custom env vars
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("hook %s failed: %w", entry.Name(), err)
		}
	}
	return nil
}
