package main

import (
	runtimeapp "github.com/covoyage/covo-agent/internal/app"
	"github.com/covoyage/covo-agent/internal/tui"
)

var runtimeState = runtimeapp.NewRuntimeState()

// loadUIBus is the compatibility boundary for package-main UI composition.
// UIBus methods are nil-safe, including calls on a nil receiver before setup.
func loadUIBus() *tui.UIBus { return runtimeState.UI() }
