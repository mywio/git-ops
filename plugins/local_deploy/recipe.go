package main

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	recipePath     = ".ghops/deploy.yaml"
	maxStepTimeout = 24 * time.Hour
)

type recipeDuration struct {
	time.Duration
}

func (d *recipeDuration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a string")
	}
	value, err := time.ParseDuration(node.Value)
	if err != nil || value <= 0 || value > maxStepTimeout {
		return fmt.Errorf("duration %q must be greater than zero and no more than %s", node.Value, maxStepTimeout)
	}
	d.Duration = value
	return nil
}

type deployRecipe struct {
	Version  int            `yaml:"version"`
	Checkout checkoutRecipe `yaml:"checkout"`
	Phases   recipePhases   `yaml:"phases"`
	Health   *healthRecipe  `yaml:"health,omitempty"`
}

type checkoutRecipe struct {
	Submodules string `yaml:"submodules,omitempty"`
	LFS        bool   `yaml:"lfs,omitempty"`
}

type recipePhases struct {
	Build    []recipeStep `yaml:"build"`
	Deploy   []recipeStep `yaml:"deploy"`
	Verify   []recipeStep `yaml:"verify,omitempty"`
	Rollback []recipeStep `yaml:"rollback"`
}

type recipeStep struct {
	Command          string         `yaml:"command"`
	Args             []string       `yaml:"args,omitempty"`
	WorkingDirectory string         `yaml:"working_directory,omitempty"`
	Timeout          recipeDuration `yaml:"timeout"`
}

type healthRecipe struct {
	Compose *composeHealthRecipe `yaml:"compose,omitempty"`
}

type composeHealthRecipe struct {
	Files          []string       `yaml:"files"`
	ProjectName    string         `yaml:"project_name,omitempty"`
	Timeout        recipeDuration `yaml:"timeout"`
	Interval       recipeDuration `yaml:"interval"`
	AllowCompleted []string       `yaml:"allow_completed,omitempty"`
}

func parseDeployRecipe(data []byte) (*deployRecipe, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var recipe deployRecipe
	if err := decoder.Decode(&recipe); err != nil {
		return nil, fmt.Errorf("decode %s: %w", recipePath, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, fmt.Errorf("%s must contain one YAML document", recipePath)
	} else if err != io.EOF {
		return nil, fmt.Errorf("decode trailing %s content: %w", recipePath, err)
	}
	if recipe.Version != 1 {
		return nil, fmt.Errorf("unsupported recipe version %d", recipe.Version)
	}
	if recipe.Checkout.Submodules != "" && recipe.Checkout.Submodules != "recursive" {
		return nil, fmt.Errorf("checkout.submodules must be omitted or recursive")
	}
	if err := validatePhase("build", recipe.Phases.Build, true); err != nil {
		return nil, err
	}
	if err := validatePhase("deploy", recipe.Phases.Deploy, true); err != nil {
		return nil, err
	}
	if err := validatePhase("verify", recipe.Phases.Verify, false); err != nil {
		return nil, err
	}
	if err := validatePhase("rollback", recipe.Phases.Rollback, true); err != nil {
		return nil, err
	}
	if recipe.Health != nil {
		if recipe.Health.Compose == nil {
			return nil, fmt.Errorf("health must configure compose")
		}
		if err := validateComposeHealth(recipe.Health.Compose); err != nil {
			return nil, err
		}
	}
	return &recipe, nil
}

func validatePhase(name string, steps []recipeStep, required bool) error {
	if required && len(steps) == 0 {
		return fmt.Errorf("phases.%s must contain at least one step", name)
	}
	for index := range steps {
		step := &steps[index]
		step.Command = strings.TrimSpace(step.Command)
		if step.Command == "" {
			return fmt.Errorf("phases.%s[%d].command is required", name, index)
		}
		if step.Timeout.Duration <= 0 {
			return fmt.Errorf("phases.%s[%d].timeout is required", name, index)
		}
		if step.WorkingDirectory == "" {
			step.WorkingDirectory = "."
		}
		if err := validateRelativePath(step.WorkingDirectory); err != nil {
			return fmt.Errorf("phases.%s[%d].working_directory: %w", name, index, err)
		}
	}
	return nil
}

func validateComposeHealth(health *composeHealthRecipe) error {
	if len(health.Files) == 0 {
		return fmt.Errorf("health.compose.files must contain at least one file")
	}
	for index, file := range health.Files {
		if err := validateRelativePath(file); err != nil {
			return fmt.Errorf("health.compose.files[%d]: %w", index, err)
		}
	}
	if health.Timeout.Duration <= 0 {
		return fmt.Errorf("health.compose.timeout is required")
	}
	if health.Interval.Duration <= 0 {
		return fmt.Errorf("health.compose.interval is required")
	}
	if health.Interval.Duration > health.Timeout.Duration {
		return fmt.Errorf("health.compose.interval must not exceed timeout")
	}
	seen := make(map[string]struct{}, len(health.AllowCompleted))
	for _, service := range health.AllowCompleted {
		service = strings.TrimSpace(service)
		if service == "" {
			return fmt.Errorf("health.compose.allow_completed contains an empty service")
		}
		if _, exists := seen[service]; exists {
			return fmt.Errorf("health.compose.allow_completed contains duplicate service %q", service)
		}
		seen[service] = struct{}{}
	}
	return nil
}

func validateRelativePath(value string) error {
	if value == "" || filepath.IsAbs(value) {
		return fmt.Errorf("path must be relative")
	}
	cleaned := filepath.Clean(value)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path must remain inside the checkout")
	}
	return nil
}

func recipeCommandSummary(recipe *deployRecipe, phase string) []string {
	if recipe == nil {
		return nil
	}
	var steps []recipeStep
	switch phase {
	case "build":
		steps = recipe.Phases.Build
	case "deploy":
		steps = recipe.Phases.Deploy
	case "verify":
		steps = recipe.Phases.Verify
	case "rollback":
		steps = recipe.Phases.Rollback
	}
	result := make([]string, 0, len(steps))
	for _, step := range steps {
		result = append(result, strings.Join(append([]string{step.Command}, step.Args...), " "))
	}
	return result
}
