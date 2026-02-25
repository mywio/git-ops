package reconciler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v57/github"
	"github.com/mywio/GHOps/pkg/config"
	"github.com/mywio/GHOps/pkg/utils"
	"golang.org/x/oauth2"
)

type Reconciler struct {
	cfg        config.Config
	client     *github.Client
	logger     *slog.Logger
	stopCh     chan struct{}
	wg         sync.WaitGroup
	ticker     *time.Ticker
	started    bool
}

func NewReconciler(cfg config.Config) *Reconciler {
	return &Reconciler{
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

func (r *Reconciler) Name() string {
	return "reconciler"
}

func (r *Reconciler) Init(ctx context.Context, logger *slog.Logger) error {
	r.logger = logger
	if r.cfg.Token == "" {
		return fmt.Errorf("missing GITHUB_TOKEN")
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.cfg.Token})
	client := github.NewClient(oauth2.NewClient(ctx, ts))
	r.client = client

	if r.cfg.TargetDir == "" {
		r.cfg.TargetDir = "./stacks"
	}

	return nil
}

func (r *Reconciler) Start(ctx context.Context) error {
	if r.started {
		return nil
	}
	r.started = true

	r.logger.Info("Starting Reconciler", "users", r.cfg.Users, "topic", r.cfg.Topic)
	r.ticker = time.NewTicker(r.cfg.Interval)

	go func() {
		// Run once immediately
		r.runReconcile(ctx)

		for {
			select {
			case <-r.ticker.C:
				r.runReconcile(ctx)
			case <-r.stopCh:
				r.ticker.Stop()
				return
			case <-ctx.Done():
				r.ticker.Stop()
				return
			}
		}
	}()

	return nil
}

func (r *Reconciler) Stop(ctx context.Context) error {
	if !r.started {
		return nil
	}
	close(r.stopCh)
	r.logger.Info("Waiting for reconciliation to finish...")
	
	// Create a channel that closes when wg.Wait returns
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		r.logger.Info("Reconciler stopped gracefully")
	case <-ctx.Done():
		r.logger.Warn("Context cancelled while waiting for reconciler to stop")
		return ctx.Err()
	}

	return nil
}

func (r *Reconciler) runReconcile(ctx context.Context) {
	r.wg.Add(1)
	defer r.wg.Done()
	r.reconcile(ctx)
}

func (r *Reconciler) reconcile(ctx context.Context) {
	// 1. Build Desired State (What should exist)
	// Map Key: "Owner/RepoName"
	desiredState := make(map[string]*github.Repository)

	for _, user := range r.cfg.Users {
		if user == "" {
			continue
		}

		// Query: user:NAME topic:TAG archived:false
		query := fmt.Sprintf("user:%s topic:%s archived:false", user, r.cfg.Topic)
		opts := &github.SearchOptions{ListOptions: github.ListOptions{PerPage: 100}}

		repos, _, err := r.client.Search.Repositories(ctx, query, opts)
		if err != nil {
			r.logger.Error("Search failed", "user", user, "error", err)
			continue
		}

		for _, repo := range repos.Repositories {
			fullName := fmt.Sprintf("%s/%s", *repo.Owner.Login, *repo.Name)
			desiredState[fullName] = repo
		}
	}

	r.logger.Info("Desired state calculated", "count", len(desiredState))

	// 2. Prune Phase (Remove what shouldn't exist)
	r.pruneLocal(desiredState)

	// 3. Deploy Phase (Update/Create what should exist)
	for fullName, repo := range desiredState {
		r.deployRepo(ctx, fullName, repo)
	}
}

func (r *Reconciler) pruneLocal(desiredState map[string]*github.Repository) {
	// Walk TARGET_DIR/OWNER/REPO
	entries, err := os.ReadDir(r.cfg.TargetDir)
	if os.IsNotExist(err) {
		return
	}

	for _, userDir := range entries {
		if !userDir.IsDir() {
			continue
		}

		userPath := filepath.Join(r.cfg.TargetDir, userDir.Name())
		repos, _ := os.ReadDir(userPath)

		for _, repoDir := range repos {
			if !repoDir.IsDir() {
				continue
			}

			// Construct key "Owner/Repo" to match desiredState map
			currentKey := fmt.Sprintf("%s/%s", userDir.Name(), repoDir.Name())
			fullPath := filepath.Join(userPath, repoDir.Name())

			if _, exists := desiredState[currentKey]; !exists {
				r.logger.Info("Pruning detected: Service no longer in desired state", "service", currentKey)

				if !r.cfg.DryRun {
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

func (r *Reconciler) deployRepo(ctx context.Context, fullName string, repo *github.Repository) {
	logger := r.logger.With("service", fullName)

	// Fetch docker-compose.yml
	fileContent, _, _, err := r.client.Repositories.GetContents(ctx, *repo.Owner.Login, *repo.Name, "docker-compose.yml", nil)
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
	repoLocalPath := filepath.Join(r.cfg.TargetDir, *repo.Owner.Login, *repo.Name)
	filePath := filepath.Join(repoLocalPath, "docker-compose.yml")

	if !r.cfg.DryRun {
		os.MkdirAll(repoLocalPath, 0755)
	}

	// Change Detection
	existing, _ := os.ReadFile(filePath)
	if string(existing) == content {
		return // No change
	}

	logger.Info("Updating deployment")

	if r.cfg.DryRun {
		return
	}

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		logger.Error("Failed to write docker-compose.yml", "error", err)
		return
	}

	// Fetch Repo Hooks (Pre & Post)
	// We fetch them now so they are ready to run locally
	err = r.fetchRepoHooks(ctx, *repo.Owner.Login, *repo.Name, "pre", repoLocalPath)
	if err != nil {
		logger.Error("Global Fetch Pre-Hook failed, aborting deploy", "error", err)
		return
	}
	err = r.fetchRepoHooks(ctx, *repo.Owner.Login, *repo.Name, "post", repoLocalPath)
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
	if r.cfg.GlobalHooksDir != "" {
		if err := utils.ExecuteHooks(filepath.Join(r.cfg.GlobalHooksDir, "pre"), hookEnv, logger); err != nil {
			logger.Error("Global Pre-hook failed, aborting deploy", "error", err)
			return
		}
	}

	// Run Repo PRE Hooks
	if err := utils.ExecuteHooks(filepath.Join(repoLocalPath, ".deploy", "pre"), hookEnv, logger); err != nil {
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
	if err := utils.ExecuteHooks(filepath.Join(repoLocalPath, ".deploy", "post"), hookEnv, logger); err != nil {
		logger.Error("Repo Post-hook failed", "error", err)
		// We don't return here, technically deploy succeeded
	}

	// Run Global POST Hooks
	if r.cfg.GlobalHooksDir != "" {
		if err = utils.ExecuteHooks(filepath.Join(r.cfg.GlobalHooksDir, "post"), hookEnv, logger); err != nil {
			logger.Error("Repo Post-hook execution failed", "error", err)
			return
		}
	}

	logger.Info("Deploy sequence complete")
}

// fetchRepoHooks downloads all scripts from .deploy/{stage} to the local repo dir
func (r *Reconciler) fetchRepoHooks(ctx context.Context, owner, repo, stage, localDir string) error {
	// Look for .deploy/pre or .deploy/post
	path := fmt.Sprintf(".deploy/%s", stage)

	// GetContents on a directory returns a list of file metadata
	_, dirContent, _, err := r.client.Repositories.GetContents(ctx, owner, repo, path, nil)
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
		fileContent, _, _, err := r.client.Repositories.GetContents(ctx, owner, repo, fileMeta.GetPath(), nil)
		if err != nil {
			r.logger.Error("Failed to fetch hook content", "file", fileMeta.GetName(), "error", err)
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
