package agent

import "errors"

var (
	errEmptyTask = errors.New("task is required")
	errNoExplore = errors.New("explore is not available")
	errNoSummary = errors.New("subagent returned an empty summary")
)
