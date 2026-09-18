package tools

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

const computerUseFrameTTL = 30 * time.Second

type computerFrame struct {
	ID       string
	Path     string
	Width    int
	Height   int
	Captured time.Time
}

type computerFrameStore struct {
	mu    sync.Mutex
	frame *computerFrame
}

func (s *computerFrameStore) set(f computerFrame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frame = &f
}

func (s *computerFrameStore) require(frameID string) (*computerFrame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frame == nil {
		return nil, fmt.Errorf("screenshot first")
	}
	if frameID != "" && frameID != s.frame.ID {
		return nil, fmt.Errorf("unknown frame_id %q; screenshot again", frameID)
	}
	if time.Since(s.frame.Captured) > computerUseFrameTTL {
		return nil, fmt.Errorf("frame stale; screenshot again")
	}
	return s.frame, nil
}

var computerFrames = &computerFrameStore{}
var computerFrameSeq atomic.Uint64

// captureScreenshotFn is replaced in tests.
var captureScreenshotFn = captureScreenshotDefault

func buildComputerUseTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "computer_use",
		Description: strings.Join([]string{
			"Control visible desktop apps via accessibility APIs.",
			"Actions: screenshot, wait, list_apps, get_app_state, focus_app, click, scroll, drag,",
			"type_text, press_key, set_value, secondary_action.",
			"",
			"For page content (navigate, click a link, fill a form) use browser, not this tool.",
			"Reserve computer_use for desktop chrome: address bar, permission prompts,",
			"extension popups, native dialogs, and anything on screen that is not page DOM.",
			"",
			"Call screenshot before click/drag/secondary_action. Coordinates are pixels",
			"in that screenshot (top-left origin). Frames expire after 30s.",
			"",
			"macOS: osascript/applescript. Linux: xdotool/wmctrl. Windows: PowerShell/user32.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{
						"screenshot", "wait",
						"list_apps", "get_app_state", "focus_app", "click",
						"scroll", "drag", "type_text", "press_key",
						"set_value", "secondary_action",
					},
				},
				"app":      map[string]any{"type": "string", "description": "App name (list_apps/get_app_state/focus_app)."},
				"x":        map[string]any{"type": "integer", "description": "X coordinate in the last screenshot."},
				"y":        map[string]any{"type": "integer", "description": "Y coordinate in the last screenshot."},
				"text":     map[string]any{"type": "string", "description": "Text to type."},
				"key":      map[string]any{"type": "string", "description": "Key name."},
				"value":    map[string]any{"type": "string", "description": "Value to set."},
				"dx":       map[string]any{"type": "integer", "description": "Drag delta X."},
				"dy":       map[string]any{"type": "integer", "description": "Drag delta Y."},
				"amount":   map[string]any{"type": "integer", "description": "Scroll amount."},
				"frame_id": map[string]any{"type": "string", "description": "Screenshot frame this action refers to."},
				"ms":       map[string]any{"type": "integer", "description": "Wait duration in milliseconds (action=wait, max 10000)."},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			p, err := parseComputerUseArgs(args)
			if err != nil {
				return nil, err
			}
			switch p.Action {
			case "screenshot":
				return takeScreenshot()
			case "wait":
				return computerWait(p.MS)
			case "list_apps":
				return listApps()
			case "get_app_state":
				return getAppState(p.App)
			case "focus_app":
				return focusApp(p.App)
			case "click":
				if _, err := computerFrames.require(p.FrameID); err != nil {
					return nil, err
				}
				return click(p.X, p.Y)
			case "scroll":
				return scroll(p.Amount)
			case "drag":
				if _, err := computerFrames.require(p.FrameID); err != nil {
					return nil, err
				}
				return drag(p.X, p.Y, p.DX, p.DY)
			case "type_text":
				return typeTxt(p.Text)
			case "press_key":
				return pressK(p.Key)
			case "set_value":
				return setVal(p.Value)
			case "secondary_action":
				if _, err := computerFrames.require(p.FrameID); err != nil {
					return nil, err
				}
				return rightClick(p.X, p.Y)
			default:
				return nil, fmt.Errorf("unknown action: %s", p.Action)
			}
		},
	}
}

type computerUseArgs struct {
	Action  string `json:"action"`
	App     string `json:"app"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	DX      int    `json:"dx"`
	DY      int    `json:"dy"`
	Amount  int    `json:"amount"`
	Text    string `json:"text"`
	Key     string `json:"key"`
	Value   string `json:"value"`
	FrameID string `json:"frame_id"`
	MS      int    `json:"ms"`
}

func parseComputerUseArgs(args json.RawMessage) (computerUseArgs, error) {
	var p computerUseArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return p, fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(p.Action) == "" {
		return p, fmt.Errorf("action is required")
	}
	return p, nil
}

func takeScreenshot() (any, error) {
	dir := filepath.Join(os.TempDir(), "covo-computer")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("screenshot dir: %w", err)
	}
	id := fmt.Sprintf("f%d", computerFrameSeq.Add(1))
	path := filepath.Join(dir, "frame-"+id+".png")
	w, h, err := captureScreenshotFn(path)
	if err != nil {
		return nil, err
	}
	computerFrames.set(computerFrame{
		ID:       id,
		Path:     path,
		Width:    w,
		Height:   h,
		Captured: time.Now(),
	})
	return map[string]any{
		"action":   "screenshot",
		"path":     path,
		"width":    w,
		"height":   h,
		"frame_id": id,
		"note":     "click/drag coordinates are pixels in this image (top-left origin). Inspect the PNG before clicking.",
	}, nil
}

func computerWait(ms int) (any, error) {
	if ms <= 0 {
		ms = 500
	}
	if ms > 10000 {
		return nil, fmt.Errorf("wait max is 10000ms")
	}
	time.Sleep(time.Duration(ms) * time.Millisecond)
	return map[string]any{"waited_ms": ms}, nil
}

func captureScreenshotDefault(path string) (width, height int, err error) {
	switch runtime.GOOS {
	case "darwin":
		if err := runDesktop("screencapture", "-x", path); err != nil {
			return 0, 0, err
		}
	case "linux":
		if lookPath("grim") {
			err = runDesktop("grim", path)
		} else if lookPath("import") {
			err = runDesktop("import", "-window", "root", path)
		} else {
			return 0, 0, fmt.Errorf("screenshot: install grim or ImageMagick import")
		}
		if err != nil {
			return 0, 0, err
		}
	case "windows":
		script := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $b=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds; $bmp=New-Object Drawing.Bitmap $b.Width,$b.Height; $g=[Drawing.Graphics]::FromImage($bmp); $g.CopyFromScreen($b.Location,[Drawing.Point]::Empty,$b.Size); $bmp.Save('%s',[Drawing.Imaging.ImageFormat]::Png)`, strings.ReplaceAll(path, `'`, `''`))
		if err := runDesktop("powershell", "-NoProfile", "-Command", script); err != nil {
			return 0, 0, err
		}
	default:
		return 0, 0, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	return pngSize(path)
}

func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func pngSize(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("read screenshot: %w", err)
	}
	defer f.Close()
	var hdr [24]byte
	if _, err := f.Read(hdr[:]); err != nil {
		return 0, 0, fmt.Errorf("read screenshot header: %w", err)
	}
	if string(hdr[1:4]) != "PNG" {
		return 0, 0, fmt.Errorf("screenshot is not a PNG")
	}
	w := int(binary.BigEndian.Uint32(hdr[16:20]))
	h := int(binary.BigEndian.Uint32(hdr[20:24]))
	return w, h, nil
}

func runDesktop(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func listApps() (any, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("osascript", "-e", `tell application "System Events" to get name of every process whose background only is false`).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("osascript: %w: %s", err, strings.TrimSpace(string(out)))
		}
		apps := strings.Split(strings.TrimSpace(string(out)), ", ")
		return map[string]any{"apps": apps, "count": len(apps)}, nil
	case "linux":
		out, err := exec.Command("wmctrl", "-l").CombinedOutput()
		if err != nil {
			out, err = exec.Command("xdotool", "search", "--name", ".").CombinedOutput()
			if err != nil {
				return nil, fmt.Errorf("wmctrl/xdotool: %w: %s", err, strings.TrimSpace(string(out)))
			}
			ids := strings.Fields(string(out))
			var names []string
			for _, id := range ids {
				n, _ := exec.Command("xdotool", "getwindowname", id).Output()
				if s := strings.TrimSpace(string(n)); s != "" {
					names = append(names, s)
				}
			}
			return map[string]any{"apps": names, "count": len(names)}, nil
		}
		seen := map[string]bool{}
		var apps []string
		for _, line := range strings.Split(string(out), "\n") {
			if f := strings.Fields(line); len(f) >= 4 {
				n := strings.Join(f[3:], " ")
				if !seen[n] {
					seen[n] = true
					apps = append(apps, n)
				}
			}
		}
		return map[string]any{"apps": apps, "count": len(apps)}, nil
	case "windows":
		out, err := exec.Command("powershell", "-NoProfile", "-Command",
			`Get-Process|Where{$_.MainWindowTitle}|Select-ExpandProperty MainWindowTitle|Sort -Unique`).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("powershell: %w: %s", err, strings.TrimSpace(string(out)))
		}
		var apps []string
		for _, line := range strings.Split(string(out), "\n") {
			if s := strings.TrimSpace(line); s != "" {
				apps = append(apps, s)
			}
		}
		return map[string]any{"apps": apps, "count": len(apps)}, nil
	}
	return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

func getAppState(app string) (any, error) {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("osascript", "-e", fmt.Sprintf(
			`tell app "System Events" to set p to first process whose name is "%s"
return "frontmost="&frontmost of p&"|windows="&count of windows of p`, escapeAppleScriptString(app)))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("osascript: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return map[string]any{"app": app, "state": strings.TrimSpace(string(out))}, nil
	case "linux":
		out, _ := exec.Command("wmctrl", "-l").Output()
		lo := strings.ToLower(app)
		var titles []string
		count := 0
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(strings.ToLower(line), lo) {
				count++
				if f := strings.Fields(line); len(f) >= 4 {
					titles = append(titles, strings.Join(f[3:], " "))
				}
			}
		}
		act, _ := exec.Command("xdotool", "getactivewindow", "getwindowname").Output()
		return map[string]any{"app": app, "windows": count, "titles": titles, "frontmost": strings.Contains(strings.ToLower(string(act)), lo), "active_win": strings.TrimSpace(string(act))}, nil
	case "windows":
		out, _ := exec.Command("powershell", "-NoProfile", "-Command",
			fmt.Sprintf(`$p=Get-Process -Name '*%s*' -ErrorAction SilentlyContinue|Select -First 1; if($p){$p.MainWindowTitle; [bool]$p.Responding}`, escapePSString(app))).Output()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		title, responding := "", false
		if len(lines) >= 1 {
			title = lines[0]
		}
		if len(lines) >= 2 {
			responding = strings.TrimSpace(lines[1]) == "True"
		}
		return map[string]any{"app": app, "title": title, "responding": responding, "frontmost": title != ""}, nil
	}
	return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

func focusApp(app string) (any, error) {
	app = strings.TrimSpace(app)
	if app == "" {
		return nil, fmt.Errorf("app is required for focus_app")
	}
	var err error
	switch runtime.GOOS {
	case "darwin":
		err = runDesktop("osascript", "-e", fmt.Sprintf(`tell application "%s" to activate`, escapeAppleScriptString(app)))
	case "linux":
		if lookPath("wmctrl") {
			err = runDesktop("wmctrl", "-a", app)
		} else {
			err = runDesktop("xdotool", "search", "--name", app, "windowactivate")
		}
	case "windows":
		err = runDesktop("powershell", "-NoProfile", "-Command", fmt.Sprintf(
			`$p=Get-Process | Where-Object { $_.MainWindowTitle -like '*%s*' -or $_.ProcessName -like '*%s*' } | Select-Object -First 1; if(-not $p){ throw 'app not found' }; Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public class W { [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h); }'; [W]::SetForegroundWindow($p.MainWindowHandle)`,
			escapePSString(app), escapePSString(app)))
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"focused": true, "app": app}, nil
}

func click(x, y int) (any, error) {
	var err error
	switch runtime.GOOS {
	case "darwin":
		err = runDesktop("osascript", "-e", fmt.Sprintf(`tell app "System Events" to click at {%d, %d}`, x, y))
	case "linux":
		err = runDesktop("xdotool", "mousemove", its(x), its(y), "click", "1")
	case "windows":
		err = runDesktop("powershell", "-NoProfile", "-Command", fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Cursor]::Position=New-Object Drawing.Point(%d,%d); Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern void mouse_event(int f,int x,int y,int d,int ei);' -Name W -Namespace S; [S.W]::mouse_event(2,0,0,0,0); [S.W]::mouse_event(4,0,0,0,0)`, x, y))
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"clicked": true, "x": x, "y": y}, nil
}

func scroll(amount int) (any, error) {
	var err error
	switch runtime.GOOS {
	case "darwin":
		c := absI(amount)
		err = runDesktop("osascript", "-e", fmt.Sprintf(`tell app "System Events" to repeat %d times; key code %s; end repeat`, c, scrollKey(amount)))
	case "linux":
		dir := "4"
		if amount < 0 {
			dir = "5"
		}
		err = runDesktop("xdotool", "click", dir)
	case "windows":
		d := 120
		if amount < 0 {
			d = -120
		}
		err = runDesktop("powershell", "-NoProfile", "-Command", fmt.Sprintf(
			`Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern void mouse_event(int f,int x,int y,int d,int ei);' -Name W -Namespace S; [S.W]::mouse_event(0x0800,0,0,%d,0)`, d))
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"scrolled": amount}, nil
}

func drag(x, y, dx, dy int) (any, error) {
	var err error
	switch runtime.GOOS {
	case "darwin":
		err = runDesktop("osascript", "-e", fmt.Sprintf(
			`tell app "System Events"; key down option; delay 0.1; click at {%d,%d}; delay 0.1; click at {%d,%d}; delay 0.1; key up option; end tell`, x, y, x+dx, y+dy))
	case "linux":
		err = runDesktop("xdotool", "mousedown", "1", "mousemove_relative", its(dx), its(dy), "mouseup", "1")
	case "windows":
		err = runDesktop("powershell", "-NoProfile", "-Command", fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Cursor]::Position=New-Object Drawing.Point(%d,%d); Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern void mouse_event(int f,int x,int y,int d,int ei);' -Name W -Namespace S; [S.W]::mouse_event(2,0,0,0,0); [System.Windows.Forms.Cursor]::Position=New-Object Drawing.Point(%d,%d); Start-Sleep -Milliseconds 100; [S.W]::mouse_event(4,0,0,0,0)`, x, y, x+dx, y+dy))
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"dragged": true, "dx": dx, "dy": dy}, nil
}

func typeTxt(text string) (any, error) {
	var err error
	switch runtime.GOOS {
	case "darwin":
		err = runDesktop("osascript", "-e", fmt.Sprintf(`tell app "System Events" to keystroke "%s"`, escapeAppleScriptString(text)))
	case "linux":
		err = runDesktop("xdotool", "type", text)
	case "windows":
		script := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.SendKeys]::SendWait('%s')`, escapeWinKeys(text))
		err = runDesktop("powershell", "-NoProfile", "-Command", script)
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"typed": text}, nil
}

func pressK(key string) (any, error) {
	var err error
	switch runtime.GOOS {
	case "darwin":
		if kc := macKeyCode(key); kc > 0 {
			err = runDesktop("osascript", "-e", fmt.Sprintf(`tell app "System Events" to key code %d`, kc))
		} else {
			err = runDesktop("osascript", "-e", fmt.Sprintf(`tell app "System Events" to keystroke "%s"`, escapeAppleScriptString(key)))
		}
	case "linux":
		err = runDesktop("xdotool", "key", key)
	case "windows":
		err = runDesktop("powershell", "-NoProfile", "-Command", fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.SendKeys]::SendWait('{%s}')`, escapePSString(escapeSendKeysBraces(key))))
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"pressed": key}, nil
}

func setVal(value string) (any, error) {
	var err error
	switch runtime.GOOS {
	case "darwin":
		if err = runDesktop("osascript", "-e", fmt.Sprintf(`set the clipboard to "%s"`, escapeAppleScriptString(value))); err == nil {
			err = runDesktop("osascript", "-e", `tell app "System Events" to keystroke "v" using command down`)
		}
	case "linux":
		err = runDesktop("xdotool", "type", value)
	case "windows":
		if err = runDesktop("powershell", "-NoProfile", "-Command",
			fmt.Sprintf(`[System.Windows.Forms.Clipboard]::SetText('%s')`, escapeWinKeys(value))); err == nil {
			err = runDesktop("powershell", "-NoProfile", "-Command",
				`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.SendKeys]::SendWait('^v')`)
		}
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"set": value, "method": "clipboard+paste"}, nil
}

func rightClick(x, y int) (any, error) {
	var err error
	switch runtime.GOOS {
	case "darwin":
		err = runDesktop("osascript", "-e", fmt.Sprintf(`tell app "System Events" to click at {%d, %d} with option down`, x, y))
	case "linux":
		err = runDesktop("xdotool", "mousemove", its(x), its(y), "click", "3")
	case "windows":
		err = runDesktop("powershell", "-NoProfile", "-Command", fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Cursor]::Position=New-Object Drawing.Point(%d,%d); Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern void mouse_event(int f,int x,int y,int d,int ei);' -Name W -Namespace S; [S.W]::mouse_event(8,0,0,0,0); [S.W]::mouse_event(16,0,0,0,0)`, x, y))
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"right_clicked": true, "x": x, "y": y}, nil
}

func absI(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
func its(n int) string { return fmt.Sprintf("%d", n) }
func scrollKey(a int) string {
	if a > 0 {
		return "126"
	}
	return "125"
}

func macKeyCode(key string) int {
	codes := map[string]int{
		"return": 36, "enter": 76, "tab": 48, "space": 49, "delete": 51, "escape": 53,
		"command": 55, "shift": 56, "option": 58, "control": 59,
		"right": 124, "left": 123, "down": 125, "up": 126,
		"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97, "f7": 98, "f8": 100,
		"f9": 101, "f10": 109, "f11": 103, "f12": 111, "home": 115, "end": 119,
		"page up": 116, "page down": 121,
	}
	return codes[strings.ToLower(key)]
}

func escapeWinKeys(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	s = strings.ReplaceAll(s, "+", "{+}")
	s = strings.ReplaceAll(s, "^", "{^}")
	s = strings.ReplaceAll(s, "%", "{%}")
	s = strings.ReplaceAll(s, "~", "{~}")
	s = strings.ReplaceAll(s, "(", "{(}")
	s = strings.ReplaceAll(s, ")", "{)}")
	return s
}

// escapeAppleScriptString escapes a Go string for safe embedding inside a
// double-quoted AppleScript string literal (e.g. `keystroke "%s"`). Backslash
// MUST be escaped before the quote character, otherwise an attacker-supplied
// backslash immediately before a quote can neutralize the quote escaping and
// break out of the string literal to inject arbitrary AppleScript/shell
// commands.
func escapeAppleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

// escapePSString escapes a Go string for safe embedding inside a
// single-quoted PowerShell string literal ('...'). In PowerShell, a literal
// single quote inside a single-quoted string is escaped by doubling it.
func escapePSString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// escapeSendKeysBraces escapes literal { and } characters for the Windows
// SendKeys mini-language, where braces normally introduce a special key
// name (e.g. {ENTER}). Without this, an attacker-supplied key value
// containing braces could close the outer {%s} early and inject additional
// SendKeys key sequences.
func escapeSendKeysBraces(s string) string {
	s = strings.ReplaceAll(s, "{", "{{}")
	s = strings.ReplaceAll(s, "}", "{}}")
	return s
}
