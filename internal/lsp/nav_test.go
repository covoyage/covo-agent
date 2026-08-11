package lsp

import (
	"encoding/json"
	"testing"
)

func TestParseLocations(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantCount int
		wantFirst string // expected path of first location
	}{
		{"null", `null`, 0, ""},
		{"empty array", `[]`, 0, ""},
		{
			name:      "single Location",
			raw:       `{"uri":"file:///a/b.go","range":{"start":{"line":2,"character":4},"end":{"line":2,"character":9}}}`,
			wantCount: 1,
			wantFirst: "/a/b.go",
		},
		{
			name:      "array of Location",
			raw:       `[{"uri":"file:///a/b.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}},{"uri":"file:///c/d.go","range":{"start":{"line":1,"character":1},"end":{"line":1,"character":2}}}]`,
			wantCount: 2,
			wantFirst: "/a/b.go",
		},
		{
			name:      "LocationLink with targetSelectionRange",
			raw:       `[{"targetUri":"file:///x/y.go","targetRange":{"start":{"line":5,"character":0},"end":{"line":9,"character":0}},"targetSelectionRange":{"start":{"line":5,"character":6},"end":{"line":5,"character":12}}}]`,
			wantCount: 1,
			wantFirst: "/x/y.go",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locs := parseLocations(json.RawMessage(tt.raw))
			if len(locs) != tt.wantCount {
				t.Fatalf("got %d locations, want %d", len(locs), tt.wantCount)
			}
			if tt.wantCount > 0 && locs[0].Path != tt.wantFirst {
				t.Errorf("first path = %q, want %q", locs[0].Path, tt.wantFirst)
			}
		})
	}
}

func TestParseLocationsLinkPrefersSelectionRange(t *testing.T) {
	raw := `[{"targetUri":"file:///x/y.go","targetRange":{"start":{"line":5,"character":0},"end":{"line":9,"character":0}},"targetSelectionRange":{"start":{"line":5,"character":6},"end":{"line":5,"character":12}}}]`
	locs := parseLocations(json.RawMessage(raw))
	if len(locs) != 1 {
		t.Fatal("expected 1 location")
	}
	if locs[0].Range.Start.Line != 5 || locs[0].Range.Start.Character != 6 {
		t.Errorf("expected selection range start 5:6, got %d:%d",
			locs[0].Range.Start.Line, locs[0].Range.Start.Character)
	}
}

func TestParseHover(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"null", `null`, ""},
		{"markup content", `{"contents":{"kind":"markdown","value":"func Foo() int"}}`, "func Foo() int"},
		{"plain string", `{"contents":"just text"}`, "just text"},
		{"marked string object", `{"contents":{"language":"go","value":"type Bar struct{}"}}`, "type Bar struct{}"},
		{"array of marked strings", `{"contents":["line one","line two"]}`, "line one\nline two"},
		{"empty contents", `{"contents":""}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseHover(json.RawMessage(tt.raw))
			if got != tt.want {
				t.Errorf("parseHover = %q, want %q", got, tt.want)
			}
		})
	}
}
