package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/mywio/git-ops/pkg/core"
)

const (
	maxGitHubWebhookBody = 2 << 20
	maxDeliveryHistory   = 2048
)

type githubPushPayload struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"`
	Repository struct {
		Name          string `json:"name"`
		DefaultBranch string `json:"default_branch"`
		Owner         struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
}

func (p *WebhookTriggerPlugin) handleGitHub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(nonEmptyWebhookSecrets(p.githubSecrets)) == 0 {
		http.Error(w, "GitHub webhook secret is not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxGitHubWebhookBody))
	if err != nil {
		http.Error(w, "invalid webhook body", http.StatusBadRequest)
		return
	}
	if !validGitHubSignatureAny(body, r.Header.Get("X-Hub-Signature-256"), p.githubSecrets) {
		http.Error(w, "invalid webhook signature", http.StatusUnauthorized)
		return
	}
	deliveryID := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	if deliveryID == "" {
		http.Error(w, "missing GitHub delivery ID", http.StatusBadRequest)
		return
	}
	if p.deliverySeen(deliveryID) {
		writeWebhookJSON(w, http.StatusAccepted, "ignored", "duplicate delivery")
		return
	}
	eventName := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	switch eventName {
	case "ping":
		p.rememberDelivery(deliveryID)
		writeWebhookJSON(w, http.StatusAccepted, "accepted", "GitHub webhook configured")
		return
	case "workflow_run", "workflow_job":
		p.handleGitHubWorkflowEvent(r, w, body, eventName, deliveryID)
		return
	case "push":
	default:
		p.rememberDelivery(deliveryID)
		writeWebhookJSON(w, http.StatusAccepted, "ignored", "unsupported GitHub event")
		return
	}
	var payload githubPushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid webhook JSON", http.StatusBadRequest)
		return
	}
	defaultRef := "refs/heads/" + payload.Repository.DefaultBranch
	if payload.Deleted || payload.After == "" || allZeroSHA(payload.After) {
		p.rememberDelivery(deliveryID)
		writeWebhookJSON(w, http.StatusAccepted, "ignored", "deleted ref")
		return
	}
	if !validCommitSHA(payload.After) {
		http.Error(w, "invalid after commit SHA", http.StatusBadRequest)
		return
	}
	if payload.Repository.DefaultBranch == "" || payload.Ref != defaultRef {
		p.rememberDelivery(deliveryID)
		writeWebhookJSON(w, http.StatusAccepted, "ignored", "push is not for the default branch")
		return
	}
	if payload.Repository.Owner.Login == "" || payload.Repository.Name == "" {
		http.Error(w, "repository metadata is incomplete", http.StatusBadRequest)
		return
	}
	p.rememberDelivery(deliveryID)
	request := core.RepositoryRevisionRequested{Owner: payload.Repository.Owner.Login, Repo: payload.Repository.Name, Ref: payload.Ref,
		CommitSHA: payload.After, DeliveryID: deliveryID, Trigger: "github_push"}
	if p.registry != nil {
		p.registry.Publish(r.Context(), core.NewRepositoryRevisionRequestedEvent(request, p.Name()))
		p.registry.Publish(r.Context(), core.InternalEvent{Type: "webhook_received", Source: p.Name(), Repo: request.Repo,
			Details: map[string]any{"owner": request.Owner, "repo": request.Repo, "delivery_id": deliveryID, "event": "push", "ref": request.Ref, "commit_sha": request.CommitSHA}})
	}
	writeWebhookJSON(w, http.StatusAccepted, "accepted", "repository revision queued")
}

func (p *WebhookTriggerPlugin) handleGitHubWorkflowEvent(r *http.Request, w http.ResponseWriter, body []byte, eventName, deliveryID string) {
	var payload struct {
		Action     string `json:"action"`
		Repository struct {
			Name  string `json:"name"`
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"repository"`
		WorkflowRun map[string]any `json:"workflow_run"`
		WorkflowJob map[string]any `json:"workflow_job"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Repository.Name == "" || payload.Repository.Owner.Login == "" {
		http.Error(w, "invalid workflow webhook JSON", http.StatusBadRequest)
		return
	}
	p.rememberDelivery(deliveryID)
	details := map[string]any{"owner": payload.Repository.Owner.Login, "repo": payload.Repository.Name, "delivery_id": deliveryID, "action": payload.Action}
	if eventName == "workflow_run" {
		details["workflow_run"] = payload.WorkflowRun
	} else {
		details["workflow_job"] = payload.WorkflowJob
	}
	if p.registry != nil {
		p.registry.Publish(r.Context(), core.InternalEvent{Type: core.EventTypeName("github_" + eventName), Source: p.Name(), Repo: payload.Repository.Name, Details: details})
	}
	writeWebhookJSON(w, http.StatusAccepted, "accepted", eventName+" queued")
}

func validGitHubSignature(body []byte, signature, secret string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(signature, prefix) {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, prefix))
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

func validGitHubSignatureAny(body []byte, signature string, secrets []string) bool {
	valid := false
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret != "" && validGitHubSignature(body, signature, secret) {
			valid = true
		}
	}
	return valid
}

func nonEmptyWebhookSecrets(secrets []string) []string {
	result := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" {
			result = append(result, secret)
		}
	}
	return result
}

func allZeroSHA(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char != '0' {
			return false
		}
	}
	return true
}
func (p *WebhookTriggerPlugin) deliverySeen(deliveryID string) bool {
	p.deliveryMu.Lock()
	defer p.deliveryMu.Unlock()
	_, ok := p.deliveries[deliveryID]
	return ok
}
func (p *WebhookTriggerPlugin) rememberDelivery(deliveryID string) {
	p.deliveryMu.Lock()
	defer p.deliveryMu.Unlock()
	if _, exists := p.deliveries[deliveryID]; exists {
		return
	}
	p.deliveries[deliveryID] = struct{}{}
	p.deliveryFIFO = append(p.deliveryFIFO, deliveryID)
	if len(p.deliveryFIFO) > maxDeliveryHistory {
		oldest := p.deliveryFIFO[0]
		p.deliveryFIFO = p.deliveryFIFO[1:]
		delete(p.deliveries, oldest)
	}
}
func writeWebhookJSON(w http.ResponseWriter, status int, state, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": state, "message": message})
}
