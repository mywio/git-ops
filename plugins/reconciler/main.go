package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	cfg            config.Config
	client         *github.Client
	logger         *slog.Logger
	registry       core.PluginRegistry
	stopCh         chan struct{}
	wg             sync.WaitGroup
	ticker         *time.Ticker
	started        bool
	nodeID         string
	executionState *executionStateManager
	commitTracker  *commitTracker
	lifecycleMu    sync.Mutex
	stopping       bool
}

var Plugin core.Plugin = &Reconciler{
	stopCh: make(chan struct{}),
}

var publishInternalEvent = core.Publish
var removeAll = os.RemoveAll

func (r *Reconciler) Name() string {
	return "reconciler"
}

func (r *Reconciler) Description() string {
	return "Core GitOps Reconciler"
}

func (r *Reconciler) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityDeployer, core.CapabilitySystem}
}

func (r *Reconciler) Status() core.ServiceStatus {
	if r.started {
		return core.StatusHealthy
	}
	return core.StatusDegraded
}

func (r *Reconciler) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	switch action {
	case "reconcile_stack":
		owner, okOwner := params["owner"].(string)
		repo, okRepo := params["repo"].(string)
		if !okOwner || !okRepo || owner == "" || repo == "" {
			return nil, fmt.Errorf("reconcile_stack requires 'owner' and 'repo' string parameters")
		}

		forceType, _ := params["force_type"].(string)

		triggerCtx := context.Background()
		if ctx != nil {
			triggerCtx = ctx
		}

		if !r.scheduleReconcileStack(triggerCtx, owner, repo, forceType) {
			return nil, fmt.Errorf("reconciler is stopping")
		}
		return true, nil
	case "list_deployments":
		// Quick heuristic: return all stacks
		return r.listManagedDeployments()
	case "system_info":
		return r.getSystemInfo()
	case "stream_logs":
		owner, _ := params["owner"].(string)
		repo, _ := params["repo"].(string)
		lines, _ := params["lines"].(string)
		if lines == "" {
			lines = "100"
		}
		return r.streamLogs(ctx, owner, repo, lines)
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

func (r *Reconciler) Config() any {
	return r.cfg
}

func (r *Reconciler) handleReconcileNowEvent(ctx context.Context, event core.InternalEvent) {
	r.logger.Info("Received reconcile_now event, triggering reconciliation", "source", event.Source)
	if !r.scheduleReconcile(ctx) {
		r.logger.Info("Skipping reconcile_now event while stopping", "source", event.Source)
	}
}

func (r *Reconciler) handleReconcileStackEvent(ctx context.Context, event core.InternalEvent) {
	owner, okOwner := event.Details["owner"].(string)
	repo, okRepo := event.Details["repo"].(string)
	if !okOwner || !okRepo {
		r.logger.Warn("reconcile_stack event missing owner or repo details", "source", event.Source)
		return
	}
	forceType, _ := event.Details["force_type"].(string)

	r.logger.Info("Received reconcile_stack event", "source", event.Source, "owner", owner, "repo", repo, "force_type", forceType)
	if !r.scheduleReconcileStack(ctx, owner, repo, forceType) {
		r.logger.Info("Skipping reconcile_stack event while stopping", "source", event.Source, "owner", owner, "repo", repo)
	}
}

func (r *Reconciler) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	r.logger = logger
	r.registry = registry
	if r.executionState == nil {
		r.executionState = newExecutionStateManager(time.Now)
	}
	if r.commitTracker == nil {
		r.commitTracker = newCommitTracker()
	}
	if r.nodeID == "" {
		hostName, err := os.Hostname()
		if err != nil || strings.TrimSpace(hostName) == "" {
			r.nodeID = "unknown"
		} else {
			r.nodeID = hostName
		}
	}

	if registry != nil {
		cfgMap := registry.GetConfig()
		if coreSection, ok := cfgMap["core"]; ok {
			r.cfg = config.LoadConfigFromMap(coreSection)
		}
	}
	envCfg := config.LoadConfig()
	r.cfg = config.MergeConfig(r.cfg, envCfg)

	if r.cfg.Token == "" {
		return fmt.Errorf("missing GITHUB_TOKEN")
	}

	// Register Events
	if registry != nil {
		registry.RegisterEventType(core.EventTypeDesc{
			Name:        "reconcile_now",
			Description: "Request an immediate full reconciliation",
			PayloadSpec: map[string]core.PayloadField{
				"force": {Type: "bool", Description: "Force even if locked", Required: false},
			},
		})
		registry.RegisterEventType(core.EventTypeDesc{
			Name:        "reconcile_stack",
			Description: "Request reconciliation for a specific stack",
			PayloadSpec: map[string]core.PayloadField{
				"owner":      {Type: "string", Description: "Repository owner", Required: true},
				"repo":       {Type: "string", Description: "Repository name", Required: true},
				"force_type": {Type: "string", Description: "Force deploy type: bypass_check, clean_local_state, remove_images, restart_only", Required: false},
			},
		})
		registry.RegisterEventType(core.EventTypeDesc{
			Name:        "deploy_success",
			Description: "Stack deployed successfully",
			PayloadSpec: map[string]core.PayloadField{
				"duration": {Type: "time.Duration", Description: "Deploy time", Required: true},
			},
		})
		registry.RegisterEventType(core.EventTypeDesc{
			Name:        "deploy_failed",
			Description: "Stack deployment failed",
			PayloadSpec: map[string]core.PayloadField{
				"error": {Type: "string", Description: "Error message", Required: true},
			},
		})
		registry.RegisterEventType(core.EventTypeDesc{
			Name:        "deploy_start",
			Description: "Stack deployment starting",
		})
		registry.RegisterEventType(core.EventTypeDesc{
			Name:        core.EventTypeExecution,
			Description: "Stack execution lifecycle update",
			PayloadSpec: map[string]core.PayloadField{
				"execution_id": {Type: "string", Description: "Execution identifier", Required: true},
				"owner":        {Type: "string", Description: "Repository owner", Required: true},
				"repo":         {Type: "string", Description: "Repository name", Required: true},
				"full_name":    {Type: "string", Description: "Repository full name", Required: true},
				"stage":        {Type: "string", Description: "Current execution stage", Required: true},
				"status":       {Type: "string", Description: "Current execution status", Required: true},
			},
		})
		registry.RegisterEventType(core.EventTypeDesc{
			Name:        "stack_commit_changed",
			Description: "Stack commit advanced after successful reconciliation",
			PayloadSpec: map[string]core.PayloadField{
				"owner":           {Type: "string", Description: "Repository owner", Required: true},
				"repo":            {Type: "string", Description: "Repository name", Required: true},
				"full_name":       {Type: "string", Description: "Repository full name", Required: true},
				"stack_path":      {Type: "string", Description: "Absolute stack path", Required: true},
				"old_commit":      {Type: "string", Description: "Previous reconciler-observed commit", Required: false},
				"new_commit":      {Type: "string", Description: "New reconciler-observed commit", Required: true},
				"compose_changed": {Type: "bool", Description: "Whether compose changed in the successful reconcile path", Required: true},
			},
		})
		registry.RegisterEventType(core.EventTypeDesc{
			Name:        "notify_secret_conflict",
			Description: "Duplicate secret detected during deployment",
			PayloadSpec: map[string]core.PayloadField{
				"key":     {Type: "string", Description: "Secret key", Required: true},
				"winner":  {Type: "string", Description: "Plugin that provided it", Required: true},
				"skipped": {Type: "string", Description: "Plugin that was skipped", Required: true},
			},
		})

		registry.Subscribe("reconcile_now", r.handleReconcileNowEvent)
		registry.Subscribe("reconcile_stack", r.handleReconcileStackEvent)
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
	r.lifecycleMu.Lock()
	if !r.started {
		r.lifecycleMu.Unlock()
		return nil
	}
	if !r.stopping {
		r.stopping = true
		close(r.stopCh)
	}
	r.lifecycleMu.Unlock()
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
	if !r.beginWork() {
		return
	}
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
	r.processLocalState(ctx, desiredState, removalState)

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
		r.deployRepo(ctx, fullName, repo, "")
	}
}

func (r *Reconciler) runReconcileStack(ctx context.Context, owner, repo, forceType string) {
	if r.isStopping() {
		return
	}

	fullName := fmt.Sprintf("%s/%s", owner, repo)
	logger := r.logger.With("service", fullName)

	snapshot, acquired := r.acquireExecution(fullName, owner, repo, "reconcile_stack")
	if !acquired {
		logger.Warn("Execution already in progress, skipping targeted reconciliation", "execution_id", snapshot.ExecutionID)
		return
	}
	r.publishExecutionEvent(ctx, snapshot)
	r.markExecutionRunning(ctx, fullName, core.ExecutionStageFetch)

	// Query to check if the specific repo is marked for gitops
	queryDesired := fmt.Sprintf("repo:%s topic:%s archived:false", fullName, r.cfg.Topic)
	desiredState := make(map[string]*github.Repository)
	r.fetchReposInto(ctx, queryDesired, desiredState)

	if len(desiredState) == 0 {
		err := fmt.Errorf("stack not found or not tagged for git-ops")
		logger.Warn(err.Error(), "owner", owner, "repo", repo)
		r.failExecution(ctx, fullName, core.ExecutionStageFetch, err)
		return
	}

	repository := desiredState[fullName]
	if repository == nil {
		err := fmt.Errorf("stack not found in query results")
		logger.Warn(err.Error(), "owner", owner, "repo", repo)
		r.failExecution(ctx, fullName, core.ExecutionStageFetch, err)
		return
	}

	logger.Info("Targeted stack reconciliation initiated", "force_type", forceType)
	r.deployRepoWithExecution(ctx, fullName, repository, forceType)
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

func (r *Reconciler) beginWork() bool {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()

	if r.stopping {
		return false
	}
	r.wg.Add(1)
	return true
}

func (r *Reconciler) isStopping() bool {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	return r.stopping
}

func (r *Reconciler) scheduleReconcile(ctx context.Context) bool {
	if !r.beginWork() {
		return false
	}
	go func() {
		defer r.wg.Done()
		r.reconcile(ctx)
	}()
	return true
}

func (r *Reconciler) scheduleReconcileStack(ctx context.Context, owner, repo, forceType string) bool {
	if !r.beginWork() {
		return false
	}
	go func() {
		defer r.wg.Done()
		r.runReconcileStack(ctx, owner, repo, forceType)
	}()
	return true
}

func (r *Reconciler) acquireExecution(fullName, owner, repo, trigger string) (executionSnapshot, bool) {
	if r.executionState == nil {
		r.executionState = newExecutionStateManager(time.Now)
	}
	return r.executionState.acquire(fullName, owner, repo, r.nodeID, trigger)
}

func (r *Reconciler) markExecutionRunning(ctx context.Context, fullName string, stage core.ExecutionStage) {
	if r.executionState == nil {
		return
	}
	snapshot, ok := r.executionState.markRunning(fullName, stage)
	if !ok {
		return
	}
	r.publishExecutionEvent(ctx, snapshot)
}

func (r *Reconciler) succeedExecution(ctx context.Context, fullName string) {
	if r.executionState == nil {
		return
	}
	snapshot, ok := r.executionState.markSucceeded(fullName, core.ExecutionStageComplete)
	if !ok {
		return
	}
	r.publishExecutionEvent(ctx, snapshot)
}

func (r *Reconciler) failExecution(ctx context.Context, fullName string, stage core.ExecutionStage, err error) {
	if r.executionState == nil {
		return
	}
	failureClass := classifyFailure(err, stage)
	snapshot, ok := r.executionState.markFailed(fullName, stage, err)
	if !ok {
		return
	}
	r.publishExecutionEventWithFailureClass(ctx, snapshot, failureClass)
}

func (r *Reconciler) publishExecutionEvent(ctx context.Context, snapshot executionSnapshot) {
	r.publishExecutionEventWithFailureClass(ctx, snapshot, "")
}

func (r *Reconciler) publishExecutionEventWithFailureClass(ctx context.Context, snapshot executionSnapshot, failureClass core.FailureClass) {
	details := map[string]any{}
	if snapshot.LastError != "" {
		details["last_error"] = snapshot.LastError
	}
	if !snapshot.RequestedAt.IsZero() {
		details["requested_at"] = snapshot.RequestedAt.Format(time.RFC3339)
	}
	if !snapshot.StartedAt.IsZero() {
		details["started_at"] = snapshot.StartedAt.Format(time.RFC3339)
	}

	severity := core.AlertSeverityInfo
	if snapshot.Status == core.ExecutionStatusFailed {
		severity = core.AlertSeverityError
	}

	publishInternalEvent(ctx, core.NewExecutionEvent(core.ExecutionEventInput{
		ExecutionID:   snapshot.ExecutionID,
		Owner:         snapshot.Owner,
		Repo:          snapshot.Repo,
		FullName:      snapshot.FullName,
		Stage:         snapshot.Stage,
		Status:        snapshot.Status,
		FailureClass:  failureClass,
		AlertSeverity: severity,
		NodeID:        snapshot.NodeID,
		Trigger:       snapshot.Trigger,
		Source:        "reconciler",
		Details:       details,
	}))
}

// Deployer Implementations
func (r *Reconciler) listManagedDeployments() ([]map[string]interface{}, error) {
	entries, err := os.ReadDir(r.cfg.TargetDir)
	if os.IsNotExist(err) {
		return []map[string]interface{}{}, nil
	}

	var deployments []map[string]interface{}
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

			repoPath := filepath.Join(userPath, repoDir.Name())

			// Docker Compose LS lookup (requires compose plugin)
			cmd := exec.Command("docker", "compose", "ps", "-a", "--format", "json")
			cmd.Dir = repoPath
			out, _ := cmd.Output()

			status := "unknown"
			if len(out) > 10 {
				status = "running"
			}

			fullName := fmt.Sprintf("%s/%s", userDir.Name(), repoDir.Name())
			deployment := map[string]interface{}{
				"owner":            userDir.Name(),
				"repo":             repoDir.Name(),
				"path":             repoPath,
				"status":           status,
				"execution_id":     "",
				"execution_status": "",
				"execution_stage":  "",
				"last_error":       "",
			}
			if r.executionState != nil {
				if snapshot, ok := r.executionState.snapshot(fullName); ok {
					deployment["execution_id"] = snapshot.ExecutionID
					deployment["execution_status"] = string(snapshot.Status)
					deployment["execution_stage"] = string(snapshot.Stage)
					deployment["last_error"] = snapshot.LastError
				}
			}

			deployments = append(deployments, deployment)
		}
	}
	return deployments, nil
}

func (r *Reconciler) getSystemInfo() (map[string]interface{}, error) {
	cmd := exec.Command("docker", "info", "--format", "{{json .}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var info map[string]interface{}
	// fallback if unable to parse docker system info perfectly
	if err := json.Unmarshal(out, &info); err != nil {
		return map[string]interface{}{"raw_docker_info": string(out)}, nil
	}
	return info, nil
}

func (r *Reconciler) streamLogs(ctx context.Context, owner, repo, lines string) (<-chan string, error) {
	repoPath := filepath.Join(r.cfg.TargetDir, owner, repo)
	cmd := exec.Command("docker", "compose", "logs", "-f", "--tail", lines)
	cmd.Dir = repoPath

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout // combine output

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	logChan := make(chan string)

	go func() {
		defer close(logChan)
		defer cmd.Wait()

		// Read output into channel
		// Using a simple bufio.Scanner
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return // Request cancelled (user closed page)
			case logChan <- scanner.Text():
			}
		}
	}()

	return logChan, nil
}

func (r *Reconciler) processLocalState(ctx context.Context, desiredState map[string]*github.Repository, removalState map[string]bool) {
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
				if !r.pruneService(ctx, currentKey, userDir.Name(), repoDir.Name(), fullPath) {
					r.logger.Info("Skipping prune while execution is active", "service", currentKey)
				}
			} else if !isDesired {
				// Exists locally, but NOT in Desired, and NOT in Removal.
				// This is the "Safety Warning" - Do NOT Delete.
				r.logger.Warn("Sync Divergence: Local service exists but not found in Desired State. Skipping removal.", "service", currentKey)
			}
		}
	}
}

func (r *Reconciler) pruneService(ctx context.Context, fullName, owner, repo, path string) bool {
	snapshot, acquired := r.acquireExecution(fullName, owner, repo, "prune")
	if !acquired {
		return false
	}
	r.publishExecutionEvent(ctx, snapshot)

	if r.cfg.DryRun {
		r.markExecutionRunning(ctx, fullName, core.ExecutionStageComposeDown)
		r.logger.Info("DryRun: Would remove service", "path", path)
		r.succeedExecution(ctx, fullName)
		return true
	}

	composeDownErr := r.runComposeDown(ctx, fullName, path, false)
	removeErr := removeAll(path)

	if composeDownErr != nil {
		r.logger.Error("Failed to stop service before prune", "path", path, "error", composeDownErr)
	}

	if removeErr != nil {
		r.logger.Error("Failed to remove service folder", "path", path, "error", removeErr)
		if composeDownErr == nil {
			r.failExecution(ctx, fullName, core.ExecutionStageComposeDown, removeErr)
		}
		return true
	}

	if composeDownErr == nil {
		r.succeedExecution(ctx, fullName)
	}
	return true
}

func (r *Reconciler) runComposeDown(ctx context.Context, fullName, repoLocalPath string, removeImages bool) error {
	r.markExecutionRunning(ctx, fullName, core.ExecutionStageComposeDown)

	args := []string{"down", "--remove-orphans"}
	if removeImages {
		args = []string{"down", "--rmi", "all", "--remove-orphans"}
	}

	if err := executeComposeCommand(repoLocalPath, nil, nil, args...); err != nil {
		r.failExecution(ctx, fullName, core.ExecutionStageComposeDown, err)
		return err
	}
	return nil
}

func localComposeStateExists(repoLocalPath string) bool {
	info, err := os.Stat(filepath.Join(repoLocalPath, "docker-compose.yml"))
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func (r *Reconciler) runRemoveImagesIfPresent(ctx context.Context, fullName, repoLocalPath string, logger *slog.Logger) error {
	if localComposeStateExists(repoLocalPath) {
		if err := r.runComposeDown(ctx, fullName, repoLocalPath, true); err != nil {
			logger.Error("Failed to remove local images before deploy", "error", err)
			return err
		}
		return nil
	}

	logger.Info("Skipping image removal because no local compose state exists", "path", repoLocalPath)
	return nil
}

func (r *Reconciler) runRestartOnly(ctx context.Context, fullName, repoLocalPath string, composeEnv, runtimeFileEnv []string) error {
	r.markExecutionRunning(ctx, fullName, core.ExecutionStageComposeUp)

	if err := runComposePreflight(repoLocalPath, runtimeFileEnv); err != nil {
		r.failExecution(ctx, fullName, core.ExecutionStageComposeUp, err)
		return err
	}
	if err := executeComposeCommand(repoLocalPath, composeEnv, runtimeFileEnv, "restart"); err != nil {
		r.failExecution(ctx, fullName, core.ExecutionStageComposeUp, err)
		return err
	}

	r.succeedExecution(ctx, fullName)
	return nil
}

func (r *Reconciler) prepareComposeEnvironment(ctx context.Context, owner, repo string, logger *slog.Logger) ([]string, []string, func(), error) {
	secretPlugins := []core.Plugin{}
	if r.registry != nil {
		secretPlugins = r.registry.GetPluginsWithCapability(core.CapabilitySecrets)
	}

	secretEnv := []string{}
	secretValues := make(map[string]string)
	secretSources := make(map[string]string)

	for _, p := range secretPlugins {
		res, err := p.Execute(ctx, "get_secrets", map[string]interface{}{
			"owner": owner,
			"repo":  repo,
		})
		if err != nil {
			return nil, nil, func() {}, err
		}

		secrets, ok := res.(map[string]string)
		if !ok {
			continue
		}

		for k, v := range secrets {
			if _, exists := secretValues[k]; exists {
				winner := secretSources[k]
				logger.Warn("Duplicate secret key, skipping", "key", k, "winner", winner, "skipped", p.Name())
				core.Publish(ctx, core.InternalEvent{
					Type:   "notify_secret_conflict",
					Source: "reconciler",
					String: fmt.Sprintf("Secret %s already provided by %s; skipping %s", k, winner, p.Name()),
					Details: map[string]interface{}{
						"key":     k,
						"winner":  winner,
						"skipped": p.Name(),
					},
				})
				continue
			}
			secretValues[k] = v
			secretSources[k] = p.Name()
		}
	}

	if len(secretValues) > 0 {
		keys := make([]string, 0, len(secretValues))
		for k := range secretValues {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			secretEnv = append(secretEnv, fmt.Sprintf("%s=%s", k, secretValues[k]))
		}
	}

	runtimeFiles, err := r.collectRuntimeFiles(ctx, owner, repo, logger, secretSources)
	if err != nil {
		return nil, nil, func() {}, err
	}

	runtimeFileEnv := []string{}
	cleanupRuntimeFiles := func() {}
	if len(runtimeFiles) > 0 {
		runtimeFileEnv, cleanupRuntimeFiles, err = materializeRuntimeFiles(runtimeFiles)
		if err != nil {
			return nil, nil, func() {}, err
		}
	}

	return secretEnv, runtimeFileEnv, cleanupRuntimeFiles, nil
}

func (r *Reconciler) deployRepo(ctx context.Context, fullName string, repo *github.Repository, forceType string) {
	if repo == nil || repo.Owner == nil || repo.Owner.Login == nil || repo.Name == nil {
		return
	}

	snapshot, acquired := r.acquireExecution(fullName, *repo.Owner.Login, *repo.Name, "reconcile")
	if !acquired {
		r.logger.Warn("Execution already in progress, skipping deploy", "service", fullName, "execution_id", snapshot.ExecutionID)
		return
	}

	r.publishExecutionEvent(ctx, snapshot)
	r.markExecutionRunning(ctx, fullName, core.ExecutionStageFetch)
	r.deployRepoWithExecution(ctx, fullName, repo, forceType)
}

func (r *Reconciler) deployRepoWithExecution(ctx context.Context, fullName string, repo *github.Repository, forceType string) {
	logger := r.logger.With("service", fullName)

	// Fetch docker-compose.yml
	fileContent, _, _, err := r.client.Repositories.GetContents(ctx, *repo.Owner.Login, *repo.Name, "docker-compose.yml", nil)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			logger.Debug("No docker-compose.yml found, skipping")
			r.succeedExecution(ctx, fullName)
		} else {
			logger.Error("Failed to fetch file", "error", err)
			r.failExecution(ctx, fullName, core.ExecutionStageFetch, err)
		}
		return
	}

	content, err := fileContent.GetContent()
	if err != nil {
		logger.Error("Failed to decode docker-compose.yml", "error", err)
		r.failExecution(ctx, fullName, core.ExecutionStageFetch, err)
		return
	}

	currentCommitSHA, err := fetchRepoDefaultBranchSHA(ctx, r.client, repo)
	if err != nil {
		logger.Error("Failed to resolve repository commit sha", "error", err)
		r.failExecution(ctx, fullName, core.ExecutionStageFetch, err)
		return
	}

	// Structure: TARGET_DIR / OWNER / REPO / docker-compose.yml
	repoLocalPath := filepath.Join(r.cfg.TargetDir, *repo.Owner.Login, *repo.Name)
	filePath := filepath.Join(repoLocalPath, "docker-compose.yml")

	if forceType == "clean_local_state" {
		logger.Info("Cleaning local state before deploy", "force_type", forceType)
		if !r.cfg.DryRun {
			os.Remove(filePath)
			os.RemoveAll(filepath.Join(repoLocalPath, ".deploy"))
		}
	} else if forceType == "remove_images" {
		logger.Info("Removing local images before deploy", "force_type", forceType)
		if !r.cfg.DryRun {
			if err := r.runRemoveImagesIfPresent(ctx, fullName, repoLocalPath, logger); err != nil {
				return
			}
		}
	}

	if forceType == "restart_only" {
		logger.Info("Restarting stack containers", "force_type", forceType)
		secretEnv, runtimeFileEnv, cleanupRuntimeFiles, err := r.prepareComposeEnvironment(ctx, *repo.Owner.Login, *repo.Name, logger)
		if err != nil {
			logger.Error("Failed to prepare compose environment for restart", "error", err)
			r.failExecution(ctx, fullName, core.ExecutionStageHooks, err)
			return
		}
		defer cleanupRuntimeFiles()

		if err := r.runRestartOnly(ctx, fullName, repoLocalPath, secretEnv, runtimeFileEnv); err != nil {
			logger.Error("Restart failed", "error", err)
			return
		}
		return
	}

	if !r.cfg.DryRun {
		if err := os.MkdirAll(repoLocalPath, 0755); err != nil {
			logger.Error("Failed to create local repo directory", "error", err)
			r.failExecution(ctx, fullName, core.ExecutionStageFetch, err)
			return
		}
	}

	// Change Detection
	existing, _ := os.ReadFile(filePath)
	if string(existing) == content && forceType == "" {
		// No force type specified and no changes detected
		r.completeSuccessfulStack(ctx, fullName, repo, repoLocalPath, currentCommitSHA, false)
		return
	}

	if forceType != "" {
		logger.Info("Bypassing file change check due to force type", "force_type", forceType)
	}

	r.markExecutionRunning(ctx, fullName, core.ExecutionStageDiff)
	logger.Info("Updating deployment")

	if r.cfg.DryRun {
		r.completeSuccessfulStack(ctx, fullName, repo, repoLocalPath, currentCommitSHA, true)
		return
	}

	deployStart := time.Now()
	r.publishDeployEvent(ctx, "deploy_start", repo, "starting", "", "", deployStart)

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		logger.Error("Failed to write docker-compose.yml", "error", err)
		r.publishDeployEvent(ctx, "deploy_failed", repo, "failed", err.Error(), "", deployStart)
		r.failExecution(ctx, fullName, core.ExecutionStageDiff, err)
		return
	}

	// Fetch Repo Hooks (Pre & Post)
	r.markExecutionRunning(ctx, fullName, core.ExecutionStageHooks)
	err = r.fetchRepoHooks(ctx, *repo.Owner.Login, *repo.Name, "pre", repoLocalPath)
	if err != nil {
		logger.Error("Global Fetch Pre-Hook failed, aborting deploy", "error", err)
		r.publishDeployEvent(ctx, "deploy_failed", repo, "failed", err.Error(), "", deployStart)
		r.failExecution(ctx, fullName, core.ExecutionStageHooks, err)
		return
	}
	err = r.fetchRepoHooks(ctx, *repo.Owner.Login, *repo.Name, "post", repoLocalPath)
	if err != nil {
		logger.Error("Global Fetch Post-Hook failed, aborting deploy", "error", err)
		r.publishDeployEvent(ctx, "deploy_failed", repo, "failed", err.Error(), "", deployStart)
		r.failExecution(ctx, fullName, core.ExecutionStageHooks, err)
		return
	}

	secretEnv, runtimeFileEnv, cleanupRuntimeFiles, err := r.prepareComposeEnvironment(ctx, *repo.Owner.Login, *repo.Name, logger)
	if err != nil {
		logger.Error("Failed to prepare compose environment", "error", err)
		r.publishDeployEvent(ctx, "deploy_failed", repo, "failed", err.Error(), "", deployStart)
		r.failExecution(ctx, fullName, core.ExecutionStageHooks, err)
		return
	}
	defer cleanupRuntimeFiles()

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
			r.publishDeployEvent(ctx, "deploy_failed", repo, "failed", err.Error(), "", deployStart)
			r.failExecution(ctx, fullName, core.ExecutionStageHooks, err)
			return
		}
	}

	// Run Repo PRE Hooks
	if err := utils.ExecuteHooks(filepath.Join(repoLocalPath, ".deploy", "pre"), hookEnv, logger); err != nil {
		logger.Error("Repo Pre-hook failed, aborting deploy", "error", err)
		r.publishDeployEvent(ctx, "deploy_failed", repo, "failed", err.Error(), "", deployStart)
		r.failExecution(ctx, fullName, core.ExecutionStageHooks, err)
		return
	}

	// Docker Compose Up
	r.markExecutionRunning(ctx, fullName, core.ExecutionStageComposeUp)
	if err := runComposePreflight(repoLocalPath, runtimeFileEnv); err != nil {
		logger.Error("Preflight failed", "error", err)
		r.publishDeployEvent(ctx, "deploy_failed", repo, "failed", err.Error(), "", deployStart)
		r.failExecution(ctx, fullName, core.ExecutionStageComposeUp, err)
		return
	}
	logger.Info("Running docker compose up")
	if err := executeComposeCommand(repoLocalPath, secretEnv, runtimeFileEnv, "up", "-d", "--remove-orphans"); err != nil {
		logger.Error("Deploy failed", "error", err)
		r.publishDeployEvent(ctx, "deploy_failed", repo, "failed", err.Error(), "", deployStart)
		r.failExecution(ctx, fullName, core.ExecutionStageComposeUp, err)
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
			r.publishDeployEvent(ctx, "deploy_failed", repo, "failed", err.Error(), "", deployStart)
			r.failExecution(ctx, fullName, core.ExecutionStageHooks, err)
			return
		}
	}

	logger.Info("Deploy sequence complete")
	r.publishDeployEvent(ctx, "deploy_success", repo, "success", "", time.Since(deployStart).String(), deployStart)
	r.completeSuccessfulStack(ctx, fullName, repo, repoLocalPath, currentCommitSHA, true)
}

func (r *Reconciler) collectRuntimeFiles(ctx context.Context, owner, repo string, logger *slog.Logger, existingSources map[string]string) ([]core.RuntimeFile, error) {
	runtimePlugins := []core.Plugin{}
	if r.registry != nil {
		runtimePlugins = r.registry.GetPluginsWithCapability(core.CapabilityRuntimeFiles)
	}
	files := make([]core.RuntimeFile, 0)
	runtimeSources := make(map[string]string)

	for _, p := range runtimePlugins {
		res, err := p.Execute(ctx, "get_runtime_files", map[string]interface{}{
			"owner": owner,
			"repo":  repo,
		})
		if err != nil {
			return nil, fmt.Errorf("plugin %s: %w", p.Name(), err)
		}

		runtimeFiles, ok := res.([]core.RuntimeFile)
		if !ok {
			return nil, fmt.Errorf("plugin %s returned unexpected runtime file payload", p.Name())
		}

		for _, file := range runtimeFiles {
			key := strings.TrimSpace(file.EnvKey)
			if key == "" {
				return nil, fmt.Errorf("plugin %s returned runtime file with empty env key", p.Name())
			}
			if strings.Contains(key, "=") {
				return nil, fmt.Errorf("plugin %s returned invalid env key %q", p.Name(), key)
			}

			if winner, exists := existingSources[key]; exists {
				logger.Warn("Duplicate env key from runtime file plugin, skipping", "key", key, "winner", winner, "skipped", p.Name())
				continue
			}
			if winner, exists := runtimeSources[key]; exists {
				logger.Warn("Duplicate env key from runtime file plugin, skipping", "key", key, "winner", winner, "skipped", p.Name())
				continue
			}

			file.EnvKey = key
			files = append(files, file)
			runtimeSources[key] = p.Name()
		}
	}

	return files, nil
}

func materializeRuntimeFiles(files []core.RuntimeFile) ([]string, func(), error) {
	runtimeDir, err := os.MkdirTemp("", "gitops-runtime-files-*")
	if err != nil {
		return nil, func() {}, fmt.Errorf("create temp runtime dir: %w", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(runtimeDir)
	}

	if err := os.Chmod(runtimeDir, 0700); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("chmod runtime dir: %w", err)
	}

	envToPath := make(map[string]string, len(files))
	for idx, file := range files {
		envKey := strings.TrimSpace(file.EnvKey)
		if envKey == "" || strings.Contains(envKey, "=") {
			cleanup()
			return nil, func() {}, fmt.Errorf("invalid runtime file env key")
		}

		filename := strings.TrimSpace(file.Filename)
		if filename == "" {
			filename = fmt.Sprintf("runtime_file_%d", idx)
		}
		filename = filepath.Base(filename)
		if filename == "" || filename == "." {
			cleanup()
			return nil, func() {}, fmt.Errorf("invalid runtime file name for %s", envKey)
		}

		targetPath := filepath.Join(runtimeDir, fmt.Sprintf("%02d_%s", idx, filename))
		mode := os.FileMode(file.Mode & 0o777)
		if mode == 0 {
			mode = 0600
		}
		if err := os.WriteFile(targetPath, file.Content, mode); err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("write runtime file for %s: %w", envKey, err)
		}

		envToPath[envKey] = targetPath
	}

	keys := make([]string, 0, len(envToPath))
	for key := range envToPath {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, fmt.Sprintf("%s=%s", key, envToPath[key]))
	}
	return env, cleanup, nil
}

func (r *Reconciler) publishDeployEvent(ctx context.Context, eventType string, repo *github.Repository, status, message, duration string, start time.Time) {
	if repo == nil || repo.Owner == nil || repo.Name == nil {
		return
	}
	core.Publish(ctx, core.InternalEvent{
		Type:   core.EventTypeName(eventType),
		Source: "reconciler",
		Repo:   *repo.Name,
		String: message,
		Details: map[string]interface{}{
			"owner":      *repo.Owner.Login,
			"repo":       *repo.Name,
			"full_name":  fmt.Sprintf("%s/%s", *repo.Owner.Login, *repo.Name),
			"status":     status,
			"duration":   duration,
			"started_at": start.Format(time.RFC3339),
		},
	})
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
