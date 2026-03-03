package main

import (
	"sort"
	"strings"
	"sync"

	"github.com/mywio/git-ops/pkg/core"
)

type memoryStore struct {
	events []core.InternalEvent
	mu     sync.RWMutex
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		events: make([]core.InternalEvent, 0),
	}
}

func (s *memoryStore) Save(event core.InternalEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *memoryStore) GetLastEvents(filter map[string]any, limit, offset int, order string) ([]core.InternalEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []core.InternalEvent
	for _, ev := range s.events {
		if s.matches(ev, filter) {
			matched = append(matched, ev)
		}
	}

	if strings.ToLower(order) == "asc" {
		sort.SliceStable(matched, func(i, j int) bool {
			return matched[i].Timestamp.Before(matched[j].Timestamp)
		})
	} else {
		sort.SliceStable(matched, func(i, j int) bool {
			return matched[i].Timestamp.After(matched[j].Timestamp)
		})
	}

	if offset >= len(matched) {
		return []core.InternalEvent{}, nil
	}

	end := offset + limit
	if limit <= 0 || end > len(matched) {
		end = len(matched)
	}

	return matched[offset:end], nil
}

func (s *memoryStore) matches(event core.InternalEvent, filter map[string]any) bool {
	if filter == nil {
		return true
	}

	if t, ok := filter["type"].(string); ok && t != "" && string(event.Type) != t {
		return false
	}
	if src, ok := filter["source"].(string); ok && src != "" && event.Source != src {
		return false
	}
	if repo, ok := filter["repo"].(string); ok && repo != "" && event.Repo != repo {
		return false
	}
	return true
}

func (s *memoryStore) Cleanup(keep int) error {
	if keep <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.events) > keep {
		s.events = s.events[len(s.events)-keep:]
	}
	return nil
}

func (s *memoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = nil
	return nil
}
