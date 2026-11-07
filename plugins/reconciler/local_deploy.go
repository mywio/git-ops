package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v57/github"
	"github.com/mywio/git-ops/pkg/core"
)

const localDeployRecipePath = ".ghops/deploy.yaml"
const actionsDeployRecipePath = ".ghops/actions.yaml"

func (r *Reconciler) handleRepositoryRevisionRequested(ctx context.Context, event core.InternalEvent) {
	request, ok := core.RepositoryRevisionFromEvent(event)
	if !ok {
		r.logger.Warn("repository revision request is incomplete", "source", event.Source)
		return
	}
	if err := core.ValidateStackIdentity(request.Owner, request.Repo); err != nil {
		r.logger.Warn("repository revision request identity is invalid", "error", err)
		return
	}
	if !r.ownerAllowed(request.Owner) {
		r.logger.Warn("repository revision owner is not allowed", "owner", request.Owner, "repo", request.Repo)
		return
	}

	repository, _, err := r.client.Repositories.Get(ctx, request.Owner, request.Repo)
	if err != nil {
		r.logger.Error("failed to validate webhook repository eligibility", "owner", request.Owner, "repo", request.Repo, "error", err)
		return
	}
	if repository.GetArchived() || !r.repositoryTopicAllowed(repository) {
		r.logger.Warn("repository revision is not opted in", "owner", request.Owner, "repo", request.Repo)
		return
	}
	expectedRef := "refs/heads/" + repository.GetDefaultBranch()
	if request.Ref != expectedRef {
		r.logger.Warn("repository revision no longer matches the default branch", "ref", request.Ref, "default_ref", expectedRef)
		return
	}

	hasActions, err := r.repositoryHasRecipe(ctx, request.Owner, request.Repo, request.CommitSHA, actionsDeployRecipePath)
	if err != nil {
		r.logger.Error("failed to check Actions deployment manifest", "owner", request.Owner, "repo", request.Repo, "sha", request.CommitSHA, "error", err)
		return
	}
	if hasActions {
		r.logger.Info("skipping legacy deployment for Actions-backed repository", "owner", request.Owner, "repo", request.Repo, "sha", request.CommitSHA)
		return
	}

	hasRecipe, err := r.repositoryHasLocalDeployRecipe(ctx, request.Owner, request.Repo, request.CommitSHA)
	if err != nil {
		r.logger.Error("failed to check local deploy recipe", "owner", request.Owner, "repo", request.Repo, "sha", request.CommitSHA, "error", err)
		return
	}
	if hasRecipe {
		r.publishLocalDeployRequest(ctx, request)
		return
	}
	if !r.scheduleReconcileStack(ctx, request.Owner, request.Repo, "") {
		r.logger.Info("skipping webhook fallback reconciliation while stopping", "owner", request.Owner, "repo", request.Repo)
	}
}

func (r *Reconciler) routeRecipeRevision(ctx context.Context, repo *github.Repository, sha, trigger string) (bool, error) {
	if repo == nil || repo.Owner == nil || repo.Name == nil {
		return false, fmt.Errorf("repository metadata incomplete")
	}
	owner := repo.GetOwner().GetLogin()
	name := repo.GetName()
	hasActions, err := r.repositoryHasRecipe(ctx, owner, name, sha, actionsDeployRecipePath)
	if err != nil || hasActions {
		if hasActions {
			r.logger.Info("skipping periodic legacy deployment for Actions-backed repository", "owner", owner, "repo", name, "sha", sha)
		}
		return hasActions, err
	}
	hasRecipe, err := r.repositoryHasLocalDeployRecipe(ctx, owner, name, sha)
	if err != nil || !hasRecipe {
		return hasRecipe, err
	}
	r.publishLocalDeployRequest(ctx, core.RepositoryRevisionRequested{
		Owner: owner, Repo: name, Ref: "refs/heads/" + repo.GetDefaultBranch(),
		CommitSHA: sha, Trigger: trigger,
	})
	return true, nil
}

func (r *Reconciler) publishLocalDeployRequest(ctx context.Context, request core.RepositoryRevisionRequested) {
	r.publish(ctx, core.InternalEvent{
		Type:   core.EventTypeLocalDeployRequested,
		Source: r.Name(),
		Repo:   request.Repo,
		Details: map[string]any{
			"owner": request.Owner, "repo": request.Repo, "ref": request.Ref,
			"commit_sha": request.CommitSHA, "delivery_id": request.DeliveryID, "trigger": request.Trigger,
		},
	})
}

func (r *Reconciler) repositoryHasLocalDeployRecipe(ctx context.Context, owner, repo, sha string) (bool, error) {
	return r.repositoryHasRecipe(ctx, owner, repo, sha, localDeployRecipePath)
}

func (r *Reconciler) repositoryHasRecipe(ctx context.Context, owner, repo, sha, recipePath string) (bool, error) {
	if sha == "" {
		return false, fmt.Errorf("commit SHA is required for recipe lookup")
	}
	_, _, response, err := r.client.Repositories.GetContents(
		ctx, owner, repo, recipePath, &github.RepositoryContentGetOptions{Ref: sha},
	)
	if err == nil {
		return true, nil
	}
	if response != nil && response.StatusCode == 404 {
		return false, nil
	}
	if strings.Contains(err.Error(), "404") {
		return false, nil
	}
	return false, err
}

func (r *Reconciler) ownerAllowed(owner string) bool {
	for _, allowed := range r.cfg.Users {
		if strings.EqualFold(strings.TrimSpace(allowed), owner) {
			return true
		}
	}
	return false
}

func (r *Reconciler) repositoryTopicAllowed(repo *github.Repository) bool {
	allowed := make(map[string]bool)
	for _, topic := range nonEmptyStrings(r.cfg.Topics) {
		allowed[strings.ToLower(topic)] = true
	}
	for _, topic := range repo.Topics {
		if allowed[strings.ToLower(topic)] {
			return true
		}
	}
	return false
}
