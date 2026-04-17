package core

import (
	"fmt"
	"time"
)

type ExecutionStatus string

const (
	// ExecutionStatusRequested indicates an execution has been queued but not yet running.
	ExecutionStatusRequested ExecutionStatus = "requested"
	// ExecutionStatusRunning indicates an execution is currently in progress.
	ExecutionStatusRunning   ExecutionStatus = "running"
	// ExecutionStatusSucceeded indicates an execution completed successfully.
	ExecutionStatusSucceeded ExecutionStatus = "succeeded"
	// ExecutionStatusFailed indicates an execution ended with an error.
	ExecutionStatusFailed    ExecutionStatus = "failed"
	// ExecutionStatusCancelled indicates an execution was cancelled before completion.
	ExecutionStatusCancelled ExecutionStatus = "cancelled"
)

// ExecutionStage identifies the current phase of a stack execution lifecycle.
type ExecutionStage string

const (
	// ExecutionStageRequested is the point where execution has been requested.
	ExecutionStageRequested ExecutionStage = "requested"
	// ExecutionStageFetch is the phase that reads desired repository state.
	ExecutionStageFetch ExecutionStage = "fetch"
	// ExecutionStageDiff is the phase that compares desired and local state.
	ExecutionStageDiff ExecutionStage = "diff"
	// ExecutionStageHooks is the phase that runs deployment hooks.
	ExecutionStageHooks ExecutionStage = "hooks"
	// ExecutionStageComposeUp is the phase that applies stack changes via compose up.
	ExecutionStageComposeUp ExecutionStage = "compose_up"
	// ExecutionStageComposeDown is the phase that tears a stack down via compose down.
	ExecutionStageComposeDown ExecutionStage = "compose_down"
	// ExecutionStageComplete is the terminal success stage for an execution.
	ExecutionStageComplete ExecutionStage = "complete"
)

// FailureClass categorizes why an execution failed.
type FailureClass string

const (
	// FailureClassUnknown is used when the failure could not be categorized.
	FailureClassUnknown FailureClass = "unknown"
	// FailureClassTransient indicates a retry may succeed later.
	FailureClassTransient FailureClass = "transient"
	// FailureClassPermanent indicates the same operation is expected to fail again.
	FailureClassPermanent FailureClass = "permanent"
	// FailureClassValidation indicates invalid inputs or local preconditions.
	FailureClassValidation FailureClass = "validation"
	// FailureClassDependency indicates an external dependency is missing or unavailable.
	FailureClassDependency FailureClass = "dependency"
)

// AlertSeverity describes how strongly downstream notifiers should surface an event.
type AlertSeverity string

const (
	// AlertSeverityInfo is a low-urgency informational signal.
	AlertSeverityInfo AlertSeverity = "info"
	// AlertSeverityWarning is a warning-level signal that may need attention.
	AlertSeverityWarning AlertSeverity = "warning"
	// AlertSeverityError is an error-level signal for failed operations.
	AlertSeverityError AlertSeverity = "error"
	// AlertSeverityCritical is a high-urgency signal for severe failures.
	AlertSeverityCritical AlertSeverity = "critical"
)

// EventTypeExecution is the canonical event type name for execution lifecycle updates.
const EventTypeExecution EventTypeName = "execution"

// ExecutionEventInput captures the normalized fields used to build an execution event.
type ExecutionEventInput struct {
	ExecutionID   string
	Owner         string
	Repo          string
	FullName      string
	Stage         ExecutionStage
	Status        ExecutionStatus
	FailureClass  FailureClass
	AlertSeverity AlertSeverity
	NodeID        string
	Trigger       string
	Source        string
	Details       map[string]any
}

// NewExecutionEvent builds a normalized InternalEvent for execution lifecycle updates.
func NewExecutionEvent(input ExecutionEventInput) InternalEvent {
	details := make(map[string]any, len(input.Details)+10)
	for key, value := range input.Details {
		details[key] = value
	}

	fullName := input.FullName
	if fullName == "" && input.Owner != "" && input.Repo != "" {
		fullName = fmt.Sprintf("%s/%s", input.Owner, input.Repo)
	}

	details["execution_id"] = input.ExecutionID
	details["owner"] = input.Owner
	details["repo"] = input.Repo
	details["full_name"] = fullName
	details["stage"] = string(input.Stage)
	details["status"] = string(input.Status)
	if input.FailureClass != "" {
		details["failure_class"] = string(input.FailureClass)
	}
	if input.AlertSeverity != "" {
		details["alert_severity"] = string(input.AlertSeverity)
	}
	if input.NodeID != "" {
		details["node_id"] = input.NodeID
	}
	if input.Trigger != "" {
		details["trigger"] = input.Trigger
	}
	if input.Source != "" {
		details["source"] = input.Source
	}

	return InternalEvent{
		Type:      EventTypeExecution,
		Timestamp: time.Now().UTC(),
		Source:    input.Source,
		Repo:      input.Repo,
		Details:   details,
	}
}
