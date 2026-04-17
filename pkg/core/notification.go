package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type NotificationPayload struct {
	EventType     EventTypeName  `json:"event_type"`
	Timestamp     time.Time      `json:"timestamp"`
	Source        string         `json:"source,omitempty"`
	Repo          string         `json:"repo,omitempty"`
	Message       string         `json:"message,omitempty"`
	Title         string         `json:"title"`
	Body          string         `json:"body"`
	ExecutionID   string         `json:"execution_id,omitempty"`
	FullName      string         `json:"full_name,omitempty"`
	Stage         string         `json:"stage,omitempty"`
	Status        string         `json:"status,omitempty"`
	FailureClass  string         `json:"failure_class,omitempty"`
	AlertSeverity string         `json:"alert_severity,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
}

func (p NotificationPayload) MarshalJSON() ([]byte, error) {
	type payloadJSON struct {
		EventType     EventTypeName  `json:"event_type"`
		Timestamp     *time.Time     `json:"timestamp,omitempty"`
		Source        string         `json:"source,omitempty"`
		Repo          string         `json:"repo,omitempty"`
		Message       string         `json:"message,omitempty"`
		Title         string         `json:"title"`
		Body          string         `json:"body"`
		ExecutionID   string         `json:"execution_id,omitempty"`
		FullName      string         `json:"full_name,omitempty"`
		Stage         string         `json:"stage,omitempty"`
		Status        string         `json:"status,omitempty"`
		FailureClass  string         `json:"failure_class,omitempty"`
		AlertSeverity string         `json:"alert_severity,omitempty"`
		LastError     string         `json:"last_error,omitempty"`
		Details       map[string]any `json:"details,omitempty"`
	}

	out := payloadJSON{
		EventType:     p.EventType,
		Source:        p.Source,
		Repo:          p.Repo,
		Message:       p.Message,
		Title:         p.Title,
		Body:          p.Body,
		ExecutionID:   p.ExecutionID,
		FullName:      p.FullName,
		Stage:         p.Stage,
		Status:        p.Status,
		FailureClass:  p.FailureClass,
		AlertSeverity: p.AlertSeverity,
		LastError:     p.LastError,
		Details:       p.Details,
	}
	if !p.Timestamp.IsZero() {
		out.Timestamp = &p.Timestamp
	}

	return json.Marshal(out)
}

func NewNotificationPayload(event InternalEvent) NotificationPayload {
	payload := NotificationPayload{
		EventType: event.Type,
		Timestamp: event.Timestamp,
		Source:    event.Source,
		Repo:      event.Repo,
		Message:   event.Message,
		Details:   cloneNotificationDetails(event.Details),
	}

	if payload.Repo == "" {
		payload.Repo = notificationDetailString(event.Details, "repo")
	}

	if event.Type == EventTypeExecution {
		payload.ExecutionID = notificationDetailString(event.Details, "execution_id")
		payload.FullName = notificationFullName(event)
		payload.Stage = notificationDetailString(event.Details, "stage")
		payload.Status = notificationDetailString(event.Details, "status")
		payload.FailureClass = notificationDetailString(event.Details, "failure_class")
		payload.AlertSeverity = notificationDetailString(event.Details, "alert_severity")
		payload.LastError = notificationDetailString(event.Details, "last_error")
		payload.Title = notificationExecutionTitle(payload)
		payload.Body = notificationExecutionBody(event, payload)
		payload.Message = notificationExecutionMessage(event, payload)
		return payload
	}

	payload.Title = notificationGenericTitle(event, payload)
	payload.Body = notificationGenericBody(event, payload)
	payload.Message = notificationPrimaryMessage(event, payload)
	return payload
}

func cloneNotificationDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(details))
	for key, value := range details {
		cloned[key] = value
	}
	return cloned
}

func notificationFullName(event InternalEvent) string {
	fullName := notificationDetailString(event.Details, "full_name")
	if fullName != "" {
		return fullName
	}

	owner := notificationDetailString(event.Details, "owner")
	repo := notificationDetailString(event.Details, "repo")
	if repo == "" {
		repo = event.Repo
	}
	if owner != "" && repo != "" {
		return fmt.Sprintf("%s/%s", owner, repo)
	}
	return repo
}

func notificationDetailString(details map[string]any, key string) string {
	if len(details) == 0 {
		return ""
	}

	value, ok := details[key]
	if !ok || value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func notificationExecutionTitle(payload NotificationPayload) string {
	subject := firstNonEmpty(payload.FullName, payload.Repo, payload.ExecutionID)
	status := firstNonEmpty(payload.Status, "update")
	if subject == "" {
		return fmt.Sprintf("Execution %s", status)
	}
	return fmt.Sprintf("Execution %s: %s", status, subject)
}

func notificationExecutionBody(event InternalEvent, payload NotificationPayload) string {
	lines := make([]string, 0, 6)
	if payload.ExecutionID != "" {
		lines = append(lines, fmt.Sprintf("Execution ID: %s", payload.ExecutionID))
	}
	if payload.Stage != "" {
		lines = append(lines, fmt.Sprintf("Stage: %s", payload.Stage))
	}
	if payload.Status != "" {
		lines = append(lines, fmt.Sprintf("Status: %s", payload.Status))
	}
	if payload.AlertSeverity != "" {
		lines = append(lines, fmt.Sprintf("Severity: %s", payload.AlertSeverity))
	}
	if payload.FailureClass != "" {
		lines = append(lines, fmt.Sprintf("Failure Class: %s", payload.FailureClass))
	}
	if payload.LastError != "" {
		lines = append(lines, fmt.Sprintf("Error: %s", payload.LastError))
	}
	if len(lines) > 0 {
		return strings.Join(lines, "\n")
	}
	return notificationGenericBody(event, payload)
}

func notificationPrimaryMessage(event InternalEvent, payload NotificationPayload) string {
	if event.Type == EventTypeExecution {
		if msg := strings.TrimSpace(event.Message); msg != "" {
			return msg
		}
		if title := strings.TrimSpace(payload.Title); title != "" {
			return title
		}
		return notificationGenericSummary(event, payload)
	}
	if msg := strings.TrimSpace(event.Message); msg != "" {
		return msg
	}
	if title := strings.TrimSpace(payload.Title); title != "" {
		return title
	}
	return notificationGenericSummary(event, payload)
}

func notificationExecutionMessage(event InternalEvent, payload NotificationPayload) string {
	if msg := strings.TrimSpace(event.Message); msg != "" {
		return msg
	}
	return notificationPrimaryMessage(event, payload)
}

func notificationGenericTitle(event InternalEvent, payload NotificationPayload) string {
	context := notificationEventContext(payload)
	if context == "" {
		return fmt.Sprintf("Event: %s", event.Type)
	}
	return fmt.Sprintf("Event: %s (%s)", event.Type, context)
}

func notificationGenericBody(event InternalEvent, payload NotificationPayload) string {
	lines := make([]string, 0, 5)
	if event.Type != "" {
		lines = append(lines, fmt.Sprintf("Event Type: %s", event.Type))
	}
	if payload.Source != "" {
		lines = append(lines, fmt.Sprintf("Source: %s", payload.Source))
	}
	if payload.Repo != "" {
		lines = append(lines, fmt.Sprintf("Repo: %s", payload.Repo))
	}
	if msg := strings.TrimSpace(event.Message); msg != "" {
		lines = append(lines, fmt.Sprintf("Message: %s", msg))
	}
	if len(payload.Details) > 0 {
		data, err := json.Marshal(payload.Details)
		if err != nil {
			lines = append(lines, fmt.Sprintf("Details: %v", payload.Details))
		} else {
			lines = append(lines, fmt.Sprintf("Details: %s", data))
		}
	}
	if len(lines) == 0 {
		return notificationGenericSummary(event, payload)
	}
	return strings.Join(lines, "\n")
}

func notificationGenericSummary(event InternalEvent, payload NotificationPayload) string {
	parts := make([]string, 0, 3)
	if event.Type != "" {
		parts = append(parts, fmt.Sprintf("Event: %s", event.Type))
	}
	if context := notificationEventContext(payload); context != "" {
		parts = append(parts, context)
	}
	if len(parts) == 0 {
		return string(event.Type)
	}
	return strings.Join(parts, " ")
}

func notificationEventContext(payload NotificationPayload) string {
	source := strings.TrimSpace(payload.Source)
	repo := strings.TrimSpace(payload.Repo)
	switch {
	case source != "" && repo != "":
		return fmt.Sprintf("%s/%s", source, repo)
	case source != "":
		return source
	case repo != "":
		return repo
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
