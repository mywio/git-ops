// Package fleetapi contains the versioned controller/agent wire contract.
package fleetapi

import "time"

const ProtocolVersion = "v1"

type AgentFacts struct {
	NodeName     string            `json:"node_name"`
	OS           string            `json:"os"`
	Architecture string            `json:"architecture"`
	AgentVersion string            `json:"agent_version"`
	Pool         string            `json:"pool"`
	Labels       []string          `json:"labels,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type RunnerStatus struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Scope      string   `json:"scope"`
	Owner      string   `json:"owner"`
	Repository string   `json:"repository,omitempty"`
	Group      string   `json:"group,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Version    string   `json:"version,omitempty"`
	Service    string   `json:"service"`
	Online     bool     `json:"online"`
	Busy       bool     `json:"busy"`
	LastError  string   `json:"last_error,omitempty"`
}

type EnrollRequest struct {
	Token string     `json:"token"`
	CSR   string     `json:"csr"`
	Facts AgentFacts `json:"facts"`
}

type EnrollResponse struct {
	AgentID              string    `json:"agent_id"`
	CertificatePEM       string    `json:"certificate_pem"`
	CACertificatePEM     string    `json:"ca_certificate_pem"`
	CertificateExpiresAt time.Time `json:"certificate_expires_at"`
}

type HeartbeatRequest struct {
	Facts   AgentFacts     `json:"facts"`
	Runners []RunnerStatus `json:"runners,omitempty"`
}

type Task struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Generation int64          `json:"generation"`
	ExpiresAt  time.Time      `json:"expires_at"`
	Payload    map[string]any `json:"payload,omitempty"`
}

type TaskResult struct {
	Status    string         `json:"status"`
	Error     string         `json:"error,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	StartedAt time.Time      `json:"started_at"`
	EndedAt   time.Time      `json:"ended_at"`
}

type RegistrationCredential struct {
	URL       string    `json:"url"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type RunnerSpec struct {
	ID                  string   `json:"id"`
	AgentID             string   `json:"agent_id"`
	Name                string   `json:"name"`
	Scope               string   `json:"scope"`
	Owner               string   `json:"owner"`
	Repository          string   `json:"repository,omitempty"`
	Group               string   `json:"group,omitempty"`
	Labels              []string `json:"labels,omitempty"`
	SupplementaryGroups []string `json:"supplementary_groups,omitempty"`
	AutoUpdate          bool     `json:"auto_update"`
}
