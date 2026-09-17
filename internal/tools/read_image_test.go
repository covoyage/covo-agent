package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toolsmedia "github.com/covoyage/covo-agent/internal/tools/media"
	"github.com/covoyage/covonaut/agentcore"
)

// pngBytes is a minimal valid PNG header payload for MIME detection.
var pngBytes = []byte{
	0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 13, 'I', 'H', 'D', 'R',
}

func writeTestImage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dot.png")
	if err := os.WriteFile(path, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func callReadImage(t *testing.T, provider string, analyze toolsmedia.ImageAnalyzer, path string) (any, error) {
	t.Helper()
	tool := BuildReadImageTool(provider, analyze)
	args, _ := json.Marshal(map[string]string{"path": path})
	return tool.Func(context.Background(), args)
}

func TestReadImageDirectInjection(t *testing.T) {
	path := writeTestImage(t)
	res, err := callReadImage(t, "anthropic", nil, path)
	if err != nil {
		t.Fatalf("read_image: %v", err)
	}
	carrier, ok := res.(agentcore.BlockCarrier)
	if !ok {
		t.Fatalf("expected BlockCarrier, got %T", res)
	}
	blocks := carrier.ToolBlocks()
	if len(blocks) != 1 || blocks[0].Kind != agentcore.BlockKindImage {
		t.Fatalf("expected one image block, got %+v", blocks)
	}
	if !strings.HasPrefix(blocks[0].URL, "data:image/png;base64,") {
		t.Fatalf("expected png data URL prefix, got %q", blocks[0].URL)
	}
	if !strings.Contains(carrier.ToolText(), "attached") {
		t.Fatalf("text form unexpected: %q", carrier.ToolText())
	}
}

func TestReadImageFallbackWithoutAnalyzer(t *testing.T) {
	path := writeTestImage(t)
	res, err := callReadImage(t, "unknown-provider", nil, path)
	if err != nil {
		t.Fatalf("read_image: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map result on fallback path, got %T", res)
	}
	if m["loaded"] != true {
		t.Fatalf("loaded flag missing: %v", m)
	}
	if note, _ := m["note"].(string); !strings.Contains(note, "vision_analyze") {
		t.Fatalf("expected guidance note, got: %v", m["note"])
	}
}

func TestReadImageFallbackWithAnalyzer(t *testing.T) {
	path := writeTestImage(t)
	analyzer := func(ctx context.Context, prompt, mediaType, dataURL string) (string, error) {
		return "a tiny dot", nil
	}
	res, err := callReadImage(t, "ollama-local", analyzer, path)
	if err != nil {
		t.Fatalf("read_image: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map result on analyzer path, got %T", res)
	}
	if m["analysis"] != "a tiny dot" {
		t.Fatalf("analysis missing: %v", m)
	}
}

func TestReadImageRejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello world, definitely not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := callReadImage(t, "anthropic", nil, path); err == nil {
		t.Fatal("expected error for non-image file")
	}
}
