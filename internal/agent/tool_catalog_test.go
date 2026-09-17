package agent

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/logutil"
)

// updateToolCatalog regenerates docs/tool-catalog.md from the live registry
// when true: go test ./internal/agent -run TestToolCatalog -update-tool-catalog
var updateToolCatalog = flag.Bool("update-tool-catalog", false, "regenerate docs/tool-catalog.md from the registered tool set")

// catalogNameRe extracts tool names from catalog table rows like "| `read` | ...".
var catalogNameRe = regexp.MustCompile(`(?m)^\| \` + "`" + `([a-z_0-9]+)` + "`")

// catalogRelPath is the documented tool catalog, relative to this package.
const catalogRelPath = "../../docs/tool-catalog.md"

// buildFullRegistryAgent constructs a throwaway agent with the full tool
// profile so tests can inspect the real registry contents.
func buildFullRegistryAgent(t *testing.T) *CovoAgent {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelError)}))
	ca, err := NewCovoAgent(CovoAgentConfig{
		Mode:         ModeGeneral,
		Provider:     &streamMockProvider{},
		ProviderName: "mock",
		Model:        "mock-model",
		WorkingDir:   t.TempDir(),
		HomeDir:      t.TempDir(),
		Logger:       logger,
		ToolProfile:  "full",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { ca.Close() })
	return ca
}

// registeredTools returns the sorted name+description pairs from the agent's
// live registry.
func registeredTools(ca *CovoAgent) []toolRow {
	names := ca.core.ToolNames()
	rows := make([]toolRow, 0, len(names))
	for _, name := range names {
		tool, ok := ca.core.GetTool(name)
		if !ok {
			continue
		}
		safe := "undeclared"
		if tool.ConcurrencySafe != nil {
			if *tool.ConcurrencySafe {
				safe = "safe"
			} else {
				safe = "exclusive"
			}
		}
		rows = append(rows, toolRow{Name: name, Summary: firstSentence(tool.Description), Safety: safe})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

type toolRow struct {
	Name    string
	Summary string
	Safety  string
}

// firstSentence trims a description to its first sentence for the catalog.
func firstSentence(desc string) string {
	desc = strings.ReplaceAll(desc, "\n", " ")
	if idx := strings.Index(desc, ". "); idx >= 0 {
		return desc[:idx+1]
	}
	if len(desc) > 120 {
		return desc[:117] + "..."
	}
	return desc
}

// TestToolCatalogCoversRegistry is the documentation CI gate: every tool in
// the live registry must have a row in docs/tool-catalog.md. A new tool
// without a catalog entry fails the build until documented (dsh-style
// "docs as tests"). Regenerate with -update-tool-catalog.
func TestToolCatalogCoversRegistry(t *testing.T) {
	ca := buildFullRegistryAgent(t)
	rows := registeredTools(ca)
	if len(rows) < 50 {
		t.Fatalf("registry suspiciously small (%d tools); construction path changed?", len(rows))
	}

	catalogPath := filepath.Join(catalogRelPath)
	if *updateToolCatalog {
		if err := writeToolCatalog(catalogPath, rows); err != nil {
			t.Fatalf("write catalog: %v", err)
		}
		t.Logf("regenerated %s with %d tools", catalogPath, len(rows))
		return
	}

	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read catalog: %v (regenerate with go test ./internal/agent -run TestToolCatalog -update-tool-catalog)", err)
	}
	documented := map[string]bool{}
	for _, m := range catalogNameRe.FindAllStringSubmatch(string(data), -1) {
		documented[m[1]] = true
	}

	var missing []string
	for _, row := range rows {
		if !documented[row.Name] {
			missing = append(missing, row.Name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d tools missing from docs/tool-catalog.md — document them (or regenerate with -update-tool-catalog):\n%s",
			len(missing), strings.Join(missing, "\n"))
	}
}

func writeToolCatalog(path string, rows []toolRow) error {
	var b strings.Builder
	b.WriteString("# Tool Catalog\n\n")
	b.WriteString("Auto-checked by TestToolCatalogCoversRegistry: every registered tool must have a row here.\n")
	b.WriteString("Regenerate with: go test ./internal/agent -run TestToolCatalog -update-tool-catalog\n\n")
	b.WriteString("| 工具 | 功能 | 并发声明 |\n")
	b.WriteString("|------|------|----------|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", row.Name, row.Summary, row.Safety)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
