package core

import (
	"context"
	"time"
)

// EventTypeName is a string alias for event type identifiers (e.g., "reconcile_now")
type EventTypeName string

// EventTypeDesc describes an event type that can be registered on the internal
// event bus for validation and discoverability.
type EventTypeDesc struct {
	Name        EventTypeName           // Unique ID, e.g., "deploy_success"
	Description string                  // Human-readable, e.g., "Fired when a stack deploys successfully"
	PayloadSpec map[string]PayloadField // Optional: Expected fields in event.Details (for validation/docs)
}

// PayloadField describes one field expected in an event Details payload.
type PayloadField struct {
	Type        string // e.g., "string", "int", "map[string]interface{}"
	Description string
	Required    bool
}

// InternalEvent is the message sent over the internal event bus.
//
// Structured metadata belongs in Details. Message is a short human-readable
// summary used by notifiers and audit views.
type InternalEvent struct {
	Type      EventTypeName          `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	Source    string                 `json:"source"` // "timer", "webhook_trigger", "notifications", etc.
	Repo      string                 `json:"repo,omitempty"`
	Details   map[string]interface{} `json:"details,omitempty"` // Structured metadata lives here; keep the top-level surface small.
	Message   string                 `json:"message,omitempty"`
}

// Listener handles a published InternalEvent delivered by the event bus.
type Listener func(ctx context.Context, event InternalEvent)
