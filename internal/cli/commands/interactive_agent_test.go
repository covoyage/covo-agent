package commands

import "testing"

func TestSideQuestionToolAllowed(t *testing.T) {
	for _, name := range []string{"read", "grep", "glob", "web_search", "web_fetch"} {
		if !sideQuestionToolAllowed(name) {
			t.Fatalf("%s should be allowed", name)
		}
	}
	for _, name := range []string{"bash", "write_file", "edit", "delete_file"} {
		if sideQuestionToolAllowed(name) {
			t.Fatalf("%s should be denied", name)
		}
	}
}
