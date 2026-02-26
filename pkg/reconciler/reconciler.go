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
	"github.com/mywio/git-ops/pkg/config"
	"github.com/mywio/git-ops/pkg/core"
	"github.com/mywio/git-ops/pkg/utils"
	"golang.org/x/oauth2"
)

type Reconciler struct {
	cfg      config.Config
	client   *github.Client
	logger   *slog.Logger
	registry core.PluginRegistry
	stopCh   chan struct{}
	wg       sync.WaitGroup
	ticker   *time.Ticker
	started  bool
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

func (r *Reconciler) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	r.logger = logger
	r.registry = registry
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

	// 2. Build Removal State (What should be explicitly removed)
	removalState := make(map[string]bool)

	for _, user := range r.cfg.Users {
		if user == "" {
			continue
		}

		// Query 1: Desired State (user:NAME topic:TAG archived:false)
		queryDesired := fmt.Sprintf("user:%s topic:%s archived:false", user, r.cfg.Topic)
		r.fetchReposInto(ctx, queryDesired, desiredState)

		// Query 2: Removal Candidates - Topic "git-ops-remove"
		queryRemoveTopic := fmt.Sprintf("user:%s topic:git-ops-remove", user)
		r.fetchRemovalInto(ctx, queryRemoveTopic, removalState)

		// Query 3: Removal Candidates - Archived but with main Topic
		// Note: searching for archived:true explicitly
		queryArchived := fmt.Sprintf("user:%s topic:%s archived:true", user, r.cfg.Topic)
		r.fetchRemovalInto(ctx, queryArchived, removalState)
	}

	r.logger.Info("State calculated", "desired", len(desiredState), "removal", len(removalState))

	// 3. Process Local State (The "Kill Switch" Logic)
	r.processLocalState(desiredState, removalState)

	// 4. Deploy Phase (Update/Create what should exist)
	for fullName, repo := range desiredState {
		// If it's also in removal list (conflict), removal takes precedence?
		// Logic: If it's in removal list, it should have been handled by processLocalState (deleted).
		// But if it's in desiredState map, we might re-deploy it.
		// GitHub search is eventually consistent.
		// If a repo has both tags? User error.
		// Let's assume Removal trumps Desired.
		if removalState[fullName] {
			r.logger.Warn("Repo found in both Desired and Removal state, skipping deploy", "repo", fullName)
			continue
		}
		r.deployRepo(ctx, fullName, repo)
	}
}

func (r *Reconciler) fetchReposInto(ctx context.Context, query string, target map[string]*github.Repository) {
	opts := &github.SearchOptions{ListOptions: github.ListOptions{PerPage: 100}}
	repos, _, err := r.client.Search.Repositories(ctx, query, opts)
	if err != nil {
		r.logger.Error("Search failed", "query", query, "error", err)
		return
	}
	for _, repo := range repos.Repositories {
		fullName := fmt.Sprintf("%s/%s", *repo.Owner.Login, *repo.Name)
		target[fullName] = repo
	}
}

func (r *Reconciler) fetchRemovalInto(ctx context.Context, query string, target map[string]bool) {
	opts := &github.SearchOptions{ListOptions: github.ListOptions{PerPage: 100}}
	repos, _, err := r.client.Search.Repositories(ctx, query, opts)
	if err != nil {
		r.logger.Error("Search failed", "query", query, "error", err)
		return
	}
	for _, repo := range repos.Repositories {
		fullName := fmt.Sprintf("%s/%s", *repo.Owner.Login, *repo.Name)
		target[fullName] = true
	}
}

func (r *Reconciler) processLocalState(desiredState map[string]*github.Repository, removalState map[string]bool) {
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

			// Construct key "Owner/Repo"
			currentKey := fmt.Sprintf("%s/%s", userDir.Name(), repoDir.Name())
			fullPath := filepath.Join(userPath, repoDir.Name())

			isDesired := desiredState[currentKey] != nil
			isRemoval := removalState[currentKey]

			if isRemoval {
				r.logger.Info("Explicit removal detected", "service", currentKey)
				r.pruneService(fullPath)
			} else if !isDesired {
				// Exists locally, but NOT in Desired, and NOT in Removal.
				// This is the "Safety Warning" - Do NOT Delete.
				r.logger.Warn("Sync Divergence: Local service exists but not found in Desired State. Skipping removal.", "service", currentKey)
			}
		}
	}
}

func (r *Reconciler) pruneService(path string) {
	if r.cfg.DryRun {
		r.logger.Info("DryRun: Would remove service", "path", path)
		return
	}

	// Docker Down
	cmd := exec.Command("docker", "compose", "down", "--remove-orphans")
	cmd.Dir = path
	cmd.Run() // Ignore error

	// Delete Folder
	if err := os.RemoveAll(path); err != nil {
		r.logger.Error("Failed to remove service folder", "path", path, "error", err)
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
		// Even if file didn't change, we might need to redeploy if secrets changed?
		// But for now, we follow the "file change" trigger.
		// However, if the user manually restarted, or if secrets rotated, we might miss it.
		// For this task, we stick to file change detection as primary trigger,
		// OR we can force update if we assume secrets might have changed.
		// The prompt didn't strictly say "always deploy".
		// But to be safe with secrets, maybe we should just return if no file change?
		// No, usually you want to redeploy if secrets update.
		// But we don't know if secrets updated.
		// Let's stick to file change for now to avoid restart loops.
		return
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

	// Collect Secrets from Plugins
	secretPlugins := r.registry.GetPluginsWithCapability("secrets")
	secretEnv := []string{}

	for _, p := range secretPlugins {
		res, err := p.Execute("get_secrets", map[string]interface{}{
			"owner": *repo.Owner.Login,
			"repo":  *repo.Name,
		})
		if err != nil {
			logger.Error("Failed to fetch secrets from plugin, aborting deploy", "plugin", p.Name(), "error", err)
			return
		}

		if secrets, ok := res.(map[string]string); ok {
			for k, v := range secrets {
				// Append as KEY=VALUE
				secretEnv = append(secretEnv, fmt.Sprintf("%s=%s", k, v))
			}
		}
	}

	// Prepare Env for Hooks (Pass service context)
	hookEnv := []string{
		fmt.Sprintf("REPO_NAME=%s", *repo.Name),
		fmt.Sprintf("REPO_OWNER=%s", *repo.Owner.Login),
		fmt.Sprintf("TARGET_DIR=%s", repoLocalPath),
	}
	// Append secrets to hookEnv as well?
	// The prompt said: "Reconciler injects these into the docker compose execution context".
	// It didn't explicitly say hooks. But hooks might need them.
	// For safety, let's keep them out of hooks unless requested.
	// Hooks usually do migrations, which need DB pass. So yes, they likely need them.
	// But let's verify constraint: "ensure these values are passed only to the exec.Command environment of the specific docker compose process."
	// Okay, strictly docker compose process.

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

	// Inject Secrets + Standard Env
	cmd.Env = append(os.Environ(), secretEnv...)

	if err := cmd.Run(); err != nil {
		logger.Error("Deploy failed", "error", err)
		return
	}

	// Run Repo POST Hooks
	if err := utils.ExecuteHooks(filepath.Join(repoLocalPath, ".deploy", "post"), hookEnv, logger); err != nil {
		logger.Error("Repo Post-hook failed", "error", err)
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
	path := fmt.Sprintf(".deploy/%s", stage)
	_, dirContent, _, err := r.client.Repositories.GetContents(ctx, owner, repo, path, nil)
	if err != nil {
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

		if err := os.WriteFile(localScriptPath, []byte(decoded), 0755); err != nil {
			return err
		}
	}
	return nil
}
