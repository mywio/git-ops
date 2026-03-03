package core

type Capability string // Capabilities of services

const (
	CapabilityNotifier     Capability = "NOTIFIER"
	CapabilityUI           Capability = "UI"
	CapabilityAPI          Capability = "API"
	CapabilityMCP          Capability = "MCP"
	CapabilityTrigger      Capability = "TRIGGER"
	CapabilitySecrets      Capability = "SECRETS"
	CapabilityRuntimeFiles Capability = "RUNTIME_FILES"
	CapabilityAudit        Capability = "AUDIT"
)
