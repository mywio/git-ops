package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

type executionSnapshot struct {
	ExecutionID string
	Owner       string
	Repo        string
	FullName    string
	Status      core.ExecutionStatus
	Stage       core.ExecutionStage
	LastError   string
	NodeID      string
	Trigger     string
	RequestedAt time.Time
	StartedAt   time.Time
	UpdatedAt   time.Time
	Active      bool
}

type executionStateManager struct {
	mu        sync.RWMutex
	now       func() time.Time
	snapshots map[string]executionSnapshot
	history   map[string][]executionSnapshot
}

func newExecutionStateManager(now func() time.Time) *executionStateManager {
	if now == nil {
		now = time.Now
	}
	return &executionStateManager{
		now:       now,
		snapshots: make(map[string]executionSnapshot),
		history:   make(map[string][]executionSnapshot),
	}
}

func (m *executionStateManager) acquire(fullName, owner, repo, nodeID, trigger string) (executionSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.snapshots[fullName]; ok && existing.Active {
		return existing, false
	}

	ts := m.now().UTC()
	snapshot := executionSnapshot{
		ExecutionID: fmt.Sprintf("%s-%d", fullName, ts.UnixNano()),
		Owner:       owner,
		Repo:        repo,
		FullName:    fullName,
		Status:      core.ExecutionStatusRequested,
		Stage:       core.ExecutionStageRequested,
		NodeID:      nodeID,
		Trigger:     trigger,
		RequestedAt: ts,
		UpdatedAt:   ts,
		Active:      true,
	}
	m.snapshots[fullName] = snapshot
	return snapshot, true
}

func (m *executionStateManager) markRunning(fullName string, stage core.ExecutionStage) (executionSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot, ok := m.snapshots[fullName]
	if !ok {
		return executionSnapshot{}, false
	}

	ts := m.now().UTC()
	snapshot.Status = core.ExecutionStatusRunning
	snapshot.Stage = stage
	snapshot.LastError = ""
	snapshot.UpdatedAt = ts
	snapshot.Active = true
	if snapshot.StartedAt.IsZero() {
		snapshot.StartedAt = ts
	}

	m.snapshots[fullName] = snapshot
	return snapshot, true
}

func (m *executionStateManager) markFailed(fullName string, stage core.ExecutionStage, err error) (executionSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot, ok := m.snapshots[fullName]
	if !ok {
		return executionSnapshot{}, false
	}

	snapshot.Status = core.ExecutionStatusFailed
	snapshot.Stage = stage
	snapshot.LastError = ""
	if err != nil {
		snapshot.LastError = err.Error()
	}
	snapshot.UpdatedAt = m.now().UTC()
	snapshot.Active = false

	m.snapshots[fullName] = snapshot
	m.appendHistory(fullName, snapshot)
	return snapshot, true
}

func (m *executionStateManager) markSucceeded(fullName string, stage core.ExecutionStage) (executionSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot, ok := m.snapshots[fullName]
	if !ok {
		return executionSnapshot{}, false
	}

	snapshot.Status = core.ExecutionStatusSucceeded
	snapshot.Stage = stage
	snapshot.LastError = ""
	snapshot.UpdatedAt = m.now().UTC()
	snapshot.Active = false

	m.snapshots[fullName] = snapshot
	m.appendHistory(fullName, snapshot)
	return snapshot, true
}

func (m *executionStateManager) snapshot(fullName string) (executionSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshot, ok := m.snapshots[fullName]
	return snapshot, ok
}

// snapshotHistory returns completed executions for a stack in newest-first order.
func (m *executionStateManager) snapshotHistory(fullName string) []executionSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	history := m.history[fullName]
	if len(history) == 0 {
		return nil
	}

	out := make([]executionSnapshot, 0, len(history))
	for i := len(history) - 1; i >= 0; i-- {
		out = append(out, history[i])
	}
	return out
}

// appendHistory must be called with m.mu held.
func (m *executionStateManager) appendHistory(fullName string, snapshot executionSnapshot) {
	const maxHistoryEntries = 10

	history := append(m.history[fullName], snapshot)
	if len(history) > maxHistoryEntries {
		history = history[len(history)-maxHistoryEntries:]
	}
	m.history[fullName] = history
}
