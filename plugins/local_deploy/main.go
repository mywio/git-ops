package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

var Plugin core.Plugin = &LocalDeployPlugin{}

type LocalDeployPlugin struct {
	cfg       localDeployConfig
	logger    *slog.Logger
	registry  core.PluginRegistry
	statusMu  sync.RWMutex
	healthErr error

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	sem    chan struct{}

	repositoriesMu sync.Mutex
	repositories   map[string]*repositoryController
}

type repositoryController struct {
	plugin  *LocalDeployPlugin
	mu      sync.Mutex
	state   repositoryState
	running bool
}

type localDeployConfigView struct {
	Enabled             bool     `json:"enabled"`
	RequireSystemd      bool     `json:"require_systemd"`
	StateDir            string   `json:"state_dir"`
	CacheDir            string   `json:"cache_dir"`
	LogDir              string   `json:"log_dir"`
	GlobalConcurrency   int      `json:"global_concurrency"`
	CheckoutRetention   int      `json:"checkout_retention"`
	ExecutionRetention  int      `json:"execution_retention"`
	SuccessfulRetention int      `json:"successful_retention"`
	MaxLogSize          int64    `json:"max_log_size"`
	AllowedEnvironment  []string `json:"allowed_environment"`
	HealthError         string   `json:"health_error,omitempty"`
}

func (plugin *LocalDeployPlugin) Name() string { return "local_deploy" }

func (plugin *LocalDeployPlugin) Description() string {
	return "Host-only, commit-pinned repository deployment with verification and rollback"
}

func (plugin *LocalDeployPlugin) Capabilities() []core.Capability {
	return []core.Capability{
		core.CapabilityDeployRevision,
		core.CapabilityRetryDeployment,
		core.CapabilityDeployLatest,
		core.CapabilityRollbackDeployment,
		core.CapabilityListDeployments,
		core.CapabilityStreamLogs,
	}
}

func (plugin *LocalDeployPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	plugin.logger = logger
	plugin.registry = registry
	plugin.repositories = make(map[string]*repositoryController)

	var section map[string]any
	if registry != nil {
		section = registry.GetConfig()["local_deploy"]
	}
	cfg, err := decodeLocalDeployConfig(section)
	if err != nil {
		return err
	}
	plugin.cfg = cfg
	plugin.sem = make(chan struct{}, cfg.GlobalConcurrency)
	plugin.ctx, plugin.cancel = context.WithCancel(ctx)

	if registry != nil {
		for _, desc := range localDeployEventTypes() {
			if err := registry.RegisterEventType(desc); err != nil {
				return fmt.Errorf("register event type %q: %w", desc.Name, err)
			}
		}
		registry.Subscribe(string(core.EventTypeLocalDeployRequested), plugin.handleRequestedEvent)
	}

	if !cfg.Enabled {
		return nil
	}
	if err := plugin.preflight(); err != nil {
		plugin.setHealthError(err)
		logger.Error("local_deploy preflight failed", "error", err)
	}
	return nil
}

func localDeployEventTypes() []core.EventTypeDesc {
	fields := map[string]core.PayloadField{
		"execution_id": {Type: "string", Description: "Execution identifier", Required: true},
		"owner":        {Type: "string", Description: "Repository owner", Required: true},
		"repo":         {Type: "string", Description: "Repository name", Required: true},
		"commit_sha":   {Type: "string", Description: "Exact revision", Required: true},
	}
	return []core.EventTypeDesc{
		{Name: core.EventTypeLocalDeployRequested, Description: "Eligible exact revision queued for local deployment"},
		{Name: "local_deploy_started", Description: "Local deployment execution started", PayloadSpec: fields},
		{Name: "local_deploy_phase", Description: "Local deployment phase changed", PayloadSpec: fields},
		{Name: "local_deploy_succeeded", Description: "Local deployment succeeded", PayloadSpec: fields},
		{Name: "local_deploy_failed", Description: "Local deployment failed", PayloadSpec: fields},
		{Name: "local_deploy_superseded", Description: "Local deployment superseded before deploy", PayloadSpec: fields},
		{Name: "local_deploy_rollback_started", Description: "Local deployment rollback started", PayloadSpec: fields},
		{Name: "local_deploy_rollback_succeeded", Description: "Local deployment rollback succeeded", PayloadSpec: fields},
		{Name: "local_deploy_rollback_failed", Description: "Local deployment rollback failed", PayloadSpec: fields},
	}
}

func (plugin *LocalDeployPlugin) preflight() error {
	var problems []string
	if plugin.cfg.RequireSystemd {
		if _, inContainer := os.Stat("/.dockerenv"); inContainer == nil {
			problems = append(problems, "container execution is unsupported")
		}
		if os.Getenv("INVOCATION_ID") == "" && os.Getenv("JOURNAL_STREAM") == "" {
			problems = append(problems, "systemd invocation environment is required")
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		problems = append(problems, "git is unavailable")
	}
	if err := checkDockerAccess(); err != nil {
		problems = append(problems, err.Error())
	}
	for _, directory := range []string{plugin.cfg.StateDir, plugin.cfg.CacheDir, plugin.cfg.LogDir} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", directory, err))
			continue
		}
		probe := filepath.Join(directory, ".write-test")
		if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
			problems = append(problems, fmt.Sprintf("%s is not writable: %v", directory, err))
		} else {
			_ = os.Remove(probe)
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (plugin *LocalDeployPlugin) Start(context.Context) error {
	if !plugin.cfg.Enabled || plugin.healthError() != nil {
		return nil
	}
	states, err := loadRepositoryStates(plugin.cfg.StateDir)
	if err != nil {
		plugin.setHealthError(err)
		return nil
	}
	for index := range states {
		state := states[index]
		if state.Active != nil {
			now := time.Now().UTC()
			state.Active.Status = core.ExecutionStatusFailed
			state.Active.FailureClass = core.FailureClassTransient
			state.Active.Error = "execution interrupted by process restart"
			state.Active.FinishedAt = &now
			state.History = append([]deploymentExecution{*state.Active}, state.History...)
			state.Active = nil
			trimExecutionHistory(&state, plugin.cfg.ExecutionRetention)
			if err := persistRepositoryState(plugin.cfg.StateDir, state); err != nil {
				plugin.setHealthError(err)
				continue
			}
		}
		controller := &repositoryController{plugin: plugin, state: state}
		plugin.repositories[state.Owner+"/"+state.Repo] = controller
		if state.Pending != nil {
			controller.running = true
			plugin.wg.Add(1)
			go controller.work()
		}
	}
	return nil
}

func (plugin *LocalDeployPlugin) Stop(ctx context.Context) error {
	if plugin.cancel != nil {
		plugin.cancel()
	}
	done := make(chan struct{})
	go func() {
		plugin.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (plugin *LocalDeployPlugin) Status() core.ServiceStatus {
	if !plugin.cfg.Enabled {
		return core.StatusDegraded
	}
	if plugin.healthError() != nil {
		return core.StatusUnhealthy
	}
	return core.StatusHealthy
}

func (plugin *LocalDeployPlugin) Config() any {
	healthError := ""
	if err := plugin.healthError(); err != nil {
		healthError = err.Error()
	}
	return localDeployConfigView{
		Enabled: plugin.cfg.Enabled, RequireSystemd: plugin.cfg.RequireSystemd,
		StateDir: plugin.cfg.StateDir, CacheDir: plugin.cfg.CacheDir, LogDir: plugin.cfg.LogDir,
		GlobalConcurrency: plugin.cfg.GlobalConcurrency, CheckoutRetention: plugin.cfg.CheckoutRetention,
		ExecutionRetention: plugin.cfg.ExecutionRetention, SuccessfulRetention: plugin.cfg.SuccessfulRetention,
		MaxLogSize: plugin.cfg.MaxLogSize, AllowedEnvironment: append([]string(nil), plugin.cfg.AllowedEnvironment...),
		HealthError: healthError,
	}
}

func (plugin *LocalDeployPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	switch action {
	case "list_deployments":
		return plugin.listDeployments(), nil
	case "stream_logs":
		return plugin.streamLogs(ctx, params)
	}
	if !plugin.cfg.Enabled {
		return nil, fmt.Errorf("local_deploy is disabled")
	}
	if err := plugin.healthError(); err != nil {
		return nil, fmt.Errorf("local_deploy is unhealthy: %w", err)
	}
	owner, repo, err := localDeployRepoParams(params)
	if err != nil {
		return nil, err
	}
	trigger := stringParam(params, "trigger")
	if trigger == "" {
		trigger = action
	}
	requestedBy := stringParam(params, "requested_by")
	switch action {
	case "deploy_revision":
		sha := stringParam(params, "sha")
		if !fullCommitSHA.MatchString(sha) {
			return nil, fmt.Errorf("deploy_revision requires a full commit SHA")
		}
		return true, plugin.queue(revisionRequest{Owner: owner, Repo: repo, Ref: stringParam(params, "ref"), SHA: sha, Trigger: trigger, RequestedBy: requestedBy})
	case "deploy_latest":
		sha, err := resolveRemoteHEAD(ctx, owner, repo)
		if err != nil {
			return nil, err
		}
		return true, plugin.queue(revisionRequest{Owner: owner, Repo: repo, Ref: "HEAD", SHA: sha, Trigger: trigger, RequestedBy: requestedBy})
	case "retry_deployment":
		controller := plugin.controller(owner, repo)
		controller.mu.Lock()
		sha := stringParam(params, "sha")
		if sha == "" {
			for _, execution := range controller.state.History {
				if execution.Status == core.ExecutionStatusFailed || execution.Status == core.ExecutionStatusCancelled {
					sha = execution.SHA
					break
				}
			}
		}
		controller.mu.Unlock()
		if !fullCommitSHA.MatchString(sha) {
			return nil, fmt.Errorf("no failed revision is available to retry")
		}
		return true, plugin.queue(revisionRequest{Owner: owner, Repo: repo, Ref: "retry", SHA: sha, Trigger: trigger, RequestedBy: requestedBy})
	case "rollback_deployment":
		target := stringParam(params, "sha")
		controller := plugin.controller(owner, repo)
		controller.mu.Lock()
		retained := successfulContains(controller.state.Successful, target)
		controller.mu.Unlock()
		if !retained {
			return nil, fmt.Errorf("rollback target must be a retained successful revision")
		}
		return true, plugin.queue(revisionRequest{Owner: owner, Repo: repo, Ref: "rollback", SHA: target, Trigger: trigger, RequestedBy: requestedBy, ManualRollback: true})
	case "rollback_summary":
		target := stringParam(params, "sha")
		controller := plugin.controller(owner, repo)
		return controller.rollbackSummary(target)
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

func localDeployRepoParams(params map[string]interface{}) (string, string, error) {
	owner := stringParam(params, "owner")
	repo := stringParam(params, "repo")
	if err := core.ValidateStackIdentity(owner, repo); err != nil {
		return "", "", err
	}
	return owner, repo, nil
}

func stringParam(params map[string]interface{}, name string) string {
	value, _ := params[name].(string)
	return strings.TrimSpace(value)
}

func (plugin *LocalDeployPlugin) handleRequestedEvent(ctx context.Context, event core.InternalEvent) {
	request, ok := core.RepositoryRevisionFromEvent(event)
	if !ok {
		plugin.logger.Warn("invalid local deployment request event")
		return
	}
	if err := plugin.queue(revisionRequest{
		Owner: request.Owner, Repo: request.Repo, Ref: request.Ref, SHA: request.CommitSHA,
		DeliveryID: request.DeliveryID, Trigger: request.Trigger,
	}); err != nil {
		plugin.logger.Error("failed to queue local deployment", "owner", request.Owner, "repo", request.Repo, "sha", request.CommitSHA, "error", err)
	}
}

func (plugin *LocalDeployPlugin) queue(request revisionRequest) error {
	if !plugin.cfg.Enabled {
		return fmt.Errorf("local_deploy is disabled")
	}
	if err := plugin.healthError(); err != nil {
		return err
	}
	if err := core.ValidateStackIdentity(request.Owner, request.Repo); err != nil {
		return err
	}
	if !fullCommitSHA.MatchString(request.SHA) {
		return fmt.Errorf("invalid commit SHA")
	}
	controller := plugin.controller(request.Owner, request.Repo)
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.state.CurrentSHA == request.SHA && !request.ManualRollback {
		return nil
	}
	if controller.state.Pending != nil && controller.state.Pending.SHA == request.SHA && controller.state.Pending.ManualRollback == request.ManualRollback {
		return nil
	}
	if controller.state.Active != nil && controller.state.Active.SHA == request.SHA && !request.ManualRollback {
		return nil
	}
	controller.state.Pending = &request
	if err := persistRepositoryState(plugin.cfg.StateDir, controller.state); err != nil {
		return err
	}
	plugin.publishSimple("local_deploy_requested", nil, controller.state, request, "", "", "")
	if !controller.running {
		controller.running = true
		plugin.wg.Add(1)
		go controller.work()
	}
	return nil
}

func (plugin *LocalDeployPlugin) controller(owner, repo string) *repositoryController {
	key := owner + "/" + repo
	plugin.repositoriesMu.Lock()
	defer plugin.repositoriesMu.Unlock()
	if controller := plugin.repositories[key]; controller != nil {
		return controller
	}
	controller := &repositoryController{plugin: plugin, state: repositoryState{Owner: owner, Repo: repo}}
	plugin.repositories[key] = controller
	return controller
}

func (controller *repositoryController) work() {
	defer controller.plugin.wg.Done()
	for {
		controller.mu.Lock()
		if controller.state.Pending == nil {
			controller.running = false
			controller.mu.Unlock()
			return
		}
		request := *controller.state.Pending
		controller.state.Pending = nil
		execution := &deploymentExecution{
			ID: newExecutionID(request.SHA), SHA: request.SHA, PreviousSuccessful: controller.state.CurrentSHA,
			Trigger: request.Trigger, Status: core.ExecutionStatusRequested, Phase: core.ExecutionStageRequested,
			Verification: "pending", StartedAt: time.Now().UTC(),
		}
		activeCopy := *execution
		controller.state.Active = &activeCopy
		_ = persistRepositoryState(controller.plugin.cfg.StateDir, controller.state)
		controller.mu.Unlock()

		select {
		case controller.plugin.sem <- struct{}{}:
		case <-controller.plugin.ctx.Done():
			controller.finishInterrupted(execution)
			return
		}
		controller.execute(request, execution)
		<-controller.plugin.sem

		controller.mu.Lock()
		controller.state.Active = nil
		controller.state.History = append([]deploymentExecution{*execution}, controller.state.History...)
		trimExecutionHistory(&controller.state, controller.plugin.cfg.ExecutionRetention)
		_ = persistRepositoryState(controller.plugin.cfg.StateDir, controller.state)
		controller.mu.Unlock()
		controller.cleanupCheckouts("")
	}
}

func newExecutionID(sha string) string {
	short := sha
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%d-%s", time.Now().UTC().UnixNano(), short)
}

func (controller *repositoryController) execute(request revisionRequest, execution *deploymentExecution) {
	plugin := controller.plugin
	if err := os.MkdirAll(plugin.cfg.LogDir, 0o750); err != nil {
		controller.fail(execution, core.ExecutionStageCheckout, core.FailureClassDependency, err)
		return
	}
	execution.LogPath = filepath.Join(plugin.cfg.LogDir, execution.ID+".log")
	logFile, err := os.OpenFile(execution.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		controller.fail(execution, core.ExecutionStageCheckout, core.FailureClassDependency, err)
		return
	}
	defer func() { _ = logFile.Close() }()

	baseMetadata := map[string]string{
		"GHOPS_OWNER": request.Owner, "GHOPS_REPO": request.Repo, "GHOPS_COMMIT_SHA": request.SHA,
		"GHOPS_PREVIOUS_SUCCESS_SHA": execution.PreviousSuccessful, "GHOPS_EXECUTION_ID": execution.ID,
	}
	_, secretValues := recipeEnvironment(plugin.cfg.AllowedEnvironment, baseMetadata)
	output := newBoundedLogWriter(logFile, plugin.cfg.MaxLogSize, secretValues)

	execution.Status = core.ExecutionStatusRunning
	plugin.publishSimple("local_deploy_started", execution, controller.snapshot(), request, "", "", "")
	controller.setPhase(execution, core.ExecutionStageCheckout)
	checkout, recipe, err := plugin.prepareCheckout(plugin.ctx, request, output)
	if err != nil {
		controller.fail(execution, core.ExecutionStageCheckout, classifyFailure(err), err)
		return
	}
	if err := validateRecipeExecutables(recipe); err != nil {
		plugin.setHealthError(err)
		controller.fail(execution, core.ExecutionStageCheckout, core.FailureClassDependency, err)
		return
	}
	execution.RollbackCommands = recipeCommandSummary(recipe, "rollback")
	if controller.superseded(request.SHA) {
		controller.supersede(execution, request)
		return
	}
	if plugin.coreDryRun() {
		execution.Verification = "dry_run"
		controller.succeed(execution, request, false)
		return
	}
	baseMetadata["GHOPS_SOURCE_DIR"] = checkout
	environment, secretValues := recipeEnvironment(plugin.cfg.AllowedEnvironment, baseMetadata)
	output.secrets = nil
	for _, value := range secretValues {
		if len(value) >= 4 {
			output.secrets = append(output.secrets, []byte(value))
		}
	}

	if request.ManualRollback {
		controller.executeManualRollback(request, execution, checkout, recipe, environment, output)
		return
	}
	if err := controller.runPhase(execution, core.ExecutionStageBuild, recipe.Phases.Build, checkout, environment, output, true); err != nil {
		if errors.Is(err, errSuperseded) {
			controller.supersede(execution, request)
		} else {
			controller.fail(execution, core.ExecutionStageBuild, classifyFailure(err), err)
		}
		return
	}
	if controller.superseded(request.SHA) {
		controller.supersede(execution, request)
		return
	}
	if err := controller.runPhase(execution, core.ExecutionStageDeploy, recipe.Phases.Deploy, checkout, environment, output, false); err != nil {
		controller.failWithRollback(request, execution, core.ExecutionStageDeploy, err, output)
		return
	}
	if err := controller.verify(execution, recipe, checkout, environment, output); err != nil {
		controller.failWithRollback(request, execution, core.ExecutionStageVerify, err, output)
		return
	}
	execution.Verification = verificationLabel(recipe)
	controller.succeed(execution, request, true)
}

var errSuperseded = errors.New("revision superseded")

func (controller *repositoryController) runPhase(execution *deploymentExecution, phase core.ExecutionStage, steps []recipeStep, checkout string, environment []string, output io.Writer, checkSuperseded bool) error {
	controller.setPhase(execution, phase)
	for _, step := range steps {
		phaseEnvironment := append([]string(nil), environment...)
		phaseEnvironment = append(phaseEnvironment, "GHOPS_PHASE="+string(phase))
		if err := runRecipeStep(controller.plugin.ctx, checkout, step, phaseEnvironment, output); err != nil {
			return err
		}
		if checkSuperseded && controller.superseded(execution.SHA) {
			return errSuperseded
		}
	}
	return nil
}

func (controller *repositoryController) verify(execution *deploymentExecution, recipe *deployRecipe, checkout string, environment []string, output io.Writer) error {
	if len(recipe.Phases.Verify) > 0 {
		if err := controller.runPhase(execution, core.ExecutionStageVerify, recipe.Phases.Verify, checkout, environment, output, false); err != nil {
			return err
		}
	} else {
		controller.setPhase(execution, core.ExecutionStageVerify)
	}
	if recipe.Health != nil && recipe.Health.Compose != nil {
		if _, err := exec.LookPath("docker"); err != nil {
			controller.plugin.setHealthError(fmt.Errorf("docker is unavailable: %w", err))
			return fmt.Errorf("docker is unavailable: %w", err)
		}
		if err := runComposeHealth(controller.plugin.ctx, checkout, recipe.Health.Compose, output); err != nil {
			return err
		}
	}
	return nil
}

func verificationLabel(recipe *deployRecipe) string {
	if len(recipe.Phases.Verify) == 0 && (recipe.Health == nil || recipe.Health.Compose == nil) {
		return "unverified"
	}
	return "verified"
}

func (controller *repositoryController) superseded(sha string) bool {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.state.Pending != nil && controller.state.Pending.SHA != sha
}

func (controller *repositoryController) setPhase(execution *deploymentExecution, phase core.ExecutionStage) {
	controller.mu.Lock()
	execution.Phase = phase
	setActiveExecution(&controller.state, execution)
	_ = persistRepositoryState(controller.plugin.cfg.StateDir, controller.state)
	state := cloneRepositoryState(controller.state)
	controller.mu.Unlock()
	controller.plugin.publishSimple("local_deploy_phase", execution, state, revisionRequest{Owner: state.Owner, Repo: state.Repo, SHA: execution.SHA, Trigger: execution.Trigger}, string(phase), "", "")
	controller.plugin.publishExecution(execution, state, core.ExecutionStatusRunning)
}

func (controller *repositoryController) fail(execution *deploymentExecution, phase core.ExecutionStage, class core.FailureClass, err error) {
	now := time.Now().UTC()
	execution.Status = core.ExecutionStatusFailed
	execution.Phase = phase
	execution.FailureClass = class
	execution.Error = err.Error()
	execution.FinishedAt = &now
	controller.mu.Lock()
	setActiveExecution(&controller.state, execution)
	_ = persistRepositoryState(controller.plugin.cfg.StateDir, controller.state)
	state := cloneRepositoryState(controller.state)
	controller.mu.Unlock()
	controller.plugin.publishSimple("local_deploy_failed", execution, state, revisionRequest{Owner: state.Owner, Repo: state.Repo, SHA: execution.SHA, Trigger: execution.Trigger}, string(phase), class, err.Error())
	controller.plugin.publishExecution(execution, state, core.ExecutionStatusFailed)
}

func (controller *repositoryController) supersede(execution *deploymentExecution, request revisionRequest) {
	now := time.Now().UTC()
	execution.Status = core.ExecutionStatusCancelled
	execution.Error = "superseded by a newer pending revision"
	execution.FinishedAt = &now
	controller.mu.Lock()
	setActiveExecution(&controller.state, execution)
	_ = persistRepositoryState(controller.plugin.cfg.StateDir, controller.state)
	state := cloneRepositoryState(controller.state)
	controller.mu.Unlock()
	controller.plugin.publishSimple("local_deploy_superseded", execution, state, request, string(execution.Phase), "", execution.Error)
	controller.plugin.publishExecution(execution, state, core.ExecutionStatusCancelled)
}

func (controller *repositoryController) succeed(execution *deploymentExecution, request revisionRequest, updateCurrent bool) {
	now := time.Now().UTC()
	execution.Status = core.ExecutionStatusSucceeded
	execution.Phase = core.ExecutionStageComplete
	execution.FinishedAt = &now
	controller.mu.Lock()
	setActiveExecution(&controller.state, execution)
	if updateCurrent {
		controller.state.PreviousSuccessfulSHA = controller.state.CurrentSHA
		controller.state.CurrentSHA = execution.SHA
		controller.state.Successful = append([]successfulRevision{{
			SHA: execution.SHA, ExecutionID: execution.ID, DeployedAt: now, Verification: execution.Verification, RollbackCommands: append([]string(nil), execution.RollbackCommands...),
		}}, removeSuccessful(controller.state.Successful, execution.SHA)...)
		if len(controller.state.Successful) > controller.plugin.cfg.SuccessfulRetention {
			controller.state.Successful = controller.state.Successful[:controller.plugin.cfg.SuccessfulRetention]
		}
	}
	_ = persistRepositoryState(controller.plugin.cfg.StateDir, controller.state)
	state := cloneRepositoryState(controller.state)
	controller.mu.Unlock()
	controller.plugin.publishSimple("local_deploy_succeeded", execution, state, request, string(execution.Phase), "", "")
	controller.plugin.publishExecution(execution, state, core.ExecutionStatusSucceeded)
}

func (controller *repositoryController) failWithRollback(request revisionRequest, execution *deploymentExecution, phase core.ExecutionStage, cause error, output io.Writer) {
	controller.mu.Lock()
	target := controller.state.CurrentSHA
	controller.mu.Unlock()
	if target == "" {
		execution.RollbackStatus = "not_available"
		controller.fail(execution, phase, core.FailureClassPermanent, fmt.Errorf("%w; no previous successful revision exists", cause))
		return
	}
	execution.RollbackTarget = target
	execution.RollbackStatus = "running"
	state := controller.snapshot()
	controller.plugin.publishSimple("local_deploy_rollback_started", execution, state, request, "rollback", "", cause.Error())
	controller.setPhase(execution, core.ExecutionStageRollback)

	rollbackRequest := revisionRequest{Owner: request.Owner, Repo: request.Repo, SHA: target, Ref: "rollback", Trigger: "automatic_rollback"}
	checkout, recipe, err := controller.plugin.prepareCheckout(controller.plugin.ctx, rollbackRequest, output)
	if err == nil {
		metadata := map[string]string{
			"GHOPS_OWNER": request.Owner, "GHOPS_REPO": request.Repo, "GHOPS_COMMIT_SHA": target,
			"GHOPS_PREVIOUS_SUCCESS_SHA": execution.PreviousSuccessful, "GHOPS_EXECUTION_ID": execution.ID,
			"GHOPS_SOURCE_DIR": checkout, "GHOPS_FAILED_SHA": request.SHA,
		}
		environment, _ := recipeEnvironment(controller.plugin.cfg.AllowedEnvironment, metadata)
		err = controller.runPhase(execution, core.ExecutionStageRollback, recipe.Phases.Rollback, checkout, environment, output, false)
		if err == nil {
			err = controller.verify(execution, recipe, checkout, environment, output)
		}
	}
	if err != nil {
		execution.RollbackStatus = "failed"
		controller.plugin.publishSimple("local_deploy_rollback_failed", execution, controller.snapshot(), request, "rollback", classifyFailure(err), err.Error())
		controller.fail(execution, phase, classifyFailure(err), fmt.Errorf("%w; rollback to %s failed: %v", cause, target, err))
		return
	}
	execution.RollbackStatus = "succeeded"
	controller.plugin.publishSimple("local_deploy_rollback_succeeded", execution, controller.snapshot(), request, "rollback", "", "")
	controller.fail(execution, phase, classifyFailure(cause), fmt.Errorf("%w; restored %s", cause, target))
}

func (controller *repositoryController) executeManualRollback(request revisionRequest, execution *deploymentExecution, checkout string, recipe *deployRecipe, environment []string, output io.Writer) {
	execution.RollbackTarget = request.SHA
	execution.RollbackStatus = "running"
	controller.plugin.publishSimple("local_deploy_rollback_started", execution, controller.snapshot(), request, "rollback", "", "")
	if err := controller.runPhase(execution, core.ExecutionStageRollback, recipe.Phases.Rollback, checkout, environment, output, false); err != nil {
		execution.RollbackStatus = "failed"
		controller.plugin.publishSimple("local_deploy_rollback_failed", execution, controller.snapshot(), request, "rollback", classifyFailure(err), err.Error())
		controller.fail(execution, core.ExecutionStageRollback, classifyFailure(err), err)
		return
	}
	if err := controller.verify(execution, recipe, checkout, environment, output); err != nil {
		execution.RollbackStatus = "failed"
		controller.plugin.publishSimple("local_deploy_rollback_failed", execution, controller.snapshot(), request, "verify", classifyFailure(err), err.Error())
		controller.fail(execution, core.ExecutionStageVerify, classifyFailure(err), err)
		return
	}
	execution.RollbackStatus = "succeeded"
	execution.Verification = verificationLabel(recipe)
	controller.plugin.publishSimple("local_deploy_rollback_succeeded", execution, controller.snapshot(), request, "rollback", "", "")
	controller.succeed(execution, request, true)
}

func classifyFailure(err error) core.FailureClass {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "required"), strings.Contains(message, "invalid"), strings.Contains(message, "unsupported"), strings.Contains(message, "must"):
		return core.FailureClassValidation
	case strings.Contains(message, "unavailable"), strings.Contains(message, "not found"), strings.Contains(message, "permission"):
		return core.FailureClassDependency
	case strings.Contains(message, "timed out"), strings.Contains(message, "timeout"):
		return core.FailureClassTransient
	default:
		return core.FailureClassUnknown
	}
}

func (controller *repositoryController) finishInterrupted(execution *deploymentExecution) {
	controller.fail(execution, execution.Phase, core.FailureClassTransient, context.Canceled)
	controller.mu.Lock()
	controller.state.Active = nil
	controller.state.History = append([]deploymentExecution{*execution}, controller.state.History...)
	controller.running = false
	_ = persistRepositoryState(controller.plugin.cfg.StateDir, controller.state)
	controller.mu.Unlock()
}

func (controller *repositoryController) snapshot() repositoryState {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return cloneRepositoryState(controller.state)
}

func trimExecutionHistory(state *repositoryState, limit int) {
	if len(state.History) > limit {
		state.History = state.History[:limit]
	}
}

func removeSuccessful(values []successfulRevision, sha string) []successfulRevision {
	result := make([]successfulRevision, 0, len(values))
	for _, value := range values {
		if value.SHA != sha {
			result = append(result, value)
		}
	}
	return result
}

func successfulContains(values []successfulRevision, sha string) bool {
	for _, value := range values {
		if value.SHA == sha {
			return true
		}
	}
	return false
}

func (plugin *LocalDeployPlugin) coreDryRun() bool {
	if plugin.registry == nil {
		return false
	}
	value := plugin.registry.GetConfig()["core"]["dry_run"]
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func (plugin *LocalDeployPlugin) setHealthError(err error) {
	plugin.statusMu.Lock()
	plugin.healthErr = err
	plugin.statusMu.Unlock()
}

func (plugin *LocalDeployPlugin) healthError() error {
	plugin.statusMu.RLock()
	defer plugin.statusMu.RUnlock()
	return plugin.healthErr
}

func (plugin *LocalDeployPlugin) publishSimple(eventType string, execution *deploymentExecution, state repositoryState, request revisionRequest, phase string, class core.FailureClass, failure string) {
	if plugin.registry == nil {
		return
	}
	details := map[string]any{
		"owner": request.Owner, "repo": request.Repo, "commit_sha": request.SHA,
		"trigger": request.Trigger, "delivery_id": request.DeliveryID,
	}
	if execution != nil {
		details["execution_id"] = execution.ID
		details["previous_success_sha"] = execution.PreviousSuccessful
		details["status"] = string(execution.Status)
		details["verification"] = execution.Verification
		details["rollback_status"] = execution.RollbackStatus
		details["rollback_target"] = execution.RollbackTarget
		details["duration"] = time.Since(execution.StartedAt).String()
	}
	if phase != "" {
		details["phase"] = phase
	}
	if class != "" {
		details["failure_class"] = string(class)
	}
	if failure != "" {
		details["error"] = failure
	}
	plugin.registry.Publish(plugin.ctx, core.InternalEvent{Type: core.EventTypeName(eventType), Source: plugin.Name(), Repo: request.Repo, Details: details})
}

func (plugin *LocalDeployPlugin) publishExecution(execution *deploymentExecution, state repositoryState, status core.ExecutionStatus) {
	if plugin.registry == nil {
		return
	}
	severity := core.AlertSeverityInfo
	if status == core.ExecutionStatusFailed {
		severity = core.AlertSeverityError
		if execution.RollbackStatus == "not_available" || execution.RollbackStatus == "failed" {
			severity = core.AlertSeverityCritical
		}
	}
	plugin.registry.Publish(plugin.ctx, core.NewExecutionEvent(core.ExecutionEventInput{
		ExecutionID: execution.ID, Owner: state.Owner, Repo: state.Repo,
		Stage: execution.Phase, Status: status, FailureClass: execution.FailureClass,
		AlertSeverity: severity, Trigger: execution.Trigger, Source: plugin.Name(),
		Details: map[string]any{
			"commit_sha": execution.SHA, "previous_success_sha": execution.PreviousSuccessful,
			"duration": time.Since(execution.StartedAt).String(), "verification": execution.Verification,
			"rollback_status": execution.RollbackStatus, "rollback_target": execution.RollbackTarget,
			"error": execution.Error,
		},
	}))
}

func (plugin *LocalDeployPlugin) listDeployments() []map[string]interface{} {
	plugin.repositoriesMu.Lock()
	controllers := make([]*repositoryController, 0, len(plugin.repositories))
	for _, controller := range plugin.repositories {
		controllers = append(controllers, controller)
	}
	plugin.repositoriesMu.Unlock()
	result := make([]map[string]interface{}, 0, len(controllers))
	for _, controller := range controllers {
		state := controller.snapshot()
		result = append(result, deploymentMap(plugin.cfg, state))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i]["display_name"].(string) < result[j]["display_name"].(string)
	})
	return result
}

func deploymentMap(cfg localDeployConfig, state repositoryState) map[string]interface{} {
	executionStatus := ""
	executionStage := ""
	executionID := ""
	lastError := ""
	elapsed := ""
	logPath := ""
	verification := ""
	if state.Active != nil {
		executionStatus = string(state.Active.Status)
		executionStage = string(state.Active.Phase)
		executionID = state.Active.ID
		lastError = state.Active.Error
		elapsed = time.Since(state.Active.StartedAt).Round(time.Second).String()
		logPath = state.Active.LogPath
		verification = state.Active.Verification
	} else if len(state.History) > 0 {
		last := state.History[0]
		executionStatus = string(last.Status)
		executionStage = string(last.Phase)
		executionID = last.ID
		lastError = last.Error
		logPath = last.LogPath
		verification = last.Verification
	}
	pendingSHA := ""
	if state.Pending != nil {
		pendingSHA = state.Pending.SHA
	}
	status := "stopped"
	if state.CurrentSHA != "" {
		status = "running"
	}
	if executionStatus == string(core.ExecutionStatusFailed) && state.CurrentSHA == "" {
		status = "failed"
	}
	return map[string]interface{}{
		"id": "local-deploy:" + state.Owner + "/" + state.Repo, "kind": "local_deploy",
		"source": "local_deploy", "managed": true, "display_name": state.Owner + "/" + state.Repo,
		"group_id": "local-deploy-owner:" + state.Owner, "group_name": state.Owner,
		"group_kind": "local_deploy", "group_path": filepath.Join(cfg.CacheDir, "checkouts", state.Owner),
		"owner": state.Owner, "repo": state.Repo, "path": filepath.Join(cfg.CacheDir, "checkouts", state.Owner, state.Repo),
		"status": status, "execution_id": executionID, "execution_status": executionStatus,
		"execution_stage": executionStage, "last_error": lastError, "history": state.History,
		"current_sha": state.CurrentSHA, "pending_sha": pendingSHA,
		"previous_success_sha": state.PreviousSuccessfulSHA, "successful_revisions": state.Successful,
		"verification": verification, "elapsed": elapsed, "log_path": logPath,
	}
}

func (plugin *LocalDeployPlugin) streamLogs(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	owner, repo, err := localDeployRepoParams(params)
	if err != nil {
		return nil, err
	}
	controller := plugin.controller(owner, repo)
	state := controller.snapshot()
	path := ""
	if state.Active != nil {
		path = state.Active.LogPath
	} else if len(state.History) > 0 {
		path = state.History[0].LogPath
	}
	if path == "" {
		return nil, fmt.Errorf("no local deployment logs found")
	}
	initial, err := tailLog(path, parseLines(params["lines"]))
	if err != nil {
		return nil, err
	}
	ch := make(chan string, 8)
	go func() {
		defer close(ch)
		if initial != "" {
			select {
			case ch <- initial:
			case <-ctx.Done():
				return
			}
		}
		file, err := os.Open(path)
		if err != nil {
			return
		}
		defer func() { _ = file.Close() }()
		offset, _ := file.Seek(0, io.SeekEnd)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		buffer := make([]byte, 32*1024)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = file.Seek(offset, io.SeekStart)
				n, _ := file.Read(buffer)
				if n > 0 {
					offset += int64(n)
					select {
					case ch <- string(buffer[:n]):
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return (<-chan string)(ch), nil
}

func (controller *repositoryController) rollbackSummary(target string) (interface{}, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if !successfulContains(controller.state.Successful, target) {
		return nil, fmt.Errorf("rollback target must be a retained successful revision")
	}
	for _, revision := range controller.state.Successful {
		if revision.SHA == target && len(revision.RollbackCommands) > 0 {
			return map[string]any{
				"target_sha": target, "current_sha": controller.state.CurrentSHA,
				"commands": append([]string(nil), revision.RollbackCommands...),
			}, nil
		}
	}
	checkout := controller.plugin.checkoutPath(controller.state.Owner, controller.state.Repo, target)
	recipe, err := readRecipe(checkout)
	if err != nil {
		return map[string]any{"target_sha": target, "current_sha": controller.state.CurrentSHA, "commands": []string{"recipe checkout will be recreated before rollback"}}, nil
	}
	return map[string]any{
		"target_sha": target, "current_sha": controller.state.CurrentSHA,
		"commands": recipeCommandSummary(recipe, "rollback"),
	}, nil
}

func (controller *repositoryController) cleanupCheckouts(activeCandidate string) {
	controller.mu.Lock()
	keep := map[string]bool{controller.state.CurrentSHA: true, activeCandidate: true}
	for index, revision := range controller.state.Successful {
		if index >= controller.plugin.cfg.CheckoutRetention {
			break
		}
		keep[revision.SHA] = true
	}
	owner, repo := controller.state.Owner, controller.state.Repo
	controller.mu.Unlock()
	root := filepath.Join(controller.plugin.cfg.CacheDir, "checkouts", owner, repo)
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.IsDir() && !keep[entry.Name()] && fullCommitSHA.MatchString(entry.Name()) {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
}
