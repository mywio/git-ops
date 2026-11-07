package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mywio/git-ops/pkg/core"
	"github.com/mywio/git-ops/pkg/fleetapi"
)

var Plugin core.Plugin = &GitHubActionsPlugin{}

type workflowInfo struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
}
type runInfo struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	WorkflowID int64     `json:"workflow_id"`
	HeadBranch string    `json:"head_branch"`
	HeadSHA    string    `json:"head_sha"`
	Event      string    `json:"event"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	RunAttempt int       `json:"run_attempt"`
	HTMLURL    string    `json:"html_url"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Actor      struct {
		Login string `json:"login"`
	} `json:"actor"`
}
type repositorySnapshot struct {
	Owner         string           `json:"owner"`
	Repo          string           `json:"repo"`
	DefaultBranch string           `json:"default_branch"`
	SHA           string           `json:"sha"`
	Topics        []string         `json:"topics"`
	Manifest      *actionsManifest `json:"manifest,omitempty"`
	ManifestError string           `json:"manifest_error,omitempty"`
	Workflows     []workflowInfo   `json:"workflows"`
	Runs          []runInfo        `json:"runs"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

type GitHubActionsPlugin struct {
	cfg          actionsConfig
	logger       *slog.Logger
	registry     core.PluginRegistry
	client       *http.Client
	store        *actionsStore
	connections  map[string]*githubConnection
	owners       map[string]*githubConnection
	topics       []string
	mu           sync.RWMutex
	repositories map[string]repositorySnapshot
	healthErr    error
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	refresh      chan struct{}
	dryRun       bool
}

func (p *GitHubActionsPlugin) Name() string { return "github_actions" }
func (p *GitHubActionsPlugin) Description() string {
	return "GitHub Actions workflows, deployments, and persistent self-hosted runner management"
}
func (p *GitHubActionsPlugin) Capabilities() []core.Capability {
	return []core.Capability{
		core.CapabilityListActionWorkflows, core.CapabilityManageActionRuns, core.CapabilityListRunners,
		core.CapabilityManageRunners, core.CapabilityActionsSetup, core.CapabilityListDeployments,
	}
}

func (p *GitHubActionsPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger, p.registry, p.client = logger, registry, http.DefaultClient
	if registry != nil {
		p.client = registry.GetHTTPClient()
	}
	config := registry.GetConfig()
	cfg, err := decodeActionsConfig(config["github_actions"])
	if err != nil {
		return err
	}
	p.cfg = cfg
	p.connections = map[string]*githubConnection{}
	p.owners = map[string]*githubConnection{}
	p.repositories = map[string]repositorySnapshot{}
	p.refresh = make(chan struct{}, 1)
	if coreCfg := config["core"]; coreCfg != nil {
		p.dryRun, _ = coreCfg["dry_run"].(bool)
	}
	var fleet struct {
		Pools []struct {
			RepositoryTopics []string `yaml:"repository_topics"`
		} `yaml:"pools"`
	}
	if section := config["fleet_controller"]; section != nil {
		_ = core.DecodeConfigSection(section, &fleet)
		for _, pool := range fleet.Pools {
			p.topics = append(p.topics, pool.RepositoryTopics...)
		}
	}
	p.topics = cleanStrings(p.topics)
	if !cfg.Enabled {
		return nil
	}
	if len(p.topics) == 0 {
		return fmt.Errorf("github_actions requires fleet_controller pools with repository_topics")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.StateDB), 0o750); err != nil {
		return err
	}
	p.store, err = openActionsStore(cfg.StateDB)
	if err != nil {
		return err
	}
	for _, item := range cfg.Connections {
		connection, err := newGitHubConnection(item, p.client)
		if err != nil {
			return fmt.Errorf("connection %s: %w", item.Name, err)
		}
		p.connections[item.Name] = connection
		for _, owner := range item.Owners {
			p.owners[strings.ToLower(strings.TrimSpace(owner))] = connection
		}
	}
	p.ctx, p.cancel = context.WithCancel(ctx)
	for _, desc := range actionsEventTypes() {
		if err := registry.RegisterEventType(desc); err != nil {
			return err
		}
	}
	registry.Subscribe("github_workflow_run", p.handleGitHubEvent)
	registry.Subscribe("github_workflow_job", p.handleGitHubEvent)
	registry.Subscribe(string(core.EventTypeRepositoryRevisionRequested), p.handleGitHubEvent)
	return nil
}

func actionsEventTypes() []core.EventTypeDesc {
	return []core.EventTypeDesc{
		{Name: "github_workflow_run", Description: "Signed GitHub workflow_run webhook"},
		{Name: "github_workflow_job", Description: "Signed GitHub workflow_job webhook"},
		{Name: "github_actions_manifest_invalid", Description: "Actions deployment manifest is invalid"},
		{Name: "github_actions_runner_requested", Description: "Persistent Actions runner lifecycle requested"},
	}
}

func (p *GitHubActionsPlugin) Start(context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	p.wg.Add(1)
	go p.refreshLoop()
	return nil
}
func (p *GitHubActionsPlugin) Stop(ctx context.Context) error {
	if p.cancel != nil {
		p.cancel()
	}
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if p.store != nil {
		return p.store.close()
	}
	return nil
}
func (p *GitHubActionsPlugin) Status() core.ServiceStatus {
	if !p.cfg.Enabled {
		return core.StatusDegraded
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.healthErr != nil {
		return core.StatusUnhealthy
	}
	return core.StatusHealthy
}
func (p *GitHubActionsPlugin) Config() any {
	items := make([]map[string]any, 0, len(p.cfg.Connections))
	for _, c := range p.cfg.Connections {
		items = append(items, map[string]any{"name": c.Name, "owners": c.Owners, "auth_type": c.Auth.Type, "credential": "REDACTED"})
	}
	return map[string]any{"enabled": p.cfg.Enabled, "state_db": p.cfg.StateDB, "refresh_interval": p.cfg.RefreshInterval.String(), "connections": items, "topics": p.topics}
}

func (p *GitHubActionsPlugin) refreshLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.cfg.RefreshInterval)
	defer ticker.Stop()
	for {
		p.refreshAll(p.ctx)
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		case <-p.refresh:
		}
	}
}
func (p *GitHubActionsPlugin) handleGitHubEvent(ctx context.Context, event core.InternalEvent) {
	if deliveryID := strings.TrimSpace(fmt.Sprint(event.Details["delivery_id"])); deliveryID != "" && p.store != nil {
		fresh, err := p.store.rememberDelivery(ctx, deliveryID, string(event.Type))
		if err != nil || !fresh {
			return
		}
	}
	if event.Type == "github_workflow_run" {
		if raw, ok := event.Details["workflow_run"].(map[string]any); ok {
			owner, repo := param(event.Details, "owner"), param(event.Details, "repo")
			pathValue := strings.TrimSpace(fmt.Sprint(raw["path"]))
			if snapshot, err := p.repository(owner, repo); err == nil && snapshot.Manifest != nil {
				managed := false
				for _, deployment := range snapshot.Manifest.Deployments {
					if deployment.Workflow == pathValue {
						managed = true
						break
					}
				}
				if managed {
					status := core.ExecutionStatusRequested
					runStatus, conclusion := strings.TrimSpace(fmt.Sprint(raw["status"])), strings.TrimSpace(fmt.Sprint(raw["conclusion"]))
					switch {
					case runStatus == "in_progress":
						status = core.ExecutionStatusRunning
					case conclusion == "success":
						status = core.ExecutionStatusSucceeded
					case conclusion == "cancelled":
						status = core.ExecutionStatusCancelled
					case conclusion != "":
						status = core.ExecutionStatusFailed
					}
					p.registry.Publish(ctx, core.NewExecutionEvent(core.ExecutionEventInput{
						ExecutionID: fmt.Sprintf("gha:%v:%v", raw["id"], raw["run_attempt"]), Owner: owner, Repo: repo,
						Stage: core.ExecutionStageActions, Status: status, Source: p.Name(), Trigger: fmt.Sprint(raw["event"]),
						Details: map[string]any{"provider": "github_actions", "run_id": raw["id"], "run_attempt": raw["run_attempt"],
							"workflow_path": pathValue, "commit_sha": raw["head_sha"], "branch": raw["head_branch"],
							"conclusion": conclusion, "html_url": raw["html_url"]},
					}))
				}
			}
		}
	}
	select {
	case p.refresh <- struct{}{}:
	default:
	}
}

func (p *GitHubActionsPlugin) refreshAll(ctx context.Context) {
	next := map[string]repositorySnapshot{}
	var lastErr error
	for owner, connection := range p.owners {
		for _, topic := range p.topics {
			var search struct {
				Items []struct {
					Name          string   `json:"name"`
					Private       bool     `json:"private"`
					DefaultBranch string   `json:"default_branch"`
					Topics        []string `json:"topics"`
					Owner         struct {
						Login string `json:"login"`
					} `json:"owner"`
				} `json:"items"`
			}
			query := url.QueryEscape("user:" + owner + " topic:" + topic + " archived:false is:private")
			if _, err := connection.do(ctx, http.MethodGet, "/search/repositories?q="+query+"&per_page=100", nil, &search); err != nil {
				lastErr = err
				continue
			}
			for _, repo := range search.Items {
				if !repo.Private {
					continue
				}
				key := strings.ToLower(repo.Owner.Login + "/" + repo.Name)
				if _, ok := next[key]; ok {
					continue
				}
				snapshot, err := p.loadRepository(ctx, connection, repo.Owner.Login, repo.Name, repo.DefaultBranch, repo.Topics)
				if err != nil {
					lastErr = err
					continue
				}
				next[key] = snapshot
			}
		}
	}
	p.mu.Lock()
	p.repositories = next
	p.healthErr = lastErr
	p.mu.Unlock()
}

func (p *GitHubActionsPlugin) loadRepository(ctx context.Context, c *githubConnection, owner, repo, branch string, topics []string) (repositorySnapshot, error) {
	s := repositorySnapshot{Owner: owner, Repo: repo, DefaultBranch: branch, Topics: topics, UpdatedAt: time.Now().UTC()}
	var commit struct {
		SHA string `json:"sha"`
	}
	if _, err := c.do(ctx, http.MethodGet, githubPath("repos", owner, repo, "commits", branch), nil, &commit); err != nil {
		return s, err
	}
	s.SHA = commit.SHA
	manifestData, status, err := fetchContent(ctx, c, owner, repo, actionsManifestPath, commit.SHA)
	if err != nil && status != http.StatusNotFound {
		return s, err
	}
	if status != http.StatusNotFound {
		manifest, parseErr := parseActionsManifest(manifestData)
		if parseErr != nil {
			s.ManifestError = parseErr.Error()
			p.publish(ctx, "github_actions_manifest_invalid", repo, map[string]any{"owner": owner, "repo": repo, "commit_sha": commit.SHA, "error": parseErr.Error()})
		} else {
			if _, deployStatus, _ := fetchContent(ctx, c, owner, repo, ".ghops/deploy.yaml", commit.SHA); deployStatus != http.StatusNotFound {
				s.ManifestError = "both .ghops/actions.yaml and .ghops/deploy.yaml exist"
			} else {
				s.Manifest = &manifest
			}
		}
	}
	var workflows struct {
		Workflows []workflowInfo `json:"workflows"`
	}
	_, _ = c.do(ctx, http.MethodGet, githubPath("repos", owner, repo, "actions", "workflows")+"?per_page=100", nil, &workflows)
	s.Workflows = workflows.Workflows
	var runs struct {
		Runs []runInfo `json:"workflow_runs"`
	}
	_, _ = c.do(ctx, http.MethodGet, githubPath("repos", owner, repo, "actions", "runs")+"?per_page=50", nil, &runs)
	s.Runs = runs.Runs
	return s, nil
}

func fetchContent(ctx context.Context, c *githubConnection, owner, repo, path, ref string) ([]byte, int, error) {
	var result struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	status, err := c.do(ctx, http.MethodGet, githubPath("repos", owner, repo, "contents", path)+"?ref="+url.QueryEscape(ref), nil, &result)
	if err != nil {
		return nil, status, err
	}
	if result.Encoding != "base64" {
		return nil, status, fmt.Errorf("unexpected content encoding")
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(result.Content, "\n", ""))
	return data, status, err
}

func (p *GitHubActionsPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	if !p.cfg.Enabled {
		return nil, fmt.Errorf("github_actions is disabled")
	}
	switch action {
	case "list_action_workflows":
		return p.listRepositories(), nil
	case "list_deployments":
		return p.listDeployments(), nil
	case "list_runners":
		return p.listRunners(ctx)
	case "actions_setup":
		return p.setup(ctx), nil
	case "dispatch_workflow":
		return p.workflowAction(ctx, "dispatch", params)
	case "cancel_workflow", "rerun_workflow", "rerun_failed_workflow":
		return p.workflowAction(ctx, action, params)
	case "create_runner", "runner_action":
		return p.runnerAction(ctx, action, params)
	case "runner_credential":
		return p.runnerCredential(ctx, params)
	default:
		return nil, fmt.Errorf("unsupported github_actions action %q", action)
	}
}

func (p *GitHubActionsPlugin) listRepositories() []repositorySnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]repositorySnapshot, 0, len(p.repositories))
	for _, item := range p.repositories {
		out = append(out, item)
	}
	return out
}
func (p *GitHubActionsPlugin) listDeployments() []map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var out []map[string]interface{}
	for _, repo := range p.repositories {
		if repo.Manifest == nil {
			continue
		}
		for _, deployment := range repo.Manifest.Deployments {
			var active, lastSuccess *runInfo
			for i := range repo.Runs {
				run := &repo.Runs[i]
				if run.Name != deployment.Name && workflowPath(repo.Workflows, run.WorkflowID) != deployment.Workflow {
					continue
				}
				if active == nil && (run.Status == "queued" || run.Status == "in_progress") {
					active = run
				}
				if lastSuccess == nil && run.Conclusion == "success" {
					lastSuccess = run
				}
			}
			card := map[string]interface{}{"id": "github-actions:" + repo.Owner + "/" + repo.Repo + ":" + deployment.ID, "kind": "github_actions", "source": "github_actions", "managed": true, "owner": repo.Owner, "repo": repo.Repo, "display_name": deployment.Name, "status": "idle", "verification": "workflow-owned", "group_id": "github-actions:" + repo.Owner, "group_name": repo.Owner, "group_kind": "github_actions", "workflow": deployment.Workflow, "environment": deployment.Environment}
			if lastSuccess != nil {
				card["current_sha"] = lastSuccess.HeadSHA
				card["status"] = "succeeded"
				card["run_id"] = lastSuccess.ID
			}
			if active != nil {
				card["pending_sha"] = active.HeadSHA
				card["status"] = active.Status
				card["run_id"] = active.ID
			}
			out = append(out, card)
		}
	}
	return out
}
func workflowPath(workflows []workflowInfo, id int64) string {
	for _, w := range workflows {
		if w.ID == id {
			return w.Path
		}
	}
	return ""
}

func (p *GitHubActionsPlugin) connection(owner string) (*githubConnection, error) {
	c := p.owners[strings.ToLower(owner)]
	if c == nil {
		return nil, fmt.Errorf("owner %s has no GitHub connection", owner)
	}
	return c, nil
}
func (p *GitHubActionsPlugin) repository(owner, repo string) (repositorySnapshot, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	value, ok := p.repositories[strings.ToLower(owner+"/"+repo)]
	if !ok {
		return value, fmt.Errorf("repository is not a private topic-eligible Actions repository")
	}
	return value, nil
}

func (p *GitHubActionsPlugin) workflowAction(ctx context.Context, operation string, params map[string]interface{}) (interface{}, error) {
	if p.dryRun {
		return nil, fmt.Errorf("actions mutations are disabled by core.dry_run")
	}
	owner, repo := param(params, "owner"), param(params, "repo")
	snapshot, err := p.repository(owner, repo)
	if err != nil {
		return nil, err
	}
	connection, _ := p.connection(owner)
	switch operation {
	case "dispatch":
		workflow := param(params, "workflow")
		if workflow == "" {
			return nil, fmt.Errorf("workflow is required")
		}
		inputs, _ := params["inputs"].(map[string]any)
		_, err = connection.do(ctx, http.MethodPost, githubPath("repos", owner, repo, "actions", "workflows", workflow, "dispatches"), map[string]any{"ref": snapshot.DefaultBranch, "inputs": inputs}, nil)
	default:
		runID := param(params, "run_id")
		if _, parseErr := strconv.ParseInt(runID, 10, 64); parseErr != nil {
			return nil, fmt.Errorf("valid run_id is required")
		}
		suffix := map[string]string{"cancel_workflow": "cancel", "rerun_workflow": "rerun", "rerun_failed_workflow": "rerun-failed-jobs"}[operation]
		_, err = connection.do(ctx, http.MethodPost, githubPath("repos", owner, repo, "actions", "runs", runID, suffix), nil, nil)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"accepted": true}, nil
}

func (p *GitHubActionsPlugin) listRunners(ctx context.Context) (interface{}, error) {
	fleet, err := p.registry.GetPlugin("fleet_controller")
	if err != nil {
		return nil, err
	}
	return fleet.Execute(ctx, "list_agents", nil)
}
func (p *GitHubActionsPlugin) setup(ctx context.Context) map[string]any {
	return map[string]any{"enabled": p.cfg.Enabled, "dry_run": p.dryRun, "connections": len(p.connections), "owners": len(p.owners), "topics": p.topics, "repositories": len(p.listRepositories()), "private_repositories_only": true, "credentials_on_controller_only": true, "workflow_triggers": "repository-owned", "rollback": "repository-owned"}
}

func (p *GitHubActionsPlugin) runnerAction(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	if p.dryRun {
		return nil, fmt.Errorf("runner mutations are disabled by core.dry_run")
	}
	fleet, err := p.registry.GetPlugin("fleet_controller")
	if err != nil {
		return nil, err
	}
	var spec fleetapi.RunnerSpec
	operation := "install"
	var payload map[string]any
	if action == "create_runner" {
		data, _ := json.Marshal(params["spec"])
		if err := json.Unmarshal(data, &spec); err != nil {
			return nil, err
		}
		if spec.ID == "" {
			spec.ID = uuid.NewString()
		}
		if spec.Scope != "organization" && spec.Scope != "repository" {
			return nil, fmt.Errorf("scope must be organization or repository")
		}
		if spec.Repository == "" {
			return nil, fmt.Errorf("repository is required for private-repository eligibility")
		}
		if _, err := p.repository(spec.Owner, spec.Repository); err != nil {
			return nil, err
		}
		agentsRaw, err := fleet.Execute(ctx, "list_agents", nil)
		if err != nil {
			return nil, err
		}
		architecture, err := agentArchitecture(agentsRaw, spec.AgentID)
		if err != nil {
			return nil, err
		}
		download, err := p.runnerDownload(ctx, spec, architecture)
		if err != nil {
			return nil, err
		}
		payload = map[string]any{"spec": spec, "package_url": download.URL, "sha256": download.SHA256, "version": download.Version}
	} else {
		operation = param(params, "operation")
		id := param(params, "runner_id")
		spec, err = p.store.runner(ctx, id)
		if err != nil {
			return nil, err
		}
		payload = map[string]any{"spec": spec, "force": params["force"], "busy": params["busy"]}
		if operation == "update" {
			agentsRaw, listErr := fleet.Execute(ctx, "list_agents", nil)
			if listErr != nil {
				return nil, listErr
			}
			architecture, archErr := agentArchitecture(agentsRaw, spec.AgentID)
			if archErr != nil {
				return nil, archErr
			}
			download, downloadErr := p.runnerDownload(ctx, spec, architecture)
			if downloadErr != nil {
				return nil, downloadErr
			}
			payload["package_url"], payload["sha256"], payload["version"] = download.URL, download.SHA256, download.Version
		}
	}
	if operation == "" {
		operation = "install"
	}
	queued, err := fleet.Execute(ctx, "queue_agent_task", map[string]interface{}{"agent_id": spec.AgentID, "task_type": "runner." + operation, "payload": payload})
	if err != nil {
		return nil, err
	}
	task, ok := queued.(fleetapi.Task)
	if !ok {
		return nil, fmt.Errorf("fleet controller returned an invalid task")
	}
	if operation == "install" || operation == "remove" {
		if err := p.store.saveTaskCredential(ctx, task.ID, spec, operation); err != nil {
			return nil, err
		}
	}
	if operation == "remove" {
		_ = p.store.deleteRunner(ctx, spec.ID)
	} else {
		_ = p.store.saveRunner(ctx, spec)
	}
	p.publish(ctx, "github_actions_runner_requested", spec.Repository, map[string]any{"agent_id": spec.AgentID, "runner_id": spec.ID, "operation": operation, "task_id": task.ID})
	return task, nil
}

type runnerDownload struct {
	URL     string
	SHA256  string
	Version string
}

func (p *GitHubActionsPlugin) runnerDownload(ctx context.Context, spec fleetapi.RunnerSpec, architecture string) (runnerDownload, error) {
	connection, err := p.connection(spec.Owner)
	if err != nil {
		return runnerDownload{}, err
	}
	endpoint := githubPath("orgs", spec.Owner, "actions", "runners", "downloads")
	if spec.Scope == "repository" {
		endpoint = githubPath("repos", spec.Owner, spec.Repository, "actions", "runners", "downloads")
	}
	var downloads []struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		DownloadURL  string `json:"download_url"`
		Filename     string `json:"filename"`
		SHA256       string `json:"sha256_checksum"`
	}
	if _, err := connection.do(ctx, http.MethodGet, endpoint, nil, &downloads); err != nil {
		return runnerDownload{}, err
	}
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[architecture]
	if arch == "" {
		return runnerDownload{}, fmt.Errorf("agent architecture %q is unsupported", architecture)
	}
	for _, item := range downloads {
		if item.OS == "linux" && item.Architecture == arch {
			return runnerDownload{item.DownloadURL, item.SHA256, item.Filename}, nil
		}
	}
	return runnerDownload{}, fmt.Errorf("no compatible runner package")
}

func (p *GitHubActionsPlugin) runnerCredential(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	record, err := p.store.consumeTaskCredential(ctx, param(params, "task_id"))
	if err != nil {
		return nil, err
	}
	if record.Spec.AgentID != param(params, "agent_id") {
		return nil, fmt.Errorf("runner credential is assigned to another agent")
	}
	connection, err := p.connection(record.Spec.Owner)
	if err != nil {
		return nil, err
	}
	kind := "registration-token"
	if record.Operation == "remove" {
		kind = "remove-token"
	}
	endpoint := githubPath("orgs", record.Spec.Owner, "actions", "runners", kind)
	target := "https://github.com/" + record.Spec.Owner
	if record.Spec.Scope == "repository" {
		endpoint = githubPath("repos", record.Spec.Owner, record.Spec.Repository, "actions", "runners", kind)
		target += "/" + record.Spec.Repository
	}
	var token struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if _, err := connection.do(ctx, http.MethodPost, endpoint, nil, &token); err != nil {
		return nil, err
	}
	return fleetapi.RegistrationCredential{URL: target, Token: token.Token, ExpiresAt: token.ExpiresAt}, nil
}

type agentRecordView struct {
	ID    string `json:"id"`
	Facts struct {
		Architecture string `json:"architecture"`
	} `json:"facts"`
}

func agentArchitecture(value any, agentID string) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var agents []agentRecordView
	if err := json.Unmarshal(data, &agents); err != nil {
		return "", fmt.Errorf("decode fleet agents: %w", err)
	}
	for _, agent := range agents {
		if agent.ID == agentID {
			if agent.Facts.Architecture == "" {
				return "", fmt.Errorf("agent %s has not reported an architecture", agentID)
			}
			return agent.Facts.Architecture, nil
		}
	}
	return "", fmt.Errorf("agent %s was not found", agentID)
}

func (p *GitHubActionsPlugin) publish(ctx context.Context, event, repo string, details map[string]any) {
	if p.registry != nil {
		p.registry.Publish(ctx, core.InternalEvent{Type: core.EventTypeName(event), Source: p.Name(), Repo: repo, Details: details})
	}
}
func param(params map[string]interface{}, key string) string {
	if params == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(params[key]))
}
