package core

// Capability identifies one behavior that a plugin provides to the system.
type Capability string

const (
	// CapabilityNotifier marks plugins that emit notifications for selected events.
	CapabilityNotifier     Capability = "NOTIFIER"
	// CapabilityUI marks plugins that expose user-facing HTTP routes or assets.
	CapabilityUI           Capability = "UI"
	// CapabilityAPI marks plugins that expose machine-facing API endpoints.
	CapabilityAPI          Capability = "API"
	// CapabilityMCP marks plugins that expose MCP-compatible endpoints or services.
	CapabilityMCP          Capability = "MCP"
	// CapabilityTrigger marks plugins that request reconciliations from external input.
	CapabilityTrigger      Capability = "TRIGGER"
	// CapabilitySecrets marks plugins that return secret key/value pairs.
	CapabilitySecrets      Capability = "SECRETS"
	// CapabilityRuntimeFiles marks plugins that materialize files for compose execution.
	CapabilityRuntimeFiles Capability = "RUNTIME_FILES"
	// CapabilityAudit marks plugins that record or expose event history.
	CapabilityAudit        Capability = "AUDIT"
	// CapabilityDeployer marks plugins that deploy or manage stacks.
	CapabilityDeployer     Capability = "DEPLOYER"
	// CapabilitySystem marks plugins that provide core system behavior.
	CapabilitySystem       Capability = "SYSTEM"
)
