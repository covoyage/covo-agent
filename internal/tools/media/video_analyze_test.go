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

func TestVideoAnalyzeLocalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, []byte("fake-mp4"), 0644); err != nil {
		t.Fatal(err)
	}
	restore := stubVideoDeps(t, []extractedStill{
		{seconds: 0.5, png: tinyPNG()},
		{seconds: 1.5, png: tinyPNG()},
	})
	defer restore()

	var prompts []string
	tool := BuildVideoAnalyzeTool(dir, func(ctx context.Context, prompt, mediaType, dataURL string) (string, error) {
		prompts = append(prompts, prompt)
		if mediaType != "image/png" || !strings.HasPrefix(dataURL, "data:image/png;base64,") {
			t.Fatalf("analyzer args type=%q url=%q", mediaType, dataURL)
		}
		return "a person waving", nil
	})
	raw, _ := json.Marshal(map[string]any{"path": "clip.mp4", "prompt": "who is on screen?", "frames": 2})
	got, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["truncated"] != false {
		t.Fatalf("got %+v", m)
	}
	desc, _ := m["description"].(string)
	if !strings.Contains(desc, "Frame 1") || !strings.Contains(desc, "a person waving") {
		t.Fatalf("description=%q", desc)
	}
	sampled, _ := m["sampled"].([]videoFrame)
	if len(sampled) != 2 {
		t.Fatalf("sampled=%v", sampled)
	}
	if len(prompts) != 2 || !strings.Contains(prompts[0], "who is on screen?") {
		t.Fatalf("prompts=%v", prompts)
	}
}

func TestVideoAnalyzeRejectsRemoteURLWithoutTransport(t *testing.T) {
	restore := stubVideoDeps(t, nil)
	defer restore()
	tool := BuildVideoAnalyzeTool(t.TempDir(), func(context.Context, string, string, string) (string, error) {
		t.Fatal("should not analyze")
		return "", nil
	})
	_, err := tool.Func(context.Background(), json.RawMessage(`{"path":"https://example.com/a.mp4"}`))
	if err == nil {
		t.Fatal("expected remote url error")
	}
}

func TestVideoAnalyzeRejectsOutsideWorkspace(t *testing.T) {
	restore := stubVideoDeps(t, nil)
	defer restore()
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.mp4")
	if err := os.WriteFile(outside, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := BuildVideoAnalyzeTool(ws, func(context.Context, string, string, string) (string, error) {
		return "nope", nil
	})
	raw, _ := json.Marshal(map[string]any{"path": outside})
	_, err := tool.Func(context.Background(), raw)
	if err == nil {
		t.Fatal("expected outside workspace error")
	}
}

func TestVideoAnalyzeRequiresFFmpeg(t *testing.T) {
	orig := lookPath
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { lookPath = orig })

	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := BuildVideoAnalyzeTool(dir, func(context.Context, string, string, string) (string, error) {
		return "ok", nil
	})
	_, err := tool.Func(context.Background(), json.RawMessage(`{"path":"clip.mp4"}`))
	if err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("err=%v", err)
	}
}

func TestVideoAnalyzeFetchesRemoteWithTransport(t *testing.T) {
	restore := stubVideoDeps(t, []extractedStill{{seconds: 1.0, png: tinyPNG()}})
	defer restore()
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"video/mp4"}},
			Body:       io.NopCloser(strings.NewReader("remote-mp4-bytes")),
			Request:    r,
		}, nil
	})
	tool := BuildVideoAnalyzeToolWithTransport(t.TempDir(), func(context.Context, string, string, string) (string, error) {
		return "remote scene", nil
	}, rt)
	raw, _ := json.Marshal(map[string]any{"path": "https://example.com/a.mp4", "frames": 1})
	got, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["description"] == "" {
		t.Fatalf("got %+v", got)
	}
}

func TestVideoAnalyzeRejectsBadFrameCount(t *testing.T) {
	restore := stubVideoDeps(t, nil)
	defer restore()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := BuildVideoAnalyzeTool(dir, func(context.Context, string, string, string) (string, error) {
		return "ok", nil
	})
	_, err := tool.Func(context.Background(), json.RawMessage(`{"path":"clip.mp4","frames":9}`))
	if err == nil || !strings.Contains(err.Error(), "frames") {
		t.Fatalf("err=%v", err)
	}
}

func TestVideoAnalyzeRejectsUnsupportedType(t *testing.T) {
	restore := stubVideoDeps(t, nil)
	defer restore()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := BuildVideoAnalyzeTool(dir, func(context.Context, string, string, string) (string, error) {
		return "ok", nil
	})
	_, err := tool.Func(context.Background(), json.RawMessage(`{"path":"notes.txt"}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("err=%v", err)
	}
}

func TestSampleTimestampsEvenlySpaced(t *testing.T) {
	got := sampleTimestamps(0, 10, 5)
	if len(got) != 5 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0] != 1 || got[4] != 9 {
		t.Fatalf("got=%v", got)
	}
}

func TestSampleWindowClampsToDuration(t *testing.T) {
	start, end := sampleWindow(10, 0, 0)
	if start != 0 || end != 10 {
		t.Fatalf("start=%v end=%v", start, end)
	}
	start, end = sampleWindow(10, 2, 8)
	if start != 2 || end != 8 {
		t.Fatalf("start=%v end=%v", start, end)
	}
}

func TestParseFFmpegDuration(t *testing.T) {
	d, ok := parseFFmpegDuration("  Duration: 00:01:02.50, start: 0.000000, bitrate: 128 kb/s")
	if !ok || d != 62.5 {
		t.Fatalf("d=%v ok=%v", d, ok)
	}
}

func stubVideoDeps(t *testing.T, frames []extractedStill) func() {
	t.Helper()
	origLook := lookPath
	origExtract := extractVideoFrames
	lookPath = func(name string) (string, error) {
		if name == "ffmpeg" || name == "ffprobe" {
			return "/usr/bin/" + name, nil
		}
		return origLook(name)
	}
	extractVideoFrames = func(ctx context.Context, videoPath string, n int, start, end float64) ([]extractedStill, error) {
		_ = ctx
		_ = videoPath
		_ = start
		_ = end
		if frames == nil {
			return nil, nil
		}
		if n < len(frames) {
			return frames[:n], nil
		}
		return frames, nil
	}
	return func() {
		lookPath = origLook
		extractVideoFrames = origExtract
	}
}

func tinyPNG() []byte {
	return []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 24))
}
