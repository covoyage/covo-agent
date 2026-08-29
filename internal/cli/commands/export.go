package commands

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/i18n"
)

// exportSessionHTML exports the current session to an HTML file.
func exportSessionHTML(app *chat.ChatApp, ag *agentcore.Agent, path string) {
	msgs := ag.State().Messages()
	if len(msgs) == 0 {
		app.PrintSystem(i18n.T("system.no_export_messages"))
		return
	}
	if path == "" {
		home, _ := os.UserHomeDir()
		ts := time.Now().Format("20060102_150405")
		path = filepath.Join(home, "Downloads", fmt.Sprintf("covo-session-%s.html", ts))
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)

	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\">\n")
	sb.WriteString("<title>Covo-Agent Session</title>\n")
	sb.WriteString("<style>body{font-family:system-ui,sans-serif;max-width:900px;margin:0 auto;padding:20px;background:#1a1a2e;color:#e0e0e0}")
	sb.WriteString(".user{background:#16213e;padding:12px 16px;border-radius:8px;margin:8px 0;border-left:3px solid #0f3460}")
	sb.WriteString(".assistant{background:#1a1a2e;padding:12px 16px;border-radius:8px;margin:8px 0;border-left:3px solid #e94560}")
	sb.WriteString(".system{color:#888;font-size:.85em;margin:4px 0}")
	sb.WriteString(".role{font-weight:bold;font-size:.8em;text-transform:uppercase;margin-bottom:4px}")
	sb.WriteString("pre{background:#0f0f23;padding:12px;border-radius:6px;overflow-x:auto;font-size:.9em}")
	sb.WriteString("code{font-family:'Fira Code',monospace}</style></head><body>\n")
	sb.WriteString("<h1>Covo-Agent Session Export</h1>\n")
	sb.WriteString(fmt.Sprintf("<p class=\"system\">Exported: %s | %d messages</p>\n",
		time.Now().Format("2006-01-02 15:04:05"), len(msgs)))

	for _, msg := range msgs {
		role := html.EscapeString(string(msg.Role))
		content := html.EscapeString(msg.Content)
		content = strings.ReplaceAll(content, "\n", "<br>")
		sb.WriteString(fmt.Sprintf("<div class=\"%s\">", role))
		sb.WriteString(fmt.Sprintf("<div class=\"role\">%s</div>", role))
		sb.WriteString(fmt.Sprintf("<div>%s</div></div>\n", content))
	}
	sb.WriteString("</body></html>")

	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		app.PrintError(fmt.Errorf("export: %w", err))
		return
	}
	app.PrintSystem(i18n.T("system.exported", "path", path))
}

// exportTrajectoryJSONL exports the session trajectory as JSONL.
func exportTrajectoryJSONL(app *chat.ChatApp, covoAgent *agent.CovoAgent) {
	entries := covoAgent.Trajectory().Snapshot()
	if len(entries) == 0 {
		app.PrintSystem(i18n.T("system.no_trajectory"))
		return
	}

	home, _ := os.UserHomeDir()
	ts := time.Now().Format("20060102_150405")
	path := filepath.Join(home, "Downloads", fmt.Sprintf("covo-trajectory-%s.jsonl", ts))
	_ = os.MkdirAll(filepath.Dir(path), 0o755)

	f, err := os.Create(path)
	if err != nil {
		app.PrintError(fmt.Errorf("export trajectory: %w", err))
		return
	}
	defer f.Close()

	for _, entry := range entries {
		data, _ := json.Marshal(entry)
		f.Write(data)
		f.Write([]byte("\n"))
	}
	app.PrintSystem(i18n.T("system.trajectory_exported", "path", path))
}
