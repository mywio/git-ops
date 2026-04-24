package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type stackState struct {
	Disabled  bool      `json:"disabled"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

func stackStatePath(repoLocalPath string) string {
	return filepath.Join(repoLocalPath, ".git-ops", "stack_state.json")
}

func loadStackState(repoLocalPath string) (stackState, error) {
	data, err := os.ReadFile(stackStatePath(repoLocalPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return stackState{}, nil
		}
		return stackState{}, err
	}

	var state stackState
	if err := json.Unmarshal(data, &state); err != nil {
		return stackState{}, err
	}
	return state, nil
}

func saveStackState(repoLocalPath string, state stackState) error {
	state.UpdatedAt = time.Now().UTC()
	stateDir := filepath.Dir(stackStatePath(repoLocalPath))
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(stackStatePath(repoLocalPath), data, 0o644)
}

func isStackDisabled(repoLocalPath string) bool {
	state, err := loadStackState(repoLocalPath)
	if err != nil {
		return false
	}
	return state.Disabled
}

func setStackDisabled(repoLocalPath string, disabled bool) error {
	state, err := loadStackState(repoLocalPath)
	if err != nil {
		return err
	}
	state.Disabled = disabled
	return saveStackState(repoLocalPath, state)
}
