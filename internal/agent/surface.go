package agent

import "strings"

// PresentationSurface names the process that owns this agent. The same
// runtime can hide tools or inject extra prompt text per surface.
type PresentationSurface string

const (
	SurfaceTerminal PresentationSurface = "terminal"
	SurfaceACP      PresentationSurface = "acp"
	SurfaceGateway  PresentationSurface = "gateway"
	SurfaceExec     PresentationSurface = "exec"
)

func NormalizeSurface(raw string) PresentationSurface {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "acp":
		return SurfaceACP
	case "gateway":
		return SurfaceGateway
	case "exec", "headless", "print":
		return SurfaceExec
	default:
		return SurfaceTerminal
	}
}

func (s PresentationSurface) PromptDirective() string {
	switch s {
	case SurfaceACP:
		return "You are running inside an editor session. Prefer editor file tools over printing huge dumps. Do not assume a local TUI is present."
	case SurfaceGateway:
		return "You are running behind a messaging gateway. Keep replies concise. Do not rely on interactive TUI pickers; ask_user may be delayed or unavailable."
	case SurfaceExec:
		return "You are running non-interactively. Do not wait for hidden UI. Finish the task or report a concrete error."
	default:
		return ""
	}
}
