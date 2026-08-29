package model

import (
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"os"
	"testing"
)

func TestFilterProviderModelsSearchesIDNameAndDescription(t *testing.T) {
	models := []cli.ProviderModel{
		{ID: "openai/gpt-5.6", Name: "GPT-5.6", Description: "Current OpenAI model"},
		{ID: "anthropic/claude-sonnet-4", Name: "Claude Sonnet 4", Description: "Strong coding model"},
		{ID: "google/gemini-2.5-flash", Name: "Gemini Flash", Description: "Low latency"},
	}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "id", query: "claude", want: []string{"anthropic/claude-sonnet-4"}},
		{name: "name", query: "flash", want: []string{"google/gemini-2.5-flash"}},
		{name: "description", query: "coding", want: []string{"anthropic/claude-sonnet-4"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterProviderModels(models, tt.query, 10)
			if len(got) != len(tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("expected %v, got %v", tt.want, got)
				}
			}
		})
	}
}

func TestFilterProviderModelsLimit(t *testing.T) {
	models := []cli.ProviderModel{
		{ID: "a/model"},
		{ID: "b/model"},
		{ID: "c/model"},
	}

	got := filterProviderModels(models, "", 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 models, got %d: %v", len(got), got)
	}
}

func TestDecodeEscapeSeq(t *testing.T) {
	cases := []struct {
		seq      []byte
		key      string
		consumed int
	}{
		{[]byte{'[', 'D'}, "left", 2},
		{[]byte{'[', 'C'}, "right", 2},
		{[]byte{'[', 'A'}, "up", 2},
		{[]byte{'[', 'B'}, "down", 2},
		{[]byte{'[', 'H'}, "home", 2},
		{[]byte{'[', 'F'}, "end", 2},
		{[]byte{'[', '1', '~'}, "home", 3},
		{[]byte{'[', '4', '~'}, "end", 3},
		{[]byte{'[', '7', '~'}, "home", 3},
		{[]byte{'[', '8', '~'}, "end", 3},
		{[]byte{'[', '2', '~'}, "", 3},
		{[]byte{'[', '1', ';', '5', 'D'}, "", 5},
	}
	for _, c := range cases {
		key, consumed := decodeEscapeSeq(c.seq, 0, len(c.seq))
		if key != c.key || consumed != c.consumed {
			t.Fatalf("decodeEscapeSeq(%q) = (%q, %d), want (%q, %d)", c.seq, key, consumed, c.key, c.consumed)
		}
	}

	if key, consumed := decodeEscapeSeq([]byte{'[', 'D'}, 0, 1); consumed != 0 {
		t.Fatalf("incomplete sequence should report consumed=0, got (%q, %d)", key, consumed)
	}
	if key, consumed := decodeEscapeSeq([]byte{'X'}, 0, 1); key != "" || consumed != 1 {
		t.Fatalf("non-CSI escape should be unrecognized, got (%q, %d)", key, consumed)
	}
}

func runPromptLineRaw(t *testing.T, input string) (string, bool) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	type result struct {
		line      string
		cancelled bool
	}
	done := make(chan result, 1)
	go func() {
		line, cancelled := promptLineRaw(-1, nil, "Name", nil)
		done <- result{line, cancelled}
	}()

	w.Write([]byte(input))
	w.Close()
	return func() (string, bool) {
		got := <-done
		return got.line, got.cancelled
	}()
}

func TestPromptLineRawCaretEditing(t *testing.T) {
	line, cancelled := runPromptLineRaw(t, "abc\x1b[DX\x01Y\x05\r")
	if cancelled || line != "YabXc" {
		t.Fatalf("expected (YabXc, false), got (%q, %v)", line, cancelled)
	}
}

func TestPromptLineRawBackspaceAtCaret(t *testing.T) {
	line, cancelled := runPromptLineRaw(t, "ab\x1b[D\x7f\r")
	if cancelled || line != "b" {
		t.Fatalf("expected (b, false), got (%q, %v)", line, cancelled)
	}
}

func TestPromptLineRawHomeEnd(t *testing.T) {
	line, cancelled := runPromptLineRaw(t, "ab\x1b[1~\x1b[4~X\r")
	if cancelled || line != "abX" {
		t.Fatalf("expected (abX, false), got (%q, %v)", line, cancelled)
	}
}

func TestPromptLineRawEscCancels(t *testing.T) {
	line, cancelled := runPromptLineRaw(t, "a\x1b")
	if !cancelled || line != "" {
		t.Fatalf("expected cancel, got (%q, %v)", line, cancelled)
	}
}

func TestOverlayEndCursor(t *testing.T) {
	if got := shared.OverlayEndCursor(""); got != "▎" {
		t.Fatalf("empty should render a single block, got %q", got)
	}
	if got := shared.OverlayEndCursor("abc"); got != "ab\x1b[7mc\x1b[0m" {
		t.Fatalf("last char should be reversed, got %q", got)
	}
}
