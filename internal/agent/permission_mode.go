package agent

import (
	"fmt"
	"strings"

	"github.com/covoyage/covo-agent/internal/agent/approval"
)

// PermissionMode is the user-facing approval policy.
// TUI uses ask|auto|full; exec uses smart|full|off.
type PermissionMode string

const (
	PermissionAsk  PermissionMode = "ask"
	PermissionAuto PermissionMode = "auto"
	PermissionFull PermissionMode = "full"
	PermissionOff  PermissionMode = "off"
)

// NormalizePermissionMode maps CLI/TUI aliases onto a canonical mode.
func NormalizePermissionMode(raw string, interactive bool) (PermissionMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		if interactive {
			return PermissionAsk, nil
		}
		return PermissionAuto, nil
	case "ask", "manual", "build":
		if !interactive {
			return "", fmt.Errorf("permission ask requires an interactive session; use smart, full, or off")
		}
		return PermissionAsk, nil
	case "auto", "smart", "edit":
		return PermissionAuto, nil
	case "plan":
		if !interactive {
			return "", fmt.Errorf("permission plan requires an interactive session; use smart, full, or off")
		}
		return PermissionAsk, nil
	case "full", "yolo":
		return PermissionFull, nil
	case "off":
		if interactive {
			return PermissionFull, nil
		}
		return PermissionOff, nil
	default:
		if interactive {
			return "", fmt.Errorf("unknown permission mode %q (expected plan, ask, auto, or yolo)", raw)
		}
		return "", fmt.Errorf("unknown permission mode %q (expected smart, full, or off)", raw)
	}
}

// ApplyPermissionMode updates the approval system for the given mode.
func ApplyPermissionMode(sys *approval.System, mode PermissionMode) {
	if sys == nil {
		return
	}
	switch mode {
	case PermissionAuto:
		sys.SetYoloMode(false)
		sys.DisableSessionYolo("cli")
		sys.DisableSessionYolo("settings")
		sys.SetMode("smart")
	case PermissionFull:
		sys.SetMode("off")
		sys.SetYoloMode(true)
		sys.EnableSessionYolo("cli")
	case PermissionOff:
		sys.SetYoloMode(false)
		sys.DisableSessionYolo("cli")
		sys.DisableSessionYolo("settings")
		sys.SetMode("off")
	default:
		sys.SetYoloMode(false)
		sys.DisableSessionYolo("cli")
		sys.DisableSessionYolo("settings")
		sys.SetMode("manual")
	}
}

// PermissionModeFromApproval infers the TUI-facing mode from approval state.
func PermissionModeFromApproval(sys *approval.System) PermissionMode {
	if sys == nil {
		return PermissionAsk
	}
	if sys.IsYolo() || sys.Mode() == "off" {
		return PermissionFull
	}
	if sys.Mode() == "smart" {
		return PermissionAuto
	}
	return PermissionAsk
}
