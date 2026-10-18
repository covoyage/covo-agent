package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covo-agent/internal/security"
	"github.com/google/uuid"
)

type browserDownload struct {
	ID              string `json:"download_id"`
	Filename        string `json:"suggested_filename"`
	URL             string `json:"url"`
	State           string `json:"state"`
	Bytes           int64  `json:"bytes"`
	Path            string `json:"path,omitempty"`
	Error           string `json:"error,omitempty"`
	CancelRequested bool   `json:"cancel_requested,omitempty"`
}

type browserDownloads struct {
	mu        sync.Mutex
	dir       string
	items     map[string]*browserDownload
	notify    chan struct{}
	cancel    chan string
	validate  func(string) bool
	authorize func(cdp.FrameID, string) bool
	stop      context.CancelFunc
	jobs      map[string]context.CancelFunc
	pending   map[string]struct{}
}

func (state *browserDownloads) handle(event any) {
	state.mu.Lock()
	defer state.mu.Unlock()
	reject := func(id string) {
		if state.items[id] == nil {
			if state.pending == nil {
				state.pending = make(map[string]struct{})
			}
			if len(state.pending) < 64 {
				state.pending[id] = struct{}{}
			} else {
				state.failClosedLocked("download cancellation tracking overflow")
				return
			}
		}
		select {
		case state.cancel <- id:
		default:
			state.failClosedLocked("download cancellation queue overflow")
		}
	}
	switch message := event.(type) {
	case *browser.EventDownloadWillBegin:
		parsed, err := uuid.Parse(message.GUID)
		if err != nil || parsed.String() != message.GUID {
			return
		}
		if len(state.items) >= 32 || !state.validate(message.URL) || (state.authorize != nil && !state.authorize(message.FrameID, message.URL)) {
			reject(message.GUID)
			return
		}
		state.items[message.GUID] = &browserDownload{ID: message.GUID, Filename: security.RedactDiagnostic(filepath.Base(message.SuggestedFilename)), URL: security.RedactDiagnostic(message.URL), State: "inProgress"}
	case *browser.EventDownloadProgress:
		item := state.items[message.GUID]
		if item == nil {
			if _, pending := state.pending[message.GUID]; pending && (message.State == browser.DownloadProgressStateCanceled || message.State == browser.DownloadProgressStateCompleted) {
				delete(state.pending, message.GUID)
				_ = os.Remove(filepath.Join(state.dir, message.GUID))
				_ = os.Remove(filepath.Join(state.dir, message.GUID+".crdownload"))
			}
			return
		}
		if browserDownloadTerminal(item.State) {
			return
		}
		item.Bytes = int64(message.ReceivedBytes)
		var total int64
		for _, download := range state.items {
			total += download.Bytes
		}
		if !item.CancelRequested && (message.TotalBytes > 32<<20 || item.Bytes > 32<<20 || total > 128<<20) {
			item.State, item.Error = "canceling", "download size limit exceeded (32 MiB per file, 128 MiB per session)"
			item.CancelRequested = true
			reject(item.ID)
		}
		if item.CancelRequested && message.State == browser.DownloadProgressStateInProgress {
			item.State = "canceling"
		} else {
			item.State = string(message.State)
			if item.CancelRequested && item.Error != "" && item.State == "completed" {
				item.State = "rejected"
				_ = os.Remove(filepath.Join(state.dir, item.ID))
			}
			if item.State == "canceled" {
				_ = os.Remove(filepath.Join(state.dir, item.ID))
				_ = os.Remove(filepath.Join(state.dir, item.ID+".crdownload"))
			}
			if item.State == "completed" {
				root, err := os.OpenRoot(state.dir)
				if err == nil {
					file, openErr := root.Open(item.ID)
					if openErr == nil {
						info, statErr := file.Stat()
						if statErr != nil || !info.Mode().IsRegular() || info.Size() > 32<<20 {
							err = fmt.Errorf("download is not a valid bounded file")
						} else {
							err = file.Chmod(0600)
							item.Bytes = info.Size()
						}
						_ = file.Close()
					} else {
						err = openErr
					}
					_ = root.Close()
				}
				if err != nil {
					item.State, item.Error = "failed", security.ErrorSummary(err)
				} else {
					item.Path = filepath.Join(state.dir, item.ID)
				}
			}
		}
	default:
		return
	}
	select {
	case state.notify <- struct{}{}:
	default:
	}
}

func browserDownloadTerminal(value string) bool {
	return value == "completed" || value == "canceled" || value == "failed" || value == "cancel_failed" || value == "rejected"
}

func (state *browserDownloads) cancelFailed(id string, err error) {
	state.mu.Lock()
	if state.items[id] == nil {
		if _, pending := state.pending[id]; !pending {
			state.mu.Unlock()
			return
		}
	}
	if item := state.items[id]; item != nil && browserDownloadTerminal(item.State) {
		state.mu.Unlock()
		return
	}
	state.failClosedLocked(security.ErrorSummary(err))
	state.mu.Unlock()
}

func (state *browserDownloads) failClosedLocked(reason string) {
	for _, item := range state.items {
		if !browserDownloadTerminal(item.State) {
			item.State, item.Error = "cancel_failed", reason
		}
	}
	select {
	case state.notify <- struct{}{}:
	default:
	}
	if state.stop != nil {
		safego.SafeGo(state.stop, nil)
		state.stop = nil
	}
}

func (state *browserDownloads) confirmCancellation(ctx context.Context, id string, timeout time.Duration) {
	safego.SafeGo(func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			state.cancelFailed(id, fmt.Errorf("browser closed before download cancellation confirmation"))
		case <-timer.C:
			state.cancelFailed(id, fmt.Errorf("browser did not confirm download cancellation before deadline"))
		}
	}, nil)
}

func (e *BrowserToolsExtension) manageDownloads(ctx context.Context, action, id, rawURL string) (any, error) {
	if e.driver == nil {
		return nil, fmt.Errorf("call navigate before enabling downloads")
	}
	if action == "downloads_enable" && e.downloads == nil {
		dir, err := os.MkdirTemp(e.dir, "downloads-")
		if err != nil {
			return nil, err
		}
		state := &browserDownloads{dir: dir, items: make(map[string]*browserDownload), jobs: make(map[string]context.CancelFunc), notify: make(chan struct{}, 1), cancel: make(chan string, 64), stop: e.driverCancel, validate: func(raw string) bool {
			_, err := normalizeBrowserDownloadURL(raw, e.cfg.Browser.AllowPrivate)
			return err == nil
		}}
		state.authorize = e.authorizeDownloadFrame
		if e.cfg.Browser.CDPURL != "" {
			e.downloads = state
			return map[string]any{"enabled": true, "mode": "explicit", "next_action": "Use download with an absolute URL; native link clicks remain managed by the external browser."}, nil
		}
		chromedp.ListenBrowser(e.driver, state.handle)
		err = e.withPage(ctx, e.driver, func(runCtx context.Context) error {
			return browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllowAndName).WithDownloadPath(dir).WithEventsEnabled(true).Do(cdp.WithExecutor(runCtx, chromedp.FromContext(e.driver).Browser))
		})
		if err != nil {
			e.reset()
			_ = os.RemoveAll(dir)
			return nil, err
		}
		e.downloads = state
		driver := e.driver
		safego.SafeGo(func() {
			for {
				select {
				case <-driver.Done():
					return
				case guid := <-state.cancel:
					cancelCtx, cancel := context.WithTimeout(driver, 5*time.Second)
					err := browser.CancelDownload(guid).Do(cdp.WithExecutor(cancelCtx, chromedp.FromContext(driver).Browser))
					cancel()
					if err != nil {
						state.cancelFailed(guid, err)
					} else {
						state.confirmCancellation(driver, guid, 5*time.Second)
					}
				}
			}
		}, nil)
	}
	state := e.downloads
	if state == nil {
		return nil, fmt.Errorf("downloads are disabled; enable explicitly with downloads_enable")
	}
	if action == "downloads_enable" {
		mode := "native"
		if e.cfg.Browser.CDPURL != "" {
			mode = "explicit"
		}
		return map[string]any{"enabled": true, "mode": mode}, nil
	}
	if action == "download" {
		return e.startExplicitDownload(ctx, state, rawURL)
	}
	if action == "downloads" {
		state.mu.Lock()
		items := make([]browserDownload, 0, len(state.items))
		for _, item := range state.items {
			items = append(items, *item)
		}
		state.mu.Unlock()
		sort.Slice(items, func(first, second int) bool { return items[first].ID < items[second].ID })
		return map[string]any{"downloads": items}, nil
	}
	state.mu.Lock()
	item := state.items[id]
	state.mu.Unlock()
	if item == nil {
		return nil, fmt.Errorf("unknown download_id")
	}
	if action == "download_cancel" {
		state.mu.Lock()
		if browserDownloadTerminal(item.State) {
			copy := *item
			state.mu.Unlock()
			return copy, nil
		}
		item.CancelRequested, item.State = true, "canceling"
		cancelJob := state.jobs[id]
		state.mu.Unlock()
		if cancelJob != nil {
			cancelJob()
			return map[string]any{"download_id": id, "cancel_requested": true}, nil
		}
		if err := e.withPage(ctx, e.driver, func(runCtx context.Context) error {
			return browser.CancelDownload(id).Do(cdp.WithExecutor(runCtx, chromedp.FromContext(e.driver).Browser))
		}); err != nil {
			state.cancelFailed(id, err)
			return nil, err
		}
		state.confirmCancellation(e.driver, id, 5*time.Second)
		return map[string]any{"download_id": id, "cancel_requested": true}, nil
	}
	timeout := e.cfg.Browser.CommandTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		state.mu.Lock()
		copy := *item
		state.mu.Unlock()
		if browserDownloadTerminal(copy.State) {
			return copy, nil
		}
		select {
		case <-waitCtx.Done():
			return nil, waitCtx.Err()
		case <-state.notify:
		}
	}
}

func (e *BrowserToolsExtension) startExplicitDownload(ctx context.Context, state *browserDownloads, raw string) (any, error) {
	parsed, err := normalizeBrowserDownloadURL(raw, e.cfg.Browser.AllowPrivate)
	if err != nil {
		return nil, err
	}
	if e.page == nil {
		return nil, fmt.Errorf("select a controlled page before downloading")
	}
	var frame cdp.FrameID
	if err := e.withPage(ctx, e.page, func(runCtx context.Context) error {
		return chromedp.Run(runCtx, chromedp.ActionFunc(func(actionCtx context.Context) error {
			tree, err := page.GetFrameTree().Do(actionCtx)
			if err == nil && tree != nil && tree.Frame != nil {
				frame = tree.Frame.ID
			}
			return err
		}))
	}); err != nil {
		return nil, err
	}
	if !state.authorize(frame, parsed) {
		return nil, fmt.Errorf("download does not belong to the selected page origin")
	}
	state.mu.Lock()
	if len(state.items) >= 32 {
		state.mu.Unlock()
		return nil, fmt.Errorf("download limit reached (32)")
	}
	id := uuid.NewString()
	item := &browserDownload{ID: id, URL: security.RedactDiagnostic(parsed), State: "inProgress"}
	state.items[id] = item
	timeout := e.cfg.Browser.CommandTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	jobCtx, cancel := context.WithTimeout(e.page, timeout)
	state.jobs[id] = cancel
	state.mu.Unlock()
	pageCtx := e.page
	safego.SafeGo(func() {
		defer cancel()
		err := streamBrowserDownload(jobCtx, pageCtx, state, item, parsed)
		state.mu.Lock()
		delete(state.jobs, id)
		if err != nil && !browserDownloadTerminal(item.State) {
			item.Path = ""
			if errors.Is(err, context.Canceled) && item.CancelRequested {
				item.State = "canceled"
			} else {
				item.State, item.Error = "failed", security.ErrorSummary(err)
			}
		}
		state.mu.Unlock()
		select {
		case state.notify <- struct{}{}:
		default:
		}
	}, nil)
	return map[string]any{"download_id": id, "state": "inProgress"}, nil
}

func streamBrowserDownload(ctx, pageCtx context.Context, state *browserDownloads, item *browserDownload, raw string) (resultErr error) {
	encoded, _ := json.Marshal(raw)
	var objectID runtime.RemoteObjectID
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		object, exception, err := runtime.Evaluate(`({controller:new AbortController()})`).Do(actionCtx)
		if err != nil {
			return err
		}
		if exception != nil || object == nil || object.ObjectID == "" {
			return fmt.Errorf("cannot create browser download reader")
		}
		objectID = object.ObjectID
		return nil
	}))
	if err != nil {
		return err
	}
	path := filepath.Join(state.dir, item.ID)
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(pageCtx, 5*time.Second)
		defer cancel()
		if resultErr != nil {
			abortErr := chromedp.Run(cleanupCtx, chromedp.ActionFunc(func(actionCtx context.Context) error {
				_, exception, err := runtime.CallFunctionOn(`function(){this.controller.abort(); return true;}`).WithObjectID(objectID).Do(actionCtx)
				if exception != nil {
					return fmt.Errorf("download abort was not confirmed")
				}
				return err
			}))
			if abortErr != nil {
				state.cancelFailed(item.ID, abortErr)
			}
			_ = os.Remove(path)
		}
		_ = chromedp.Run(cleanupCtx, chromedp.ActionFunc(func(actionCtx context.Context) error { return runtime.ReleaseObject(objectID).Do(actionCtx) }))
	}()
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		_, exception, err := runtime.CallFunctionOn(fmt.Sprintf(`function(){ const job=this; job.ready=fetch(%s,{credentials:'same-origin',signal:job.controller.signal}).then(response => { if (!response.ok) throw new Error('Download HTTP '+response.status); if (Number(response.headers.get('content-length'))>33554432) {job.controller.abort(); throw new Error('Download exceeds 32 MiB');} job.disposition=response.headers.get('content-disposition')||''; job.reader=response.body.getReader(); }); job.ready.catch(()=>{}); return true; }`, encoded)).WithObjectID(objectID).Do(actionCtx)
		if exception != nil {
			return fmt.Errorf("cannot start browser download reader")
		}
		return err
	})); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	for {
		var chunk struct {
			Done        bool   `json:"done"`
			Data        string `json:"data"`
			Disposition string `json:"disposition"`
		}
		err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
			object, exception, err := runtime.CallFunctionOn(`async function(){await this.ready; if(!this.pending){const part=await this.reader.read();if(part.done)return {done:true,disposition:this.disposition};if(part.value.byteLength>33554432)throw new Error('Download chunk exceeds limit');this.pending=part.value;this.offset=0;}const data=this.pending.subarray(this.offset,this.offset+65536);this.offset+=data.length;let text='';for(let offset=0;offset<data.length;offset+=8192)text+=String.fromCharCode(...data.subarray(offset,offset+8192));if(this.offset===this.pending.length)this.pending=null;return {done:false,data:btoa(text),disposition:this.disposition};}`).WithObjectID(objectID).WithAwaitPromise(true).WithReturnByValue(true).Do(actionCtx)
			if err != nil {
				return err
			}
			if exception != nil || object == nil {
				return fmt.Errorf("browser download reader failed (network, CORS, size limit or abort)")
			}
			return json.Unmarshal(object.Value, &chunk)
		}))
		if err != nil {
			return err
		}
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil {
			return err
		}
		state.mu.Lock()
		item.Bytes += int64(len(data))
		var total int64
		for _, tracked := range state.items {
			total += tracked.Bytes
		}
		oversized := item.Bytes > 32<<20 || total > 128<<20
		if _, params, err := mime.ParseMediaType(chunk.Disposition); err == nil {
			item.Filename = security.RedactDiagnostic(filepath.Base(params["filename"]))
		}
		state.mu.Unlock()
		if oversized {
			return fmt.Errorf("download exceeds size limit")
		}
		if _, err := file.Write(data); err != nil {
			return err
		}
		if chunk.Done {
			if err := file.Close(); err != nil {
				return err
			}
			state.mu.Lock()
			item.State, item.Path = "completed", path
			state.mu.Unlock()
			return nil
		}
	}
}

func normalizeBrowserDownloadURL(raw string, allowPrivate bool) (string, error) {
	if strings.HasPrefix(raw, "blob:") {
		inner, err := url.Parse(strings.TrimPrefix(raw, "blob:"))
		if err != nil || inner == nil || (inner.Scheme != "http" && inner.Scheme != "https") || inner.Host == "" {
			return "", fmt.Errorf("blob downloads require an HTTP(S) origin")
		}
		if _, err := normalizeBrowserURL(inner.String(), allowPrivate); err != nil {
			return "", err
		}
		return raw, nil
	}
	return normalizeBrowserURL(raw, allowPrivate)
}

func (e *BrowserToolsExtension) authorizeDownloadFrame(frame cdp.FrameID, raw string) bool {
	e.ownerMu.Lock()
	source, owned := e.frameOrigins[frame]
	e.ownerMu.Unlock()
	if !owned {
		return false
	}
	if !strings.HasPrefix(raw, "blob:") {
		return true
	}
	origin, err := url.Parse(strings.TrimPrefix(raw, "blob:"))
	if err != nil {
		return false
	}
	parent, err := url.Parse(source)
	return err == nil && strings.EqualFold(origin.Scheme, parent.Scheme) && strings.EqualFold(origin.Host, parent.Host)
}
