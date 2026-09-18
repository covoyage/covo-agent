package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeOpenURL(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr string
	}{
		{in: "https://example.com/a", want: "https://example.com/a"},
		{in: "example.com", want: "https://example.com"},
		{in: "www.baidu.com", want: "https://www.baidu.com"},
		{in: "localhost:3000", want: "http://localhost:3000"},
		{in: "127.0.0.1:8080/docs", want: "http://127.0.0.1:8080/docs"},
		{in: "  https://news.ycombinator.com  ", want: "https://news.ycombinator.com"},
		{in: "file:///etc/passwd", wantErr: "only http and https"},
		{in: "javascript:alert(1)", wantErr: "only http and https"},
		{in: "mailto:user@example.com", wantErr: "only http and https"},
		{in: "", wantErr: "url is required"},
	}
	for _, tt := range tests {
		got, err := normalizeOpenURL(tt.in)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("normalizeOpenURL(%q) err=%v, want %q", tt.in, err, tt.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeOpenURL(%q) unexpected err: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("normalizeOpenURL(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOpenURLToolOpensNormalizedURL(t *testing.T) {
	orig := openSystemURL
	t.Cleanup(func() { openSystemURL = orig })

	var opened string
	openSystemURL = func(target string) error {
		opened = target
		return nil
	}

	tool := BuildOpenURLTool()
	raw, _ := json.Marshal(map[string]any{"url": "baidu.com"})
	got, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatalf("open_url: %v", err)
	}
	if opened != "https://baidu.com" {
		t.Fatalf("opened %q, want https://baidu.com", opened)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("result type %T", got)
	}
	if m["opened"] != true || m["browser"] != "system default" {
		t.Fatalf("unexpected result: %#v", m)
	}
}

func TestOpenURLToolRejectsFileScheme(t *testing.T) {
	orig := openSystemURL
	t.Cleanup(func() { openSystemURL = orig })
	openSystemURL = func(string) error {
		t.Fatal("must not launch browser for file URLs")
		return nil
	}

	tool := BuildOpenURLTool()
	raw, _ := json.Marshal(map[string]any{"url": "file:///tmp/x.html"})
	if _, err := tool.Func(context.Background(), raw); err == nil {
		t.Fatal("expected error for file URL")
	}
}
