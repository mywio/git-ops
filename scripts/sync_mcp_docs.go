package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func main() {
	repoRoot, err := os.Getwd()
	if err != nil {
		fail(err)
	}

	src := filepath.Join(repoRoot, "docs")
	dst := filepath.Join(repoRoot, "plugins", "mcp", "docs")

	if err := os.RemoveAll(dst); err != nil {
		fail(err)
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		fail(err)
	}

	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
	if err != nil {
		fail(err)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func fail(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}
