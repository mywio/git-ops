package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/go-github/v57/github"
	"github.com/mywio/git-ops/pkg/core"
)

type commitTracker struct {
	mu      sync.Mutex
	commits map[string]string
}

func newCommitTracker() *commitTracker {
	return &commitTracker{commits: make(map[string]string)}
}

func commitTrackerKey(fullName, stackPath string) string {
	return fullName + "|" + stackPath
}

func (t *commitTracker) record(fullName, stackPath, newCommit string) (string, bool) {
	if t == nil {
		return "", false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	key := commitTrackerKey(fullName, stackPath)
	oldCommit := t.commits[key]
	if oldCommit == newCommit {
		return oldCommit, false
	}
	t.commits[key] = newCommit
	return oldCommit, true
}

func newStackCommitChangedEvent(owner, repo, fullName, stackPath, oldCommit, newCommit string, composeChanged bool) core.InternalEvent {
	return core.InternalEvent{
		Type:      core.EventTypeName("stack_commit_changed"),
		Timestamp: time.Now().UTC(),
		Source:    "reconciler",
		Repo:      repo,
		Details: map[string]any{
			"owner":           owner,
			"repo":            repo,
			"full_name":       fullName,
			"stack_path":      stackPath,
			"old_commit":      oldCommit,
			"new_commit":      newCommit,
			"compose_changed": composeChanged,
		},
	}
}

var fetchRepoDefaultBranchSHA = func(ctx context.Context, client *github.Client, repo *github.Repository) (string, error) {
	if client == nil || repo == nil || repo.Owner == nil || repo.Owner.Login == nil || repo.Name == nil {
		return "", fmt.Errorf("repository metadata incomplete")
	}
	defaultBranch := repo.GetDefaultBranch()
	if defaultBranch == "" {
		return "", fmt.Errorf("repository default branch missing")
	}
	branch, _, err := client.Repositories.GetBranch(ctx, repo.GetOwner().GetLogin(), repo.GetName(), defaultBranch, 0)
	if err != nil {
		return "", err
	}
	if branch == nil || branch.Commit == nil || branch.Commit.SHA == nil || branch.GetCommit().GetSHA() == "" {
		return "", fmt.Errorf("repository branch sha missing")
	}
	return branch.GetCommit().GetSHA(), nil
}

func (r *Reconciler) completeSuccessfulStack(ctx context.Context, fullName string, repo *github.Repository, stackPath, newCommit string, composeChanged bool) {
	r.succeedExecution(ctx, fullName)
	r.recordCommitChange(ctx, fullName, repo, stackPath, newCommit, composeChanged)
}

func (r *Reconciler) recordCommitChange(ctx context.Context, fullName string, repo *github.Repository, stackPath, newCommit string, composeChanged bool) {
	if r.commitTracker == nil || repo == nil || repo.Owner == nil || repo.Owner.Login == nil || repo.Name == nil || newCommit == "" {
		return
	}
	oldCommit, changed := r.commitTracker.record(fullName, stackPath, newCommit)
	if !changed {
		return
	}
	r.publish(ctx, newStackCommitChangedEvent(repo.GetOwner().GetLogin(), repo.GetName(), fullName, stackPath, oldCommit, newCommit, composeChanged))
}
