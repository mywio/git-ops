package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

type managedTimer interface {
	Stop() bool
}

type timerFactory func(time.Duration, func()) managedTimer

type attemptStatus string

const (
	attemptStatusSucceeded       attemptStatus = "succeeded"
	attemptStatusRetry           attemptStatus = "retry"
	attemptStatusTerminalFailure attemptStatus = "terminal_failure"
)

type refreshAttempt struct {
	Request refreshJobRequest
	Number  int
	Delay   time.Duration
}

type attemptResult struct {
	Status attemptStatus
}

type attemptRunner func(context.Context, refreshAttempt) attemptResult

type retryingCallback func(refreshAttempt)
type jobRequestCallback func(refreshJobRequest)

type jobManagerConfig struct {
	Logger       *slog.Logger
	Context      context.Context
	NewTimer     timerFactory
	RunAttempt   attemptRunner
	OnSuperseded jobRequestCallback
	OnRetrying   retryingCallback
	OnExhausted  jobRequestCallback
}

type jobManager struct {
	logger       *slog.Logger
	newTimer     timerFactory
	runAttempt   attemptRunner
	onSuperseded jobRequestCallback
	onRetrying   retryingCallback
	onExhausted  jobRequestCallback

	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	stopped bool
	jobs    map[refreshJobKey]*jobState
}

type jobState struct {
	request      refreshJobRequest
	timer        managedTimer
	running      bool
	attemptIndex int
	generation   int
	pending      *refreshJobRequest
}

func newJobManager(cfg jobManagerConfig) *jobManager {
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)

	factory := cfg.NewTimer
	if factory == nil {
		factory = func(delay time.Duration, callback func()) managedTimer {
			return time.AfterFunc(delay, callback)
		}
	}
	runner := cfg.RunAttempt
	if runner == nil {
		runner = func(context.Context, refreshAttempt) attemptResult {
			return attemptResult{Status: attemptStatusSucceeded}
		}
	}

	return &jobManager{
		logger:       cfg.Logger,
		newTimer:     factory,
		runAttempt:   runner,
		onSuperseded: cfg.OnSuperseded,
		onRetrying:   cfg.OnRetrying,
		onExhausted:  cfg.OnExhausted,
		ctx:          ctx,
		cancel:       cancel,
		jobs:         map[refreshJobKey]*jobState{},
	}
}

func (m *jobManager) Schedule(req refreshJobRequest) error {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return errors.New("job manager stopped")
	}

	state, ok := m.jobs[req.Key]
	if !ok {
		state = &jobState{request: req}
		m.jobs[req.Key] = state
		gen := m.scheduleAttemptLocked(req.Key, state, req, 0)
		_ = gen
		m.mu.Unlock()
		return nil
	}

	var superseded *refreshJobRequest
	if state.running {
		if state.pending != nil {
			copyReq := *state.pending
			superseded = &copyReq
		} else {
			copyReq := state.request
			superseded = &copyReq
		}
		pendingCopy := req
		state.pending = &pendingCopy
		m.mu.Unlock()
		m.emitSuperseded(*superseded)
		return nil
	}

	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	copyReq := state.request
	superseded = &copyReq
	state.request = req
	state.pending = nil
	m.scheduleAttemptLocked(req.Key, state, req, 0)
	m.mu.Unlock()
	m.emitSuperseded(*superseded)
	return nil
}

func (m *jobManager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	for _, state := range m.jobs {
		if state.timer != nil {
			state.timer.Stop()
		}
	}
	m.jobs = map[refreshJobKey]*jobState{}
	m.mu.Unlock()
	m.cancel()
}

func (m *jobManager) scheduleAttemptLocked(key refreshJobKey, state *jobState, req refreshJobRequest, attemptIndex int) int {
	state.request = req
	state.attemptIndex = attemptIndex
	state.generation++
	generation := state.generation
	delay := req.RetryDelays[attemptIndex]
	state.timer = m.newTimer(delay, func() {
		m.startAttempt(key, generation)
	})
	return generation
}

func (m *jobManager) startAttempt(key refreshJobKey, generation int) {
	m.mu.Lock()
	state, ok := m.jobs[key]
	if !ok || m.stopped || state.generation != generation || state.running {
		m.mu.Unlock()
		return
	}

	state.timer = nil
	state.running = true
	attemptIndex := state.attemptIndex
	request := state.request
	delay := request.RetryDelays[attemptIndex]
	m.mu.Unlock()

	attempt := refreshAttempt{Request: request, Number: attemptIndex + 1, Delay: delay}
	go func() {
		result := m.runAttempt(m.ctx, attempt)
		m.finishAttempt(key, generation, attemptIndex, result)
	}()
}

func (m *jobManager) finishAttempt(key refreshJobKey, generation int, attemptIndex int, result attemptResult) {
	m.mu.Lock()
	state, ok := m.jobs[key]
	if !ok || state.generation != generation {
		m.mu.Unlock()
		return
	}
	state.running = false

	if state.pending != nil {
		req := *state.pending
		state.pending = nil
		m.scheduleAttemptLocked(key, state, req, 0)
		m.mu.Unlock()
		return
	}

	switch result.Status {
	case attemptStatusRetry:
		nextIndex := attemptIndex + 1
		if nextIndex >= len(state.request.RetryDelays) {
			req := state.request
			delete(m.jobs, key)
			m.mu.Unlock()
			m.emitExhausted(req)
			return
		}
		req := state.request
		delay := req.RetryDelays[nextIndex]
		m.scheduleAttemptLocked(key, state, req, nextIndex)
		m.mu.Unlock()
		m.emitRetrying(refreshAttempt{Request: req, Number: nextIndex + 1, Delay: delay})
		return
	default:
		delete(m.jobs, key)
		m.mu.Unlock()
		return
	}
}

func (m *jobManager) emitSuperseded(req refreshJobRequest) {
	if m.onSuperseded != nil {
		m.onSuperseded(req)
	}
}

func (m *jobManager) emitRetrying(attempt refreshAttempt) {
	if m.onRetrying != nil {
		m.onRetrying(attempt)
	}
}

func (m *jobManager) emitExhausted(req refreshJobRequest) {
	if m.onExhausted != nil {
		m.onExhausted(req)
	}
}
