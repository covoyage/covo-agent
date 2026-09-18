package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseComputerUseArgsBindsCoordinates(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"action":   "click",
		"x":        12,
		"y":        34,
		"dx":       5,
		"dy":       6,
		"amount":   3,
		"app":      "Finder",
		"text":     "hi",
		"key":      "return",
		"value":    "ok",
		"frame_id": "f1",
		"ms":       250,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseComputerUseArgs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != "click" || got.X != 12 || got.Y != 34 || got.DX != 5 || got.DY != 6 || got.Amount != 3 {
		t.Fatalf("got %+v", got)
	}
	if got.App != "Finder" || got.Text != "hi" || got.Key != "return" || got.Value != "ok" {
		t.Fatalf("got %+v", got)
	}
	if got.FrameID != "f1" || got.MS != 250 {
		t.Fatalf("got %+v", got)
	}
}

func TestComputerUseClickRequiresScreenshot(t *testing.T) {
	computerFrames = &computerFrameStore{}
	tool := buildComputerUseTool()
	raw, _ := json.Marshal(map[string]any{"action": "click", "x": 1, "y": 2})
	_, err := tool.Func(context.Background(), raw)
	if err == nil || err.Error() != "screenshot first" {
		t.Fatalf("expected screenshot first, got %v", err)
	}
}

func TestComputerUseWaitRejectsOverMax(t *testing.T) {
	tool := buildComputerUseTool()
	raw, _ := json.Marshal(map[string]any{"action": "wait", "ms": 20000})
	_, err := tool.Func(context.Background(), raw)
	if err == nil {
		t.Fatal("expected wait max error")
	}
}

func TestComputerUseScreenshotThenClick(t *testing.T) {
	computerFrames = &computerFrameStore{}
	prev := captureScreenshotFn
	t.Cleanup(func() { captureScreenshotFn = prev })
	captureScreenshotFn = func(path string) (int, int, error) {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return 0, 0, err
		}
		if err := os.WriteFile(path, []byte("not-a-png"), 0644); err != nil {
			return 0, 0, err
		}
		return 100, 80, nil
	}
	tool := buildComputerUseTool()
	got, err := tool.Func(context.Background(), json.RawMessage(`{"action":"screenshot"}`))
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	id, _ := m["frame_id"].(string)
	if id == "" || m["width"] != 100 || m["height"] != 80 {
		t.Fatalf("screenshot result %+v", m)
	}
	// click still needs a real desktop command; we only assert the frame gate
	// accepts a fresh screenshot. Stale frames are covered separately.
	if _, err := computerFrames.require(id); err != nil {
		t.Fatal(err)
	}
}

func TestComputerUseFrameStale(t *testing.T) {
	computerFrames = &computerFrameStore{}
	computerFrames.set(computerFrame{ID: "f1", Captured: time.Now().Add(-time.Minute)})
	if _, err := computerFrames.require("f1"); err == nil {
		t.Fatal("expected stale frame")
	}
}

func TestComputerUseFocusAppRequiresName(t *testing.T) {
	tool := buildComputerUseTool()
	raw, _ := json.Marshal(map[string]any{"action": "focus_app"})
	_, err := tool.Func(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "app is required") {
		t.Fatalf("expected app required, got %v", err)
	}
}
