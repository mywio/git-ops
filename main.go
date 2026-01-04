package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-github/v57/github"
	"golang.org/x/oauth2"
)

type Config struct {
	Token          string
	Users          []string // Changed to slice
	Topic          string
	TargetDir      string
	Interval       time.Duration
	GlobalHooksDir string
	DryRun         bool
}

func loadConfig() Config {
	interval, _ := time.ParseDuration(os.Getenv("SYNC_INTERVAL"))
	if interval == 0 {
		interval = 5 * time.Minute
	}

	usersStr := os.Getenv("GITHUB_USERS") // Expect comma-separated: "user1,org2,user3"
	users := strings.Split(usersStr, ",")
	for i := range users {
		users[i] = strings.TrimSpace(users[i])
	}

	return Config{
		Token:          os.Getenv("GITHUB_TOKEN"),
		Users:          users,
		Topic:          os.Getenv("TOPIC_FILTER"),
		TargetDir:      os.Getenv("TARGET_DIR"),
		Interval:       interval,
		DryRun:         os.Getenv("DRY_RUN") == "true",
		GlobalHooksDir: os.Getenv("GLOBAL_HOOKS_DIR"),
	}
}

func main() {
	cfg := loadConfig()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if cfg.Token == "" || len(cfg.Users) == 0 || cfg.Topic == "" {
		logger.Error("Missing env vars: GITHUB_TOKEN, GITHUB_USERS, TOPIC_FILTER")
		os.Exit(1)
	}

	if cfg.TargetDir == "" {
		cfg.TargetDir = "./stacks"
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: cfg.Token})
	client := github.NewClient(oauth2.NewClient(ctx, ts))

	logger.Info("Starting Reconciler", "users", cfg.Users, "topic", cfg.Topic)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	reconcile(ctx, client, cfg, logger)

	for range ticker.C {
		reconcile(ctx, client, cfg, logger)
	}
}

func reconcile(ctx context.Context, client *github.Client, cfg Config, logger *slog.Logger) {
	// 1. Build Desired State (What should exist)
	// Map Key: "Owner/RepoName"
	desiredState := make(map[string]*github.Repository)

	for _, user := range cfg.Users {
		if user == "" {
			continue
		}

		// Query: user:NAME topic:TAG archived:false
		query := fmt.Sprintf("user:%s topic:%s archived:false", user, cfg.Topic)
		opts := &github.SearchOptions{ListOptions: github.ListOptions{PerPage: 100}}

		repos, _, err := client.Search.Repositories(ctx, query, opts)
		if err != nil {
			logger.Error("Search failed", "user", user, "error", err)
			continue
		}

		for _, repo := range repos.Repositories {
			fullName := fmt.Sprintf("%s/%s", *repo.Owner.Login, *repo.Name)
			desiredState[fullName] = repo
		}
	}

	logger.Info("Desired state calculated", "count", len(desiredState))

	// 2. Prune Phase (Remove what shouldn't exist)
	pruneLocal(cfg, desiredState, logger)

	// 3. Deploy Phase (Update/Create what should exist)
	for fullName, repo := range desiredState {
		deployRepo(ctx, client, fullName, repo, cfg, logger)
	}
}

func pruneLocal(cfg Config, desiredState map[string]*github.Repository, logger *slog.Logger) {
	// Walk TARGET_DIR/OWNER/REPO
	entries, err := os.ReadDir(cfg.TargetDir)
	if os.IsNotExist(err) {
		return
	}

	for _, userDir := range entries {
		if !userDir.IsDir() {
			continue
		}

		userPath := filepath.Join(cfg.TargetDir, userDir.Name())
		repos, _ := os.ReadDir(userPath)

		for _, repoDir := range repos {
			if !repoDir.IsDir() {
				continue
			}

			// Construct key "Owner/Repo" to match desiredState map
			currentKey := fmt.Sprintf("%s/%s", userDir.Name(), repoDir.Name())
			fullPath := filepath.Join(userPath, repoDir.Name())

			if _, exists := desiredState[currentKey]; !exists {
				logger.Info("Pruning detected: Service no longer in desired state", "service", currentKey)

				if !cfg.DryRun {
					// Docker Down
					cmd := exec.Command("docker", "compose", "down", "--remove-orphans")
					cmd.Dir = fullPath
					cmd.Run() // Ignore error, maybe container is already gone

					// Delete Folder
					os.RemoveAll(fullPath)
				}
			}
		}
	}
}

func deployRepo(ctx context.Context, client *github.Client, fullName string, repo *github.Repository, cfg Config, logger *slog.Logger) {
	logger = logger.With("service", fullName)

	// Fetch docker-compose.yml
	fileContent, _, _, err := client.Repositories.GetContents(ctx, *repo.Owner.Login, *repo.Name, "docker-compose.yml", nil)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			logger.Debug("No docker-compose.yml found, skipping")
		} else {
			logger.Error("Failed to fetch file", "error", err)
		}
		return
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return
	}

	// Structure: TARGET_DIR / OWNER / REPO / docker-compose.yml
	repoLocalPath := filepath.Join(cfg.TargetDir, *repo.Owner.Login, *repo.Name)
	filePath := filepath.Join(repoLocalPath, "docker-compose.yml")

	if !cfg.DryRun {
		os.MkdirAll(repoLocalPath, 0755)
	}

	// Change Detection
	existing, _ := os.ReadFile(filePath)
	if string(existing) == content {
		return // No change
	}

	logger.Info("Updating deployment")

	if cfg.DryRun {
		return
	}

	// Fetch Repo Hooks (Pre & Post)
	// We fetch them now so they are ready to run locally
	err = fetchRepoHooks(ctx, client, *repo.Owner.Login, *repo.Name, "pre", repoLocalPath, logger)
	if err != nil {
		logger.Error("Global Fetch Pre-Hook failed, aborting deploy", "error", err)
		return
	}
	err = fetchRepoHooks(ctx, client, *repo.Owner.Login, *repo.Name, "post", repoLocalPath, logger)
	if err != nil {
		logger.Error("Global Fetch Post-Hook failed, aborting deploy", "error", err)
		return
	}

	// Prepare Env for Hooks (Pass service context)
	hookEnv := []string{
		fmt.Sprintf("REPO_NAME=%s", *repo.Name),
		fmt.Sprintf("REPO_OWNER=%s", *repo.Owner.Login),
		fmt.Sprintf("TARGET_DIR=%s", repoLocalPath),
	}

	// Run Global PRE Hooks
	if cfg.GlobalHooksDir != "" {
		if err := executeHooks(filepath.Join(cfg.GlobalHooksDir, "pre"), hookEnv, logger); err != nil {
			logger.Error("Global Pre-hook failed, aborting deploy", "error", err)
			return
		}
	}

	// Run Repo PRE Hooks
	if err := executeHooks(filepath.Join(repoLocalPath, ".deploy", "pre"), hookEnv, logger); err != nil {
		logger.Error("Repo Pre-hook failed, aborting deploy", "error", err)
		return
	}

	// Docker Compose Up
	logger.Info("Running docker compose up")
	cmd := exec.Command("docker", "compose", "up", "-d", "--remove-orphans")
	cmd.Dir = repoLocalPath
	if err := cmd.Run(); err != nil {
		logger.Error("Deploy failed", "error", err)
		return // Do not run post hooks if deploy failed
	}

	// Run Repo POST Hooks
	if err := executeHooks(filepath.Join(repoLocalPath, ".deploy", "post"), hookEnv, logger); err != nil {
		logger.Error("Repo Post-hook failed", "error", err)
		// We don't return here, technically deploy succeeded
	}

	// Run Global POST Hooks
	if cfg.GlobalHooksDir != "" {
		if err = executeHooks(filepath.Join(cfg.GlobalHooksDir, "post"), hookEnv, logger); err != nil {
			logger.Error("Repo Post-hook execution failed", "error", err)
			return
		}
	}

	logger.Info("Deploy sequence complete")
}

// executeHooks runs all executable scripts in a specific directory (lexical order)
func executeHooks(dir string, env []string, logger *slog.Logger) error {
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

// fetchRepoHooks downloads all scripts from .deploy/{stage} to the local repo dir
func fetchRepoHooks(ctx context.Context, client *github.Client, owner, repo, stage, localDir string, logger *slog.Logger) error {
	// Look for .deploy/pre or .deploy/post
	path := fmt.Sprintf(".deploy/%s", stage)

	// GetContents on a directory returns a list of file metadata
	_, dirContent, _, err := client.Repositories.GetContents(ctx, owner, repo, path, nil)
	if err != nil {
		// 404 just means no hooks for this stage
		if strings.Contains(err.Error(), "404") {
			return nil
		}
		return err
	}

	hooksDir := filepath.Join(localDir, ".deploy", stage)
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return err
	}

	for _, fileMeta := range dirContent {
		if fileMeta.GetType() != "file" || !strings.HasSuffix(fileMeta.GetName(), ".sh") {
			continue
		}

		// Download individual script content
		fileContent, _, _, err := client.Repositories.GetContents(ctx, owner, repo, fileMeta.GetPath(), nil)
		if err != nil {
			logger.Error("Failed to fetch hook content", "file", fileMeta.GetName(), "error", err)
			continue
		}

		decoded, err := fileContent.GetContent()
		if err != nil {
			continue
		}

		localScriptPath := filepath.Join(hooksDir, fileMeta.GetName())

		// Write and chmod +x
		if err := os.WriteFile(localScriptPath, []byte(decoded), 0755); err != nil {
			return err
		}
	}
	return nil
}
