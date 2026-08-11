package panels

import (
	"testing"
	"time"

	covosession "github.com/covoyage/covonaut/session"
)

func TestBuildSessionTreeParentChildOrphanAndDepth(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	infos := []covosession.Info{
		{ID: "root", Name: "Root", UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: "child-new", Name: "Child New", ParentSession: "root", UpdatedAt: now.Add(-10 * time.Minute)},
		{ID: "child-old", Name: "Child Old", ParentSession: "root", UpdatedAt: now.Add(-30 * time.Minute)},
		{ID: "grand", Name: "Grand", ParentSession: "child-new", UpdatedAt: now.Add(-5 * time.Minute)},
		{ID: "orphan", Name: "Orphan", ParentSession: "missing-parent", UpdatedAt: now.Add(-1 * time.Minute)},
	}

	roots := buildSessionTree(infos)
	if len(roots) != 2 {
		t.Fatalf("root count = %d, want 2", len(roots))
	}
	if roots[0].Info.ID != "orphan" || roots[1].Info.ID != "root" {
		t.Fatalf("root order = [%s, %s], want [orphan, root]", roots[0].Info.ID, roots[1].Info.ID)
	}

	root := roots[1]
	if root.Depth != 0 {
		t.Fatalf("root depth = %d, want 0", root.Depth)
	}
	if len(root.Children) != 2 {
		t.Fatalf("root child count = %d, want 2", len(root.Children))
	}

	if root.Children[0].Info.ID != "child-new" || root.Children[1].Info.ID != "child-old" {
		t.Fatalf("child order = [%s, %s], want [child-new, child-old]", root.Children[0].Info.ID, root.Children[1].Info.ID)
	}
	if root.Children[0].Parent != root {
		t.Fatal("child-new parent pointer is not set to root")
	}
	if root.Children[0].Depth != 1 {
		t.Fatalf("child-new depth = %d, want 1", root.Children[0].Depth)
	}
	if len(root.Children[0].Children) != 1 {
		t.Fatalf("child-new grandchild count = %d, want 1", len(root.Children[0].Children))
	}
	if root.Children[0].Children[0].Info.ID != "grand" {
		t.Fatalf("grandchild id = %s, want grand", root.Children[0].Children[0].Info.ID)
	}
	if root.Children[0].Children[0].Depth != 2 {
		t.Fatalf("grandchild depth = %d, want 2", root.Children[0].Children[0].Depth)
	}
}

func TestVisibleRangeBoundaries(t *testing.T) {
	start, end := VisibleRange(0, 5, 3)
	if start != 0 || end != 3 {
		t.Fatalf("visibleRange(0,5,3) = (%d,%d), want (0,3)", start, end)
	}

	start, end = VisibleRange(0, 5, 20)
	if start != 0 || end != 5 {
		t.Fatalf("visibleRange(0,5,20) = (%d,%d), want (0,5)", start, end)
	}

	start, end = VisibleRange(10, 5, 20)
	if start != 8 || end != 13 {
		t.Fatalf("visibleRange(10,5,20) = (%d,%d), want (8,13)", start, end)
	}

	start, end = VisibleRange(19, 5, 20)
	if start != 15 || end != 20 {
		t.Fatalf("visibleRange(19,5,20) = (%d,%d), want (15,20)", start, end)
	}
}

func TestSessionTreeApplyFilterNameIDLabelSummary(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	tree := NewSessionTree()
	tree.SetItems([]covosession.Info{
		{ID: "id-alpha-001", Name: "Alpha Session", Label: "Feature", Summary: "Parser refactor", UpdatedAt: now.Add(-1 * time.Hour)},
		{ID: "id-beta-002", Name: "Beta Session", Label: "Ops", Summary: "Cleanup scripts", UpdatedAt: now.Add(-2 * time.Hour)},
	})

	setFilter := func(filter string) []string {
		tree.mu.Lock()
		tree.filter = filter
		tree.applyFilter()
		nodes := make([]*sessionTreeNode, len(tree.flat))
		copy(nodes, tree.flat)
		tree.mu.Unlock()
		ids := make([]string, 0, len(nodes))
		for _, n := range nodes {
			ids = append(ids, n.Info.ID)
		}
		return ids
	}

	assertIDs := func(got []string, want []string, label string) {
		if len(got) != len(want) {
			t.Fatalf("%s: count = %d, want %d (got=%v)", label, len(got), len(want), got)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("%s: ids = %v, want %v", label, got, want)
			}
		}
	}

	assertIDs(setFilter("alpha"), []string{"id-alpha-001"}, "name filter")
	assertIDs(setFilter("BETA-002"), []string{"id-beta-002"}, "id filter")
	assertIDs(setFilter("fea"), []string{"id-alpha-001"}, "label filter")
	assertIDs(setFilter("cleanup"), []string{"id-beta-002"}, "summary filter")
	assertIDs(setFilter(""), []string{"id-alpha-001", "id-beta-002"}, "clear filter")
}
