package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covo-agent/internal/security"
	"github.com/covoyage/covo-agent/internal/tools/capability"
	toolsmedia "github.com/covoyage/covo-agent/internal/tools/media"
	"github.com/covoyage/covonaut/agentcore"
	builtin "github.com/covoyage/covonaut/tools"
)

type BrowserToolsExtension struct {
	cfg             builtin.ExtensionConfig
	analyze         toolsmedia.ImageAnalyzer
	base            *builtin.Extension
	agent           *agentcore.Agent
	delegate        func(context.Context, json.RawMessage) (any, error)
	mu              sync.Mutex
	driver          context.Context
	driverCancel    context.CancelFunc
	page            context.Context
	dir             string
	consoleMu       sync.Mutex
	console         []map[string]string
	refs            *builtin.RefMapper
	dialogMu        sync.Mutex
	dialog          *page.EventJavascriptDialogOpening
	current         *browserTab
	tabs            map[target.ID]*browserTab
	uploadBytes     int64
	ownerMu         sync.Mutex
	owned           map[target.ID]*target.Info
	frameOrigins    map[cdp.FrameID]string
	downloads       *browserDownloads
	profileDir      string
	profileUnlock   func()
	dialogControlMu sync.Mutex
	humanPaused     bool
}

type browserTab struct {
	ctx     context.Context
	cancel  context.CancelFunc
	refs    *builtin.RefMapper
	console []map[string]string
	dialog  *page.EventJavascriptDialogOpening
}

func NewBrowserToolsExtension(cfg builtin.ExtensionConfig, analyze toolsmedia.ImageAnalyzer, options ...BrowserOptions) *BrowserToolsExtension {
	ext := &BrowserToolsExtension{cfg: cfg, analyze: analyze}
	if len(options) != 0 {
		ext.profileDir = options[0].ProfileDir
	}
	return ext
}

func (e *BrowserToolsExtension) Name() string { return "builtin-tools" }

func (e *BrowserToolsExtension) Init(ctx context.Context, agent *agentcore.Agent) error {
	e.agent = agent
	return e.install(ctx, e.cfg)
}

func (e *BrowserToolsExtension) install(ctx context.Context, cfg builtin.ExtensionConfig) error {
	if e.native() {
		browser := builtin.NewBrowserTool(cfg.Browser)
		properties := browser.Parameters["properties"].(map[string]any)
		properties["headed"] = map[string]any{"type": "boolean", "description": "For the first local navigate only: show a visible automation window. Cannot change a live session or remote browser."}
		action := properties["action"].(map[string]any)
		action["enum"] = append(action["enum"].([]string), "status", "close", "handoff", "resume", "wait", "tabs", "tab_new", "tab_switch", "tab_close", "upload", "downloads_enable", "downloads", "download", "download_wait", "download_cancel")
		properties["selector"] = map[string]any{"type": "string", "description": "CSS selector to wait until visible (action=wait, bounded by the command timeout)."}
		properties["tab_id"] = map[string]any{"type": "string", "description": "Owned tab ID from action=tabs, required for tab_switch and tab_close."}
		properties["paths"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Workspace files to attach to a file input (action=upload)."}
		properties["download_id"] = map[string]any{"type": "string", "description": "Download ID from action=downloads, required for download_wait and download_cancel."}
		cfg.Browser = nil
		e.base = builtin.NewExtension(cfg)
		if err := e.base.Init(ctx, e.agent); err != nil {
			return err
		}
		e.delegate = e.interact
		browser.Func = e.run
		capability.DeclareBuiltin(browser)
		browser.Description += " Screenshots return a private PNG path readable with read. Vision performs actual image analysis. Console returns captured page messages. CDP supports only page-scoped diagnostic methods. User profiles are not imported; BROWSER_CDP_URL explicitly connects an external browser."
		e.agent.RegisterTools(browser)
		return nil
	}
	e.base = builtin.NewExtension(cfg)
	if err := e.base.Init(ctx, e.agent); err != nil {
		return err
	}
	for _, tool := range e.base.Tools() {
		if tool.Name == "browser" {
			e.delegate = tool.Func
			tool.Func = e.run
			capability.DeclareBuiltin(tool)
			tool.Description += " Screenshots return a private PNG path readable with read. Vision performs actual image analysis. Console returns captured page messages. CDP supports only page-scoped diagnostic methods. Use BROWSER_CDP_URL to explicitly connect a remote browser; existing user profiles are not imported."
		}
	}
	return nil
}

func (e *BrowserToolsExtension) Dispose() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reset()
	if e.dir != "" {
		return os.RemoveAll(e.dir)
	}
	return nil
}

func (e *BrowserToolsExtension) reset() {
	if e.driver != nil {
		if e.profileUnlock != nil && e.cfg.Browser.CDPURL == "" && e.driver.Err() == nil {
			ctx, cancel := context.WithTimeout(e.driver, 5*time.Second)
			_ = chromedp.Cancel(ctx)
			cancel()
		}
		e.ownerMu.Lock()
		var ids []target.ID
		for id := range e.owned {
			ids = append(ids, id)
		}
		e.owned = nil
		e.frameOrigins = nil
		e.ownerMu.Unlock()
		ctx, cancel := context.WithTimeout(e.driver, 5*time.Second)
		browser := chromedp.FromContext(e.driver).Browser
		if browser != nil {
			for _, id := range ids {
				_ = target.CloseTarget(id).Do(cdp.WithExecutor(ctx, browser))
			}
		}
		cancel()
	}
	if e.driverCancel != nil {
		e.driverCancel()
	}
	if e.profileUnlock != nil {
		e.profileUnlock()
		e.profileUnlock = nil
	}
	if e.base != nil {
		_ = e.base.Dispose()
	}
	e.page, e.driver = nil, nil
	e.driverCancel = nil
	e.downloads = nil
	e.setHumanPaused(false)
	e.consoleMu.Lock()
	e.dialogMu.Lock()
	e.console = nil
	e.dialog = nil
	e.current = nil
	e.tabs = nil
	e.dialogMu.Unlock()
	e.consoleMu.Unlock()
}

func (e *BrowserToolsExtension) native() bool {
	cfg := e.cfg.Browser
	return cfg != nil && (cfg.CDPURL != "" || (cfg.CamofoxURL == "" && !cfg.AgentBrowserEnabled && os.Getenv("AGENT_BROWSER_ENABLED") != "true" && os.Getenv("AGENT_BROWSER_PATH") == "" && cfg.Engine != "lightpanda" && (cfg.CloudProvider == "" || cfg.CloudProvider == "local")))
}

func (e *BrowserToolsExtension) start(ctx context.Context) error {
	if e.driver != nil {
		return nil
	}
	if e.dir == "" {
		dir, err := os.MkdirTemp("", "covo-browser-")
		if err != nil {
			return err
		}
		e.dir = dir
	}
	cfg := *e.cfg.Browser
	var alloc context.Context
	var allocCancel context.CancelFunc
	if cfg.CDPURL != "" {
		alloc, allocCancel = chromedp.NewRemoteAllocator(context.Background(), cfg.CDPURL)
	} else {
		chrome, err := builtin.FindChrome()
		if err != nil {
			return err
		}
		var profile string
		if e.profileDir == "" {
			profile, err = os.MkdirTemp(e.dir, "profile-")
		} else {
			profile, e.profileUnlock, err = prepareBrowserProfile(e.profileDir)
		}
		if err != nil {
			return err
		}
		width, height := cfg.ViewportWidth, cfg.ViewportHeight
		if width <= 0 {
			width = 1280
		}
		if height <= 0 {
			height = 800
		}
		opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome.Path), chromedp.UserDataDir(profile), chromedp.Flag("headless", cfg.Headless), chromedp.Flag("remote-debugging-address", "127.0.0.1"), chromedp.Flag("remote-debugging-port", "0"), chromedp.Flag("disable-blink-features", "AutomationControlled"), chromedp.WindowSize(width, height))
		if cfg.UserAgent != "" {
			opts = append(opts, chromedp.UserAgent(cfg.UserAgent))
		}
		if cfg.AcceptLanguage != "" {
			opts = append(opts, chromedp.Flag("accept-lang", cfg.AcceptLanguage))
		}
		if cfg.ProxyURL != "" {
			opts = append(opts, chromedp.ProxyServer(cfg.ProxyURL))
		}
		alloc, allocCancel = chromedp.NewExecAllocator(context.Background(), opts...)
	}
	if e.driver == nil {
		e.driver, e.driverCancel = chromedp.NewContext(alloc)
		if err := e.boot(ctx); err != nil {
			e.driverCancel()
			allocCancel()
			e.driver = nil
			if e.profileUnlock != nil {
				e.profileUnlock()
				e.profileUnlock = nil
			}
			return err
		}
	}
	pageCancel := e.driverCancel
	e.driverCancel = func() { pageCancel(); allocCancel() }
	e.tabs = make(map[target.ID]*browserTab)
	e.ownerMu.Lock()
	e.owned = map[target.ID]*target.Info{chromedp.FromContext(e.driver).Target.TargetID: {TargetID: chromedp.FromContext(e.driver).Target.TargetID, Type: "page", URL: "about:blank"}}
	e.frameOrigins = make(map[cdp.FrameID]string)
	e.ownerMu.Unlock()
	chromedp.ListenBrowser(e.driver, func(event any) {
		switch message := event.(type) {
		case *target.EventTargetCreated:
			e.rememberTab(message.TargetInfo)
		case *target.EventTargetInfoChanged:
			e.rememberTab(message.TargetInfo)
		}
	})
	if err := e.attachTab(ctx, e.driver, nil); err != nil {
		e.reset()
		return err
	}
	if cfg.CDPURL == "" {
		if err := e.withPage(ctx, e.driver, func(runCtx context.Context) error {
			return browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorDeny).Do(cdp.WithExecutor(runCtx, chromedp.FromContext(e.driver).Browser))
		}); err != nil {
			e.reset()
			return err
		}
	}
	return nil
}

func (e *BrowserToolsExtension) rememberTab(info *target.Info) {
	if info == nil || info.Type != "page" {
		return
	}
	e.ownerMu.Lock()
	defer e.ownerMu.Unlock()
	if e.owned == nil {
		return
	}
	_, known := e.owned[info.TargetID]
	_, child := e.owned[info.OpenerID]
	if known || (child && len(e.owned) < 32) {
		copy := *info
		e.owned[info.TargetID] = &copy
	}
}

func (e *BrowserToolsExtension) selectTab(tab *browserTab) {
	e.consoleMu.Lock()
	e.dialogMu.Lock()
	e.current = tab
	e.page, e.refs = tab.ctx, tab.refs
	e.console, e.dialog = tab.console, tab.dialog
	e.dialogMu.Unlock()
	e.consoleMu.Unlock()
}

func (e *BrowserToolsExtension) attachTab(ctx, pageCtx context.Context, cancel context.CancelFunc) error {
	if chromedp.FromContext(pageCtx).Target == nil {
		if err := e.bootContext(ctx, pageCtx, cancel); err != nil {
			return err
		}
	}
	tab := &browserTab{ctx: pageCtx, cancel: cancel, refs: builtin.NewRefMapper()}
	chromedp.ListenTarget(pageCtx, func(event any) {
		if message, ok := event.(*page.EventFrameNavigated); ok && message.Frame != nil {
			e.ownerMu.Lock()
			if e.frameOrigins != nil {
				e.frameOrigins[message.Frame.ID] = message.Frame.URL
			}
			e.ownerMu.Unlock()
		}
	})
	chromedp.ListenTarget(pageCtx, func(event any) { e.onTabConsole(tab, event) })
	chromedp.ListenTarget(pageCtx, func(event any) {
		e.onTabDialog(tab, event)
		policy := e.cfg.Browser.DialogPolicy
		if _, opening := event.(*page.EventJavascriptDialogOpening); opening && (policy == builtin.DialogAutoAccept || policy == builtin.DialogAutoDismiss) {
			safego.SafeGo(func() {
				e.dialogControlMu.Lock()
				defer e.dialogControlMu.Unlock()
				if e.humanPaused {
					return
				}
				ctx, cancel := context.WithTimeout(pageCtx, 5*time.Second)
				defer cancel()
				_ = chromedp.Run(ctx, page.HandleJavaScriptDialog(policy == builtin.DialogAutoAccept))
			}, nil)
		}
	})
	if err := e.withPage(ctx, pageCtx, func(runCtx context.Context) error { return chromedp.Run(runCtx, runtime.Enable()) }); err != nil {
		return err
	}
	if err := startBrowserNetworkCapture(pageCtx, e.cfg.Browser.NetworkObservation); err != nil {
		return err
	}
	if err := e.withPage(ctx, pageCtx, func(runCtx context.Context) error {
		return chromedp.Run(runCtx, chromedp.ActionFunc(func(actionCtx context.Context) error {
			tree, err := page.GetFrameTree().Do(actionCtx)
			if err != nil {
				return err
			}
			e.ownerMu.Lock()
			defer e.ownerMu.Unlock()
			var remember func(*page.FrameTree)
			remember = func(item *page.FrameTree) {
				if item != nil && item.Frame != nil {
					e.frameOrigins[item.Frame.ID] = item.Frame.URL
					for _, child := range item.ChildFrames {
						remember(child)
					}
				}
			}
			remember(tree)
			return nil
		}))
	}); err != nil {
		return err
	}
	id := chromedp.FromContext(pageCtx).Target.TargetID
	e.tabs[id] = tab
	e.ownerMu.Lock()
	e.owned[id] = &target.Info{TargetID: id, Type: "page", URL: "about:blank"}
	e.ownerMu.Unlock()
	e.selectTab(tab)
	return nil
}

func (e *BrowserToolsExtension) boot(ctx context.Context) error {
	return e.bootContext(ctx, e.driver, e.driverCancel)
}

func (e *BrowserToolsExtension) bootContext(ctx, persistent context.Context, stopPage context.CancelFunc) error {
	timeout := e.cfg.Browser.CommandTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	stop := context.AfterFunc(waitCtx, stopPage)
	err := chromedp.Run(persistent)
	if waitCtx.Err() != nil {
		err = waitCtx.Err()
	}
	stop()
	cancel()
	return err
}

func (e *BrowserToolsExtension) withPage(ctx, pageCtx context.Context, action func(context.Context) error) error {
	runCtx, cancel := context.WithCancel(pageCtx)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	timeout := e.cfg.Browser.CommandTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, timeoutCancel := context.WithTimeout(runCtx, timeout)
	defer timeoutCancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	err := action(runCtx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (e *BrowserToolsExtension) onConsole(event any) {
	e.onTabConsole(nil, event)
}

func (e *BrowserToolsExtension) onTabConsole(tab *browserTab, event any) {
	var level, text string
	switch message := event.(type) {
	case *runtime.EventConsoleAPICalled:
		level = string(message.Type)
		var values []string
		for _, arg := range message.Args {
			if len(arg.Value) != 0 {
				values = append(values, string(arg.Value))
			} else {
				values = append(values, arg.Description)
			}
		}
		text = strings.Join(values, " ")
	case *runtime.EventExceptionThrown:
		if message.ExceptionDetails == nil {
			return
		}
		level, text = "error", message.ExceptionDetails.Text
		if message.ExceptionDetails.Exception != nil {
			text += " " + message.ExceptionDetails.Exception.Description
		}
	default:
		return
	}
	e.consoleMu.Lock()
	defer e.consoleMu.Unlock()
	messages := &e.console
	if tab != nil {
		messages = &tab.console
	}
	if len(*messages) == 200 {
		*messages = (*messages)[1:]
	}
	*messages = append(*messages, map[string]string{"level": level, "text": security.RedactDiagnostic(text)})
	if tab != nil && tab == e.current {
		e.console = tab.console
	}
}

func (e *BrowserToolsExtension) run(ctx context.Context, args json.RawMessage) (any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var input struct {
		Action     string         `json:"action"`
		Question   string         `json:"question"`
		FullPage   bool           `json:"full_page"`
		Headed     *bool          `json:"headed"`
		URL        string         `json:"url"`
		CDPMethod  string         `json:"cdp_method"`
		CDPParams  map[string]any `json:"cdp_params"`
		TabID      target.ID      `json:"tab_id"`
		DownloadID string         `json:"download_id"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	advanced := input.Action == "screenshot" || input.Action == "vision" || input.Action == "console" || input.Action == "cdp"
	if !e.native() {
		if advanced {
			return nil, fmt.Errorf("action %s requires local Chromium or BROWSER_CDP_URL; this backend does not expose the page connection", input.Action)
		}
		return e.delegate(ctx, args)
	}
	if e.driver != nil && e.driver.Err() != nil {
		if e.downloads != nil && (input.Action == "downloads" || input.Action == "download_wait" || input.Action == "download_cancel") {
			return e.manageDownloads(ctx, input.Action, input.DownloadID, input.URL)
		}
		e.reset()
	}
	if input.Action == "navigate" && e.driver == nil {
		if _, err := normalizeBrowserURL(input.URL, e.cfg.Browser.AllowPrivate); err != nil {
			return nil, err
		}
	}
	if input.Headed != nil {
		if input.Action != "navigate" || e.driver != nil || e.cfg.Browser.CDPURL != "" {
			return nil, fmt.Errorf("headed can only be selected on the first local navigate; live and remote sessions are unchanged")
		}
		cfg := *e.cfg.Browser
		cfg.Headless = !*input.Headed
		e.cfg.Browser = &cfg
	}
	if input.Action == "status" {
		result := map[string]any{"active": e.page != nil, "headless": e.cfg.Browser.Headless, "remote": e.cfg.Browser.CDPURL != "", "persistent_profile": e.profileDir != "" && e.cfg.Browser.CDPURL == "", "human_control": e.humanPaused}
		if e.cfg.Browser.CDPURL != "" {
			result["headless"] = nil
		}
		if e.page != nil {
			result["tab_id"] = chromedp.FromContext(e.page).Target.TargetID
		}
		e.dialogMu.Lock()
		if e.dialog != nil {
			result["pending_dialog"] = map[string]any{"dialog_id": "current", "type": e.dialog.Type, "message": security.RedactDiagnostic(e.dialog.Message)}
		}
		e.dialogMu.Unlock()
		return result, nil
	}
	if input.Action == "close" {
		e.reset()
		return map[string]any{"closed": true}, nil
	}
	if input.Action == "handoff" {
		if e.page == nil {
			return nil, fmt.Errorf("no controlled page to hand off")
		}
		if e.cfg.Browser.CDPURL == "" && e.cfg.Browser.Headless {
			return nil, fmt.Errorf("human handoff requires a visible browser; select headed=true on the first navigate or explicitly connect a visible CDP browser")
		}
		e.setHumanPaused(true)
		return map[string]any{"status": "human_required", "automation_paused": true, "tab_id": chromedp.FromContext(e.page).Target.TargetID, "next_action": "Ask the user to complete the visible step. Only after user confirmation, request approved resume, optionally with the original tab_id if the login popup closed."}, nil
	}
	if input.Action == "resume" {
		if !e.humanPaused {
			return nil, fmt.Errorf("browser is not waiting for human handoff")
		}
		id := input.TabID
		if id == "" && e.page != nil {
			id = chromedp.FromContext(e.page).Target.TargetID
		}
		result, err := e.manageTabs(ctx, "resume", id, "")
		if output, ok := result.(map[string]any); ok && output["status"] == "dialog_required" {
			return result, err
		}
		if err == nil {
			e.setHumanPaused(false)
		}
		return result, err
	}
	if e.humanPaused {
		switch input.Action {
		case "tabs", "screenshot", "console", "downloads", "download_wait", "download_cancel":
		default:
			return nil, fmt.Errorf("browser automation is paused for human control; await user confirmation before approved resume")
		}
	}
	if input.Action == "tabs" || input.Action == "tab_new" || input.Action == "tab_switch" || input.Action == "tab_close" {
		return e.manageTabs(ctx, input.Action, input.TabID, input.URL)
	}
	if input.Action == "downloads_enable" || input.Action == "downloads" || input.Action == "download" || input.Action == "download_wait" || input.Action == "download_cancel" {
		return e.manageDownloads(ctx, input.Action, input.DownloadID, input.URL)
	}
	if input.Action != "dialog" {
		if pending := e.pendingDialog(); pending != nil {
			return pending, nil
		}
	}
	if input.Action == "navigate" && e.driver == nil {
		if err := e.start(ctx); err != nil {
			return nil, fmt.Errorf("start browser: %w", err)
		}
	}
	if advanced {
		if e.page == nil {
			return nil, fmt.Errorf("no controlled browser page; call browser action=navigate first")
		}
		switch input.Action {
		case "screenshot", "vision":
			if input.Action == "vision" && strings.TrimSpace(input.Question) == "" {
				return nil, fmt.Errorf("question is required for vision")
			}
			var data []byte
			err := e.withPage(ctx, e.page, func(runCtx context.Context) error {
				if input.FullPage {
					return chromedp.Run(runCtx, chromedp.FullScreenshot(&data, 100))
				}
				return chromedp.Run(runCtx, chromedp.CaptureScreenshot(&data))
			})
			if err != nil {
				return nil, err
			}
			question := ""
			if input.Action == "vision" {
				question = input.Question
			}
			return browserScreenshotResult(ctx, e.dir, data, question, e.analyze)
		case "console":
			e.consoleMu.Lock()
			messages := append([]map[string]string{}, e.console...)
			e.consoleMu.Unlock()
			return map[string]any{"messages": messages, "count": len(messages)}, nil
		case "cdp":
			if !browserCDPAllowed(input.CDPMethod) {
				return nil, fmt.Errorf("CDP method %q is not a page diagnostic; use browser actions for navigation and interaction", input.CDPMethod)
			}
			var response json.RawMessage
			err := e.withPage(ctx, e.page, func(runCtx context.Context) error {
				return chromedp.Run(runCtx, chromedp.ActionFunc(func(actionCtx context.Context) error {
					return cdp.Execute(actionCtx, input.CDPMethod, input.CDPParams, &response)
				}))
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"result": response}, nil
		}
	}
	result, err := e.delegate(ctx, args)
	return result, err
}

func (e *BrowserToolsExtension) setHumanPaused(paused bool) {
	e.dialogControlMu.Lock()
	defer e.dialogControlMu.Unlock()
	e.humanPaused = paused
}

func (e *BrowserToolsExtension) interact(ctx context.Context, args json.RawMessage) (any, error) {
	if e.page == nil {
		return nil, fmt.Errorf("no controlled browser page; call action=navigate first")
	}
	var input struct {
		Action     string   `json:"action"`
		URL        string   `json:"url"`
		Ref        string   `json:"ref"`
		Text       string   `json:"text"`
		Direction  string   `json:"direction"`
		Key        string   `json:"key"`
		Full       bool     `json:"full"`
		Mode       string   `json:"mode"`
		Expression string   `json:"expression"`
		FrameID    string   `json:"frame_id"`
		DialogID   string   `json:"dialog_id"`
		Accept     bool     `json:"accept"`
		PromptText string   `json:"prompt_text"`
		Selector   string   `json:"selector"`
		Paths      []string `json:"paths"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	var snapshot string
	var value any
	err := e.withPage(ctx, e.page, func(runCtx context.Context) error {
		var action chromedp.Action
		switch input.Action {
		case "navigate":
			parsed, err := normalizeBrowserURL(input.URL, e.cfg.Browser.AllowPrivate)
			if err != nil {
				return err
			}
			action = chromedp.Navigate(parsed)
		case "snapshot":
		case "wait":
			if strings.TrimSpace(input.Selector) == "" {
				return fmt.Errorf("selector is required for wait")
			}
			action = chromedp.WaitVisible(input.Selector, chromedp.ByQuery)
		case "click", "type", "upload":
			ref := "@" + strings.TrimPrefix(input.Ref, "@")
			xpath, found := e.refs.Get(ref)
			if !found {
				return fmt.Errorf("unknown element ref %q; request a fresh snapshot", input.Ref)
			}
			if input.Action == "upload" {
				if e.uploadBytes >= 128<<20 {
					return fmt.Errorf("session upload staging limit reached (128 MiB)")
				}
				workDir := e.cfg.WorkingDir
				if workDir == "" {
					workDir = "."
				}
				staged, count, err := stageBrowserUploads(ctx, workDir, e.dir, input.Paths)
				if err != nil {
					return err
				}
				folder := filepath.Dir(filepath.Dir(staged[0]))
				if e.uploadBytes+count > 128<<20 {
					_ = os.RemoveAll(folder)
					return fmt.Errorf("session upload staging limit exceeded (128 MiB)")
				}
				err = chromedp.Run(runCtx, chromedp.SetUploadFiles(xpath, staged, chromedp.BySearch))
				if err != nil {
					_ = os.RemoveAll(folder)
					return err
				}
				e.uploadBytes += count
				value = map[string]any{"files_attached": len(staged), "bytes": count, "submitted": false}
			} else if input.Action == "click" {
				action = chromedp.Click(xpath, chromedp.BySearch)
			} else {
				action = chromedp.Tasks{chromedp.Clear(xpath, chromedp.BySearch), chromedp.SendKeys(xpath, input.Text, chromedp.BySearch)}
			}
		case "scroll":
			amount := 1
			if input.Direction == "up" {
				amount = -1
			} else if input.Direction != "down" {
				return fmt.Errorf("scroll direction must be up or down")
			}
			action = chromedp.Evaluate(fmt.Sprintf("window.scrollBy(0, Math.round(innerHeight*0.8)*%d)", amount), nil)
		case "back":
			action = chromedp.NavigateBack()
		case "press":
			keys := map[string]string{"Enter": kb.Enter, "Tab": kb.Tab, "Escape": kb.Escape, "ArrowUp": kb.ArrowUp, "ArrowDown": kb.ArrowDown, "ArrowLeft": kb.ArrowLeft, "ArrowRight": kb.ArrowRight, "Backspace": kb.Backspace, "Delete": kb.Delete, "Home": kb.Home, "End": kb.End, "PageUp": kb.PageUp, "PageDown": kb.PageDown}
			key, found := keys[input.Key]
			if !found {
				return fmt.Errorf("unsupported browser key %q", input.Key)
			}
			action = chromedp.KeyEvent(key)
		case "evaluate":
			if strings.TrimSpace(input.Expression) == "" {
				return fmt.Errorf("expression is required")
			}
			return chromedp.Run(runCtx, chromedp.ActionFunc(func(actionCtx context.Context) error {
				params := runtime.Evaluate(input.Expression).WithReturnByValue(true).WithAwaitPromise(true)
				if input.FrameID != "" {
					contextID, err := page.CreateIsolatedWorld(cdp.FrameID(input.FrameID)).WithWorldName("covo-agent").Do(actionCtx)
					if err != nil {
						return err
					}
					params = params.WithContextID(contextID)
				}
				object, exception, err := params.Do(actionCtx)
				if err != nil {
					return err
				}
				if exception != nil {
					return fmt.Errorf("JavaScript evaluation failed: %s", security.RedactDiagnostic(exception.Text))
				}
				if object != nil && len(object.Value) != 0 {
					return json.Unmarshal(object.Value, &value)
				}
				return nil
			}))
		case "dialog":
			e.dialogMu.Lock()
			pending := e.dialog != nil
			e.dialogMu.Unlock()
			if !pending || input.DialogID != "current" {
				return fmt.Errorf("no matching dialog; use dialog_id=current from a snapshot")
			}
			return chromedp.Run(runCtx, page.HandleJavaScriptDialog(input.Accept).WithPromptText(input.PromptText))
		case "get_images":
			return chromedp.Run(runCtx, chromedp.Evaluate(`Array.from(document.images).slice(0,100).map(img => ({src:img.currentSrc||img.src,alt:img.alt,width:img.naturalWidth,height:img.naturalHeight}))`, &value))
		default:
			return fmt.Errorf("unsupported browser action %q", input.Action)
		}
		if action != nil {
			if err := chromedp.Run(runCtx, action); err != nil {
				return err
			}
		}
		var err error
		snapshot, err = builtin.GeneratePageSnapshot(runCtx, input.Full, e.refs, input.Mode)
		return err
	})
	if err != nil {
		if ctx.Err() == nil {
			if pending := e.pendingDialog(); pending != nil {
				return pending, nil
			}
		}
		return nil, fmt.Errorf("browser %s: %w", input.Action, err)
	}
	result := map[string]any{"snapshot": snapshot}
	if input.Action == "upload" {
		result["upload"] = value
	}
	if input.Action == "evaluate" || input.Action == "get_images" {
		result = map[string]any{"result": value}
	}
	e.dialogMu.Lock()
	if e.dialog != nil {
		result["pending_dialog"] = map[string]any{"dialog_id": "current", "type": e.dialog.Type, "message": security.RedactDiagnostic(e.dialog.Message)}
	}
	e.dialogMu.Unlock()
	return result, nil
}

func (e *BrowserToolsExtension) onDialog(event any) {
	e.onTabDialog(nil, event)
}

func (e *BrowserToolsExtension) onTabDialog(tab *browserTab, event any) {
	e.dialogMu.Lock()
	defer e.dialogMu.Unlock()
	dialog := &e.dialog
	if tab != nil {
		dialog = &tab.dialog
	}
	switch message := event.(type) {
	case *page.EventJavascriptDialogOpening:
		*dialog = message
	case *page.EventJavascriptDialogClosed:
		*dialog = nil
	}
	if tab != nil && tab == e.current {
		e.dialog = tab.dialog
	}
}

func (e *BrowserToolsExtension) manageTabs(ctx context.Context, action string, id target.ID, rawURL string) (any, error) {
	if e.driver == nil {
		return nil, fmt.Errorf("call navigate before managing tabs")
	}
	var targets []*target.Info
	if err := e.withPage(ctx, e.driver, func(runCtx context.Context) error {
		var err error
		targets, err = chromedp.Targets(runCtx)
		return err
	}); err != nil {
		return nil, err
	}
	for count := 0; count < len(targets); count++ {
		for _, info := range targets {
			e.rememberTab(info)
		}
	}
	e.ownerMu.Lock()
	owned := make(map[target.ID]*target.Info)
	for _, info := range targets {
		if _, ok := e.owned[info.TargetID]; ok && info.Type == "page" {
			owned[info.TargetID] = info
		}
	}
	e.ownerMu.Unlock()
	if action == "tabs" {
		items := make([]map[string]any, 0, len(owned))
		for _, info := range owned {
			items = append(items, map[string]any{"tab_id": info.TargetID, "url": security.RedactDiagnostic(info.URL), "title": security.RedactDiagnostic(info.Title), "active": e.page != nil && info.TargetID == chromedp.FromContext(e.page).Target.TargetID})
		}
		sort.Slice(items, func(first, second int) bool {
			return fmt.Sprint(items[first]["tab_id"]) < fmt.Sprint(items[second]["tab_id"])
		})
		return map[string]any{"tabs": items}, nil
	}
	if action == "tab_new" {
		if len(owned) >= 32 {
			return nil, fmt.Errorf("browser tab limit reached (32)")
		}
		parsed, err := normalizeBrowserURL(rawURL, e.cfg.Browser.AllowPrivate)
		if err != nil {
			return nil, err
		}
		pageCtx, cancel := chromedp.NewContext(e.driver)
		if err := e.bootContext(ctx, pageCtx, cancel); err != nil {
			cancel()
			return nil, err
		}
		if err := e.attachTab(ctx, pageCtx, cancel); err != nil {
			cancel()
			return nil, err
		}
		args, _ := json.Marshal(map[string]string{"action": "navigate", "url": parsed})
		result, err := e.interact(ctx, args)
		if output, ok := result.(map[string]any); ok {
			output["tab_id"] = chromedp.FromContext(pageCtx).Target.TargetID
		}
		return result, err
	}
	info, allowed := owned[id]
	if !allowed || id == "" {
		return nil, fmt.Errorf("tab_id is not an agent-owned live page")
	}
	if action == "tab_close" {
		if err := e.withPage(ctx, e.driver, func(runCtx context.Context) error {
			return target.CloseTarget(id).Do(cdp.WithExecutor(runCtx, chromedp.FromContext(e.driver).Browser))
		}); err != nil {
			return nil, err
		}
		if tab := e.tabs[id]; tab != nil && tab.cancel != nil {
			tab.cancel()
		}
		delete(e.tabs, id)
		e.ownerMu.Lock()
		delete(e.owned, id)
		e.ownerMu.Unlock()
		if len(owned) == 1 {
			e.reset()
			return map[string]any{"closed": true, "tab_id": id}, nil
		}
		if e.page != nil && chromedp.FromContext(e.page).Target.TargetID == id {
			e.page = nil
			for candidate := range owned {
				if candidate != id {
					if _, err := e.manageTabs(ctx, "tab_switch", candidate, ""); err != nil {
						return nil, err
					}
					break
				}
			}
		}
		return map[string]any{"closed": true, "tab_id": id}, nil
	}
	if action == "resume" {
		e.dialogMu.Lock()
		var pending map[string]any
		for candidate := range owned {
			tab := e.tabs[candidate]
			if tab != nil && tab.dialog != nil {
				pending = map[string]any{"status": "dialog_required", "action_outcome": "unverified", "tab_id": candidate, "pending_dialog": map[string]any{"type": tab.dialog.Type, "message": security.RedactDiagnostic(tab.dialog.Message)}, "next_action": "Finish the dialog in the visible browser, then request resume again. Automation remains paused."}
				break
			}
		}
		e.dialogMu.Unlock()
		if pending != nil {
			return pending, nil
		}
	}
	if info.URL != "about:blank" {
		if _, err := normalizeBrowserURL(info.URL, e.cfg.Browser.AllowPrivate); err != nil {
			return nil, err
		}
	}
	tab := e.tabs[id]
	if tab == nil {
		pageCtx, cancel := chromedp.NewContext(e.driver, chromedp.WithTargetID(id))
		if err := e.attachTab(ctx, pageCtx, cancel); err != nil {
			cancel()
			return nil, err
		}
		tab = e.tabs[id]
	}
	e.selectTab(tab)
	tab.refs.Clear()
	if err := e.withPage(ctx, tab.ctx, func(runCtx context.Context) error {
		return target.ActivateTarget(id).Do(cdp.WithExecutor(runCtx, chromedp.FromContext(e.driver).Browser))
	}); err != nil {
		return nil, err
	}
	result, err := e.interact(ctx, json.RawMessage(`{"action":"snapshot"}`))
	if output, ok := result.(map[string]any); ok {
		output["tab_id"] = id
	}
	return result, err
}

func (e *BrowserToolsExtension) pendingDialog() map[string]any {
	e.dialogMu.Lock()
	defer e.dialogMu.Unlock()
	if e.dialog == nil {
		return nil
	}
	return map[string]any{"status": "dialog_required", "action_outcome": "unverified", "pending_dialog": map[string]any{"dialog_id": "current", "type": e.dialog.Type, "message": security.RedactDiagnostic(e.dialog.Message)}, "next_action": "Handle dialog_id=current, then inspect a fresh snapshot before retrying; do not duplicate the previous action automatically."}
}

var browserSecretToken = regexp.MustCompile(`(?i)(sk-[a-zA-Z0-9]{20,}|ghp_[a-zA-Z0-9]{36})`)

func normalizeBrowserURL(raw string, allowPrivate bool) (string, error) {
	if browserSecretToken.MatchString(raw) {
		return "", fmt.Errorf("browser URL contains a secret token")
	}
	value, err := normalizeOpenURL(raw)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	ip := net.ParseIP(host)
	metadataIP := ip != nil && (ip.Equal(net.ParseIP("169.254.169.254")) || ip.Equal(net.ParseIP("169.254.170.2")))
	if parsed.User != nil || metadataIP || host == "metadata.google.internal" || host == "metadata.azure.com" || strings.HasSuffix(host, ".internal") {
		return "", fmt.Errorf("browser URL contains credentials or a blocked metadata host")
	}
	for name := range parsed.Query() {
		switch strings.ToLower(strings.ReplaceAll(name, "-", "_")) {
		case "api_key", "apikey", "secret_key", "secretkey", "access_token", "auth_token", "password", "passwd":
			return "", fmt.Errorf("browser URL contains a secret query parameter")
		}
	}
	if !allowPrivate {
		if host == "localhost" || strings.HasSuffix(host, ".localhost") || (ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())) {
			return "", fmt.Errorf("private browser URLs require BROWSER_ALLOW_PRIVATE_URLS=true")
		}
	}
	return value, nil
}

func browserCDPAllowed(method string) bool {
	switch method {
	case "Page.captureScreenshot", "Page.getLayoutMetrics", "Page.getFrameTree", "DOM.getDocument", "DOM.querySelector", "DOM.describeNode", "DOM.getBoxModel", "Accessibility.getFullAXTree", "Runtime.evaluate":
		return true
	default:
		return false
	}
}

func browserScreenshotResult(ctx context.Context, dir string, data []byte, question string, analyze toolsmedia.ImageAnalyzer) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > 4<<20 {
		return nil, fmt.Errorf("browser screenshot exceeds 4 MiB; use a viewport capture")
	}
	size, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid browser screenshot: %w", err)
	}
	file, err := os.CreateTemp(dir, "screenshot-*.png")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("save browser screenshot: %w", errors.Join(writeErr, closeErr))
	}
	result := map[string]any{"path": path, "format": "png", "size_bytes": len(data), "width": size.Width, "height": size.Height}
	if question != "" {
		analysis, err := toolsmedia.AnalyzeImages(ctx, toolsmedia.AnalyzeImagesInput{Path: path, Prompt: question, Analyze: analyze})
		if err != nil {
			return nil, fmt.Errorf("browser vision: %w", err)
		}
		result["analysis"] = analysis
	}
	return result, nil
}

func stageBrowserUploads(ctx context.Context, workDir, dir string, paths []string) (staged []string, total int64, err error) {
	if len(paths) == 0 || len(paths) > 16 {
		return nil, 0, fmt.Errorf("upload requires 1 to 16 workspace files")
	}
	base, err := filepath.Abs(workDir)
	if err != nil {
		return nil, 0, err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	stage, err := os.MkdirTemp(dir, "upload-")
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(stage)
		}
	}()
	for index, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		rel := path
		if filepath.IsAbs(path) {
			rel, err = filepath.Rel(base, path)
			if err != nil {
				return nil, 0, err
			}
		}
		if !filepath.IsLocal(rel) {
			return nil, 0, fmt.Errorf("upload paths must be inside the workspace")
		}
		if info, statErr := root.Stat(rel); statErr != nil || !info.Mode().IsRegular() {
			return nil, 0, fmt.Errorf("upload must be a regular workspace file")
		}
		input, err := root.Open(rel)
		if err != nil {
			return nil, 0, fmt.Errorf("open workspace upload: %w", err)
		}
		info, err := input.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<20 || total+info.Size() > 64<<20 {
			_ = input.Close()
			return nil, 0, fmt.Errorf("uploads must be regular files, at most 32 MiB each and 64 MiB combined")
		}
		folder := filepath.Join(stage, fmt.Sprintf("%d", index))
		if err := os.Mkdir(folder, 0700); err != nil {
			_ = input.Close()
			return nil, 0, err
		}
		outputPath := filepath.Join(folder, filepath.Base(rel))
		output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = input.Close()
			return nil, 0, err
		}
		count, copyErr := io.Copy(output, io.LimitReader(input, (32<<20)+1))
		closeErr := errors.Join(input.Close(), output.Close())
		if copyErr != nil || closeErr != nil {
			return nil, 0, errors.Join(copyErr, closeErr)
		}
		total += count
		if count > 32<<20 || total > 64<<20 {
			return nil, 0, fmt.Errorf("upload size limit exceeded")
		}
		staged = append(staged, outputPath)
	}
	return staged, total, nil
}
