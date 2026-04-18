package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/sergi/go-diff/diffmatchpatch"
	"golang.org/x/oauth2"
)

type Reconciler struct {
	cfg            config.Config
	client         *github.Client
	logger         *slog.Logger
	registry       core.PluginRegistry
	publishEvent   func(context.Context, core.InternalEvent)
	stopCh         chan struct{}
	healthStopCh   chan struct{}
	wg             sync.WaitGroup
	ticker         *time.Ticker
	healthTicker   *time.Ticker
	started        bool
	nodeID         string
	executionState *executionStateManager
	commitTracker  *commitTracker
	healthMu       sync.Mutex
	lastHealth     map[string]stackHealthSnapshot
	lifecycleMu    sync.Mutex
	stopping       bool
}

type reconcilerConfigView struct {
	Token          core.Secret   `json:"Token"`
	Users          []string      `json:"Users"`
	Topics         []string      `json:"Topics"`
	TargetDir      string        `json:"TargetDir"`
	Interval       time.Duration `json:"Interval"`
	HookTimeout    time.Duration `json:"HookTimeout"`
	GlobalHooksDir string        `json:"GlobalHooksDir"`
	DryRun         bool          `json:"DryRun"`
	SecretsDir     string        `json:"SecretsDir"`
}

type composePSContainer struct {
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
}

type dockerPSContainer struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
}

type composeSpec struct {
	content          string
	currentCommitSHA string
	repoLocalPath    string
	filePath         string
}

type deployRunContext struct {
	fullName    string
	repo        *github.Repository
	spec        composeSpec
	logger      *slog.Logger
	deployStart time.Time
}

type stackHealthContainer struct {
	Name  string
	State string
}

type stackHealthSnapshot struct {
	Status     string
	Containers []stackHealthContainer
}

var remoteComposeFilenames = []string{"compose.yaml", "docker-compose.yml"}

const (
	dockerFormatFlag         = "--format"
	dockerJSONFormat         = "json"
	repoOwnerDescription     = "Repository owner"
	repoNameDescription      = "Repository name"
	repoFullNameDescription  = "Repository full name"
	deployDirName            = ".deploy"
	composeRemoveOrphansFlag = "--remove-orphans"
)

var Plugin core.Plugin = &Reconciler{
	stopCh:       make(chan struct{}),
	healthStopCh: make(chan struct{}),
}

var removeAll = os.RemoveAll
var listComposePSContainers = func(repoPath string) ([]composePSContainer, error) {
	cmd := exec.Command("docker", "compose", "ps", "-a", dockerFormatFlag, dockerJSONFormat)
	cmd.Dir = repoPath

	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parseComposePSOutput(out), nil
}
var listDockerContainers = func() ([]dockerPSContainer, error) {
	cmd := exec.Command("docker", "ps", dockerFormatFlag, dockerJSONFormat)

	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parseDockerPSOutput(out), nil
}
var checkDockerComposeAvailable = func() error {
	cmd := exec.Command("docker", "compose", "version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose not available: %w", err)
	}
	return nil
}

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
		return r.listDeployments()
	case "system_info":
		return r.getSystemInfo()
	case "stream_logs":
		if container, _ := params["container"].(string); strings.TrimSpace(container) != "" {
			lines, _ := params["lines"].(string)
			if lines == "" {
				lines = "100"
			}
			return r.streamContainerLogs(ctx, container, lines)
		}
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
	return reconcilerConfigView{
		Token:          core.NewSecret(r.cfg.Token),
		Users:          append([]string(nil), r.cfg.Users...),
		Topics:         append([]string(nil), r.cfg.Topics...),
		TargetDir:      r.cfg.TargetDir,
		Interval:       r.cfg.Interval,
		HookTimeout:    r.cfg.HookTimeout,
		GlobalHooksDir: r.cfg.GlobalHooksDir,
		DryRun:         r.cfg.DryRun,
		SecretsDir:     r.cfg.SecretsDir,
	}
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
	r.initializeState(registry)
	r.loadConfigFromRegistry(registry)

	if err := r.validateConfig(); err != nil {
		return err
	}

	if err := r.registerEvents(registry); err != nil {
		return err
	}

	r.client = newGitHubClient(ctx, r.cfg.Token)

	if r.cfg.TargetDir == "" {
		r.cfg.TargetDir = "./stacks"
	}
	if err := checkDockerComposeAvailable(); err != nil {
		return err
	}

	return nil
}

func (r *Reconciler) initializeState(registry core.PluginRegistry) {
	if r.publishEvent == nil && registry != nil {
		r.publishEvent = registry.Publish
	}
	if r.executionState == nil {
		r.executionState = newExecutionStateManager(time.Now)
	}
	if r.commitTracker == nil {
		r.commitTracker = newCommitTracker()
	}
	if r.lastHealth == nil {
		r.lastHealth = make(map[string]stackHealthSnapshot)
	}
	if r.stopCh == nil {
		r.stopCh = make(chan struct{})
	}
	if r.healthStopCh == nil {
		r.healthStopCh = make(chan struct{})
	}
	if r.nodeID == "" {
		r.nodeID = detectNodeID()
	}
}

func detectNodeID() string {
	hostName, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostName) == "" {
		return "unknown"
	}
	return hostName
}

func (r *Reconciler) loadConfigFromRegistry(registry core.PluginRegistry) {
	if registry == nil {
		return
	}
	cfgMap := registry.GetConfig()
	if coreSection, ok := cfgMap["core"]; ok {
		r.cfg = config.LoadConfigFromMap(coreSection)
	}
}

func (r *Reconciler) registerEvents(registry core.PluginRegistry) error {
	if registry == nil {
		return nil
	}

	for _, desc := range reconcilerEventTypes() {
		if err := registry.RegisterEventType(desc); err != nil {
			return fmt.Errorf("register event type %q: %w", desc.Name, err)
		}
	}

	registry.Subscribe("reconcile_now", r.handleReconcileNowEvent)
	registry.Subscribe("reconcile_stack", r.handleReconcileStackEvent)
	return nil
}

func reconcilerEventTypes() []core.EventTypeDesc {
	return []core.EventTypeDesc{
		{
			Name:        "reconcile_now",
			Description: "Request an immediate full reconciliation",
			PayloadSpec: map[string]core.PayloadField{
				"force": {Type: "bool", Description: "Force even if locked", Required: false},
			},
		},
		{
			Name:        "reconcile_stack",
			Description: "Request reconciliation for a specific stack",
			PayloadSpec: map[string]core.PayloadField{
				"owner":      {Type: "string", Description: repoOwnerDescription, Required: true},
				"repo":       {Type: "string", Description: repoNameDescription, Required: true},
				"force_type": {Type: "string", Description: "Force deploy type: bypass_check, clean_local_state, remove_images, restart_only", Required: false},
			},
		},
		{
			Name:        "deploy_success",
			Description: "Stack deployed successfully",
			PayloadSpec: map[string]core.PayloadField{
				"duration": {Type: "time.Duration", Description: "Deploy time", Required: true},
			},
		},
		{
			Name:        "deploy_failed",
			Description: "Stack deployment failed",
			PayloadSpec: map[string]core.PayloadField{
				"error": {Type: "string", Description: "Error message", Required: true},
			},
		},
		{Name: "deploy_start", Description: "Stack deployment starting"},
		{
			Name:        core.EventTypeExecution,
			Description: "Stack execution lifecycle update",
			PayloadSpec: map[string]core.PayloadField{
				"execution_id": {Type: "string", Description: "Execution identifier", Required: true},
				"owner":        {Type: "string", Description: repoOwnerDescription, Required: true},
				"repo":         {Type: "string", Description: repoNameDescription, Required: true},
				"full_name":    {Type: "string", Description: repoFullNameDescription, Required: true},
				"stage":        {Type: "string", Description: "Current execution stage", Required: true},
				"status":       {Type: "string", Description: "Current execution status", Required: true},
			},
		},
		{
			Name:        "stack_commit_changed",
			Description: "Stack commit advanced after successful reconciliation",
			PayloadSpec: map[string]core.PayloadField{
				"owner":           {Type: "string", Description: repoOwnerDescription, Required: true},
				"repo":            {Type: "string", Description: repoNameDescription, Required: true},
				"full_name":       {Type: "string", Description: repoFullNameDescription, Required: true},
				"stack_path":      {Type: "string", Description: "Absolute stack path", Required: true},
				"old_commit":      {Type: "string", Description: "Previous reconciler-observed commit", Required: false},
				"new_commit":      {Type: "string", Description: "New reconciler-observed commit", Required: true},
				"compose_changed": {Type: "bool", Description: "Whether compose changed in the successful reconcile path", Required: true},
			},
		},
		{
			Name:        "notify_secret_conflict",
			Description: "Duplicate secret detected during deployment",
			PayloadSpec: map[string]core.PayloadField{
				"key":     {Type: "string", Description: "Secret key", Required: true},
				"winner":  {Type: "string", Description: "Plugin that provided it", Required: true},
				"skipped": {Type: "string", Description: "Plugin that was skipped", Required: true},
			},
		},
		{
			Name:        "notify_compose_env_persistence_risk",
			Description: "Forwarded compose env is referenced outside the service runtime environment",
			PayloadSpec: map[string]core.PayloadField{
				"owner":      {Type: "string", Description: repoOwnerDescription, Required: true},
				"repo":       {Type: "string", Description: repoNameDescription, Required: true},
				"full_name":  {Type: "string", Description: repoFullNameDescription, Required: true},
				"services":   {Type: "array", Description: "Affected compose service names", Required: true},
				"keys":       {Type: "array", Description: "Forwarded env keys involved in risky references", Required: true},
				"risk_count": {Type: "int", Description: "Number of grouped risk findings", Required: true},
				"findings":   {Type: "array", Description: "Per-service risk details", Required: true},
			},
		},
		{
			Name:        "stack_locked",
			Description: "Stack deployment or prune was skipped because a lock file is present",
			PayloadSpec: map[string]core.PayloadField{
				"owner":      {Type: "string", Description: repoOwnerDescription, Required: true},
				"repo":       {Type: "string", Description: repoNameDescription, Required: true},
				"full_name":  {Type: "string", Description: repoFullNameDescription, Required: true},
				"stack_path": {Type: "string", Description: "Absolute stack path", Required: true},
				"lock_file":  {Type: "string", Description: "Absolute lock file path", Required: true},
			},
		},
		{
			Name:        "stack_health",
			Description: "Stack health changed based on docker compose ps state",
			PayloadSpec: map[string]core.PayloadField{
				"owner":      {Type: "string", Description: repoOwnerDescription, Required: true},
				"repo":       {Type: "string", Description: repoNameDescription, Required: true},
				"full_name":  {Type: "string", Description: repoFullNameDescription, Required: true},
				"status":     {Type: "string", Description: "Derived stack status", Required: true},
				"containers": {Type: "array", Description: "Per-container health state", Required: true},
			},
		},
	}
}

func newGitHubClient(ctx context.Context, token string) *github.Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	return github.NewClient(oauth2.NewClient(ctx, ts))
}

func (r *Reconciler) validateConfig() error {
	var errs []string

	if strings.TrimSpace(r.cfg.Token) == "" {
		errs = append(errs, "GITHUB_TOKEN is required")
	}

	hasUser := false
	for _, user := range r.cfg.Users {
		if strings.TrimSpace(user) != "" {
			hasUser = true
			break
		}
	}
	if !hasUser {
		errs = append(errs, "GITHUB_USERS is required")
	}

	if len(nonEmptyStrings(r.cfg.Topics)) == 0 {
		errs = append(errs, "TOPIC_FILTER is required")
	}

	if len(errs) > 0 {
		return fmt.Errorf("configuration errors:\n  - %s", strings.Join(errs, "\n  - "))
	}

	return nil
}

func (r *Reconciler) Start(ctx context.Context) error {
	if r.started {
		return nil
	}
	r.started = true

	r.logger.Info("Starting Reconciler", "users", r.cfg.Users, "topics", r.cfg.Topics)
	r.ticker = time.NewTicker(r.cfg.Interval)
	r.healthTicker = time.NewTicker(60 * time.Second)

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

	go r.runHealthPolling(ctx)

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
		if r.stopCh != nil {
			close(r.stopCh)
		}
		if r.healthStopCh != nil {
			close(r.healthStopCh)
		}
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

func (r *Reconciler) runHealthPolling(ctx context.Context) {
	for {
		select {
		case <-r.healthTicker.C:
			r.pollStackHealth(ctx)
		case <-r.healthStopCh:
			r.healthTicker.Stop()
			return
		case <-ctx.Done():
			r.healthTicker.Stop()
			return
		}
	}
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
		for _, topic := range nonEmptyStrings(r.cfg.Topics) {
			queryDesired := fmt.Sprintf("user:%s topic:%s archived:false", user, topic)
			r.fetchReposInto(ctx, queryDesired, desiredState)
		}

		// Query 2: Removal Candidates - Topic "git-ops-remove"
		queryRemoveTopic := fmt.Sprintf("user:%s topic:git-ops-remove", user)
		r.fetchRemovalInto(ctx, queryRemoveTopic, removalState)

		// Query 3: Removal Candidates - Archived but with configured topics
		// Note: searching for archived:true explicitly
		for _, topic := range nonEmptyStrings(r.cfg.Topics) {
			queryArchived := fmt.Sprintf("user:%s topic:%s archived:true", user, topic)
			r.fetchRemovalInto(ctx, queryArchived, removalState)
		}
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
			r.logger.Warn("Repo found in both Desired and Removal state, skipping deploy", "full_name", fullName)
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
	logger := r.logger.With("full_name", fullName)

	snapshot, acquired := r.acquireExecution(fullName, owner, repo, "reconcile_stack")
	if !acquired {
		logger.Warn("Execution already in progress, skipping targeted reconciliation", "execution_id", snapshot.ExecutionID)
		return
	}
	r.publishExecutionEvent(ctx, snapshot)
	r.markExecutionRunning(ctx, fullName, core.ExecutionStageFetch)

	desiredState := make(map[string]*github.Repository)
	// Query to check if the specific repo is marked for git-ops with any configured topic.
	for _, topic := range nonEmptyStrings(r.cfg.Topics) {
		queryDesired := fmt.Sprintf("repo:%s topic:%s archived:false", fullName, topic)
		r.fetchReposInto(ctx, queryDesired, desiredState)
	}

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
	repos, err := r.searchRepositories(ctx, query)
	if err != nil {
		return
	}
	for _, repo := range repos {
		fullName := fmt.Sprintf("%s/%s", *repo.Owner.Login, *repo.Name)
		target[fullName] = repo
	}
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func healthContainersFromCompose(containers []composePSContainer) []stackHealthContainer {
	health := make([]stackHealthContainer, 0, len(containers))
	for _, container := range containers {
		name := strings.TrimSpace(container.Name)
		if name == "" {
			name = strings.TrimSpace(container.Service)
		}
		if name == "" {
			name = "unknown"
		}
		health = append(health, stackHealthContainer{
			Name:  name,
			State: strings.TrimSpace(container.State),
		})
	}
	sort.Slice(health, func(i, j int) bool {
		if health[i].Name == health[j].Name {
			return health[i].State < health[j].State
		}
		return health[i].Name < health[j].Name
	})
	return health
}

func (r *Reconciler) pollStackHealth(ctx context.Context) {
	deployments, err := r.listManagedDeployments()
	if err != nil {
		r.logger.Warn("Failed to list deployments for health polling", "error", err)
		return
	}

	for _, deployment := range deployments {
		owner, _ := deployment["owner"].(string)
		repo, _ := deployment["repo"].(string)
		repoPath, _ := deployment["path"].(string)
		fullName := fmt.Sprintf("%s/%s", owner, repo)

		// Note: health polling and list_deployments both call listComposePSContainers today.
		// Keep the duplication for now to avoid coupling the poller to UI payload assembly.
		containers, err := listComposePSContainers(repoPath)
		snapshot := stackHealthSnapshot{
			Status:     "unknown",
			Containers: nil,
		}
		if err == nil {
			snapshot.Status = deploymentStatusFromComposeContainers(containers)
			snapshot.Containers = healthContainersFromCompose(containers)
		}

		if !r.shouldPublishHealth(fullName, snapshot) {
			continue
		}

		r.publishStackHealth(ctx, owner, repo, fullName, snapshot)
	}
}

func (r *Reconciler) shouldPublishHealth(fullName string, snapshot stackHealthSnapshot) bool {
	r.healthMu.Lock()
	defer r.healthMu.Unlock()

	current, ok := r.lastHealth[fullName]
	if ok && stackHealthEqual(current, snapshot) {
		return false
	}

	r.lastHealth[fullName] = snapshot
	return true
}

func stackHealthEqual(a, b stackHealthSnapshot) bool {
	if a.Status != b.Status || len(a.Containers) != len(b.Containers) {
		return false
	}
	for i := range a.Containers {
		if a.Containers[i] != b.Containers[i] {
			return false
		}
	}
	return true
}

func (r *Reconciler) publishStackHealth(ctx context.Context, owner, repo, fullName string, snapshot stackHealthSnapshot) {
	if r.publishEvent == nil {
		return
	}

	containers := make([]map[string]string, 0, len(snapshot.Containers))
	for _, container := range snapshot.Containers {
		containers = append(containers, map[string]string{
			"name":  container.Name,
			"state": container.State,
		})
	}

	r.publishEvent(ctx, core.InternalEvent{
		Type:   "stack_health",
		Source: r.Name(),
		Details: map[string]interface{}{
			"owner":      owner,
			"repo":       repo,
			"full_name":  fullName,
			"status":     snapshot.Status,
			"containers": containers,
		},
	})
}

func (r *Reconciler) fetchRemovalInto(ctx context.Context, query string, target map[string]bool) {
	repos, err := r.searchRepositories(ctx, query)
	if err != nil {
		return
	}
	for _, repo := range repos {
		fullName := fmt.Sprintf("%s/%s", *repo.Owner.Login, *repo.Name)
		target[fullName] = true
	}
}

func (r *Reconciler) searchRepositories(ctx context.Context, query string) ([]*github.Repository, error) {
	opts := &github.SearchOptions{ListOptions: github.ListOptions{PerPage: 100}}
	var allRepos []*github.Repository

	for {
		repos, resp, err := r.client.Search.Repositories(ctx, query, opts)
		if err != nil {
			r.logger.Error("Search failed", "query", query, "page", opts.Page, "error", err)
			return nil, err
		}

		if opts.Page == 0 && repos.Total != nil && *repos.Total > 100 {
			r.logger.Warn("GitHub search returned more than one page of repositories", "query", query, "total_count", *repos.Total)
		}

		allRepos = append(allRepos, repos.Repositories...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allRepos, nil
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

	r.publish(ctx, core.NewExecutionEvent(core.ExecutionEventInput{
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

func (r *Reconciler) publish(ctx context.Context, event core.InternalEvent) {
	if r.publishEvent != nil {
		r.publishEvent(ctx, event)
	}
}

// Deployer Implementations
func (r *Reconciler) listManagedDeployments() ([]map[string]interface{}, error) {
	var deployments []map[string]interface{}
	if err := r.forEachLocalRepo(func(owner, repo, repoPath string) {
		deployments = append(deployments, r.buildManagedDeployment(owner, repo, repoPath))
	}); err != nil {
		return nil, err
	}
	return deployments, nil
}

func (r *Reconciler) forEachLocalRepo(fn func(owner, repo, repoPath string)) error {
	entries, err := os.ReadDir(r.cfg.TargetDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	for _, userDir := range entries {
		if !userDir.IsDir() {
			continue
		}
		r.forEachRepoInUserDir(userDir.Name(), fn)
	}
	return nil
}

func (r *Reconciler) forEachRepoInUserDir(owner string, fn func(owner, repo, repoPath string)) {
	userPath := filepath.Join(r.cfg.TargetDir, owner)
	repos, _ := os.ReadDir(userPath)
	for _, repoDir := range repos {
		if !repoDir.IsDir() {
			continue
		}
		fn(owner, repoDir.Name(), filepath.Join(userPath, repoDir.Name()))
	}
}

func (r *Reconciler) buildManagedDeployment(owner, repo, repoPath string) map[string]interface{} {
	fullName := fmt.Sprintf("%s/%s", owner, repo)
	status, containers := deploymentStatusAtPath(repoPath)
	deployment := map[string]interface{}{
		"id":               fullName,
		"kind":             "stack",
		"source":           "git-ops",
		"managed":          true,
		"display_name":     fullName,
		"owner":            owner,
		"repo":             repo,
		"path":             repoPath,
		"status":           status,
		"execution_id":     "",
		"execution_status": "",
		"execution_stage":  "",
		"last_error":       "",
		"history":          []executionSnapshot{},
		"container_names":  containerNames(containers),
	}
	r.addExecutionStateFields(fullName, deployment)
	return deployment
}

func deploymentStatusAtPath(repoPath string) (string, []composePSContainer) {
	containers, err := listComposePSContainers(repoPath)
	if err != nil {
		return "unknown", nil
	}
	return deploymentStatusFromComposeContainers(containers), containers
}

func (r *Reconciler) addExecutionStateFields(fullName string, deployment map[string]interface{}) {
	if r.executionState == nil {
		return
	}
	if snapshot, ok := r.executionState.snapshot(fullName); ok {
		deployment["execution_id"] = snapshot.ExecutionID
		deployment["execution_status"] = string(snapshot.Status)
		deployment["execution_stage"] = string(snapshot.Stage)
		deployment["last_error"] = snapshot.LastError
	}
	if history := r.executionState.snapshotHistory(fullName); history != nil {
		deployment["history"] = history
	}
}

func (r *Reconciler) listDeployments() ([]map[string]interface{}, error) {
	managed, err := r.listManagedDeployments()
	if err != nil {
		return nil, err
	}

	managedContainers := make(map[string]struct{})
	for _, deployment := range managed {
		if names, ok := deployment["container_names"].([]string); ok {
			for _, name := range names {
				managedContainers[name] = struct{}{}
			}
		}
		delete(deployment, "container_names")
	}

	unmanaged, err := r.listUnmanagedContainers(managedContainers)
	if err != nil {
		r.logger.Warn("Failed to list unmanaged containers", "error", err)
	}

	deployments := append(managed, unmanaged...)
	sort.Slice(deployments, func(i, j int) bool {
		managedI, _ := deployments[i]["managed"].(bool)
		managedJ, _ := deployments[j]["managed"].(bool)
		if managedI != managedJ {
			return managedI
		}
		nameI, _ := deployments[i]["display_name"].(string)
		nameJ, _ := deployments[j]["display_name"].(string)
		return nameI < nameJ
	})

	return deployments, nil
}

func (r *Reconciler) listUnmanagedContainers(exclude map[string]struct{}) ([]map[string]interface{}, error) {
	containers, err := listDockerContainers()
	if err != nil {
		return nil, err
	}

	var deployments []map[string]interface{}
	for _, container := range containers {
		name := strings.TrimSpace(container.Names)
		if name == "" {
			name = strings.TrimSpace(container.ID)
		}
		if name == "" {
			continue
		}
		if _, ok := exclude[name]; ok {
			continue
		}

		status := strings.TrimSpace(container.State)
		if status == "" {
			status = "running"
		}

		deployments = append(deployments, map[string]interface{}{
			"id":               "container:" + name,
			"kind":             "container",
			"source":           "docker",
			"managed":          false,
			"display_name":     name,
			"owner":            "",
			"repo":             name,
			"path":             "",
			"status":           status,
			"execution_id":     "",
			"execution_status": "",
			"execution_stage":  "",
			"last_error":       "",
			"history":          []executionSnapshot{},
			"container":        name,
			"container_id":     container.ID,
			"image":            strings.TrimSpace(container.Image),
			"docker_status":    strings.TrimSpace(container.Status),
		})
	}

	return deployments, nil
}

func parseComposePSOutput(out []byte) []composePSContainer {
	var containers []composePSContainer

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var container composePSContainer
		if err := json.Unmarshal([]byte(line), &container); err == nil {
			containers = append(containers, container)
		}
	}

	return containers
}

func parseDockerPSOutput(out []byte) []dockerPSContainer {
	var containers []dockerPSContainer

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var container dockerPSContainer
		if err := json.Unmarshal([]byte(line), &container); err == nil {
			containers = append(containers, container)
		}
	}

	return containers
}

func containerNames(containers []composePSContainer) []string {
	names := make([]string, 0, len(containers))
	for _, container := range containers {
		name := strings.TrimSpace(container.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names
}

func deploymentStatusFromComposeContainers(containers []composePSContainer) string {
	if len(containers) == 0 {
		return "unknown"
	}

	running := 0
	for _, container := range containers {
		if strings.EqualFold(strings.TrimSpace(container.State), "running") {
			running++
		}
	}

	switch {
	case running == len(containers):
		return "running"
	case running > 0:
		return "partial"
	default:
		return "stopped"
	}
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
		defer func() {
			if err := cmd.Wait(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				r.logger.Debug("docker compose logs process exited with error", "path", repoPath, "error", err)
			}
		}()

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

func (r *Reconciler) streamContainerLogs(ctx context.Context, container, lines string) (<-chan string, error) {
	cmd := exec.Command("docker", "logs", "-f", "--tail", lines, container)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	logChan := make(chan string)

	go func() {
		defer close(logChan)
		defer func() {
			if err := cmd.Wait(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				r.logger.Debug("docker logs process exited with error", "container", container, "error", err)
			}
		}()

		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			case logChan <- scanner.Text():
			}
		}
	}()

	return logChan, nil
}

func (r *Reconciler) processLocalState(ctx context.Context, desiredState map[string]*github.Repository, removalState map[string]bool) {
	_ = r.forEachLocalRepo(func(owner, repo, repoPath string) {
		r.processLocalRepoState(ctx, owner, repo, repoPath, desiredState, removalState)
	})
}

func (r *Reconciler) processLocalRepoState(ctx context.Context, owner, repo, repoPath string, desiredState map[string]*github.Repository, removalState map[string]bool) {
	currentKey := fmt.Sprintf("%s/%s", owner, repo)
	if removalState[currentKey] {
		r.logger.Info("Explicit removal detected", "full_name", currentKey)
		if !r.pruneService(ctx, currentKey, owner, repo, repoPath) {
			r.logger.Info("Skipping prune while execution is active", "full_name", currentKey)
		}
		return
	}
	if desiredState[currentKey] == nil {
		r.logger.Warn("Sync Divergence: Local service exists but not found in Desired State. Skipping removal.", "full_name", currentKey)
	}
}

func (r *Reconciler) pruneService(ctx context.Context, fullName, owner, repo, path string) bool {
	snapshot, acquired := r.acquireExecution(fullName, owner, repo, "prune")
	if !acquired {
		return false
	}
	r.publishExecutionEvent(ctx, snapshot)

	if locked, lockPath := isStackLocked(path); locked {
		r.logger.Warn("Skipping prune because stack is locked", "full_name", fullName, "lock_file", lockPath)
		r.publishStackLocked(ctx, owner, repo, fullName, path, lockPath)
		r.succeedExecution(ctx, fullName)
		return true
	}

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

	args := []string{"down", composeRemoveOrphansFlag}
	if removeImages {
		args = []string{"down", "--rmi", "all", composeRemoveOrphansFlag}
	}

	if err := executeComposeCommand(repoLocalPath, nil, nil, args...); err != nil {
		r.failExecution(ctx, fullName, core.ExecutionStageComposeDown, err)
		return err
	}
	return nil
}

func localComposeStateExists(repoLocalPath string) bool {
	_, err := localComposeFilePath(repoLocalPath)
	return err == nil
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

func (r *Reconciler) prepareComposeEnvironment(ctx context.Context, owner, repo, repoLocalPath string, logger *slog.Logger) ([]string, []string, func(), error) {
	secretValues, secretSources, err := r.collectComposeSecrets(ctx, owner, repo, logger)
	if err != nil {
		return nil, nil, noOpCleanup, err
	}
	if err := mergePersistedComposeSecrets(repoLocalPath, secretValues, secretSources); err != nil {
		return nil, nil, noOpCleanup, err
	}

	if err := persistComposeEnv(repoLocalPath, secretValues); err != nil {
		return nil, nil, noOpCleanup, err
	}

	runtimeFiles, err := r.collectRuntimeFiles(ctx, owner, repo, logger, secretSources)
	if err != nil {
		return nil, nil, noOpCleanup, err
	}

	runtimeFileEnv := []string{}
	cleanupRuntimeFiles := noOpCleanup
	if len(runtimeFiles) > 0 {
		runtimeFileEnv, cleanupRuntimeFiles, err = materializeRuntimeFiles(runtimeFiles)
		if err != nil {
			return nil, nil, noOpCleanup, err
		}
	}

	return secretEnvFromValues(secretValues), runtimeFileEnv, cleanupRuntimeFiles, nil
}

func (r *Reconciler) collectComposeSecrets(ctx context.Context, owner, repo string, logger *slog.Logger) (map[string]string, map[string]string, error) {
	secretValues := make(map[string]string)
	secretSources := make(map[string]string)
	for _, p := range r.secretPlugins() {
		secrets, err := loadSecretsFromPlugin(ctx, p, owner, repo)
		if err != nil {
			return nil, nil, err
		}
		r.mergeSecretValues(ctx, p.Name(), secrets, secretValues, secretSources, logger)
	}
	return secretValues, secretSources, nil
}

func (r *Reconciler) secretPlugins() []core.Plugin {
	if r.registry == nil {
		return nil
	}
	return r.registry.GetPluginsWithCapability(core.CapabilitySecrets)
}

func loadSecretsFromPlugin(ctx context.Context, p core.Plugin, owner, repo string) (map[string]string, error) {
	res, err := p.Execute(ctx, "get_secrets", map[string]interface{}{"owner": owner, "repo": repo})
	if err != nil {
		return nil, err
	}
	secrets, ok := res.(map[string]string)
	if !ok {
		return nil, nil
	}
	return secrets, nil
}

func (r *Reconciler) mergeSecretValues(ctx context.Context, pluginName string, secrets, secretValues, secretSources map[string]string, logger *slog.Logger) {
	for key, value := range secrets {
		if winner, exists := secretSources[key]; exists {
			r.publishSecretConflict(ctx, key, winner, pluginName, logger)
			continue
		}
		secretValues[key] = value
		secretSources[key] = pluginName
	}
}

func (r *Reconciler) publishSecretConflict(ctx context.Context, key, winner, skipped string, logger *slog.Logger) {
	logger.Warn("Duplicate secret key, skipping", "key", key, "winner", winner, "skipped", skipped)
	r.publish(ctx, core.InternalEvent{
		Type:    "notify_secret_conflict",
		Source:  "reconciler",
		Message: fmt.Sprintf("Secret %s already provided by %s; skipping %s", key, winner, skipped),
		Details: map[string]interface{}{
			"key":     key,
			"winner":  winner,
			"skipped": skipped,
		},
	})
}

func mergePersistedComposeSecrets(repoLocalPath string, secretValues, secretSources map[string]string) error {
	persistedSecretValues, err := loadPersistedComposeEnv(repoLocalPath)
	if err != nil {
		return err
	}
	for key, value := range persistedSecretValues {
		if _, exists := secretValues[key]; exists {
			continue
		}
		secretValues[key] = value
		secretSources[key] = "persisted_compose_env"
	}
	return nil
}

func secretEnvFromValues(secretValues map[string]string) []string {
	keys := make([]string, 0, len(secretValues))
	for key := range secretValues {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	secretEnv := make([]string, 0, len(keys))
	for _, key := range keys {
		secretEnv = append(secretEnv, fmt.Sprintf("%s=%s", key, secretValues[key]))
	}
	return secretEnv
}

func persistedComposeEnvPath(repoLocalPath string) string {
	return filepath.Join(repoLocalPath, ".git-ops", "compose_env.json")
}

func loadPersistedComposeEnv(repoLocalPath string) (map[string]string, error) {
	if strings.TrimSpace(repoLocalPath) == "" {
		return map[string]string{}, nil
	}

	data, err := os.ReadFile(persistedComposeEnvPath(repoLocalPath))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read persisted compose env: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]string{}, nil
	}

	var env map[string]string
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("decode persisted compose env: %w", err)
	}
	if env == nil {
		return map[string]string{}, nil
	}
	return env, nil
}

func persistComposeEnv(repoLocalPath string, env map[string]string) error {
	if strings.TrimSpace(repoLocalPath) == "" {
		return nil
	}
	statePath := persistedComposeEnvPath(repoLocalPath)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return fmt.Errorf("create persisted compose env dir: %w", err)
	}

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	ordered := make(map[string]string, len(env))
	for _, k := range keys {
		ordered[k] = env[k]
	}

	data, err := json.Marshal(ordered)
	if err != nil {
		return fmt.Errorf("encode persisted compose env: %w", err)
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		return fmt.Errorf("write persisted compose env: %w", err)
	}
	return nil
}

func (r *Reconciler) deployRepo(ctx context.Context, fullName string, repo *github.Repository, forceType string) {
	if repo == nil || repo.Owner == nil || repo.Owner.Login == nil || repo.Name == nil {
		return
	}

	snapshot, acquired := r.acquireExecution(fullName, *repo.Owner.Login, *repo.Name, "reconcile")
	if !acquired {
		r.logger.Warn("Execution already in progress, skipping deploy", "full_name", fullName, "execution_id", snapshot.ExecutionID)
		return
	}

	r.publishExecutionEvent(ctx, snapshot)
	r.markExecutionRunning(ctx, fullName, core.ExecutionStageFetch)
	r.deployRepoWithExecution(ctx, fullName, repo, forceType)
}

func (r *Reconciler) deployRepoWithExecution(ctx context.Context, fullName string, repo *github.Repository, forceType string) {
	logger := r.logger.With("full_name", fullName)

	spec, ok := r.fetchComposeSpec(ctx, fullName, repo, logger)
	if !ok {
		return
	}

	if locked, lockPath := isStackLocked(spec.repoLocalPath); locked {
		logger.Warn("Skipping deploy because stack is locked", "lock_file", lockPath)
		r.publishStackLocked(ctx, repo.GetOwner().GetLogin(), repo.GetName(), fullName, spec.repoLocalPath, lockPath)
		r.succeedExecution(ctx, fullName)
		return
	}

	if !r.applyForceTypePreDeploy(ctx, fullName, spec, forceType, logger) {
		return
	}

	if r.handleRestartOnly(ctx, fullName, repo, spec, forceType, logger) {
		return
	}

	if !r.cfg.DryRun {
		if err := os.MkdirAll(spec.repoLocalPath, 0755); err != nil {
			logger.Error("Failed to create local repo directory", "error", err)
			r.failExecution(ctx, fullName, core.ExecutionStageFetch, err)
			return
		}
	}

	if !r.detectComposeChange(ctx, fullName, repo, spec, forceType, logger) {
		return
	}

	r.runDeploySequence(ctx, fullName, repo, spec, logger)
}

func (r *Reconciler) fetchComposeSpec(ctx context.Context, fullName string, repo *github.Repository, logger *slog.Logger) (composeSpec, bool) {
	var (
		fileContent *github.RepositoryContent
		filename    string
		err         error
	)
	for _, candidate := range remoteComposeFilenames {
		fileContent, _, _, err = r.client.Repositories.GetContents(ctx, *repo.Owner.Login, *repo.Name, candidate, nil)
		if err == nil {
			filename = candidate
			break
		}
		if !strings.Contains(err.Error(), "404") {
			logger.Error("Failed to fetch file", "file", candidate, "error", err)
			r.failExecution(ctx, fullName, core.ExecutionStageFetch, err)
			return composeSpec{}, false
		}
	}
	if filename == "" {
		logger.Debug("No compose file found, skipping", "candidates", remoteComposeFilenames)
		r.succeedExecution(ctx, fullName)
		return composeSpec{}, false
	}

	content, err := fileContent.GetContent()
	if err != nil {
		logger.Error("Failed to decode compose file", "file", filename, "error", err)
		r.failExecution(ctx, fullName, core.ExecutionStageFetch, err)
		return composeSpec{}, false
	}

	currentCommitSHA, err := fetchRepoDefaultBranchSHA(ctx, r.client, repo)
	if err != nil {
		logger.Error("Failed to resolve repository commit sha", "error", err)
		r.failExecution(ctx, fullName, core.ExecutionStageFetch, err)
		return composeSpec{}, false
	}

	repoLocalPath := filepath.Join(r.cfg.TargetDir, *repo.Owner.Login, *repo.Name)
	return composeSpec{
		content:          content,
		currentCommitSHA: currentCommitSHA,
		repoLocalPath:    repoLocalPath,
		filePath:         filepath.Join(repoLocalPath, filename),
	}, true
}

func (r *Reconciler) applyForceTypePreDeploy(ctx context.Context, fullName string, spec composeSpec, forceType string, logger *slog.Logger) bool {
	if forceType == "clean_local_state" {
		logger.Info("Cleaning local state before deploy", "force_type", forceType)
		if !r.cfg.DryRun {
			if err := os.Remove(spec.filePath); err != nil && !os.IsNotExist(err) {
				logger.Debug("failed to remove local compose file", "path", spec.filePath, "error", err)
			}
			deployPath := filepath.Join(spec.repoLocalPath, deployDirName)
			if err := os.RemoveAll(deployPath); err != nil {
				logger.Debug("failed to remove local deploy state", "path", deployPath, "error", err)
			}
		}
		return true
	}

	if forceType == "remove_images" {
		logger.Info("Removing local images before deploy", "force_type", forceType)
		if !r.cfg.DryRun {
			if err := r.runRemoveImagesIfPresent(ctx, fullName, spec.repoLocalPath, logger); err != nil {
				return false
			}
		}
	}

	return true
}

func (r *Reconciler) handleRestartOnly(ctx context.Context, fullName string, repo *github.Repository, spec composeSpec, forceType string, logger *slog.Logger) bool {
	if forceType != "restart_only" {
		return false
	}

	logger.Info("Restarting stack containers", "force_type", forceType)
	secretEnv, runtimeFileEnv, cleanupRuntimeFiles, err := r.prepareComposeEnvironment(ctx, *repo.Owner.Login, *repo.Name, spec.repoLocalPath, logger)
	if err != nil {
		logger.Error("Failed to prepare compose environment for restart", "error", err)
		r.failExecution(ctx, fullName, core.ExecutionStageHooks, err)
		return true
	}
	defer cleanupRuntimeFiles()

	if err := r.runRestartOnly(ctx, fullName, spec.repoLocalPath, secretEnv, runtimeFileEnv); err != nil {
		logger.Error("Restart failed", "error", err)
		return true
	}
	return true
}

func (r *Reconciler) detectComposeChange(ctx context.Context, fullName string, repo *github.Repository, spec composeSpec, forceType string, logger *slog.Logger) bool {
	existing, _ := os.ReadFile(spec.filePath)
	if string(existing) == spec.content && forceType == "" {
		r.completeSuccessfulStack(ctx, fullName, repo, spec.repoLocalPath, spec.currentCommitSHA, false)
		return false
	}

	if forceType != "" {
		logger.Info("Bypassing file change check due to force type", "force_type", forceType)
	}

	r.markExecutionRunning(ctx, fullName, core.ExecutionStageDiff)
	logger.Info("Updating deployment")

	if r.cfg.DryRun {
		logger.Info("DryRun: compose diff", "diff", formatDryRunComposeDiff(string(existing), spec.content))
		r.completeSuccessfulStack(ctx, fullName, repo, spec.repoLocalPath, spec.currentCommitSHA, true)
		return false
	}

	return true
}

func formatDryRunComposeDiff(existing, next string) string {
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(existing, next, false)
	var rendered []string

	for _, diff := range diffs {
		lines := strings.Split(diff.Text, "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			switch diff.Type {
			case diffmatchpatch.DiffInsert:
				rendered = append(rendered, "+ "+line)
			case diffmatchpatch.DiffDelete:
				rendered = append(rendered, "- "+line)
			case diffmatchpatch.DiffEqual:
				rendered = append(rendered, "  "+line)
			}
		}
	}

	return strings.Join(rendered, "\n")
}

func stackLockPath(stackPath string) string {
	return filepath.Join(stackPath, ".git-ops-lock")
}

func isStackLocked(stackPath string) (bool, string) {
	lockPath := stackLockPath(stackPath)
	info, err := os.Stat(lockPath)
	if err != nil {
		return false, lockPath
	}
	return !info.IsDir(), lockPath
}

func (r *Reconciler) runDeploySequence(ctx context.Context, fullName string, repo *github.Repository, spec composeSpec, logger *slog.Logger) {
	deployCtx := deployRunContext{
		fullName:    fullName,
		repo:        repo,
		spec:        spec,
		logger:      logger,
		deployStart: time.Now(),
	}

	if !r.writeComposeFile(ctx, deployCtx) {
		return
	}

	if !r.fetchRepoHookScripts(ctx, deployCtx) {
		return
	}

	hookEnv, secretEnv, runtimeFileEnv, cleanupRuntimeFiles, ok := r.prepareDeployEnvironments(ctx, deployCtx)
	if !ok {
		return
	}
	defer cleanupRuntimeFiles()

	if !r.runPreHooks(ctx, deployCtx, hookEnv) {
		return
	}

	if !r.runComposeUpPhase(ctx, deployCtx, secretEnv, runtimeFileEnv) {
		return
	}

	if !r.runPostHooks(ctx, deployCtx, hookEnv) {
		return
	}

	deployCtx.logger.Info("Deploy sequence complete")
	r.publishDeployEvent(ctx, "deploy_success", deployCtx.repo, deploySuccessResult(deployCtx))
	r.completeSuccessfulStack(ctx, deployCtx.fullName, deployCtx.repo, deployCtx.spec.repoLocalPath, deployCtx.spec.currentCommitSHA, true)
}

func (r *Reconciler) writeComposeFile(ctx context.Context, deployCtx deployRunContext) bool {
	r.publishDeployEvent(ctx, "deploy_start", deployCtx.repo, deployStartResult(deployCtx))

	if err := os.WriteFile(deployCtx.spec.filePath, []byte(deployCtx.spec.content), 0644); err != nil {
		deployCtx.logger.Error("Failed to write docker-compose.yml", "error", err)
		r.publishDeployEvent(ctx, "deploy_failed", deployCtx.repo, deployFailureResult("failed", err, deployCtx))
		r.failExecution(ctx, deployCtx.fullName, core.ExecutionStageDiff, err)
		return false
	}

	return true
}

func (r *Reconciler) fetchRepoHookScripts(ctx context.Context, deployCtx deployRunContext) bool {
	r.markExecutionRunning(ctx, deployCtx.fullName, core.ExecutionStageHooks)
	for _, stage := range []string{"pre", "post"} {
		if err := r.fetchRepoHookScriptsForStage(ctx, *deployCtx.repo.Owner.Login, *deployCtx.repo.Name, stage, deployCtx.spec.repoLocalPath); err != nil {
			deployCtx.logger.Error(fmt.Sprintf("Global Fetch %s-Hook failed, aborting deploy", strings.Title(stage)), "error", err)
			r.publishDeployEvent(ctx, "deploy_failed", deployCtx.repo, deployFailureResult("failed", err, deployCtx))
			r.failExecution(ctx, deployCtx.fullName, core.ExecutionStageHooks, err)
			return false
		}
	}
	return true
}

func (r *Reconciler) prepareDeployEnvironments(ctx context.Context, deployCtx deployRunContext) ([]string, []string, []string, func(), bool) {
	secretEnv, runtimeFileEnv, cleanupRuntimeFiles, err := r.prepareComposeEnvironment(ctx, *deployCtx.repo.Owner.Login, *deployCtx.repo.Name, deployCtx.spec.repoLocalPath, deployCtx.logger)
	if err != nil {
		deployCtx.logger.Error("Failed to prepare compose environment", "error", err)
		r.publishDeployEvent(ctx, "deploy_failed", deployCtx.repo, deployFailureResult("failed", err, deployCtx))
		r.failExecution(ctx, deployCtx.fullName, core.ExecutionStageHooks, err)
		return nil, nil, nil, noOpCleanup, false
	}

	r.warnOnComposeEnvPersistenceRisks(ctx, deployCtx.repo, deployCtx.spec.content, secretEnv, deployCtx.logger)

	hookEnv := []string{
		fmt.Sprintf("REPO_NAME=%s", *deployCtx.repo.Name),
		fmt.Sprintf("REPO_OWNER=%s", *deployCtx.repo.Owner.Login),
		fmt.Sprintf("TARGET_DIR=%s", deployCtx.spec.repoLocalPath),
	}
	// Secrets are intentionally excluded from hook environments.
	// Hooks receive only service context values. If a hook needs credentials,
	// it must source them independently, for example from a secrets manager or
	// from files materialized by a runtime-file plugin.

	return hookEnv, secretEnv, runtimeFileEnv, cleanupRuntimeFiles, true
}

func (r *Reconciler) runPreHooks(ctx context.Context, deployCtx deployRunContext, hookEnv []string) bool {
	if r.cfg.GlobalHooksDir != "" {
		if err := utils.ExecuteHooks(ctx, filepath.Join(r.cfg.GlobalHooksDir, "pre"), hookEnv, deployCtx.logger, r.cfg.HookTimeout); err != nil {
			deployCtx.logger.Error("Global Pre-hook failed, aborting deploy", "error", err)
			r.publishDeployEvent(ctx, "deploy_failed", deployCtx.repo, deployFailureResult("failed", err, deployCtx))
			r.failExecution(ctx, deployCtx.fullName, core.ExecutionStageHooks, err)
			return false
		}
	}

	if err := utils.ExecuteHooks(ctx, filepath.Join(deployCtx.spec.repoLocalPath, deployDirName, "pre"), hookEnv, deployCtx.logger, r.cfg.HookTimeout); err != nil {
		deployCtx.logger.Error("Repo Pre-hook failed, aborting deploy", "error", err)
		r.publishDeployEvent(ctx, "deploy_failed", deployCtx.repo, deployFailureResult("failed", err, deployCtx))
		r.failExecution(ctx, deployCtx.fullName, core.ExecutionStageHooks, err)
		return false
	}

	return true
}

func (r *Reconciler) runComposeUpPhase(ctx context.Context, deployCtx deployRunContext, secretEnv, runtimeFileEnv []string) bool {
	r.markExecutionRunning(ctx, deployCtx.fullName, core.ExecutionStageComposeUp)

	if err := runComposePreflight(deployCtx.spec.repoLocalPath, runtimeFileEnv); err != nil {
		deployCtx.logger.Error("Preflight failed", "error", err)
		r.publishDeployEvent(ctx, "deploy_failed", deployCtx.repo, deployFailureResult("failed", err, deployCtx))
		r.failExecution(ctx, deployCtx.fullName, core.ExecutionStageComposeUp, err)
		return false
	}

	deployCtx.logger.Info("Running docker compose up")
	if err := executeComposeCommand(deployCtx.spec.repoLocalPath, secretEnv, runtimeFileEnv, "up", "-d", composeRemoveOrphansFlag); err != nil {
		deployCtx.logger.Error("Deploy failed", "error", err)
		r.publishDeployEvent(ctx, "deploy_failed", deployCtx.repo, deployFailureResult("failed", err, deployCtx))
		r.failExecution(ctx, deployCtx.fullName, core.ExecutionStageComposeUp, err)
		return false
	}

	return true
}

func (r *Reconciler) runPostHooks(ctx context.Context, deployCtx deployRunContext, hookEnv []string) bool {
	if err := utils.ExecuteHooks(ctx, filepath.Join(deployCtx.spec.repoLocalPath, deployDirName, "post"), hookEnv, deployCtx.logger, r.cfg.HookTimeout); err != nil {
		deployCtx.logger.Error("Repo Post-hook failed", "error", err)
	}

	if r.cfg.GlobalHooksDir != "" {
		// Repo post-hooks are best-effort and only log on failure, while global
		// post-hooks are treated as deploy-critical and still fail the execution.
		// This preserves the current behavior until hook policy is revisited.
		if err := utils.ExecuteHooks(ctx, filepath.Join(r.cfg.GlobalHooksDir, "post"), hookEnv, deployCtx.logger, r.cfg.HookTimeout); err != nil {
			deployCtx.logger.Error("Repo Post-hook execution failed", "error", err)
			r.publishDeployEvent(ctx, "deploy_failed", deployCtx.repo, deployFailureResult("failed", err, deployCtx))
			r.failExecution(ctx, deployCtx.fullName, core.ExecutionStageHooks, err)
			return false
		}
	}

	return true
}

func deployStartResult(deployCtx deployRunContext) deployEventResult {
	return deployEventResult{status: "starting", startedAt: deployCtx.deployStart}
}

func deploySuccessResult(deployCtx deployRunContext) deployEventResult {
	return deployEventResult{
		status:    "success",
		duration:  time.Since(deployCtx.deployStart).String(),
		startedAt: deployCtx.deployStart,
	}
}

func deployFailureResult(status string, err error, deployCtx deployRunContext) deployEventResult {
	result := deployEventResult{status: status, startedAt: deployCtx.deployStart}
	if err != nil {
		result.message = err.Error()
	}
	return result
}

func (r *Reconciler) collectRuntimeFiles(ctx context.Context, owner, repo string, logger *slog.Logger, existingSources map[string]string) ([]core.RuntimeFile, error) {
	files := make([]core.RuntimeFile, 0)
	runtimeSources := make(map[string]string)

	for _, p := range r.runtimeFilePlugins() {
		runtimeFiles, err := runtimeFilesFromPlugin(ctx, p, owner, repo)
		if err != nil {
			return nil, err
		}

		for _, file := range runtimeFiles {
			key, err := validateRuntimeFileKey(file.EnvKey, p.Name())
			if err != nil {
				return nil, err
			}
			if winner, skip := duplicateRuntimeFileSource(key, existingSources, runtimeSources); skip {
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

func (r *Reconciler) runtimeFilePlugins() []core.Plugin {
	if r.registry == nil {
		return nil
	}
	return r.registry.GetPluginsWithCapability(core.CapabilityRuntimeFiles)
}

func runtimeFilesFromPlugin(ctx context.Context, p core.Plugin, owner, repo string) ([]core.RuntimeFile, error) {
	res, err := p.Execute(ctx, "get_runtime_files", map[string]interface{}{"owner": owner, "repo": repo})
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", p.Name(), err)
	}
	runtimeFiles, ok := res.([]core.RuntimeFile)
	if !ok {
		return nil, fmt.Errorf("plugin %s returned unexpected runtime file payload", p.Name())
	}
	return runtimeFiles, nil
}

func validateRuntimeFileKey(envKey, pluginName string) (string, error) {
	key := strings.TrimSpace(envKey)
	switch {
	case key == "":
		return "", fmt.Errorf("plugin %s returned runtime file with empty env key", pluginName)
	case strings.Contains(key, "="):
		return "", fmt.Errorf("plugin %s returned invalid env key %q", pluginName, key)
	default:
		return key, nil
	}
}

func duplicateRuntimeFileSource(key string, existingSources, runtimeSources map[string]string) (string, bool) {
	if winner, exists := existingSources[key]; exists {
		return winner, true
	}
	if winner, exists := runtimeSources[key]; exists {
		return winner, true
	}
	return "", false
}

func materializeRuntimeFiles(files []core.RuntimeFile) ([]string, func(), error) {
	runtimeDir, err := os.MkdirTemp("", "gitops-runtime-files-*")
	if err != nil {
		return nil, noOpCleanup, fmt.Errorf("create temp runtime dir: %w", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(runtimeDir)
	}

	if err := os.Chmod(runtimeDir, 0700); err != nil {
		cleanup()
		return nil, noOpCleanup, fmt.Errorf("chmod runtime dir: %w", err)
	}

	envToPath := make(map[string]string, len(files))
	for idx, file := range files {
		envKey, filename, mode, err := runtimeFileMaterializationSpec(idx, file)
		if err != nil {
			cleanup()
			return nil, noOpCleanup, err
		}

		targetPath := filepath.Join(runtimeDir, fmt.Sprintf("%02d_%s", idx, filename))
		if err := os.WriteFile(targetPath, file.Content, mode); err != nil {
			cleanup()
			return nil, noOpCleanup, fmt.Errorf("write runtime file for %s: %w", envKey, err)
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

func runtimeFileMaterializationSpec(idx int, file core.RuntimeFile) (string, string, os.FileMode, error) {
	envKey := strings.TrimSpace(file.EnvKey)
	if envKey == "" || strings.Contains(envKey, "=") {
		return "", "", 0, fmt.Errorf("invalid runtime file env key")
	}

	filename := strings.TrimSpace(file.Filename)
	if filename == "" {
		filename = fmt.Sprintf("runtime_file_%d", idx)
	}
	filename = filepath.Base(filename)
	if filename == "" || filename == "." {
		return "", "", 0, fmt.Errorf("invalid runtime file name for %s", envKey)
	}

	mode := os.FileMode(file.Mode & 0o777)
	if mode == 0 {
		mode = 0600
	}
	return envKey, filename, mode, nil
}

func noOpCleanup() {
	// Some call sites require a cleanup callback even when there is nothing to release.
	// Keeping a named no-op function avoids nil checks and documents that behavior.
}

func (r *Reconciler) publishDeployEvent(ctx context.Context, eventType string, repo *github.Repository, result deployEventResult) {
	if repo == nil || repo.Owner == nil || repo.Name == nil || repo.Owner.Login == nil {
		return
	}
	r.publish(ctx, core.InternalEvent{
		Type:    core.EventTypeName(eventType),
		Source:  "reconciler",
		Repo:    *repo.Name,
		Message: result.message,
		Details: map[string]interface{}{
			"owner":      *repo.Owner.Login,
			"repo":       *repo.Name,
			"full_name":  fmt.Sprintf("%s/%s", *repo.Owner.Login, *repo.Name),
			"status":     result.status,
			"duration":   result.duration,
			"started_at": result.startedAt.Format(time.RFC3339),
		},
	})
}

type deployEventResult struct {
	status    string
	message   string
	duration  string
	startedAt time.Time
}

func (r *Reconciler) publishStackLocked(ctx context.Context, owner, repo, fullName, stackPath, lockPath string) {
	r.publish(ctx, core.InternalEvent{
		Type:    "stack_locked",
		Source:  "reconciler",
		Repo:    repo,
		Message: fmt.Sprintf("Stack %s is locked; skipping operation", fullName),
		Details: map[string]interface{}{
			"owner":      owner,
			"repo":       repo,
			"full_name":  fullName,
			"stack_path": stackPath,
			"lock_file":  lockPath,
		},
	})
}

// fetchRepoHookScriptsForStage downloads all scripts from .deploy/{stage} to the local repo dir.
func (r *Reconciler) fetchRepoHookScriptsForStage(ctx context.Context, owner, repo, stage, localDir string) error {
	path := fmt.Sprintf("%s/%s", deployDirName, stage)
	_, dirContent, _, err := r.client.Repositories.GetContents(ctx, owner, repo, path, nil)
	if err != nil {
		if isMissingHookDirectoryError(err) {
			return nil
		}
		return err
	}

	hooksDir := filepath.Join(localDir, deployDirName, stage)
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return err
	}

	for _, fileMeta := range dirContent {
		if fileMeta.GetType() != "file" || !strings.HasSuffix(fileMeta.GetName(), ".sh") {
			continue
		}
		if err := r.writeRepoHookScript(ctx, owner, repo, stage, hooksDir, fileMeta); err != nil {
			return err
		}
	}
	return nil
}

func isMissingHookDirectoryError(err error) bool {
	return strings.Contains(err.Error(), "404")
}

func (r *Reconciler) writeRepoHookScript(ctx context.Context, owner, repo, stage, hooksDir string, fileMeta *github.RepositoryContent) error {
	decoded, err := r.fetchRepoHookContent(ctx, owner, repo, fileMeta)
	if err != nil {
		r.logger.Error("Failed to fetch hook content", "file", fileMeta.GetName(), "error", err)
		// Pre-hooks affect deploy correctness and must be present before execution.
		// Post-hooks run after the deploy result is already known, so fetch failures stay non-fatal.
		if stage == "pre" {
			return fmt.Errorf("failed to fetch pre-hook %s: %w", fileMeta.GetName(), err)
		}
		return nil
	}

	localScriptPath := filepath.Join(hooksDir, fileMeta.GetName())
	return os.WriteFile(localScriptPath, []byte(decoded), 0755)
}

func (r *Reconciler) fetchRepoHookContent(ctx context.Context, owner, repo string, fileMeta *github.RepositoryContent) (string, error) {
	fileContent, _, _, err := r.client.Repositories.GetContents(ctx, owner, repo, fileMeta.GetPath(), nil)
	if err != nil {
		return "", err
	}
	return fileContent.GetContent()
}
