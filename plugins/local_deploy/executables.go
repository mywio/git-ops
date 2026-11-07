package main

import (
	"fmt"
	"os/exec"
)

func validateRecipeExecutables(recipe *deployRecipe) error {
	if recipe == nil {
		return fmt.Errorf("deployment recipe is missing")
	}
	steps := make([]recipeStep, 0, len(recipe.Phases.Build)+len(recipe.Phases.Deploy)+len(recipe.Phases.Verify)+len(recipe.Phases.Rollback))
	steps = append(steps, recipe.Phases.Build...)
	steps = append(steps, recipe.Phases.Deploy...)
	steps = append(steps, recipe.Phases.Verify...)
	steps = append(steps, recipe.Phases.Rollback...)
	for _, step := range steps {
		if _, err := exec.LookPath(step.Command); err != nil {
			return fmt.Errorf("required executable %q is unavailable: %w", step.Command, err)
		}
	}
	if recipe.Checkout.LFS {
		if _, err := exec.LookPath("git-lfs"); err != nil {
			return fmt.Errorf("required executable %q is unavailable: %w", "git-lfs", err)
		}
	}
	return nil
}
