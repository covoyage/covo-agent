package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/lsp"
)

// lspNavExtension contributes a precise code-navigation tool backed by LSP
// (definition / references / hover), so the model can navigate code by symbol
// semantics instead of regex/grep.
type lspNavExtension struct {
	mgr     *lsp.Manager
	workDir string
}

func newLSPNavExtension(mgr *lsp.Manager, workDir string) *lspNavExtension {
	return &lspNavExtension{mgr: mgr, workDir: workDir}
}

func (e *lspNavExtension) Name() string                                     { return "lsp-nav" }
func (e *lspNavExtension) Init(_ context.Context, _ *agentcore.Agent) error { return nil }
func (e *lspNavExtension) Dispose() error                                   { return nil }

func (e *lspNavExtension) Tools() []*agentcore.Tool {
	return []*agentcore.Tool{e.navTool()}
}

func (e *lspNavExtension) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(e.workDir, path)
}

func (e *lspNavExtension) navTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "code_navigate",
		Description: strings.Join([]string{
			"Precise code navigation and diagnostics via the language server (LSP). Prefer this over grep for resolving symbols.",
			"",
			"Actions:",
			"- definition: jump to where the symbol under the cursor is defined (requires line)",
			"- references: list every place the symbol is used (requires line)",
			"- hover: show the type/signature/documentation for the symbol (requires line)",
			"- diagnostics: list compiler/linter errors and warnings for the whole file (line/column ignored)",
			"",
			"For definition/references/hover provide the 1-based line and column of the symbol (as shown in grep/editor output).",
			"For diagnostics only the file path is needed.",
			"If the language has no configured/installed LSP server, this returns a notice — fall back to grep.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "definition, references, hover, or diagnostics",
					"enum":        []string{"definition", "references", "hover", "diagnostics"},
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File path (absolute or relative to the working directory)",
				},
				"line": map[string]any{
					"type":        "integer",
					"description": "1-based line number of the symbol (required for definition/references/hover; ignored for diagnostics)",
				},
				"column": map[string]any{
					"type":        "integer",
					"description": "1-based column of the symbol (default 1; ignored for diagnostics)",
				},
			},
			"required": []string{"action", "path"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Action string `json:"action"`
				Path   string `json:"path"`
				Line   int    `json:"line"`
				Column int    `json:"column"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if p.Path == "" {
				return nil, fmt.Errorf("path is required")
			}
			if p.Action != "diagnostics" && p.Line < 1 {
				return nil, fmt.Errorf("a 1-based line is required for %s", p.Action)
			}
			if e.mgr == nil || !e.mgr.IsActive() {
				return "LSP is not active; use grep for code search.", nil
			}
			if p.Column < 1 {
				p.Column = 1
			}
			file := e.resolvePath(p.Path)
			line0 := p.Line - 1
			col0 := p.Column - 1

			switch p.Action {
			case "definition":
				locs, err := e.mgr.Definition(file, line0, col0)
				if err != nil {
					return nil, fmt.Errorf("definition: %w", err)
				}
				return e.formatLocations("definition", locs), nil
			case "references":
				locs, err := e.mgr.References(file, line0, col0)
				if err != nil {
					return nil, fmt.Errorf("references: %w", err)
				}
				return e.formatLocations("references", locs), nil
			case "hover":
				text, err := e.mgr.Hover(file, line0, col0)
				if err != nil {
					return nil, fmt.Errorf("hover: %w", err)
				}
				if strings.TrimSpace(text) == "" {
					return "no hover information (symbol not found or LSP unavailable for this file)", nil
				}
				return text, nil
			case "diagnostics":
				// Report ERROR + WARN for the whole file. LSP may need to open and
				// analyze the file, so this can take up to diagnosticsDocumentWait.
				report := e.mgr.ReportForFile(file, []int{1, 2})
				if strings.TrimSpace(report) == "" {
					return fmt.Sprintf("no diagnostics for %s", p.Path), nil
				}
				return report, nil
			default:
				return nil, fmt.Errorf("unknown action %q", p.Action)
			}
		},
	}
}

func (e *lspNavExtension) formatLocations(kind string, locs []lsp.Location) string {
	if len(locs) == 0 {
		return fmt.Sprintf("no %s found (symbol not resolved, or no LSP server for this file — try grep)", kind)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d %s:\n", len(locs), kind))
	for _, l := range locs {
		rel := l.Path
		if r, err := filepath.Rel(e.workDir, l.Path); err == nil && !strings.HasPrefix(r, "..") {
			rel = r
		}
		// LSP positions are 0-based; present 1-based to match editors/grep.
		b.WriteString(fmt.Sprintf("  %s:%d:%d\n", rel, l.Range.Start.Line+1, l.Range.Start.Character+1))
	}
	return strings.TrimRight(b.String(), "\n")
}
