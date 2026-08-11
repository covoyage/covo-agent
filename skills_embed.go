// Package covoagent holds resources embedded into the covo-agent binary so that
// single-binary distributions ship everything they need.
//
// The embed directive must live at the module root because go:embed cannot
// reference parent directories (skills/ sits at the repo root, above the
// packages that consume it).
package covoagent

import "embed"

// BundledSkillsFS contains the built-in skills/ tree. It is unpacked at runtime
// when no on-disk bundled skills directory is found (e.g. a standalone binary
// with no skills/ folder alongside it). The "all:" prefix ensures dotfiles and
// underscore-prefixed support files are included.
//
//go:embed all:skills
var BundledSkillsFS embed.FS
