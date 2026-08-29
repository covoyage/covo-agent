package commands

import (
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"github.com/covoyage/covo-agent/internal/tui"
)

// loadUIBus is the compatibility boundary for UI composition.
// UIBus methods are nil-safe, including calls on a nil receiver before setup.
func loadUIBus() *tui.UIBus { return shared.RuntimeState.UI() }
