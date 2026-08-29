package shared

import (
	runtimeapp "github.com/covoyage/covo-agent/internal/app"
)

// RuntimeState is the process-wide runtime state shared by the interactive
// runtime and the commands that inspect it (e.g. the review command).
var RuntimeState = runtimeapp.NewRuntimeState()
