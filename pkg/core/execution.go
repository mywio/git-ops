package core

import (
	"fmt"
	"time"
)

type ExecutionStatus string

const (
	ExecutionStatusRequested ExecutionStatus = "requested"
	ExecutionStatusRunning   ExecutionStatus = "running"
	ExecutionStatusSucceeded ExecutionStatus = "succeeded"
	ExecutionStatusFailed    ExecutionStatus = "failed"
	ExecutionStatusCancelled ExecutionStatus = "cancelled"
)

type ExecutionStage string

const (
	ExecutionStageRequested  ExecutionStage = "requested"
	ExecutionStageFetch      ExecutionStage = "fetch"
	ExecutionStageDiff       ExecutionStage = "diff"
	ExecutionStageHooks      ExecutionStage = "hooks"
	ExecutionStageComposeUp  ExecutionStage = "compose_up"
	ExecutionStageComposeDown ExecutionStage = "compose_down"
	ExecutionStageComplete   ExecutionStage = "complete"
)

type FailureClass string

const (
	FailureClassUnknown     FailureClass = "unknown"
	FailureClassTransient   FailureClass = "transient"
	FailureClassPermanent   FailureClass = "permanent"
	FailureClassValidation  FailureClass = "validation"
	FailureClassDependency  FailureClass = "dependency"
)

type AlertSeverity string

const (
	AlertSeverityInfo     AlertSeverity = "info"
	AlertSeverityWarning  AlertSeverity = "warning"
	AlertSeverityError    AlertSeverity = "error"
	AlertSeverityCritical AlertSeverity = "critical"
)

const EventTypeExecution EventTypeName = "execution"

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
