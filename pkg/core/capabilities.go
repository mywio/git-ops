package core

// Capability identifies one behavior that a plugin provides to the system.
type Capability string

const (
	// CapabilityNotifier marks plugins that emit notifications for selected events.
	CapabilityNotifier Capability = "NOTIFIER"
	// CapabilityUI marks plugins that expose user-facing HTTP routes or assets.
	CapabilityUI Capability = "UI"
	// CapabilityAPI marks plugins that expose machine-facing API endpoints.
	CapabilityAPI Capability = "API"
	// CapabilityMCP marks plugins that expose MCP-compatible endpoints or services.
	CapabilityMCP Capability = "MCP"
	// CapabilityTrigger marks plugins that request reconciliations from external input.
	CapabilityTrigger Capability = "TRIGGER"
	// CapabilitySecrets marks plugins that return secret key/value pairs.
	CapabilitySecrets Capability = "SECRETS"
	// CapabilityRuntimeFiles marks plugins that materialize files for compose execution.
	CapabilityRuntimeFiles Capability = "RUNTIME_FILES"
	// CapabilityAudit marks plugins that record or expose event history.
	CapabilityAudit Capability = "AUDIT"
	// CapabilitySystemInfo marks plugins that expose system_info through Execute.
	CapabilitySystemInfo Capability = "system_info"
	// CapabilityListDeployments marks plugins that expose list_deployments through Execute.
	CapabilityListDeployments Capability = "list_deployments"
	// CapabilityStreamLogs marks plugins that expose stream_logs through Execute.
	CapabilityStreamLogs Capability = "stream_logs"
	// CapabilityReconcileStack marks plugins that expose reconcile_stack through Execute.
	CapabilityReconcileStack Capability = "reconcile_stack"
	// CapabilityStartStack marks plugins that expose start_stack through Execute.
	CapabilityStartStack Capability = "start_stack"
	// CapabilityStopStack marks plugins that expose stop_stack through Execute.
	CapabilityStopStack Capability = "stop_stack"
	// CapabilityRestartStack marks plugins that expose restart_stack through Execute.
	CapabilityRestartStack Capability = "restart_stack"
	// CapabilityEnableStack marks plugins that expose enable_stack through Execute.
	CapabilityEnableStack Capability = "enable_stack"
	// CapabilityDisableStack marks plugins that expose disable_stack through Execute.
	CapabilityDisableStack Capability = "disable_stack"
	// CapabilityRefreshStackImages marks plugins that expose refresh_stack_images through Execute.
	CapabilityRefreshStackImages Capability = "refresh_stack_images"
	// CapabilityDeployRevision marks plugins that deploy an exact repository revision.
	CapabilityDeployRevision Capability = "deploy_revision"
	// CapabilityRetryDeployment marks plugins that retry a failed exact revision.
	CapabilityRetryDeployment Capability = "retry_deployment"
	// CapabilityDeployLatest marks plugins that resolve and deploy the latest default-branch revision.
	CapabilityDeployLatest Capability = "deploy_latest"
	// CapabilityRollbackDeployment marks plugins that roll back to a retained successful revision.
	CapabilityRollbackDeployment Capability = "rollback_deployment"
	// CapabilityListAgents exposes centrally enrolled fleet agents.
	CapabilityListAgents Capability = "list_agents"
	// CapabilityManageAgents creates enrollment credentials and revokes agents.
	CapabilityManageAgents Capability = "manage_agents"
	// CapabilityListRunners exposes GitHub Actions runner inventory.
	CapabilityListRunners Capability = "list_runners"
	// CapabilityManageRunners manages persistent self-hosted runner lifecycle.
	CapabilityManageRunners Capability = "manage_runners"
	// CapabilityListActionWorkflows exposes eligible workflows and workflow runs.
	CapabilityListActionWorkflows Capability = "list_action_workflows"
	// CapabilityManageActionRuns dispatches, cancels, and reruns workflows.
	CapabilityManageActionRuns Capability = "manage_action_runs"
	// CapabilityActionsSetup exposes redacted readiness and setup information.
	CapabilityActionsSetup Capability = "actions_setup"
)
