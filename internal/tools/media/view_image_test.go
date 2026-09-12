package media

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViewImageLocalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(path, []byte("png-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	var gotPrompt, gotType, gotURL string
	tool := BuildViewImageTool(dir, func(ctx context.Context, prompt, mediaType, dataURL string) (string, error) {
		gotPrompt, gotType, gotURL = prompt, mediaType, dataURL
		return "a red button", nil
	})
	raw, _ := json.Marshal(map[string]any{"path": "shot.png", "prompt": "what color?"})
	got, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["description"] != "a red button" || m["media_type"] != "image/png" {
		t.Fatalf("got %+v", m)
	}
	if gotPrompt != "what color?" || gotType != "image/png" || !strings.HasPrefix(gotURL, "data:image/png;base64,") {
		t.Fatalf("analyzer args prompt=%q type=%q url=%q", gotPrompt, gotType, gotURL)
	}
}

func TestViewImageRejectsRemoteURL(t *testing.T) {
	tool := BuildViewImageTool(t.TempDir(), func(context.Context, string, string, string) (string, error) {
		t.Fatal("should not analyze")
		return "", nil
	})
	_, err := tool.Func(context.Background(), json.RawMessage(`{"path":"https://example.com/a.png"}`))
	if err == nil {
		t.Fatal("expected remote url error")
	}
}

func TestViewImageRejectsOutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := BuildViewImageTool(ws, func(context.Context, string, string, string) (string, error) {
		return "nope", nil
	})
	raw, _ := json.Marshal(map[string]any{"path": outside})
	_, err := tool.Func(context.Background(), raw)
	if err == nil {
		t.Fatal("expected outside workspace error")
	}
}

func TestViewImageAllowsComputerScreenshotDir(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(os.TempDir(), "covo-computer")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "frame-test.png")
	if err := os.WriteFile(path, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	tool := BuildViewImageTool(ws, func(context.Context, string, string, string) (string, error) {
		return "ok", nil
	})
	raw, _ := json.Marshal(map[string]any{"path": path})
	got, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["description"] != "ok" {
		t.Fatalf("got %+v", got)
	}
}

func TestViewImageMultiplePaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.png"), []byte("png-a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.png"), []byte("png-b"), 0644); err != nil {
		t.Fatal(err)
	}
	n := 0
	tool := BuildViewImageTool(dir, func(context.Context, string, string, string) (string, error) {
		n++
		return "ok", nil
	})
	raw, _ := json.Marshal(map[string]any{"paths": []string{"a.png", "b.png"}, "prompt": "diff them"})
	got, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["count"] != 2 || n != 2 {
		t.Fatalf("got %+v n=%d", m, n)
	}
	desc, _ := m["description"].(string)
	if !strings.Contains(desc, "Image 1") || !strings.Contains(desc, "Image 2") {
		t.Fatalf("description=%q", desc)
	}
}

func TestViewImageRequiresPath(t *testing.T) {
	tool := BuildViewImageTool(t.TempDir(), func(context.Context, string, string, string) (string, error) {
		return "ok", nil
	})
	_, err := tool.Func(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected missing path error")
	}
}

func TestViewImageFetchesRemoteWithTransport(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 24))
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"image/png"}},
			Body:       io.NopCloser(strings.NewReader(string(png))),
			Request:    r,
		}, nil
	})
	tool := BuildViewImageToolWithTransport(t.TempDir(), func(ctx context.Context, prompt, mediaType, dataURL string) (string, error) {
		if mediaType != "image/png" {
			t.Fatalf("mediaType=%s", mediaType)
		}
		return "remote ok", nil
	}, rt)
	raw, _ := json.Marshal(map[string]any{"path": "https://example.com/a.png"})
	got, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["description"] != "remote ok" {
		t.Fatalf("got %+v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
