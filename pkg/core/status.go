package core

type ServiceStatus string

const (
	StatusHealthy   ServiceStatus = "HEALTHY"
	StatusUnhealthy ServiceStatus = "UNHEALTHY"
	StatusUnknown   ServiceStatus = "UNKNOWN"
	StatusDegraded  ServiceStatus = "DEGRADED"
)
