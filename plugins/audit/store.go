package main

import (
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

var auditFilterColumns = map[string]string{
	"type":          "type",
	"source":        "source",
	"repo":          "repo",
	"execution_id":  "execution_id",
	"full_name":     "full_name",
	"stage":         "stage",
	"status":        "status",
	"last_error":    "last_error",
	"failure_class": "failure_class",
}

var auditFilterKeys = []string{
	"type",
	"source",
	"repo",
	"execution_id",
	"full_name",
	"stage",
	"status",
	"last_error",
	"failure_class",
}

type auditExecutionFields struct {
	ExecutionID  string
	FullName     string
	Stage        string
	Status       string
	LastError    string
	FailureClass string
}

func extractAuditExecutionFields(event core.InternalEvent) auditExecutionFields {
	return auditExecutionFields{
		ExecutionID:  eventDetailString(event, "execution_id"),
		FullName:     eventDetailString(event, "full_name"),
		Stage:        eventDetailString(event, "stage"),
		Status:       eventDetailString(event, "status"),
		LastError:    eventDetailString(event, "last_error"),
		FailureClass: eventDetailString(event, "failure_class"),
	}
}

func eventMatchesAuditFilter(event core.InternalEvent, filter map[string]any) bool {
	if filter == nil {
		return true
	}

	for _, key := range auditFilterKeys {
		expected, ok := filter[key].(string)
		if !ok || expected == "" {
			continue
		}
		if auditEventFieldValue(event, key) != expected {
			return false
		}
	}

	return true
}

func auditEventFieldValue(event core.InternalEvent, key string) string {
	switch key {
	case "type":
		return string(event.Type)
	case "source":
		return event.Source
	case "repo":
		return event.Repo
	case "execution_id", "full_name", "stage", "status", "last_error", "failure_class":
		return eventDetailString(event, key)
	default:
		return ""
	}
}

func eventDetailString(event core.InternalEvent, key string) string {
	if event.Details == nil {
		return ""
	}

	value, ok := event.Details[key].(string)
	if !ok {
		return ""
	}

	return value
}

type AuditStore interface {
	Save(event core.InternalEvent) error
	GetLastEvents(filter map[string]any, limit, offset int, order string, since, until *time.Time) ([]core.InternalEvent, error)
	Cleanup(keep int) error
	Close() error
}
