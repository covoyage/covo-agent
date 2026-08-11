package canvas

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

func BuildCanvasTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "canvas",
		Description: strings.Join([]string{
			"Render HTML, SVG, or Mermaid content in a browser for visual inspection.",
			"The content is written to a temp file and opened in the default browser.",
			"",
			"Supported modes:",
			"- html: Raw HTML content (default).",
			"- svg: SVG markup (wrapped in dark-themed HTML).",
			"- mermaid: Mermaid diagram code (wrapped with CDN renderer).",
			"- plantuml: PlantUML code (rendered via public server).",
			"",
			"Use this for: visualizing architecture diagrams, data charts, UI mockups,",
			"debugging HTML/CSS, or presenting visual information to the user.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{
					"type":        "string",
					"description": "The HTML, SVG, or diagram code to render.",
				},
				"mode": map[string]any{
					"type":        "string",
					"description": "Content type: 'html', 'svg', 'mermaid', or 'plantuml' (default: 'html').",
					"enum":        []string{"html", "svg", "mermaid", "plantuml"},
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Browser tab title (default: 'Canvas').",
				},
			},
			"required": []string{"content"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Content string `json:"content"`
				Mode    string `json:"mode"`
				Title   string `json:"title"`
			}
			json.Unmarshal(args, &params)
			if strings.TrimSpace(params.Content) == "" {
				return nil, fmt.Errorf("content is required")
			}
			if params.Mode == "" {
				params.Mode = "html"
			}
			if params.Title == "" {
				params.Title = "Canvas"
			}

			html := wrapCanvasContent(params.Content, params.Mode, params.Title)
			path := writeTempCanvas(html)
			if err := openBrowser(path); err != nil {
				return nil, fmt.Errorf("open canvas: %w", err)
			}
			return map[string]any{
				"opened": true,
				"path":   path,
				"mode":   params.Mode,
			}, nil
		},
	}
}

func wrapCanvasContent(content, mode, title string) string {
	switch mode {
	case "svg":
		return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>%s</title>
<style>body{margin:0;background:#1a1a2e;display:flex;justify-content:center;align-items:center;min-height:100vh}</style>
</head><body>%s</body></html>`, title, content)

	case "mermaid":
		return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>%s</title>
<script src="https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js"></script>
<script>mermaid.initialize({startOnLoad:true,theme:'dark',themeVariables:{fontFamily:'system-ui'}});</script>
<style>body{background:#1a1a2e;display:flex;justify-content:center;padding:40px}
.mermaid{max-width:100%%}</style></head><body>
<div class="mermaid">%s</div></body></html>`, title, content)

	case "plantuml":
		encoded := plantUMLEncode(content)
		url := fmt.Sprintf("https://www.plantuml.com/plantuml/svg/%s", encoded)
		return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>%s</title>
<style>body{background:#1a1a2e;display:flex;justify-content:center;padding:40px}
img{max-width:100%%;border-radius:8px}</style></head><body>
<img src="%s" alt="PlantUML diagram"></body></html>`, title, url)

	default:
		if !strings.Contains(content, "<html") && !strings.Contains(content, "<!DOCTYPE") {
			content = fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>%s</title>
<style>body{font-family:system-ui,sans-serif;background:#1a1a2e;color:#e0e0e0;padding:20px;max-width:1200px;margin:0 auto}
pre{background:#0f0f23;padding:12px;border-radius:6px;overflow-x:auto}
code{font-family:'Fira Code',monospace}</style></head><body>%s</body></html>`, title, content)
		}
		return content
	}
}

func writeTempCanvas(html string) string {
	tmpDir := filepath.Join(os.TempDir(), "covo-canvas")
	os.MkdirAll(tmpDir, 0o755)
	ts := time.Now().Format("150405")
	path := filepath.Join(tmpDir, fmt.Sprintf("canvas-%s.html", ts))
	os.WriteFile(path, []byte(html), 0o644)
	return path
}

func openBrowser(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "linux":
		cmd = exec.Command("xdg-open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	return cmd.Start()
}

func plantUMLEncode(text string) string {
	// PlantUML's text encoding for public server URLs
	var buf strings.Builder
	text = strings.TrimSpace(text)
	for _, b := range []byte(text) {
		if b >= 0x20 && b <= 0x7E {
			buf.WriteByte(((b & 0x3F) << 2) | ((b >> 6) & 0x3))
		}
	}
	return buf.String()
}

var _ = os.TempDir
