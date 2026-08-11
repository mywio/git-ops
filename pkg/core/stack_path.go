package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateStackIdentity verifies that owner and repo are single, safe path
// components. GitHub repository names may contain dots, underscores, and
// hyphens, but separators and traversal components are never valid here.
func ValidateStackIdentity(owner, repo string) error {
	if err := validateStackComponent("owner", owner); err != nil {
		return err
	}
	return validateStackComponent("repo", repo)
}

// ParseStackRef parses an owner/repo reference and validates both components.
func ParseStackRef(ref string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(ref), "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("stack reference must use owner/repo format")
	}
	if err := ValidateStackIdentity(parts[0], parts[1]); err != nil {
		return "", "", err
	}
	return parts[0], parts[1], nil
}

// ResolveStackPath returns a symlink-aware contained path for an owner/repo stack.
func ResolveStackPath(targetDir, owner, repo string) (string, error) {
	if err := ValidateStackIdentity(owner, repo); err != nil {
		return "", err
	}

	root, err := filepath.Abs(targetDir)
	if err != nil {
		return "", fmt.Errorf("resolve target directory: %w", err)
	}
	root, err = resolveExistingSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve target directory symlinks: %w", err)
	}
	stackPath, err := resolveExistingSymlinks(filepath.Join(root, owner, repo))
	if err != nil {
		return "", fmt.Errorf("resolve stack path symlinks: %w", err)
	}
	rel, err := filepath.Rel(root, stackPath)
	if err != nil {
		return "", fmt.Errorf("resolve stack path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("stack path escapes target directory")
	}
	return stackPath, nil
}

func resolveExistingSymlinks(path string) (string, error) {
	current := filepath.Clean(path)
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

// ResolveStackRefPath resolves a validated owner/repo reference below targetDir.
func ResolveStackRefPath(targetDir, ref string) (string, error) {
	owner, repo, err := ParseStackRef(ref)
	if err != nil {
		return "", err
	}
	return ResolveStackPath(targetDir, owner, repo)
}

func validateStackComponent(label, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must be a non-empty trimmed path component", label)
	}
	if value == "." || value == ".." || filepath.Base(value) != value {
		return fmt.Errorf("invalid %s %q", label, value)
	}
	if strings.ContainsAny(value, "/\\\x00") {
		return fmt.Errorf("invalid %s %q", label, value)
	}
	return nil
}
