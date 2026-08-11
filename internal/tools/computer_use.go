package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func buildComputerUseTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "computer_use",
		Description: strings.Join([]string{
			"Control desktop applications via accessibility APIs.",
			"Actions: list_apps, get_app_state, click, scroll, drag, type_text,",
			"press_key, set_value, secondary_action.",
			"",
			"macOS: osascript/applescript. Linux: xdotool/wmctrl. Windows: PowerShell/user32.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{
						"list_apps", "get_app_state", "click",
						"scroll", "drag", "type_text", "press_key",
						"set_value", "secondary_action",
					},
				},
				"app":    map[string]any{"type": "string", "description": "App name."},
				"x":      map[string]any{"type": "integer", "description": "X coordinate."},
				"y":      map[string]any{"type": "integer", "description": "Y coordinate."},
				"text":   map[string]any{"type": "string", "description": "Text to type."},
				"key":    map[string]any{"type": "string", "description": "Key name."},
				"value":  map[string]any{"type": "string", "description": "Value to set."},
				"dx":     map[string]any{"type": "integer", "description": "Drag delta X."},
				"dy":     map[string]any{"type": "integer", "description": "Drag delta Y."},
				"amount": map[string]any{"type": "integer", "description": "Scroll amount."},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Action               string `json:"action"`
				App                  string `json:"app"`
				X, Y, DX, DY, Amount int
				Text, Key, Value     string
			}
			json.Unmarshal(args, &p)
			switch p.Action {
			case "list_apps":
				return listApps()
			case "get_app_state":
				return getAppState(p.App)
			case "click":
				return click(p.X, p.Y)
			case "scroll":
				return scroll(p.Amount)
			case "drag":
				return drag(p.X, p.Y, p.DX, p.DY)
			case "type_text":
				return typeTxt(p.Text)
			case "press_key":
				return pressK(p.Key)
			case "set_value":
				return setVal(p.Value)
			case "secondary_action":
				return rightClick(p.X, p.Y)
			default:
				return nil, fmt.Errorf("unknown action: %s", p.Action)
			}
		},
	}
}

func listApps() (any, error) {
	switch runtime.GOOS {
	case "darwin":
		out, _ := exec.Command("osascript", "-e", `tell application "System Events" to get name of every process whose background only is false`).Output()
		apps := strings.Split(strings.TrimSpace(string(out)), ", ")
		return map[string]any{"apps": apps, "count": len(apps)}, nil
	case "linux":
		out, err := exec.Command("wmctrl", "-l").Output()
		if err != nil {
			out, _ = exec.Command("xdotool", "search", "--name", ".").Output()
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
		out, _ := exec.Command("powershell", "-NoProfile", "-Command",
			`Get-Process|Where{$_.MainWindowTitle}|Select-ExpandProperty MainWindowTitle|Sort -Unique`).Output()
		var apps []string
		for _, line := range strings.Split(string(out), "\n") {
			if s := strings.TrimSpace(line); s != "" {
				apps = append(apps, s)
			}
		}
		return map[string]any{"apps": apps, "count": len(apps)}, nil
	}
	return map[string]any{"note": "unsupported OS"}, nil
}

func getAppState(app string) (any, error) {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("osascript", "-e", fmt.Sprintf(
			`tell app "System Events" to set p to first process whose name is "%s"
return "frontmost="&frontmost of p&"|windows="&count of windows of p`, escapeAppleScriptString(app)))
		out, _ := cmd.Output()
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

func click(x, y int) (any, error) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("osascript", "-e", fmt.Sprintf(`tell app "System Events" to click at {%d, %d}`, x, y)).Run()
	case "linux":
		exec.Command("xdotool", "mousemove", its(x), its(y), "click", "1").Run()
	case "windows":
		exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Cursor]::Position=New-Object Drawing.Point(%d,%d); Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern void mouse_event(int f,int x,int y,int d,int ei);' -Name W -Namespace S; [S.W]::mouse_event(2,0,0,0,0); [S.W]::mouse_event(4,0,0,0,0)`, x, y)).Run()
	}
	return map[string]any{"clicked": true, "x": x, "y": y}, nil
}

func scroll(amount int) (any, error) {
	switch runtime.GOOS {
	case "darwin":
		c := absI(amount)
		exec.Command("osascript", "-e", fmt.Sprintf(`tell app "System Events" to repeat %d times; key code %s; end repeat`, c, scrollKey(amount))).Run()
	case "linux":
		dir := "4"
		if amount < 0 {
			dir = "5"
		}
		exec.Command("xdotool", "click", dir).Run()
	case "windows":
		d := 120
		if amount < 0 {
			d = -120
		}
		exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf(
			`Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern void mouse_event(int f,int x,int y,int d,int ei);' -Name W -Namespace S; [S.W]::mouse_event(0x0800,0,0,%d,0)`, d)).Run()
	}
	return map[string]any{"scrolled": amount}, nil
}

func drag(x, y, dx, dy int) (any, error) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("osascript", "-e", fmt.Sprintf(
			`tell app "System Events"; key down option; delay 0.1; click at {%d,%d}; delay 0.1; click at {%d,%d}; delay 0.1; key up option; end tell`, x, y, x+dx, y+dy)).Run()
	case "linux":
		exec.Command("xdotool", "mousedown", "1", "mousemove_relative", its(dx), its(dy), "mouseup", "1").Run()
	case "windows":
		exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Cursor]::Position=New-Object Drawing.Point(%d,%d); Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern void mouse_event(int f,int x,int y,int d,int ei);' -Name W -Namespace S; [S.W]::mouse_event(2,0,0,0,0); [System.Windows.Forms.Cursor]::Position=New-Object Drawing.Point(%d,%d); Start-Sleep -Milliseconds 100; [S.W]::mouse_event(4,0,0,0,0)`, x, y, x+dx, y+dy)).Run()
	}
	return map[string]any{"dragged": true, "dx": dx, "dy": dy}, nil
}

func typeTxt(text string) (any, error) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("osascript", "-e", fmt.Sprintf(`tell app "System Events" to keystroke "%s"`, escapeAppleScriptString(text))).Run()
	case "linux":
		exec.Command("xdotool", "type", text).Run()
	case "windows":
		script := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.SendKeys]::SendWait('%s')`, escapeWinKeys(text))
		exec.Command("powershell", "-NoProfile", "-Command", script).Run()
	}
	return map[string]any{"typed": text}, nil
}

func pressK(key string) (any, error) {
	switch runtime.GOOS {
	case "darwin":
		if kc := macKeyCode(key); kc > 0 {
			exec.Command("osascript", "-e", fmt.Sprintf(`tell app "System Events" to key code %d`, kc)).Run()
		} else {
			exec.Command("osascript", "-e", fmt.Sprintf(`tell app "System Events" to keystroke "%s"`, escapeAppleScriptString(key))).Run()
		}
	case "linux":
		exec.Command("xdotool", "key", key).Run()
	case "windows":
		exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.SendKeys]::SendWait('{%s}')`, escapePSString(escapeSendKeysBraces(key)))).Run()
	}
	return map[string]any{"pressed": key}, nil
}

func setVal(value string) (any, error) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("osascript", "-e", fmt.Sprintf(`set the clipboard to "%s"`, escapeAppleScriptString(value))).Run()
		exec.Command("osascript", "-e", `tell app "System Events" to keystroke "v" using command down`).Run()
	case "linux":
		exec.Command("xdotool", "type", value).Run()
	case "windows":
		exec.Command("powershell", "-NoProfile", "-Command",
			fmt.Sprintf(`[System.Windows.Forms.Clipboard]::SetText('%s')`, escapeWinKeys(value))).Run()
		exec.Command("powershell", "-NoProfile", "-Command",
			`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.SendKeys]::SendWait('^v')`).Run()
	}
	return map[string]any{"set": value, "method": "clipboard+paste"}, nil
}

func rightClick(x, y int) (any, error) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("osascript", "-e", fmt.Sprintf(`tell app "System Events" to click at {%d, %d} with option down`, x, y)).Run()
	case "linux":
		exec.Command("xdotool", "mousemove", its(x), its(y), "click", "3").Run()
	case "windows":
		exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Cursor]::Position=New-Object Drawing.Point(%d,%d); Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern void mouse_event(int f,int x,int y,int d,int ei);' -Name W -Namespace S; [S.W]::mouse_event(8,0,0,0,0); [S.W]::mouse_event(16,0,0,0,0)`, x, y)).Run()
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
