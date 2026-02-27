package core

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

var (
	// RegisteredEventTypes maps name -> desc (for discoverability/validation)
	RegisteredEventTypes = make(map[EventTypeName]EventTypeDesc)
	eventTypesMu         sync.RWMutex

	// subscribers maps eventType (or pattern like "deploy_*") -> []Listener
	subscribers   = make(map[string][]Listener)
	subscribersMu sync.RWMutex
)

// registerEventType lets plugins/core define a new event type
func registerEventType(desc EventTypeDesc) error {
	eventTypesMu.Lock()
	defer eventTypesMu.Unlock()

	//TODO: we need to add validation on the names
	// Something like {type}_{description} to help and force standards and easily human read

	if _, exists := RegisteredEventTypes[desc.Name]; exists {
		return fmt.Errorf("event type %s already registered", desc.Name)
	}
	RegisteredEventTypes[desc.Name] = desc
	log.Printf("Registered event type: %s (%s)", desc.Name, desc.Description)
	return nil
}

// RegisterEventType lets plugins/core define a new event type
func RegisterEventType(desc EventTypeDesc) error {
	return registerEventType(desc)
}

// Subscribe lets plugins register a handler for an event type or pattern
// Pattern support: exact "deploy_success" or wildcard "deploy_*"
func Subscribe(pattern string, handler Listener) {
	subscribersMu.Lock()
	defer subscribersMu.Unlock()

	subscribers[pattern] = append(subscribers[pattern], handler)
	log.Printf("Subscribed to pattern: %s", pattern)
}

// Publish sends an event to all matching subscribers (async)
func Publish(ctx context.Context, event InternalEvent) {
	if ctx == nil {
		ctx = context.Background()
	}
	event.Timestamp = time.Now()

	// Optional: Validate against registered type (if exists)
	if desc, ok := RegisteredEventTypes[event.Type]; ok {
		for field, spec := range desc.PayloadSpec {
			if spec.Required {
				if _, has := event.Details[field]; !has {
					log.Printf("Warning: Published event %s missing required field %s", event.Type, field)
				}
			}
		}
	}

	subscribersMu.RLock()
	defer subscribersMu.RUnlock()

	for pattern, listeners := range subscribers {
		if matchesPattern(string(event.Type), pattern) {
			for _, listener := range listeners {
				go listener(ctx, event) // Async dispatch
			}
		}
	}
}

// matchesPattern: Simple wildcard support (e.g., "deploy_*" matches "deploy_success")
func matchesPattern(eventType, pattern string) bool {
	if pattern == eventType {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(eventType, prefix)
	}
	return false
}
