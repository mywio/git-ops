package core

import "time"

// EventTypeRepositoryRevisionRequested is emitted for an exact repository revision.
const EventTypeRepositoryRevisionRequested EventTypeName = "repository_revision_requested"

// EventTypeLocalDeployRequested routes an eligible recipe-backed revision to local_deploy.
const EventTypeLocalDeployRequested EventTypeName = "local_deploy_requested"

// RepositoryRevisionRequested is the typed payload preserved from a GitHub push.
type RepositoryRevisionRequested struct {
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	Ref        string `json:"ref"`
	CommitSHA  string `json:"commit_sha"`
	DeliveryID string `json:"delivery_id,omitempty"`
	Trigger    string `json:"trigger"`
}

// NewRepositoryRevisionRequestedEvent creates the canonical revision request event.
func NewRepositoryRevisionRequestedEvent(request RepositoryRevisionRequested, source string) InternalEvent {
	return InternalEvent{
		Type:      EventTypeRepositoryRevisionRequested,
		Timestamp: time.Now().UTC(),
		Source:    source,
		Repo:      request.Repo,
		Details: map[string]any{
			"owner":       request.Owner,
			"repo":        request.Repo,
			"ref":         request.Ref,
			"commit_sha":  request.CommitSHA,
			"delivery_id": request.DeliveryID,
			"trigger":     request.Trigger,
		},
	}
}

// RepositoryRevisionFromEvent decodes a revision request event.
func RepositoryRevisionFromEvent(event InternalEvent) (RepositoryRevisionRequested, bool) {
	request := RepositoryRevisionRequested{}
	request.Owner, _ = event.Details["owner"].(string)
	request.Repo, _ = event.Details["repo"].(string)
	request.Ref, _ = event.Details["ref"].(string)
	request.CommitSHA, _ = event.Details["commit_sha"].(string)
	request.DeliveryID, _ = event.Details["delivery_id"].(string)
	request.Trigger, _ = event.Details["trigger"].(string)
	return request, request.Owner != "" && request.Repo != "" && request.Ref != "" && request.CommitSHA != ""
}
