package core

type Capability string // Capabilities of services

const (
	CapabilityNotifier Capability = "NOTIFIER"
	CapabilityUI       Capability = "UI"
	CapabilityAPI      Capability = "API"
	CapabilityMCP      Capability = "MCP"
	CapabilityTrigger  Capability = "Webhook"
)
