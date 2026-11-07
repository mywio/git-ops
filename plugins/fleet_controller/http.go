package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mywio/git-ops/pkg/fleetapi"
)

const maxAgentBody = 2 << 20

func (p *FleetControllerPlugin) registerAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/enroll", p.handleEnroll)
	mux.HandleFunc("/v1/heartbeat", p.requireAgent(p.handleHeartbeat))
	mux.HandleFunc("/v1/tasks/claim", p.requireAgent(p.handleClaimTask))
	mux.HandleFunc("/v1/tasks/", p.requireAgent(p.handleTaskRoute))
	mux.HandleFunc("/v1/certificates/rotate", p.requireAgent(p.handleRotateCertificate))
}

func (p *FleetControllerPlugin) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		agentError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request fleetapi.EnrollRequest
	if err := decodeAgentJSON(w, r, &request); err != nil {
		agentError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(request.Token) == "" || strings.TrimSpace(request.CSR) == "" ||
		strings.TrimSpace(request.Facts.NodeName) == "" {
		agentError(w, http.StatusBadRequest, "token, csr, and node_name are required")
		return
	}
	pool, ok := p.cfg.pool(request.Facts.Pool)
	if !ok {
		agentError(w, http.StatusBadRequest, "unknown fleet pool")
		return
	}
	request.Facts.Labels = cleanStrings(append(request.Facts.Labels, pool.RunnerLabels...))
	agentID := uuid.NewString()
	certificate, serial, expires, err := p.ca.signCSR(request.CSR, agentID)
	if err != nil {
		agentError(w, http.StatusBadRequest, err.Error())
		return
	}
	record := agentRecord{ID: agentID, Facts: request.Facts, LastSeen: time.Now().UTC(), Online: true, CertSerial: serial}
	if err := p.store.enroll(r.Context(), request.Token, record); err != nil {
		agentError(w, http.StatusUnauthorized, err.Error())
		return
	}
	p.publish(r.Context(), "fleet_agent_enrolled", map[string]any{"agent_id": agentID, "node_name": request.Facts.NodeName, "pool": request.Facts.Pool})
	writeAgentJSON(w, http.StatusCreated, fleetapi.EnrollResponse{AgentID: agentID, CertificatePEM: certificate,
		CACertificatePEM: string(p.ca.certPEM), CertificateExpiresAt: expires})
}

func (p *FleetControllerPlugin) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		agentError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := agentIdentity(r)
	var request fleetapi.HeartbeatRequest
	if err := decodeAgentJSON(w, r, &request); err != nil {
		agentError(w, http.StatusBadRequest, err.Error())
		return
	}
	pool, ok := p.cfg.pool(request.Facts.Pool)
	if !ok {
		agentError(w, http.StatusBadRequest, "unknown fleet pool")
		return
	}
	request.Facts.Labels = cleanStrings(append(request.Facts.Labels, pool.RunnerLabels...))
	if err := p.store.heartbeat(r.Context(), id, request); err != nil {
		agentError(w, http.StatusUnauthorized, err.Error())
		return
	}
	writeAgentJSON(w, http.StatusOK, map[string]any{"accepted": true, "next_heartbeat_seconds": int(p.cfg.HeartbeatInterval.Seconds())})
}

func (p *FleetControllerPlugin) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		agentError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	task, err := p.store.claimTask(r.Context(), agentIdentity(r), 2*time.Minute)
	if err != nil {
		agentError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeAgentJSON(w, http.StatusOK, task)
}

func (p *FleetControllerPlugin) handleTaskRoute(w http.ResponseWriter, r *http.Request) {
	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/tasks/"), "/")
	parts := strings.Split(suffix, "/")
	if len(parts) != 2 || parts[0] == "" {
		agentError(w, http.StatusNotFound, "task route not found")
		return
	}
	taskID, operation := parts[0], parts[1]
	switch operation {
	case "result":
		if r.Method != http.MethodPost {
			agentError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var result fleetapi.TaskResult
		if err := decodeAgentJSON(w, r, &result); err != nil {
			agentError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := p.store.completeTask(r.Context(), agentIdentity(r), taskID, result); err != nil {
			agentError(w, http.StatusConflict, err.Error())
			return
		}
		p.publish(r.Context(), "fleet_task_completed", map[string]any{"agent_id": agentIdentity(r), "task_id": taskID, "status": result.Status, "error": result.Error})
		writeAgentJSON(w, http.StatusOK, map[string]bool{"accepted": true})
	case "credential":
		if r.Method != http.MethodPost {
			agentError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !p.store.taskLeasedTo(r.Context(), taskID, agentIdentity(r)) {
			agentError(w, http.StatusForbidden, "task is not leased to this agent")
			return
		}
		plugin, err := p.registry.GetPlugin("github_actions")
		if err != nil {
			agentError(w, http.StatusServiceUnavailable, "github_actions plugin unavailable")
			return
		}
		response, err := plugin.Execute(r.Context(), "runner_credential", map[string]interface{}{"agent_id": agentIdentity(r), "task_id": taskID})
		if err != nil {
			agentError(w, http.StatusBadGateway, err.Error())
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeAgentJSON(w, http.StatusOK, response)
	default:
		agentError(w, http.StatusNotFound, "task route not found")
	}
}

func (p *FleetControllerPlugin) handleRotateCertificate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		agentError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		CSR string `json:"csr"`
	}
	if err := decodeAgentJSON(w, r, &body); err != nil {
		agentError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := agentIdentity(r)
	certificate, serial, expires, err := p.ca.signCSR(body.CSR, id)
	if err != nil {
		agentError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := p.store.updateCert(r.Context(), id, serial); err != nil {
		agentError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAgentJSON(w, http.StatusOK, fleetapi.EnrollResponse{AgentID: id, CertificatePEM: certificate,
		CACertificatePEM: string(p.ca.certPEM), CertificateExpiresAt: expires})
}

func (p *FleetControllerPlugin) requireAgent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := agentIdentity(r)
		if id == "" || p.store == nil || !p.store.agentAllowed(r.Context(), id) {
			agentError(w, http.StatusUnauthorized, "valid agent client certificate required")
			return
		}
		next(w, r)
	}
}

func agentIdentity(r *http.Request) string {
	if r == nil || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	return strings.TrimSpace(r.TLS.PeerCertificates[0].Subject.CommonName)
}

func decodeAgentJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAgentBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request must contain one JSON value")
	}
	return nil
}

func writeAgentJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func agentError(w http.ResponseWriter, status int, message string) {
	writeAgentJSON(w, status, map[string]string{"error": message})
}
