package main

func setActiveExecution(state *repositoryState, execution *deploymentExecution) {
	copy := *execution
	copy.RollbackCommands = append([]string(nil), execution.RollbackCommands...)
	state.Active = &copy
}

func cloneRepositoryState(state repositoryState) repositoryState {
	copy := state
	if state.Pending != nil {
		pending := *state.Pending
		copy.Pending = &pending
	}
	if state.Active != nil {
		active := *state.Active
		active.RollbackCommands = append([]string(nil), state.Active.RollbackCommands...)
		copy.Active = &active
	}
	copy.History = append([]deploymentExecution(nil), state.History...)
	for index := range copy.History {
		copy.History[index].RollbackCommands = append([]string(nil), copy.History[index].RollbackCommands...)
	}
	copy.Successful = append([]successfulRevision(nil), state.Successful...)
	for index := range copy.Successful {
		copy.Successful[index].RollbackCommands = append([]string(nil), copy.Successful[index].RollbackCommands...)
	}
	return copy
}
