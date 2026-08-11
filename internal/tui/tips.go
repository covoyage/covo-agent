package tui

import (
	"fmt"
	"math/rand"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// RandomTip returns one localized feature-discovery tip.
func RandomTip() string {
	return i18n.T(fmt.Sprintf("tips.t%d", rand.Intn(45)))
}
