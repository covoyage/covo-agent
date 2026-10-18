package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/covoyage/covo-agent/internal/agent/approval"
	"github.com/covoyage/covo-agent/internal/tools/capability"
	"github.com/covoyage/covonaut/agentcore"
	builtin "github.com/covoyage/covonaut/tools"
	"github.com/google/uuid"
)

func browserTestPNG(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 20, 12))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestBrowserScreenshotResult(t *testing.T) {
	data := browserTestPNG(t)
	result, err := browserScreenshotResult(context.Background(), t.TempDir(), data, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	path := result["path"].(string)
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, data) || result["width"] != 20 || result["height"] != 12 {
		t.Fatalf("screenshot missing: %+v err=%v", result, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("screenshot must be private: %v err=%v", info, err)
	}
}

func TestBrowserScreenshotVision(t *testing.T) {
	data := browserTestPNG(t)
	called := false
	analyze := func(ctx context.Context, prompt, mediaType, dataURL string) (string, error) {
		called = true
		if prompt != "What is visible?" || mediaType != "image/png" || dataURL != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data) {
			t.Fatal("vision did not receive the screenshot")
		}
		return "A test image", nil
	}
	result, err := browserScreenshotResult(context.Background(), t.TempDir(), data, "What is visible?", analyze)
	if err != nil || !called || result["analysis"].(map[string]any)["description"] != "A test image" {
		t.Fatalf("vision was not performed: %+v err=%v", result, err)
	}
}

func TestBrowserScreenshotFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := browserScreenshotResult(ctx, t.TempDir(), browserTestPNG(t), "", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := browserScreenshotResult(context.Background(), t.TempDir(), []byte("not an image"), "", nil); err == nil {
		t.Fatal("invalid image accepted")
	}
	if _, err := browserScreenshotResult(context.Background(), t.TempDir(), browserTestPNG(t), "question", nil); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("missing vision model reported success: %v", err)
	}
	failure := errors.New("vision unavailable")
	if _, err := browserScreenshotResult(context.Background(), t.TempDir(), browserTestPNG(t), "question", func(context.Context, string, string, string) (string, error) { return "", failure }); !errors.Is(err, failure) {
		t.Fatalf("vision error lost: %v", err)
	}
}

func TestBrowserControlledPage(t *testing.T) {
	if _, err := builtin.FindChrome(); err != nil {
		t.Skip("Chrome unavailable: ", err)
	}
	t.Setenv("AGENT_BROWSER_ENABLED", "false")
	t.Setenv("AGENT_BROWSER_PATH", "")
	streamStarted := make(chan struct{}, 1)
	streamStopped := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/slow-export" {
			writer.Header().Set("Content-Type", "application/octet-stream")
			_, _ = writer.Write([]byte("partial-data"))
			writer.(http.Flusher).Flush()
			streamStarted <- struct{}{}
			<-request.Context().Done()
			streamStopped <- struct{}{}
			return
		}
		if request.URL.Path == "/oversize-export" {
			writer.Header().Set("Content-Length", strconv.Itoa((32<<20)+1))
			writer.WriteHeader(http.StatusOK)
			return
		}
		if request.URL.Path == "/export" {
			cookie, err := request.Cookie("covo_browser_test")
			if err != nil || cookie.Value != "visible" {
				http.Error(writer, "login required", http.StatusUnauthorized)
				return
			}
			writer.Header().Set("Content-Disposition", `attachment; filename="export.csv"`)
			_, _ = writer.Write([]byte("remote,csv\n1,2"))
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(`<html><head><link rel="icon" href="data:,"></head><body style="background:#fff"><label>Name<input id="name"></label><button onclick="document.querySelector('#status').textContent=document.querySelector('#name').value">Save</button><div id="status"></div><div style="height:1400px;background:#d03040"></div><iframe srcdoc="<p id='frame-value'>frame-content</p>"></iframe></body></html>`))
	}))
	defer server.Close()
	core := agentcore.New(agentcore.Config{})
	defer core.Close()
	visionCalls := 0
	ext := NewBrowserToolsExtension(builtin.ExtensionConfig{Browser: &builtin.BrowserToolConfig{Headless: true, AllowPrivate: true, Engine: "chrome", ViewportWidth: 800, ViewportHeight: 600, CommandTimeout: 30 * time.Second}}, func(ctx context.Context, prompt, mediaType, dataURL string) (string, error) {
		visionCalls++
		if mediaType != "image/png" || !strings.HasPrefix(dataURL, "data:image/png;base64,") {
			t.Fatal("vision received no image")
		}
		return "A red page with a form", nil
	})
	defer ext.Dispose()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := ext.Init(ctx, core); err != nil {
		t.Fatal(err)
	}
	invoke := func(input map[string]any) map[string]any {
		t.Helper()
		args, _ := json.Marshal(input)
		output, err := core.InvokeTool(ctx, "browser", args)
		if err != nil {
			t.Fatalf("browser %v failed: %v", input, err)
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("invalid browser result: %s err=%v", output, err)
		}
		return result
	}
	invoke(map[string]any{"action": "navigate", "url": server.URL})
	findRef := func(tag string) string {
		t.Helper()
		for index := 1; index <= ext.refs.Count(); index++ {
			ref := "@e" + strconv.Itoa(index)
			xpath, found := ext.refs.Get(ref)
			if !found {
				continue
			}
			encoded, _ := json.Marshal(xpath)
			var actualTag string
			if err := ext.withPage(ctx, ext.page, func(runCtx context.Context) error {
				return chromedp.Run(runCtx, chromedp.Evaluate(fmt.Sprintf(`document.evaluate(%s, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue.tagName`, encoded), &actualTag))
			}); err != nil {
				t.Fatal(err)
			}
			if strings.EqualFold(actualTag, tag) {
				return ref
			}
		}
		t.Fatalf("no %s in page snapshot", tag)
		return ""
	}
	invoke(map[string]any{"action": "type", "ref": findRef("input"), "text": "Ada"})
	invoke(map[string]any{"action": "click", "ref": findRef("button")})
	screenshot := invoke(map[string]any{"action": "screenshot", "full_page": true})
	data, err := os.ReadFile(screenshot["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil || decoded.Bounds().Dy() < 1400 {
		t.Fatalf("full-page screenshot missing: %v err=%v", screenshot, err)
	}
	redPixel, greenPixel, bluePixel, _ := decoded.At(100, 100).RGBA()
	if redPixel <= greenPixel*2 || redPixel <= bluePixel*2 {
		t.Fatal("screenshot does not show the controlled red page")
	}
	vision := invoke(map[string]any{"action": "vision", "question": "What is visible?"})
	if visionCalls != 1 || vision["analysis"] == nil {
		t.Fatal("browser vision never ran", vision)
	}
	cdpResult := invoke(map[string]any{"action": "cdp", "cdp_method": "Runtime.evaluate", "cdp_params": map[string]any{"expression": `document.querySelector('#status').textContent`, "returnByValue": true}})
	serialized, _ := json.Marshal(cdpResult)
	if !strings.Contains(string(serialized), "Ada") {
		t.Fatalf("CDP did not control the form's page: %s", serialized)
	}
	invoke(map[string]any{"action": "evaluate", "expression": `console.warn("browser-test-message"); "done"`})
	console := invoke(map[string]any{"action": "console"})
	serialized, _ = json.Marshal(console)
	if !strings.Contains(string(serialized), "browser-test-message") {
		t.Fatalf("console messages missing: %s", serialized)
	}
	frameResult := invoke(map[string]any{"action": "cdp", "cdp_method": "Page.getFrameTree"})
	frameTree := frameResult["result"].(map[string]any)["frameTree"].(map[string]any)
	child := frameTree["childFrames"].([]any)[0].(map[string]any)
	frameID := child["frame"].(map[string]any)["id"].(string)
	frameValue := invoke(map[string]any{"action": "evaluate", "frame_id": frameID, "expression": `document.querySelector('#frame-value').textContent`})
	if frameValue["result"] != "frame-content" {
		t.Fatal("frame evaluation used the wrong document", frameValue)
	}
	opened := make(chan struct{}, 1)
	chromedp.ListenTarget(ext.page, func(event any) {
		if _, ok := event.(*page.EventJavascriptDialogOpening); ok {
			select {
			case opened <- struct{}{}:
			default:
			}
		}
	})
	invoke(map[string]any{"action": "evaluate", "expression": `setTimeout(() => alert('browser-dialog-test'), 0); 'queued'`})
	select {
	case <-opened:
	case <-ctx.Done():
		t.Fatal("dialog did not open")
	}
	dialog := invoke(map[string]any{"action": "snapshot"})
	if dialog["status"] != "dialog_required" || dialog["action_outcome"] != "unverified" {
		t.Fatal("blocked dialog reported successful snapshot", dialog)
	}
	invoke(map[string]any{"action": "dialog", "dialog_id": "current", "accept": true})
	invoke(map[string]any{"action": "snapshot"})
	args := json.RawMessage(`{"action":"cdp","cdp_method":"Browser.close"}`)
	if _, err := ext.run(ctx, args); err == nil {
		t.Fatal("global CDP method allowed")
	}
	requestCtx, requestCancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer requestCancel()
	started := time.Now()
	if _, err := ext.run(requestCtx, json.RawMessage(`{"action":"evaluate","expression":"new Promise(resolve => setTimeout(() => resolve('done'), 5000))"}`)); !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
		t.Fatalf("request cancellation failed: %v duration=%v", err, time.Since(started))
	}
	invoke(map[string]any{"action": "snapshot"})
	invoke(map[string]any{"action": "wait", "selector": "#name"})
	status := invoke(map[string]any{"action": "status"})
	if status["active"] != true || status["headless"] != true {
		t.Fatal("session did not survive request cancellation", status)
	}
	if _, err := ext.run(ctx, json.RawMessage(`{"action":"navigate","headed":true,"url":"https://example.test"}`)); err == nil {
		t.Fatal("live session display mode changed")
	}
	invoke(map[string]any{"action": "evaluate", "expression": `document.cookie="covo_browser_test=visible; Path=/; SameSite=Lax"`})
	profiles, err := filepath.Glob(filepath.Join(ext.dir, "profile-*", "DevToolsActivePort"))
	if err != nil || len(profiles) != 1 {
		t.Fatal("test browser endpoint missing", profiles, err)
	}
	endpointData, err := os.ReadFile(profiles[0])
	if err != nil {
		t.Fatal(err)
	}
	endpointLines := strings.Split(strings.TrimSpace(string(endpointData)), "\n")
	remoteCore := agentcore.New(agentcore.Config{})
	defer remoteCore.Close()
	remote := NewBrowserToolsExtension(builtin.ExtensionConfig{Browser: &builtin.BrowserToolConfig{CDPURL: "ws://127.0.0.1:" + endpointLines[0] + endpointLines[1], AllowPrivate: true, DialogPolicy: builtin.DialogAutoDismiss}}, nil)
	defer remote.Dispose()
	if err := remote.Init(ctx, remoteCore); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.run(ctx, json.RawMessage(fmt.Sprintf(`{"action":"navigate","url":%q}`, server.URL))); err != nil {
		t.Fatal("remote navigate failed", err)
	}
	remoteResult, err := remote.run(ctx, json.RawMessage(`{"action":"evaluate","expression":"document.cookie"}`))
	if err != nil || !strings.Contains(fmt.Sprint(remoteResult), "covo_browser_test=visible") {
		t.Fatal("explicit CDP connection lost the browser's login session", remoteResult, err)
	}
	remoteTabs, err := remote.run(ctx, json.RawMessage(`{"action":"tabs"}`))
	if err != nil || len(remoteTabs.(map[string]any)["tabs"].([]map[string]any)) != 1 {
		t.Fatal("external user's tabs leaked into managed list", remoteTabs, err)
	}
	sourceTab := chromedp.FromContext(ext.driver).Target.TargetID
	if _, err := remote.run(ctx, json.RawMessage(fmt.Sprintf(`{"action":"tab_switch","tab_id":%q}`, sourceTab))); err == nil {
		t.Fatal("external unrelated tab was controllable")
	}
	if result, err := remote.run(ctx, json.RawMessage(`{"action":"downloads_enable"}`)); err != nil || result.(map[string]any)["mode"] != "explicit" {
		t.Fatal("external browser explicit downloads unavailable", result, err)
	}
	remoteDownload := func(url, expected string) {
		t.Helper()
		started, err := remote.run(ctx, json.RawMessage(fmt.Sprintf(`{"action":"download","url":%q}`, url)))
		if err != nil {
			t.Fatal("remote download could not start", err)
		}
		id := started.(map[string]any)["download_id"].(string)
		finished, err := remote.run(ctx, json.RawMessage(fmt.Sprintf(`{"action":"download_wait","download_id":%q}`, id)))
		if err != nil {
			t.Fatal("remote download wait failed", err)
		}
		item := finished.(browserDownload)
		data, readErr := os.ReadFile(item.Path)
		if item.State != "completed" || readErr != nil || string(data) != expected {
			t.Fatal("remote authenticated download was not saved locally", item, string(data), readErr)
		}
		info, err := os.Stat(item.Path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("remote download is not private", err)
		}
	}
	remoteDownload(server.URL+"/export", "remote,csv\n1,2")
	blob, err := remote.run(ctx, json.RawMessage(`{"action":"evaluate","expression":"URL.createObjectURL(new Blob(['remote-blob']))"}`))
	if err != nil {
		t.Fatal(err)
	}
	remoteDownload(blob.(map[string]any)["result"].(string), "remote-blob")
	slow, err := remote.run(ctx, json.RawMessage(fmt.Sprintf(`{"action":"download","url":%q}`, server.URL+"/slow-export")))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-streamStarted:
	case <-ctx.Done():
		t.Fatal("remote streaming download never started")
	}
	slowID := slow.(map[string]any)["download_id"].(string)
	if _, err := remote.run(ctx, json.RawMessage(fmt.Sprintf(`{"action":"download_cancel","download_id":%q}`, slowID))); err != nil {
		t.Fatal(err)
	}
	canceled, err := remote.run(ctx, json.RawMessage(fmt.Sprintf(`{"action":"download_wait","download_id":%q}`, slowID)))
	if err != nil || canceled.(browserDownload).State != "canceled" || canceled.(browserDownload).Path != "" {
		t.Fatal("remote download cancellation not confirmed", canceled, err)
	}
	select {
	case <-streamStopped:
	case <-ctx.Done():
		t.Fatal("remote download HTTP stream was not aborted")
	}
	oversized, err := remote.run(ctx, json.RawMessage(fmt.Sprintf(`{"action":"download","url":%q}`, server.URL+"/oversize-export")))
	if err != nil {
		t.Fatal(err)
	}
	oversizedID := oversized.(map[string]any)["download_id"].(string)
	rejected, err := remote.run(ctx, json.RawMessage(fmt.Sprintf(`{"action":"download_wait","download_id":%q}`, oversizedID)))
	if err != nil || rejected.(browserDownload).State != "failed" || rejected.(browserDownload).Path != "" {
		t.Fatal("remote oversized download exposed as completed", rejected, err)
	}
	if _, err := remote.run(ctx, json.RawMessage(`{"action":"handoff"}`)); err != nil {
		t.Fatal(err)
	}
	browserTestHumanConfirmation(t, ctx, remote)
	if _, err := remote.run(ctx, json.RawMessage(`{"action":"resume"}`)); err != nil || remote.humanPaused {
		t.Fatal("automation did not resume after human confirmation", err)
	}
	automatic, err := remote.run(ctx, json.RawMessage(`{"action":"evaluate","expression":"confirm('automatic confirmation after resume')"}`))
	if err != nil || automatic.(map[string]any)["result"] != false {
		t.Fatal("automatic dialog policy was not restored", automatic, err)
	}
	if _, err := remote.run(ctx, json.RawMessage(`{"action":"handoff"}`)); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(remote.page, chromedp.Navigate("data:text/html,<p>unsupported manual navigation</p>")); err != nil {
		t.Fatal("could not simulate external human navigation", err)
	}
	if _, err := remote.run(ctx, json.RawMessage(`{"action":"resume"}`)); err == nil || !remote.humanPaused {
		t.Fatal("resume accepted an unsupported manually navigated URL", err)
	}
	if err := chromedp.Run(remote.page, chromedp.Navigate(server.URL)); err != nil {
		t.Fatal("could not return to the authorized page", err)
	}
	if _, err := remote.run(ctx, json.RawMessage(`{"action":"resume"}`)); err != nil || remote.humanPaused {
		t.Fatal("resume did not recover after returning to a supported URL", err)
	}
	if _, err := remote.run(ctx, json.RawMessage(`{"action":"close"}`)); err != nil {
		t.Fatal(err)
	}
	invoke(map[string]any{"action": "snapshot"})
	invoke(map[string]any{"action": "close"})
	if invoke(map[string]any{"action": "status"})["active"] != false {
		t.Fatal("browser close did not release the page")
	}
	invoke(map[string]any{"action": "navigate", "url": server.URL, "headed": false})
}

func TestBrowserConsolePrivacyAndBounds(t *testing.T) {
	ext := &BrowserToolsExtension{}
	for count := 0; count < 250; count++ {
		ext.onConsole(&runtime.EventConsoleAPICalled{Type: runtime.APITypeLog, Args: []*runtime.RemoteObject{{Value: []byte(`"token=private-token"`)}}})
	}
	data, _ := json.Marshal(ext.console)
	if len(ext.console) != 200 || strings.Contains(string(data), "private-token") {
		t.Fatalf("console is unbounded or leaked secrets: %s", data)
	}
}

func TestBrowserCDPPolicyAndURL(t *testing.T) {
	for _, method := range []string{"Browser.close", "Target.createTarget", "Network.getAllCookies", "Page.navigate", "Storage.clearDataForOrigin", ""} {
		if browserCDPAllowed(method) {
			t.Fatal("unsafe CDP allowed: ", method)
		}
	}
	for _, value := range []string{"", "file:///tmp/private", "http://localhost", "http://127.0.0.1", "http://[::1]", "http://10.0.0.1", "http://169.254.169.254", "https://user:pass@example.test", "https://example.test/?access%5ftoken=secret"} {
		if _, err := normalizeBrowserURL(value, false); err == nil {
			t.Fatal("unsafe browser URL accepted: ", value)
		}
	}
	if value, err := normalizeBrowserURL("http://127.0.0.1", true); err != nil || value != "http://127.0.0.1" {
		t.Fatalf("explicit private URL permission lost: %s err=%v", value, err)
	}
	for _, value := range []string{"http://169.254.169.254", "http://[::ffff:169.254.169.254]"} {
		if _, err := normalizeBrowserURL(value, true); err == nil {
			t.Fatal("metadata bypassed by private URL permission")
		}
	}
}

func TestBrowserFallbackReportsUnsupportedActions(t *testing.T) {
	for _, cfg := range []*builtin.BrowserToolConfig{{CloudProvider: "browser_use"}, {CamofoxURL: "http://example.test"}, {Engine: "lightpanda"}, {AgentBrowserEnabled: true}} {
		ext := NewBrowserToolsExtension(builtin.ExtensionConfig{Browser: cfg}, nil)
		for _, action := range []string{"vision", "screenshot", "console", "cdp"} {
			args, _ := json.Marshal(map[string]string{"action": action})
			if _, err := ext.run(context.Background(), args); err == nil || !strings.Contains(err.Error(), "requires local Chromium") {
				t.Fatalf("unsupported backend reported success: %+v action=%s err=%v", cfg, action, err)
			}
		}
	}
}

func TestBrowserStageUploads(t *testing.T) {
	workspace, private := t.TempDir(), t.TempDir()
	path := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(path, []byte("original report"), 0600); err != nil {
		t.Fatal(err)
	}
	staged, size, err := stageBrowserUploads(context.Background(), workspace, private, []string{"report.txt"})
	if err != nil || len(staged) != 1 || size != int64(len("original report")) {
		t.Fatalf("upload staging failed: %v size=%d err=%v", staged, size, err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(staged[0])
	if err != nil || string(data) != "original report" || filepath.Base(staged[0]) != "report.txt" {
		t.Fatal("upload was not a private snapshot", staged, err)
	}
	info, err := os.Stat(staged[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("upload snapshot not private", info, err)
	}
}

func TestBrowserStageUploadBoundaries(t *testing.T) {
	workspace, private, outside := t.TempDir(), t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("outside workspace"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(workspace, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(workspace, "large.bin")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate((32 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	for _, paths := range [][]string{nil, {secret}, {"../escape.txt"}, {"escape.txt"}, {"."}, {"large.bin"}} {
		if _, _, err := stageBrowserUploads(context.Background(), workspace, private, paths); err == nil {
			t.Fatal("unsafe upload accepted", paths)
		}
	}
	entries, err := os.ReadDir(private)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed upload left private copies", entries, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := stageBrowserUploads(ctx, workspace, private, []string{"escape.txt"}); !errors.Is(err, context.Canceled) {
		t.Fatal("upload cancellation lost", err)
	}
}

func TestBrowserTabOwnership(t *testing.T) {
	ext := &BrowserToolsExtension{owned: map[target.ID]*target.Info{"root": {TargetID: "root", Type: "page"}}}
	ext.rememberTab(&target.Info{TargetID: "user-tab", Type: "page", URL: "https://example.test"})
	ext.rememberTab(&target.Info{TargetID: "popup", Type: "page", OpenerID: "root"})
	ext.rememberTab(&target.Info{TargetID: "nested-popup", Type: "page", OpenerID: "popup"})
	ext.rememberTab(&target.Info{TargetID: "worker", Type: "worker", OpenerID: "root"})
	if len(ext.owned) != 3 || ext.owned["popup"] == nil || ext.owned["nested-popup"] == nil || ext.owned["user-tab"] != nil {
		t.Fatal("unrelated targets were adopted", ext.owned)
	}
	first := &browserTab{ctx: context.Background(), refs: builtin.NewRefMapper()}
	second := &browserTab{ctx: context.Background(), refs: builtin.NewRefMapper()}
	ext.onTabConsole(first, &runtime.EventConsoleAPICalled{Type: runtime.APITypeLog, Args: []*runtime.RemoteObject{{Value: []byte(`"first"`)}}})
	ext.onTabConsole(second, &runtime.EventConsoleAPICalled{Type: runtime.APITypeLog, Args: []*runtime.RemoteObject{{Value: []byte(`"second"`)}}})
	ext.onTabDialog(first, &page.EventJavascriptDialogOpening{Message: "first dialog"})
	ext.selectTab(second)
	if ext.dialog != nil || len(ext.console) != 1 || ext.console[0]["text"] != `"second"` {
		t.Fatal("tab state was mixed", ext.dialog, ext.console)
	}
	ext.selectTab(first)
	if ext.dialog == nil || ext.dialog.Message != "first dialog" || ext.refs == second.refs {
		t.Fatal("original tab state lost")
	}
	tool := &agentcore.Tool{Name: "browser"}
	capability.DeclareBuiltin(tool)
	if metadata, ok := capability.Lookup(tool); !ok || metadata.Kind != capability.Conditional {
		t.Fatal("browser must declare conditional approval capability", metadata, ok)
	}
	for _, action := range []string{"upload", "downloads_enable", "download", "resume"} {
		args, _ := json.Marshal(map[string]string{"action": action})
		if !approval.RequiresToolApproval("browser", args) {
			t.Fatal("browser file transfer bypassed approval", action)
		}
	}
	for _, action := range []string{"navigate", "tabs", "tab_switch", "downloads", "download_wait", "status", "handoff"} {
		args, _ := json.Marshal(map[string]string{"action": action})
		if approval.RequiresToolApproval("browser", args) {
			t.Fatal("non-transfer browser action gained extra approval", action)
		}
	}
}

func TestBrowserTabsUploadDownloads(t *testing.T) {
	if _, err := builtin.FindChrome(); err != nil {
		t.Skip("Chrome unavailable", err)
	}
	t.Setenv("AGENT_BROWSER_ENABLED", "false")
	t.Setenv("AGENT_BROWSER_PATH", "")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "report.txt"), []byte("workspace report"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/download" || request.URL.Path == "/slow-download" {
			writer.Header().Set("Content-Type", "application/octet-stream")
			writer.Header().Set("Content-Disposition", `attachment; filename="../../report.txt"`)
			_, _ = writer.Write([]byte("downloaded report"))
			if request.URL.Path == "/slow-download" {
				writer.(http.Flusher).Flush()
				<-request.Context().Done()
			}
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(writer, `<html><head><title>%s</title><link rel="icon" href="data:,"></head><body><h1>%s</h1><input type="file" id="file"><a id="download" href="/download">Download</a><a id="slow" href="/slow-download">Slow download</a><script>console.log(%q)</script></body></html>`, request.URL.Path, request.URL.Path, request.URL.Path+"-console")
	}))
	defer server.Close()
	core := agentcore.New(agentcore.Config{})
	defer core.Close()
	ext := NewBrowserToolsExtension(builtin.ExtensionConfig{WorkingDir: workspace, Browser: &builtin.BrowserToolConfig{Headless: true, AllowPrivate: true}}, nil)
	defer ext.Dispose()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := ext.Init(ctx, core); err != nil {
		t.Fatal(err)
	}
	invoke := func(input map[string]any) map[string]any {
		t.Helper()
		args, _ := json.Marshal(input)
		output, err := core.InvokeTool(ctx, "browser", args)
		if err != nil {
			t.Fatalf("browser %v failed: %v", input, err)
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatal(output, err)
		}
		return result
	}
	invoke(map[string]any{"action": "navigate", "url": server.URL + "/main"})
	mainID := invoke(map[string]any{"action": "status"})["tab_id"].(string)
	otherID := invoke(map[string]any{"action": "tab_new", "url": server.URL + "/other"})["tab_id"].(string)
	console := invoke(map[string]any{"action": "console"})
	encoded, _ := json.Marshal(console)
	if !strings.Contains(string(encoded), "/other-console") || strings.Contains(string(encoded), "/main-console") {
		t.Fatal("console mixed between tabs", console)
	}
	invoke(map[string]any{"action": "tab_switch", "tab_id": mainID})
	if tabs := invoke(map[string]any{"action": "tabs"})["tabs"].([]any); len(tabs) != 2 {
		t.Fatal("new tab missing", tabs)
	}
	var fileRef string
	for index := 1; index <= ext.refs.Count(); index++ {
		ref := "@e" + strconv.Itoa(index)
		xpath, _ := ext.refs.Get(ref)
		encoded, _ := json.Marshal(xpath)
		value := invoke(map[string]any{"action": "evaluate", "expression": fmt.Sprintf(`document.evaluate(%s,document,null,XPathResult.FIRST_ORDERED_NODE_TYPE,null).singleNodeValue.id`, encoded)})
		if value["result"] == "file" {
			fileRef = ref
			break
		}
	}
	if fileRef == "" {
		t.Fatal("file input absent from snapshot")
	}
	upload := invoke(map[string]any{"action": "upload", "ref": fileRef, "paths": []string{"report.txt"}})
	if upload["upload"].(map[string]any)["submitted"] != false {
		t.Fatal("upload claimed form submission", upload)
	}
	value := invoke(map[string]any{"action": "evaluate", "expression": `document.querySelector('#file').files[0].text()`})
	if value["result"] != "workspace report" {
		t.Fatal("file input did not receive workspace snapshot", value)
	}
	popup := make(chan target.ID, 1)
	chromedp.ListenBrowser(ext.driver, func(event any) {
		if changed, ok := event.(*target.EventTargetInfoChanged); ok && changed.TargetInfo.URL == server.URL+"/popup" {
			select {
			case popup <- changed.TargetInfo.TargetID:
			default:
			}
		}
	})
	invoke(map[string]any{"action": "evaluate", "expression": fmt.Sprintf(`window.open(%q); 'opened'`, server.URL+"/popup")})
	var popupID target.ID
	select {
	case popupID = <-popup:
	case <-ctx.Done():
		t.Fatal("popup not discovered")
	}
	invoke(map[string]any{"action": "tab_switch", "tab_id": popupID})
	invoke(map[string]any{"action": "tab_close", "tab_id": popupID})
	invoke(map[string]any{"action": "tab_switch", "tab_id": mainID})
	invoke(map[string]any{"action": "downloads_enable"})
	started := make(chan string, 2)
	chromedp.ListenBrowser(ext.driver, func(event any) {
		if download, ok := event.(*browser.EventDownloadWillBegin); ok {
			select {
			case started <- download.GUID:
			default:
			}
		}
	})
	invoke(map[string]any{"action": "evaluate", "expression": `document.querySelector('#download').click(); 'clicked'`})
	var downloadID string
	select {
	case downloadID = <-started:
	case <-ctx.Done():
		t.Fatal("download did not start")
	}
	download := invoke(map[string]any{"action": "download_wait", "download_id": downloadID})
	if download["state"] != "completed" {
		t.Fatal("download did not complete", download)
	}
	path := download["path"].(string)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "downloaded report" || filepath.Base(path) != downloadID {
		t.Fatal("downloaded file missing or used server path", download, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("download was not private", info, err)
	}
	invoke(map[string]any{"action": "evaluate", "expression": `document.querySelector('#slow').click(); 'clicked'`})
	select {
	case downloadID = <-started:
	case <-ctx.Done():
		t.Fatal("slow download did not start")
	}
	invoke(map[string]any{"action": "download_cancel", "download_id": downloadID})
	if result := invoke(map[string]any{"action": "download_wait", "download_id": downloadID}); result["state"] != "canceled" {
		t.Fatal("download cancellation did not finish", result)
	}
	invoke(map[string]any{"action": "evaluate", "expression": `(() => { const link = document.createElement('a'); link.href=URL.createObjectURL(new Blob(['csv,data\n1,2'],{type:'text/csv'})); link.download='export.csv'; document.body.append(link); link.click(); return 'started'; })()`})
	select {
	case downloadID = <-started:
	case <-ctx.Done():
		t.Fatal("blob export did not start")
	}
	exported := invoke(map[string]any{"action": "download_wait", "download_id": downloadID})
	if exported["state"] != "completed" {
		t.Fatal("blob export rejected", exported)
	}
	data, err = os.ReadFile(exported["path"].(string))
	if err != nil || string(data) != "csv,data\n1,2" {
		t.Fatal("blob export contents lost", string(data), err)
	}
	invoke(map[string]any{"action": "tab_close", "tab_id": otherID})
	invoke(map[string]any{"action": "tab_close", "tab_id": mainID})
	if invoke(map[string]any{"action": "status"})["active"] != false {
		t.Fatal("last owned tab did not release session")
	}
}

func TestBrowserDownloadLimitAndPath(t *testing.T) {
	state := &browserDownloads{dir: t.TempDir(), items: make(map[string]*browserDownload), notify: make(chan struct{}, 1), cancel: make(chan string, 64), validate: func(string) bool { return true }}
	state.handle(&browser.EventDownloadWillBegin{GUID: "../escape", URL: "https://example.test"})
	if len(state.items) != 0 {
		t.Fatal("unsafe download ID accepted")
	}
	id := uuid.NewString()
	state.handle(&browser.EventDownloadWillBegin{GUID: id, URL: "https://example.test", SuggestedFilename: "../../escape"})
	state.handle(&browser.EventDownloadProgress{GUID: id, TotalBytes: 33 << 20, State: browser.DownloadProgressStateInProgress})
	if state.items[id].State != "canceling" || state.items[id].Path != "" || len(state.cancel) != 1 {
		t.Fatal("oversized download accepted", state.items[id])
	}
	state.handle(&browser.EventDownloadProgress{GUID: id, State: browser.DownloadProgressStateCanceled})
	if state.items[id].State != "canceled" {
		t.Fatal("browser cancellation confirmation missing")
	}
	failedID := uuid.NewString()
	state.handle(&browser.EventDownloadWillBegin{GUID: failedID, URL: "https://example.test"})
	state.cancelFailed(failedID, errors.New("cancel command failed"))
	if state.items[failedID].State != "cancel_failed" || state.items[failedID].Error == "" {
		t.Fatal("cancel error falsely reported success", state.items[failedID])
	}
	id = uuid.NewString()
	state.handle(&browser.EventDownloadWillBegin{GUID: id, URL: "https://example.test"})
	state.handle(&browser.EventDownloadProgress{GUID: id, State: browser.DownloadProgressStateCompleted})
	if state.items[id].State != "failed" || state.items[id].Path != "" {
		t.Fatal("missing file was reported completed", state.items[id])
	}
}

func TestBrowserPersistentProfileOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	actual, unlock, err := prepareBrowserProfile(path)
	if err != nil || actual != path {
		t.Fatal("dedicated profile initialization failed", actual, err)
	}
	if _, _, err := prepareBrowserProfile(path); err == nil {
		t.Fatal("profile lock allowed concurrent use")
	}
	unlock()
	_, unlock, err = prepareBrowserProfile(path)
	if err != nil {
		t.Fatal("profile could not be reused", err)
	}
	unlock()
	daily := t.TempDir()
	if err := os.WriteFile(filepath.Join(daily, "Cookies"), []byte("daily-browser-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareBrowserProfile(daily); err == nil {
		t.Fatal("unmarked daily browser profile imported")
	}
	linked := filepath.Join(t.TempDir(), "profile-link")
	if err := os.Symlink(path, linked); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareBrowserProfile(linked); err == nil {
		t.Fatal("symlink profile accepted")
	}
}

func TestBrowserDownloadCancellationConfirmation(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(fmt.Sprint(confirmed), func(t *testing.T) {
			id := uuid.NewString()
			stopped := make(chan struct{}, 1)
			state := &browserDownloads{items: map[string]*browserDownload{id: {ID: id, State: "canceling", CancelRequested: true}}, notify: make(chan struct{}, 1), stop: func() { stopped <- struct{}{} }}
			if confirmed {
				state.items[id].State = "canceled"
			}
			state.confirmCancellation(context.Background(), id, time.Millisecond)
			if confirmed {
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
				select {
				case <-stopped:
					t.Fatal("confirmed cancellation stopped browser")
				case <-ctx.Done():
				}
			} else {
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Fatal("unconfirmed cancellation did not fail closed")
				}
				state.mu.Lock()
				defer state.mu.Unlock()
				if state.items[id].State != "cancel_failed" || state.items[id].Error == "" {
					t.Fatal("missing cancellation confirmation reported success")
				}
			}
		})
	}
}

func TestBrowserRejectedDownloadCancellation(t *testing.T) {
	id := uuid.NewString()
	stopped := make(chan struct{}, 1)
	state := &browserDownloads{dir: t.TempDir(), items: make(map[string]*browserDownload), notify: make(chan struct{}, 1), cancel: make(chan string, 1), validate: func(string) bool { return false }, stop: func() { stopped <- struct{}{} }}
	state.handle(&browser.EventDownloadWillBegin{GUID: id, URL: "data:blocked"})
	if <-state.cancel != id {
		t.Fatal("rejected download was not queued for cancellation")
	}
	state.handle(&browser.EventDownloadProgress{GUID: id, State: browser.DownloadProgressStateCanceled})
	state.cancelFailed(id, errors.New("late timeout"))
	select {
	case <-stopped:
		t.Fatal("confirmed rejected download stopped browser")
	default:
	}
	if len(state.pending) != 0 {
		t.Fatal("confirmed rejection kept pending cancellation")
	}
}

func TestBrowserPersistentLogin(t *testing.T) {
	if _, err := builtin.FindChrome(); err != nil {
		t.Skip("Chrome unavailable: ", err)
	}
	t.Setenv("AGENT_BROWSER_ENABLED", "false")
	t.Setenv("AGENT_BROWSER_PATH", "")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`<html><body>persistent login</body></html>`))
	}))
	defer server.Close()
	profile := filepath.Join(t.TempDir(), "profile")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for iteration := 0; iteration < 2; iteration++ {
		core := agentcore.New(agentcore.Config{})
		ext := NewBrowserToolsExtension(builtin.ExtensionConfig{Browser: &builtin.BrowserToolConfig{Headless: true, AllowPrivate: true}}, nil, BrowserOptions{ProfileDir: profile})
		if err := ext.Init(ctx, core); err != nil {
			t.Fatal(err)
		}
		if _, err := ext.run(ctx, json.RawMessage(fmt.Sprintf(`{"action":"navigate","url":%q}`, server.URL))); err != nil {
			t.Fatal(err)
		}
		expression := `document.cookie='covo_login=persisted; Path=/; Max-Age=3600; SameSite=Lax'; localStorage.setItem('login','persisted'); 'saved'`
		if iteration == 1 {
			expression = `document.cookie+'|'+localStorage.getItem('login')`
		}
		args, _ := json.Marshal(map[string]any{"action": "evaluate", "expression": expression})
		result, err := ext.run(ctx, args)
		if err != nil || (iteration == 1 && !strings.Contains(fmt.Sprint(result), "covo_login=persisted|persisted")) {
			t.Fatal("login state did not survive browser restart", result, err)
		}
		if err := ext.Dispose(); err != nil {
			t.Fatal(err)
		}
		core.Close()
	}
}

func browserTestHumanConfirmation(t *testing.T, ctx context.Context, ext *BrowserToolsExtension) {
	t.Helper()
	dialogOpened := make(chan struct{}, 1)
	dialogClosed := make(chan struct{}, 1)
	chromedp.ListenTarget(ext.page, func(event any) {
		switch event.(type) {
		case *page.EventJavascriptDialogOpening:
			select {
			case dialogOpened <- struct{}{}:
			default:
			}
		case *page.EventJavascriptDialogClosed:
			select {
			case dialogClosed <- struct{}{}:
			default:
			}
		}
	})
	if err := chromedp.Run(ext.page, chromedp.Evaluate(`setTimeout(() => confirm('human-controlled confirmation'), 0); 'queued'`, nil)); err != nil {
		t.Fatal("could not open human confirmation", err)
	}
	select {
	case <-dialogOpened:
	case <-ctx.Done():
		t.Fatal("human confirmation did not open")
	}
	select {
	case <-dialogClosed:
		t.Fatal("automatic dialog policy interfered with human control")
	case <-time.After(300 * time.Millisecond):
	}
	inputs := []map[string]any{{"action": "resume"}}
	dialogTab := chromedp.FromContext(ext.page).Target.TargetID
	for id := range ext.tabs {
		inputs = append(inputs, map[string]any{"action": "resume", "tab_id": id})
	}
	for _, input := range inputs {
		args, _ := json.Marshal(input)
		requestCtx, cancel := context.WithTimeout(ctx, time.Second)
		pending, err := ext.run(requestCtx, args)
		cancel()
		if err != nil || pending.(map[string]any)["status"] != "dialog_required" || pending.(map[string]any)["tab_id"] != dialogTab || !ext.humanPaused {
			t.Fatal("resume bypassed pending human confirmation", input, pending, err)
		}
		if chromedp.FromContext(ext.page).Target.TargetID != dialogTab {
			t.Fatal("resume switched away from an unhandled human confirmation")
		}
	}
	if err := chromedp.Run(ext.page, page.HandleJavaScriptDialog(false)); err != nil {
		t.Fatal("external human confirmation could not be dismissed", err)
	}
	select {
	case <-dialogClosed:
	case <-ctx.Done():
		t.Fatal("human confirmation dismissal was not observed")
	}
}

func TestBrowserCrossOriginLogin(t *testing.T) {
	if _, err := builtin.FindChrome(); err != nil {
		t.Skip("Chrome unavailable: ", err)
	}
	t.Setenv("AGENT_BROWSER_ENABLED", "false")
	t.Setenv("AGENT_BROWSER_PATH", "")
	for _, headed := range []bool{false, true} {
		t.Run(fmt.Sprintf("headed=%v", headed), func(t *testing.T) {
			if headed && os.Getenv("COVO_BROWSER_HEADED_TEST") != "1" {
				t.Skip("set COVO_BROWSER_HEADED_TEST=1 with a desktop display for visible handoff smoke test")
			}
			var appURL string
			identity := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = fmt.Fprintf(writer, `<html><body><button id="authorize" onclick="location.href='%s/callback'">Authorize local test</button></body></html>`, appURL)
			}))
			defer identity.Close()
			app := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "text/html")
				if request.URL.Path == "/callback" {
					http.SetCookie(writer, &http.Cookie{Name: "app_login", Value: "accepted", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
					_, _ = fmt.Fprintf(writer, `<script>opener.postMessage('signed-in',%q); window.close();</script>`, appURL)
					return
				}
				if request.URL.Path == "/me" {
					cookie, err := request.Cookie("app_login")
					if err != nil || cookie.Value != "accepted" {
						http.Error(writer, "not authenticated", http.StatusUnauthorized)
						return
					}
					_, _ = writer.Write([]byte("logged-in"))
					return
				}
				_, _ = fmt.Fprintf(writer, `<html><body><button onclick="window.open('%s/login')">Login</button><div id="status">anonymous</div><script>addEventListener('message',event=>{if(event.origin===location.origin&&event.data==='signed-in')fetch('/me').then(r=>r.text()).then(text=>{document.querySelector('#status').textContent=text;document.querySelector('#status').className='ready';});});</script></body></html>`, identity.URL)
			}))
			appURL = app.URL
			defer app.Close()
			core := agentcore.New(agentcore.Config{})
			defer core.Close()
			ext := NewBrowserToolsExtension(builtin.ExtensionConfig{Browser: &builtin.BrowserToolConfig{Headless: !headed, AllowPrivate: true, DialogPolicy: builtin.DialogAutoDismiss}}, nil)
			defer ext.Dispose()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if err := ext.Init(ctx, core); err != nil {
				t.Fatal(err)
			}
			invoke := func(input map[string]any) any {
				t.Helper()
				args, _ := json.Marshal(input)
				result, err := ext.run(ctx, args)
				if err != nil {
					t.Fatal(input, err)
				}
				return result
			}
			invoke(map[string]any{"action": "navigate", "url": appURL})
			original := chromedp.FromContext(ext.page).Target.TargetID
			invoke(map[string]any{"action": "evaluate", "expression": `document.querySelector('button').click(); 'opened'`})
			var popup target.ID
			for popup == "" {
				tabs := invoke(map[string]any{"action": "tabs"}).(map[string]any)["tabs"].([]map[string]any)
				for _, tab := range tabs {
					if strings.HasPrefix(tab["url"].(string), identity.URL) {
						popup = tab["tab_id"].(target.ID)
					}
				}
				if popup == "" {
					select {
					case <-ctx.Done():
						t.Fatal("cross-origin login popup did not appear")
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
			invoke(map[string]any{"action": "tab_switch", "tab_id": popup})
			invoke(map[string]any{"action": "wait", "selector": "#authorize"})
			if headed {
				invoke(map[string]any{"action": "handoff"})
				if !ext.humanPaused {
					t.Fatal("handoff did not pause automation")
				}
				for _, action := range []string{"evaluate", "click", "tab_switch", "download"} {
					args, _ := json.Marshal(map[string]any{"action": action, "expression": "true", "tab_id": original})
					if _, err := ext.run(ctx, args); err == nil {
						t.Fatal("automation ran during human control", action)
					}
				}
				browserTestHumanConfirmation(t, ctx, ext)
				popupClosed := make(chan struct{}, 1)
				chromedp.ListenBrowser(ext.driver, func(event any) {
					if message, ok := event.(*target.EventTargetDestroyed); ok && message.TargetID == popup {
						select {
						case popupClosed <- struct{}{}:
						default:
						}
					}
				})
				if err := chromedp.Run(ext.page, chromedp.Evaluate(`document.querySelector('#authorize').click()`, nil)); err != nil {
					t.Fatal("simulated external human interaction failed", err)
				}
				select {
				case <-popupClosed:
				case <-ctx.Done():
					t.Fatal("completed login popup did not close")
				}
				if _, err := ext.run(ctx, json.RawMessage(`{"action":"resume"}`)); err == nil || !ext.humanPaused {
					t.Fatal("resume claimed success on a closed popup", err)
				}
				invoke(map[string]any{"action": "resume", "tab_id": original})
			} else {
				if _, err := ext.run(ctx, json.RawMessage(`{"action":"handoff"}`)); err == nil {
					t.Fatal("headless session advertised visible human handoff")
				}
				invoke(map[string]any{"action": "evaluate", "expression": `document.querySelector('#authorize').click(); 'authorized'`})
				invoke(map[string]any{"action": "tab_switch", "tab_id": original})
			}
			invoke(map[string]any{"action": "wait", "selector": "#status.ready"})
			result := invoke(map[string]any{"action": "evaluate", "expression": `document.querySelector('#status').textContent`})
			if !strings.Contains(fmt.Sprint(result), "logged-in") {
				t.Fatal("cross-origin popup did not complete authenticated login", result)
			}
		})
	}
}
