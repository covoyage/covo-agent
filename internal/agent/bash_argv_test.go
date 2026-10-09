package agent

import "testing"

func TestClassifyGitArgv(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"status"}, true},
		{[]string{"status", "-sb"}, true},
		{[]string{"-C", "/tmp/repo", "log", "-1"}, true},
		{[]string{"--git-dir=.git", "diff"}, true},
		{[]string{"add", "."}, false},
		{[]string{"commit", "-m", "x"}, false},
		{[]string{"push", "origin", "main"}, false},
		{[]string{"push", "--force"}, false},
		{[]string{"stash", "list"}, true},
		{[]string{"stash", "pop"}, false},
		{[]string{"branch"}, true},
		{[]string{"branch", "-d", "old"}, false},
		{[]string{"tag"}, true},
		{[]string{"tag", "-a", "v1"}, false},
		{[]string{"remote", "-v"}, true},
		{[]string{"remote", "add", "origin", "url"}, false},
		{[]string{"config", "--get", "user.name"}, true},
		{[]string{"config", "user.name", "x"}, false},
		{[]string{"unknown"}, false},
	}
	for _, tt := range tests {
		if got := classifyGit(tt.args); got != tt.want {
			t.Errorf("classifyGit(%v) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestClassifyGHArgv(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"pr", "view", "1"}, true},
		{[]string{"pr", "list"}, true},
		{[]string{"pr", "create"}, false},
		{[]string{"issue", "list"}, true},
		{[]string{"issue", "create"}, false},
		{[]string{"auth", "login"}, false},
		{[]string{"auth", "status"}, true},
		{[]string{"workflow", "run"}, false},
	}
	for _, tt := range tests {
		if got := classifyGH(tt.args); got != tt.want {
			t.Errorf("classifyGH(%v) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestClassifyShellCommandGitArgv(t *testing.T) {
	if ok, _ := ClassifyShellCommand("git status -sb"); !ok {
		t.Fatal("git status should be read-only")
	}
	if ok, _ := ClassifyShellCommand("git add ."); ok {
		t.Fatal("git add should be mutating")
	}
	if ok, _ := ClassifyShellCommand("git push --force origin main"); ok {
		t.Fatal("git push --force should be mutating")
	}
	if ok, _ := ClassifyShellCommand("gh pr view 12"); !ok {
		t.Fatal("gh pr view should be read-only")
	}
	if ok, _ := ClassifyShellCommand("gh pr create"); ok {
		t.Fatal("gh pr create should be mutating")
	}
	if ok, _ := ClassifyShellCommand("ls -la"); !ok {
		t.Fatal("ls should remain read-only")
	}
	if ok, _ := ClassifyShellCommand("git status && git add ."); ok {
		t.Fatal("compound with mutating git should not be read-only")
	}
}
