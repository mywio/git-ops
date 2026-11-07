package fleetagent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mywio/git-ops/pkg/fleetapi"
)

type Agent struct {
	cfg     Config
	logger  *slog.Logger
	client  *controllerClient
	runners *runnerManager
}

func New(cfg Config, logger *slog.Logger) (*Agent, error) {
	manager, err := newRunnerManager(cfg, logger)
	if err != nil {
		return nil, err
	}
	return &Agent{cfg: cfg, logger: logger, runners: manager}, nil
}

func (a *Agent) Run(ctx context.Context) error {
	client, err := newControllerClient(ctx, a.cfg)
	if err != nil {
		return err
	}
	a.client = client
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); a.heartbeatLoop(ctx) }()
	go func() { defer wg.Done(); a.taskLoop(ctx) }()
	<-ctx.Done()
	wg.Wait()
	return ctx.Err()
}

func (a *Agent) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		if err := a.client.heartbeat(ctx, a.runners.inventory()); err != nil && ctx.Err() == nil {
			a.logger.Warn("heartbeat failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) taskLoop(ctx context.Context) {
	for ctx.Err() == nil {
		task, err := a.client.claim(ctx)
		if err != nil {
			a.logger.Warn("task claim failed", "error", err)
			if !waitContext(ctx, 5*time.Second) {
				return
			}
			continue
		}
		if task == nil {
			if !waitContext(ctx, 2*time.Second) {
				return
			}
			continue
		}
		started := time.Now().UTC()
		err = a.runners.execute(ctx, *task, func() (fleetapi.RegistrationCredential, error) {
			return a.client.credential(ctx, task.ID)
		})
		result := fleetapi.TaskResult{Status: "succeeded", StartedAt: started, EndedAt: time.Now().UTC()}
		if err != nil {
			result.Status, result.Error = "failed", fmt.Sprintf("%v", err)
		}
		if reportErr := a.client.result(ctx, task.ID, result); reportErr != nil && ctx.Err() == nil {
			a.logger.Error("report task result", "task_id", task.ID, "error", reportErr)
		}
	}
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
