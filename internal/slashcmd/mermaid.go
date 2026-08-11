package slashcmd

import (
	"strings"

	"github.com/covoyage/covo-agent/internal/tui"
)

// handleMermaid renders a Mermaid flowchart as ASCII art in the terminal.
//
// Usage:
//
//	/mermaid graph TD; A --> B; B --> C
//	/mermaid
//	  graph LR
//	  Start --> Process --> End
func handleMermaid(sctx *SlashContext, parts []string) bool {
	input := strings.TrimSpace(strings.TrimPrefix(sctx.Input, parts[0]))
	if input == "" {
		sctx.UI.App.PrintSystem(strings.Join([]string{
			"Usage: /mermaid <mermaid syntax>",
			"",
			"Renders a Mermaid flowchart as ASCII art directly in the terminal.",
			"Supports: graph TD/LR, nodes, edges, subgraphs, labels.",
			"",
			"Example:",
			"  /mermaid graph TD; A[Start] --> B{Check}; B -->|Yes| C[Done]; B -->|No| D[Retry]",
		}, "\n"))
		return true
	}

	// Allow semicolon-separated syntax
	input = strings.ReplaceAll(input, ";", "\n")

	rendered := tui.RenderMermaid(input)
	sctx.UI.App.PrintSystem("── Mermaid Diagram ──")
	for _, line := range strings.Split(rendered, "\n") {
		sctx.UI.App.PrintSystem("  " + line)
	}
	return true
}
