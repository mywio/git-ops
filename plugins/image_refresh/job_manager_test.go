package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTimer struct {
	delay    time.Duration
	callback func()
	stopped  bool
}

func (t *fakeTimer) Stop() bool {
	if t.stopped {
		return false
	}
	t.stopped = true
	return true
}

func (t *fakeTimer) Fire() {
	if t.stopped {
		return
	}
	t.callback()
}

type fakeTimerFactory struct {
	mu     sync.Mutex
	timers []*fakeTimer
}

func (f *fakeTimerFactory) New(delay time.Duration, callback func()) managedTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	timer := &fakeTimer{delay: delay, callback: callback}
	f.timers = append(f.timers, timer)
	return timer
}

func (f *fakeTimerFactory) Timer(index int) *fakeTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.timers[index]
}

func (f *fakeTimerFactory) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

type attemptCall struct {
	Request refreshJobRequest
	Attempt int
	Delay   time.Duration
}

func testRefreshJobRequest(fullName, newCommit string, delays ...time.Duration) refreshJobRequest {
	return refreshJobRequest{
		Key: refreshJobKey{FullName: fullName, StackPath: "/tmp/" + fullName},
		Owner:       "acme",
		Repo:        "api",
		OldCommit:   "old",
		NewCommit:   newCommit,
		RetryDelays: delays,
	}
}

func TestJobManagerStartsImmediateAttempt(t *testing.T) {
	factory := &fakeTimerFactory{}
	calls := make(chan attemptCall, 1)
	manager := newJobManager(jobManagerConfig{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewTimer: factory.New,
		RunAttempt: func(ctx context.Context, attempt refreshAttempt) attemptResult {
			calls <- attemptCall{Request: attempt.Request, Attempt: attempt.Number, Delay: attempt.Delay}
			return attemptResult{Status: attemptStatusSucceeded}
		},
	})

	require.NoError(t, manager.Schedule(testRefreshJobRequest("acme/api", "new", 0, time.Minute)))
	require.Equal(t, 1, factory.Len())
	assert.Equal(t, time.Duration(0), factory.Timer(0).delay)

	factory.Timer(0).Fire()
	call := <-calls
	assert.Equal(t, 1, call.Attempt)
	assert.Equal(t, time.Duration(0), call.Delay)
}

func TestJobManagerUsesConfiguredRetrySchedule(t *testing.T) {
	factory := &fakeTimerFactory{}
	calls := make(chan attemptCall, 2)
	manager := newJobManager(jobManagerConfig{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewTimer: factory.New,
		RunAttempt: func(ctx context.Context, attempt refreshAttempt) attemptResult {
			calls <- attemptCall{Request: attempt.Request, Attempt: attempt.Number, Delay: attempt.Delay}
			if attempt.Number == 1 {
				return attemptResult{Status: attemptStatusRetry}
			}
			return attemptResult{Status: attemptStatusSucceeded}
		},
	})

	require.NoError(t, manager.Schedule(testRefreshJobRequest("acme/api", "new", 0, time.Minute, 2*time.Minute)))
	factory.Timer(0).Fire()
	first := <-calls
	assert.Equal(t, 1, first.Attempt)
	require.Equal(t, 2, factory.Len())
	assert.Equal(t, time.Minute, factory.Timer(1).delay)

	factory.Timer(1).Fire()
	second := <-calls
	assert.Equal(t, 2, second.Attempt)
	assert.Equal(t, time.Minute, second.Delay)
}

func TestJobManagerSupersedesPendingJobForSameStack(t *testing.T) {
	factory := &fakeTimerFactory{}
	superseded := make(chan refreshJobRequest, 1)
	calls := make(chan attemptCall, 1)
	manager := newJobManager(jobManagerConfig{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewTimer: factory.New,
		RunAttempt: func(ctx context.Context, attempt refreshAttempt) attemptResult {
			calls <- attemptCall{Request: attempt.Request, Attempt: attempt.Number, Delay: attempt.Delay}
			return attemptResult{Status: attemptStatusSucceeded}
		},
		OnSuperseded: func(req refreshJobRequest) {
			superseded <- req
		},
	})

	first := testRefreshJobRequest("acme/api", "commit-1", 0, time.Minute)
	second := testRefreshJobRequest("acme/api", "commit-2", 0, 2*time.Minute)
	require.NoError(t, manager.Schedule(first))
	require.NoError(t, manager.Schedule(second))

	require.True(t, factory.Timer(0).stopped)
	assert.Equal(t, first.NewCommit, (<-superseded).NewCommit)

	factory.Timer(1).Fire()
	call := <-calls
	assert.Equal(t, second.NewCommit, call.Request.NewCommit)
	assert.Equal(t, 1, call.Attempt)
}

func TestJobManagerDoesNotOverlapRunningJobForSameStack(t *testing.T) {
	factory := &fakeTimerFactory{}
	started := make(chan attemptCall, 2)
	release := make(chan attemptResult, 2)
	manager := newJobManager(jobManagerConfig{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewTimer: factory.New,
		RunAttempt: func(ctx context.Context, attempt refreshAttempt) attemptResult {
			started <- attemptCall{Request: attempt.Request, Attempt: attempt.Number, Delay: attempt.Delay}
			return <-release
		},
	})

	first := testRefreshJobRequest("acme/api", "commit-1", 0, time.Minute)
	second := testRefreshJobRequest("acme/api", "commit-2", 0, time.Minute)
	require.NoError(t, manager.Schedule(first))
	factory.Timer(0).Fire()
	call := <-started
	assert.Equal(t, first.NewCommit, call.Request.NewCommit)

	require.NoError(t, manager.Schedule(second))
	assert.Equal(t, 1, factory.Len())
	select {
	case <-started:
		t.Fatal("second attempt started while first was still running")
	default:
	}

	release <- attemptResult{Status: attemptStatusSucceeded}
	require.Eventually(t, func() bool { return factory.Len() == 2 }, time.Second, 10*time.Millisecond)
	factory.Timer(1).Fire()
	call = <-started
	assert.Equal(t, second.NewCommit, call.Request.NewCommit)
	release <- attemptResult{Status: attemptStatusSucceeded}
}

func TestJobManagerLetsActiveUpFinishBeforeSupersessionTakesEffect(t *testing.T) {
	factory := &fakeTimerFactory{}
	superseded := make(chan refreshJobRequest, 1)
	started := make(chan attemptCall, 2)
	release := make(chan attemptResult, 2)
	manager := newJobManager(jobManagerConfig{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewTimer: factory.New,
		RunAttempt: func(ctx context.Context, attempt refreshAttempt) attemptResult {
			started <- attemptCall{Request: attempt.Request, Attempt: attempt.Number, Delay: attempt.Delay}
			return <-release
		},
		OnSuperseded: func(req refreshJobRequest) {
			superseded <- req
		},
	})

	first := testRefreshJobRequest("acme/api", "commit-1", 0, time.Minute, 2*time.Minute)
	second := testRefreshJobRequest("acme/api", "commit-2", 0, 3*time.Minute)
	require.NoError(t, manager.Schedule(first))
	factory.Timer(0).Fire()
	<-started

	require.NoError(t, manager.Schedule(second))
	assert.Equal(t, first.NewCommit, (<-superseded).NewCommit)

	release <- attemptResult{Status: attemptStatusRetry}
	require.Eventually(t, func() bool { return factory.Len() == 2 }, time.Second, 10*time.Millisecond)
	assert.Equal(t, time.Duration(0), factory.Timer(1).delay)
	factory.Timer(1).Fire()
	call := <-started
	assert.Equal(t, second.NewCommit, call.Request.NewCommit)
	release <- attemptResult{Status: attemptStatusSucceeded}
}

func TestJobManagerStopsAfterExhaustion(t *testing.T) {
	factory := &fakeTimerFactory{}
	exhausted := make(chan refreshJobRequest, 1)
	calls := make(chan attemptCall, 2)
	manager := newJobManager(jobManagerConfig{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewTimer: factory.New,
		RunAttempt: func(ctx context.Context, attempt refreshAttempt) attemptResult {
			calls <- attemptCall{Request: attempt.Request, Attempt: attempt.Number, Delay: attempt.Delay}
			return attemptResult{Status: attemptStatusRetry}
		},
		OnExhausted: func(req refreshJobRequest) {
			exhausted <- req
		},
	})

	require.NoError(t, manager.Schedule(testRefreshJobRequest("acme/api", "new", 0, time.Minute)))
	factory.Timer(0).Fire()
	<-calls
	require.Eventually(t, func() bool { return factory.Len() == 2 }, time.Second, 10*time.Millisecond)
	factory.Timer(1).Fire()
	<-calls

	assert.Equal(t, "new", (<-exhausted).NewCommit)
	assert.Equal(t, 2, factory.Len())
}
