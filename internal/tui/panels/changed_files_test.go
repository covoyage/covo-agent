package panels

import (
	"path/filepath"
	"testing"
)

func TestBuildFileTreeNestedPathsAndRootFile(t *testing.T) {
	entries := []FileChange{
		{Path: "src/main.go", Action: "modified", Tool: "edit"},
		{Path: "src/lib/helper.go", Action: "created", Tool: "write_file"},
		{Path: "README.md", Action: "modified", Tool: "edit"},
	}

	roots := buildFileTree(entries, "")
	if len(roots) != 2 {
		t.Fatalf("root count = %d, want 2", len(roots))
	}
	if roots[0].Path != "src" || !roots[0].IsDir {
		t.Fatalf("roots[0] = %s (dir=%v), want src dir", roots[0].Path, roots[0].IsDir)
	}
	if roots[1].Path != "README.md" || roots[1].IsDir {
		t.Fatalf("roots[1] = %s (dir=%v), want README.md file", roots[1].Path, roots[1].IsDir)
	}

	src := roots[0]
	if len(src.Children) != 2 {
		t.Fatalf("src child count = %d, want 2", len(src.Children))
	}
	if src.Children[0].Path != "src/lib" || !src.Children[0].IsDir {
		t.Fatalf("src first child = %s (dir=%v), want src/lib dir", src.Children[0].Path, src.Children[0].IsDir)
	}
	if src.Children[1].Path != "src/main.go" || src.Children[1].IsDir {
		t.Fatalf("src second child = %s (dir=%v), want src/main.go file", src.Children[1].Path, src.Children[1].IsDir)
	}
}

func TestBuildFileTreeRelativePathAgainstWorkingDir(t *testing.T) {
	workingDir := filepath.Clean("/repo")
	entries := []FileChange{
		{Path: filepath.Join(workingDir, "dir", "a.go"), Action: "modified", Tool: "edit"},
		{Path: filepath.Join(workingDir, "b.go"), Action: "created", Tool: "write_file"},
	}

	roots := buildFileTree(entries, workingDir)
	if len(roots) != 2 {
		t.Fatalf("root count = %d, want 2", len(roots))
	}
	if roots[0].Path != "dir" || !roots[0].IsDir {
		t.Fatalf("roots[0] = %s (dir=%v), want dir directory", roots[0].Path, roots[0].IsDir)
	}
	if roots[1].Path != "b.go" || roots[1].IsDir {
		t.Fatalf("roots[1] = %s (dir=%v), want b.go file", roots[1].Path, roots[1].IsDir)
	}
	if len(roots[0].Children) != 1 || roots[0].Children[0].Path != "dir/a.go" {
		t.Fatalf("dir children = %#v, want dir/a.go", roots[0].Children)
	}
}

func TestBuildFileTreeDirectoriesFirstAlphabeticalSort(t *testing.T) {
	entries := []FileChange{
		{Path: "zeta.txt", Action: "modified", Tool: "edit"},
		{Path: "beta/file.go", Action: "modified", Tool: "edit"},
		{Path: "alpha/file.go", Action: "modified", Tool: "edit"},
		{Path: "aardvark.txt", Action: "modified", Tool: "edit"},
	}

	roots := buildFileTree(entries, "")
	if len(roots) != 4 {
		t.Fatalf("root count = %d, want 4", len(roots))
	}
	got := []string{roots[0].Path, roots[1].Path, roots[2].Path, roots[3].Path}
	want := []string{"alpha", "beta", "aardvark.txt", "zeta.txt"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("root order = %v, want %v", got, want)
		}
	}
}

func TestBuildFileTreeNoDuplicateChild(t *testing.T) {
	entries := []FileChange{
		{Path: "src/a.go", Action: "modified", Tool: "edit"},
		{Path: "src/a.go", Action: "deleted", Tool: "delete_file"},
	}

	roots := buildFileTree(entries, "")
	if len(roots) != 1 || roots[0].Path != "src" {
		t.Fatalf("roots = %#v, want single src directory", roots)
	}
	if len(roots[0].Children) != 1 {
		t.Fatalf("src child count = %d, want 1", len(roots[0].Children))
	}
	if roots[0].Children[0].Path != "src/a.go" {
		t.Fatalf("child path = %s, want src/a.go", roots[0].Children[0].Path)
	}
}
