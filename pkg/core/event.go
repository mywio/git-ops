package core

import (
	"context"
	"time"
)

// EventTypeName is a string alias for event type identifiers (e.g., "reconcile_now")
type EventTypeName string

// EventTypeDesc defines the "class" for an event type (registered dynamically)
type EventTypeDesc struct {
	Name        EventTypeName           // Unique ID, e.g., "deploy_success"
	Description string                  // Human-readable, e.g., "Fired when a stack deploys successfully"
	PayloadSpec map[string]PayloadField // Optional: Expected fields in event.Details (for validation/docs)
}

// PayloadField describes a field in the event payload
type PayloadField struct {
	Type        string // e.g., "string", "int", "map[string]interface{}"
	Description string
	Required    bool
}

// InternalEvent is the payload sent over the bus
type InternalEvent struct {
	Type      EventTypeName          `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	Source    string                 `json:"source"` // "timer", "webhook_trigger", "notifications", etc.
	Repo      string                 `json:"repo,omitempty"`
	Details   map[string]interface{} `json:"details,omitempty"`
	String    string                 `json:"string,omitempty"`
}

// Listener is a handler func for subscribers
type Listener func(ctx context.Context, event InternalEvent)
