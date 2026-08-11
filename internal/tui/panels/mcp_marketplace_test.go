package panels

import (
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covonaut/tui/core"
)

func TestMCPMarketplacePanelSortsAlphabeticallyByName(t *testing.T) {
	panel := NewMCPMarketplacePanel([]MCPEntry{
		{Name: "zeta", DisplayName: "Zeta", Description: "z", Category: "tools"},
		{Name: "Alpha", DisplayName: "Alpha", Description: "a", Category: "tools"},
		{Name: "beta", DisplayName: "Beta", Description: "b", Category: "tools"},
	}, []string{"tools"}, nil)

	panel.mu.RLock()
	defer panel.mu.RUnlock()
	if len(panel.filtered) != 3 {
		t.Fatalf("filtered count = %d, want 3", len(panel.filtered))
	}
	got := []string{panel.filtered[0].Name, panel.filtered[1].Name, panel.filtered[2].Name}
	want := []string{"Alpha", "beta", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted order = %v, want %v", got, want)
		}
	}
}

func TestMCPMarketplacePanelTextFilterMatchesExpectedFields(t *testing.T) {
	entries := []MCPEntry{
		{Name: "fs", DisplayName: "Filesystem", Description: "edit files", Category: "dev"},
		{Name: "browser", DisplayName: "Web Browser", Description: "open pages", Category: "web"},
	}
	panel := NewMCPMarketplacePanel(entries, []string{"dev", "web"}, nil)

	setFilter := func(filter string) []string {
		panel.mu.Lock()
		panel.filter = filter
		panel.mu.Unlock()
		panel.refresh()

		panel.mu.RLock()
		names := make([]string, len(panel.filtered))
		for i, e := range panel.filtered {
			names[i] = e.Name
		}
		panel.mu.RUnlock()
		return names
	}

	assertNames := func(label string, got, want []string) {
		if len(got) != len(want) {
			t.Fatalf("%s: count = %d, want %d (got=%v)", label, len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: names = %v, want %v", label, got, want)
			}
		}
	}

	assertNames("name", setFilter("brow"), []string{"browser"})
	assertNames("display", setFilter("filesys"), []string{"fs"})
	assertNames("description", setFilter("open pages"), []string{"browser"})
	assertNames("category", setFilter("DEV"), []string{"fs"})
	assertNames("clear", setFilter(""), []string{"browser", "fs"})
}

func TestMCPMarketplacePanelCategoryCycleAndFilter(t *testing.T) {
	panel := NewMCPMarketplacePanel([]MCPEntry{
		{Name: "dev-one", DisplayName: "Dev One", Category: "dev"},
		{Name: "web-one", DisplayName: "Web One", Category: "web"},
		{Name: "web-two", DisplayName: "Web Two", Category: "web"},
	}, []string{"dev", "web"}, nil)

	count := func() int {
		panel.mu.RLock()
		defer panel.mu.RUnlock()
		return len(panel.filtered)
	}

	if count() != 3 {
		t.Fatalf("initial filtered count = %d, want 3", count())
	}

	panel.cycleCategory()
	if count() != 1 {
		t.Fatalf("after first cycle count = %d, want 1", count())
	}

	panel.cycleCategory()
	if count() != 2 {
		t.Fatalf("after second cycle count = %d, want 2", count())
	}

	panel.cycleCategory()
	if count() != 3 {
		t.Fatalf("after third cycle count = %d, want 3", count())
	}
}

func TestMCPMarketplacePanelRenderShowsInstalledStatus(t *testing.T) {
	panel := NewMCPMarketplacePanel([]MCPEntry{
		{Name: "alpha", DisplayName: "Alpha", Category: "dev"},
		{Name: "beta", DisplayName: "Beta", Category: "dev"},
	}, []string{"dev"}, map[string]bool{"alpha": true})

	lines := panel.Render(120)
	contains := func(target string) bool {
		for _, line := range lines {
			if strings.Contains(line, target) {
				return true
			}
		}
		return false
	}

	if !contains("✓") {
		t.Fatal("render output missing installed marker ✓")
	}
	if !contains("○") {
		t.Fatal("render output missing uninstalled marker ○")
	}
}

func TestMCPMarketplacePanelInstallCallbackSelectedEntry(t *testing.T) {
	panel := NewMCPMarketplacePanel([]MCPEntry{
		{Name: "alpha", DisplayName: "Alpha", Category: "dev"},
		{Name: "beta", DisplayName: "Beta", Category: "dev"},
	}, []string{"dev"}, nil)

	panel.moveSelected(1)

	gotCh := make(chan MCPEntry, 1)
	panel.SetOnInstall(func(entry MCPEntry) {
		gotCh <- entry
	})

	panel.Update(core.KeyMsg{Data: "\r"})

	select {
	case got := <-gotCh:
		if got.Name != "beta" {
			t.Fatalf("installed entry = %s, want beta", got.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for install callback")
	}
}
