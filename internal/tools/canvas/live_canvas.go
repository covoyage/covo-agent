package canvas

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// liveCanvasHub manages WebSocket connections for live canvas updates.
type liveCanvasHub struct {
	mu          sync.RWMutex
	clients     map[chan []byte]struct{}
	server      *http.Server
	port        int
	started     bool
	contentType string
	title       string
}

var (
	globalCanvasHub *liveCanvasHub
	canvasOnce      sync.Once
)

func getCanvasHub() *liveCanvasHub {
	canvasOnce.Do(func() {
		globalCanvasHub = &liveCanvasHub{
			clients: make(map[chan []byte]struct{}),
		}
	})
	return globalCanvasHub
}

func (h *liveCanvasHub) start() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.started {
		return nil
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	h.port = listener.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.handleWS)
	mux.HandleFunc("/", h.handlePage)

	h.server = &http.Server{Handler: mux}
	go h.server.Serve(listener)

	h.started = true
	return nil
}

func (h *liveCanvasHub) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.started {
		return
	}

	// Close all client channels and remove them
	for ch := range h.clients {
		close(ch)
	}
	h.clients = make(map[chan []byte]struct{})

	// Shutdown HTTP server
	if h.server != nil {
		h.server.Close()
	}

	h.started = false
}

func (h *liveCanvasHub) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		return
	}

	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
		conn.Close()
	}()

	// Send current state on connect
	h.mu.RLock()
	ct := h.contentType
	title := h.title
	h.mu.RUnlock()

	if ct != "" {
		msg, _ := json.Marshal(map[string]any{
			"type":    "update",
			"content": ct,
			"title":   title,
		})
		wsutil.WriteServerMessage(conn, ws.OpText, msg)
	}

	// Read loop (keep alive)
	for {
		_, err := wsutil.ReadClientMessage(conn, nil)
		if err != nil {
			break
		}
	}
}

func (h *liveCanvasHub) handlePage(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.New("page").Parse(liveCanvasHTML))
	tmpl.Execute(w, map[string]any{
		"Port": h.port,
	})
}

func (h *liveCanvasHub) broadcast(contentType, title, content string) {
	h.mu.Lock()
	h.contentType = content
	h.title = title
	h.mu.Unlock()

	msg, _ := json.Marshal(map[string]any{
		"type":    "update",
		"content": content,
		"title":   title,
	})

	h.mu.RLock()
	defer h.mu.RUnlock()

	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
			// Client too slow, drop
		}
	}
}

const liveCanvasHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Live Canvas</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { background: #1a1a2e; color: #e0e0e0; font-family: system-ui, sans-serif; }
  #status { position: fixed; top: 8px; right: 8px; padding: 4px 10px; border-radius: 4px;
            font-size: 12px; background: #333; color: #aaa; z-index: 999; }
  #status.connected { color: #4caf50; }
  #status.disconnected { color: #f44336; }
  #content { width: 100vw; min-height: 100vh; }
  pre { background: #0f0f23; padding: 12px; border-radius: 6px; overflow-x: auto; }
  code { font-family: 'Fira Code', monospace; }
</style>
</head>
<body>
<div id="status" class="disconnected">Connecting...</div>
<div id="content"></div>
<script>
(function() {
  const status = document.getElementById('status');
  const content = document.getElementById('content');
  let ws = null;
  let reconnectTimer = null;

  function connect() {
    if (ws) { ws.close(); }
    ws = new WebSocket('ws://' + location.host + '/ws');

    ws.onopen = function() {
      status.textContent = 'Connected';
      status.className = 'connected';
    };

    ws.onclose = function() {
      status.textContent = 'Disconnected - reconnecting...';
      status.className = 'disconnected';
      if (!reconnectTimer) {
        reconnectTimer = setTimeout(function() {
          reconnectTimer = null;
          connect();
        }, 2000);
      }
    };

    ws.onmessage = function(evt) {
      try {
        const msg = JSON.parse(evt.data);
        if (msg.type === 'update') {
          if (msg.title) { document.title = msg.title; }
          renderContent(msg.content);
        }
      } catch(e) {}
    };
  }

  function renderContent(html) {
    // Check if it's a full HTML document or a fragment
    if (html.indexOf('<html') !== -1 || html.indexOf('<!DOCTYPE') !== -1) {
      // For full documents, extract body content
      const parser = new DOMParser();
      const doc = parser.parseFromString(html, 'text/html');
      content.innerHTML = '';
      while (doc.body.firstChild) {
        content.appendChild(doc.body.firstChild);
      }
    } else {
      content.innerHTML = html;
    }
  }

  connect();
})();
</script>
</body>
</html>`

func BuildLiveCanvasTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "live_canvas",
		Description: strings.Join([]string{
			"Start or update a Live Canvas -- a real-time visual workspace",
			"that updates in the browser without page reloads.",
			"",
			"On first call, starts a local WebSocket server and opens the",
			"canvas in the browser. Subsequent calls update the content live.",
			"",
			"Supported modes:",
			"- html: Raw HTML content (default).",
			"- svg: SVG markup (wrapped in dark-themed HTML).",
			"- mermaid: Mermaid diagram code (wrapped with CDN renderer).",
			"- markdown: Markdown rendered to HTML.",
			"",
			"Use this for: live dashboards, real-time data visualization,",
			"interactive demos, or any visual that updates over time.",
			"",
			"Requires: github.com/gobwas/ws (already in go.mod).",
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
					"description": "Content type: 'html', 'svg', 'mermaid', or 'markdown' (default: 'html').",
					"enum":        []string{"html", "svg", "mermaid", "markdown"},
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Canvas tab title (default: 'Live Canvas').",
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
				params.Title = "Live Canvas"
			}

			hub := getCanvasHub()

			// Start server on first call
			if err := hub.start(); err != nil {
				return nil, fmt.Errorf("start live canvas server: %w", err)
			}

			// Build the content HTML
			html := wrapLiveCanvasContent(params.Content, params.Mode)

			// Broadcast to all connected clients
			hub.broadcast(html, params.Title, html)

			// Open browser on first call
			hub.mu.RLock()
			firstCall := len(hub.clients) == 0
			hub.mu.RUnlock()

			url := fmt.Sprintf("http://127.0.0.1:%d", hub.port)
			if firstCall {
				openBrowser(url)
			}

			return map[string]any{
				"started": true,
				"url":     url,
				"mode":    params.Mode,
			}, nil
		},
	}
}

func BuildStopCanvasTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "stop_canvas",
		Description: "Stop the Live Canvas server and close all WebSocket connections.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			hub := getCanvasHub()
			hub.stop()
			return map[string]any{
				"stopped": true,
			}, nil
		},
	}
}

func wrapLiveCanvasContent(content, mode string) string {
	switch mode {
	case "svg":
		return fmt.Sprintf(`<div style="background:#1a1a2e;display:flex;justify-content:center;align-items:center;min-height:100vh">%s</div>`, content)

	case "mermaid":
		return fmt.Sprintf(`<div class="mermaid">%s</div>
<script src="https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js"></script>
<script>mermaid.initialize({startOnLoad:true,theme:'dark',themeVariables:{fontFamily:'system-ui'}});</script>`, content)

	case "markdown":
		// Simple markdown to HTML conversion
		html := mdToHTML(content)
		return fmt.Sprintf(`<div style="max-width:900px;margin:0 auto;padding:40px;line-height:1.6">%s</div>`, html)

	default:
		return content
	}
}

// mdToHTML performs basic markdown to HTML conversion.
func mdToHTML(md string) string {
	lines := strings.Split(md, "\n")
	var out []string
	inCode := false
	inList := false

	for _, line := range lines {
		// Code blocks
		if strings.HasPrefix(line, "```") {
			if inCode {
				out = append(out, "</code></pre>")
				inCode = false
			} else {
				out = append(out, "<pre><code>")
				inCode = true
			}
			continue
		}
		if inCode {
			out = append(out, template.HTMLEscapeString(line))
			continue
		}

		// Headings
		if strings.HasPrefix(line, "### ") {
			if inList { out = append(out, "</ul>"); inList = false }
			out = append(out, "<h3>"+template.HTMLEscapeString(line[4:])+"</h3>")
			continue
		}
		if strings.HasPrefix(line, "## ") {
			if inList { out = append(out, "</ul>"); inList = false }
			out = append(out, "<h2>"+template.HTMLEscapeString(line[3:])+"</h2>")
			continue
		}
		if strings.HasPrefix(line, "# ") {
			if inList { out = append(out, "</ul>"); inList = false }
			out = append(out, "<h1>"+template.HTMLEscapeString(line[2:])+"</h1>")
			continue
		}

		// List items
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			if !inList {
				out = append(out, "<ul>")
				inList = true
			}
			out = append(out, "<li>"+template.HTMLEscapeString(line[2:])+"</li>")
			continue
		}
		if inList {
			out = append(out, "</ul>")
			inList = false
		}

		// Empty lines
		if strings.TrimSpace(line) == "" {
			out = append(out, "<br>")
			continue
		}

		// Regular paragraphs
		out = append(out, "<p>"+template.HTMLEscapeString(line)+"</p>")
	}
	if inList { out = append(out, "</ul>") }
	return strings.Join(out, "\n")
}
