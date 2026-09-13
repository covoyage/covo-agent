package commands

import "testing"

// TestExtractSubagentIDFromDetail verifies the id extraction from a
// background spawn tool result body (pretty or compact JSON).
func TestExtractSubagentIDFromDetail(t *testing.T) {
	cases := map[string]string{
		`{"status":"started","id":"sub-3","task":"x"}`:              "sub-3",
		"{\n  \"status\": \"started\",\n  \"id\": \"sub-12\"\n}":    "sub-12",
		`{"status": "completed", "id": "agent-7", "output": "..."}`: "agent-7",
		`{"status":"completed","output":"no id here"}`:              "",
		"status=started id=sub-4":                                   "",
	}
	for detail, want := range cases {
		got := ""
		if m := subagentIDRe.FindStringSubmatch(detail); len(m) >= 2 {
			got = m[1]
		}
		if got != want {
			t.Errorf("detail %q: got %q, want %q", detail, got, want)
		}
	}
}
