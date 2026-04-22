package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/voltyh/extension/cli/internal/collector"
	"github.com/voltyh/extension/cli/internal/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// CDP capture types
// ─────────────────────────────────────────────────────────────────────────────

type cdpRequest struct {
	RequestID    string            `json:"requestId"`
	DocumentURL  string            `json:"documentUrl,omitempty"`
	URL          string            `json:"url"`
	Method       string            `json:"method"`
	Headers      map[string]string `json:"headers,omitempty"`
	HasPostData  bool              `json:"hasPostData,omitempty"`
	PostData     string            `json:"postData,omitempty"`
	ResourceType string            `json:"resourceType"`
	Timestamp    string            `json:"timestamp"`
	WallTime     string            `json:"wallTime"`
}

type cdpResponse struct {
	RequestID  string            `json:"requestId"`
	URL        string            `json:"url"`
	Status     int64             `json:"status"`
	StatusText string            `json:"statusText"`
	Headers    map[string]string `json:"headers,omitempty"`
	MimeType   string            `json:"mimeType"`
	Timestamp  string            `json:"timestamp"`
}

type cdpBody struct {
	RequestID  string `json:"requestId"`
	URL        string `json:"url"`
	BodyFile   string `json:"bodyFile,omitempty"`
	ByteLength int    `json:"byteLength"`
	Error      string `json:"error,omitempty"`
}

type cdpConsoleEntry struct {
	Level     string   `json:"level"`
	Text      string   `json:"text"`
	Args      []string `json:"args,omitempty"`
	Source    string   `json:"source,omitempty"`
	URL       string   `json:"url,omitempty"`
	Line      int64    `json:"line,omitempty"`
	Timestamp string   `json:"timestamp"`
}

type cdpException struct {
	Text      string `json:"text"`
	URL       string `json:"url,omitempty"`
	Line      int64  `json:"line,omitempty"`
	Stack     string `json:"stack,omitempty"`
	Timestamp string `json:"timestamp"`
}

type cdpWSFrame struct {
	RequestID string  `json:"requestId"`
	URL       string  `json:"url"`
	Direction string  `json:"direction"`
	Opcode    float64 `json:"opcode"`
	Payload   string  `json:"payload"`
	Timestamp string  `json:"timestamp"`
}

// ─────────────────────────────────────────────────────────────────────────────
// cdpCapture accumulates all protocol-level events
// ─────────────────────────────────────────────────────────────────────────────

type cdpCapture struct {
	mu         sync.Mutex
	requests   []cdpRequest
	responses  []cdpResponse
	bodies     []cdpBody
	console    []cdpConsoleEntry
	exceptions []cdpException
	wsFrames   []cdpWSFrame
	wsURLs     map[string]string // requestID → WS url
	reqURLs    map[string]string // requestID → request url

	diagDir     string
	bodiesDir   string
	bodyWorkers chan struct{} // semaphore: max concurrent GetResponseBody calls
	bodyWg      sync.WaitGroup
}

func newCDPCapture(diagDir string) *cdpCapture {
	return &cdpCapture{
		wsURLs:      make(map[string]string),
		reqURLs:     make(map[string]string),
		diagDir:     diagDir,
		bodiesDir:   filepath.Join(diagDir, "response_bodies"),
		bodyWorkers: make(chan struct{}, 8),
	}
}

// listen installs chromedp event listeners. Must be called before any Run/Navigate.
func (cap *cdpCapture) listen(browserCtx context.Context) {
	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			cap.onRequest(e)
		case *network.EventResponseReceived:
			cap.onResponse(e)
		case *network.EventLoadingFinished:
			cap.scheduleBodyFetch(browserCtx, e.RequestID)
		case *network.EventWebSocketCreated:
			cap.mu.Lock()
			cap.wsURLs[string(e.RequestID)] = e.URL
			cap.mu.Unlock()
		case *network.EventWebSocketFrameReceived:
			if e.Response != nil {
				var ts string
				if e.Timestamp != nil {
					ts = time.Time(*e.Timestamp).UTC().Format(time.RFC3339Nano)
				}
				cap.mu.Lock()
				url := cap.wsURLs[string(e.RequestID)]
				cap.wsFrames = append(cap.wsFrames, cdpWSFrame{
					RequestID: string(e.RequestID),
					URL:       url,
					Direction: "received",
					Opcode:    e.Response.Opcode,
					Payload:   e.Response.PayloadData,
					Timestamp: ts,
				})
				cap.mu.Unlock()
			}
		case *network.EventWebSocketFrameSent:
			if e.Response != nil {
				var ts string
				if e.Timestamp != nil {
					ts = time.Time(*e.Timestamp).UTC().Format(time.RFC3339Nano)
				}
				cap.mu.Lock()
				url := cap.wsURLs[string(e.RequestID)]
				cap.wsFrames = append(cap.wsFrames, cdpWSFrame{
					RequestID: string(e.RequestID),
					URL:       url,
					Direction: "sent",
					Opcode:    e.Response.Opcode,
					Payload:   e.Response.PayloadData,
					Timestamp: ts,
				})
				cap.mu.Unlock()
			}
		case *runtime.EventConsoleAPICalled:
			cap.onConsole(e)
		case *runtime.EventExceptionThrown:
			cap.onException(e)
		case *cdplog.EventEntryAdded:
			cap.onLogEntry(e)
		}
	})
}

func (cap *cdpCapture) onRequest(e *network.EventRequestWillBeSent) {
	if e.Request == nil {
		return
	}
	req := cdpRequest{
		RequestID:    string(e.RequestID),
		DocumentURL:  e.DocumentURL,
		URL:          e.Request.URL,
		Method:       e.Request.Method,
		HasPostData:  e.Request.HasPostData,
		ResourceType: string(e.Type),
	}
	if e.Timestamp != nil {
		req.Timestamp = time.Time(*e.Timestamp).UTC().Format(time.RFC3339Nano)
	}
	if e.WallTime != nil {
		req.WallTime = time.Time(*e.WallTime).UTC().Format(time.RFC3339Nano)
	}
	if len(e.Request.Headers) > 0 {
		req.Headers = make(map[string]string, len(e.Request.Headers))
		for k, v := range e.Request.Headers {
			if s, ok := v.(string); ok {
				req.Headers[k] = s
			}
		}
	}
	// Collect post data from PostDataEntries when Chrome includes it.
	for _, entry := range e.Request.PostDataEntries {
		if len(entry.Bytes) > 0 {
			req.PostData += string(entry.Bytes)
		}
	}
	cap.mu.Lock()
	cap.requests = append(cap.requests, req)
	cap.reqURLs[string(e.RequestID)] = e.Request.URL
	cap.mu.Unlock()
}

func (cap *cdpCapture) onResponse(e *network.EventResponseReceived) {
	if e.Response == nil {
		return
	}
	resp := cdpResponse{
		RequestID:  string(e.RequestID),
		URL:        e.Response.URL,
		Status:     e.Response.Status,
		StatusText: e.Response.StatusText,
		MimeType:   e.Response.MimeType,
	}
	if e.Timestamp != nil {
		resp.Timestamp = time.Time(*e.Timestamp).UTC().Format(time.RFC3339Nano)
	}
	if len(e.Response.Headers) > 0 {
		resp.Headers = make(map[string]string, len(e.Response.Headers))
		for k, v := range e.Response.Headers {
			if s, ok := v.(string); ok {
				resp.Headers[k] = s
			}
		}
	}
	cap.mu.Lock()
	cap.responses = append(cap.responses, resp)
	cap.mu.Unlock()
}

func (cap *cdpCapture) scheduleBodyFetch(browserCtx context.Context, rid network.RequestID) {
	cap.mu.Lock()
	url := cap.reqURLs[string(rid)]
	cap.mu.Unlock()

	cap.bodyWg.Add(1)
	go func() {
		defer cap.bodyWg.Done()
		cap.bodyWorkers <- struct{}{}        // acquire semaphore
		defer func() { <-cap.bodyWorkers }() // release semaphore

		fetchCtx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
		defer cancel()

		var bodyBytes []byte
		err := chromedp.Run(fetchCtx, chromedp.ActionFunc(func(c context.Context) error {
			var ferr error
			bodyBytes, ferr = network.GetResponseBody(rid).Do(c)
			return ferr
		}))

		entry := cdpBody{RequestID: string(rid), URL: url}
		if err != nil {
			entry.Error = err.Error()
		} else {
			entry.ByteLength = len(bodyBytes)
			if mkErr := os.MkdirAll(cap.bodiesDir, 0o755); mkErr == nil {
				fn := diagSanitizeID(string(rid)) + ".body"
				absPath := filepath.Join(cap.bodiesDir, fn)
				if writeErr := os.WriteFile(absPath, bodyBytes, 0o644); writeErr == nil {
					entry.BodyFile = "response_bodies/" + fn
				} else {
					entry.Error = "write: " + writeErr.Error()
				}
			} else {
				entry.Error = "mkdir: " + mkErr.Error()
			}
		}
		cap.mu.Lock()
		cap.bodies = append(cap.bodies, entry)
		cap.mu.Unlock()
	}()
}

func (cap *cdpCapture) onConsole(e *runtime.EventConsoleAPICalled) {
	msg := cdpConsoleEntry{Level: string(e.Type)}
	if e.Timestamp != nil {
		msg.Timestamp = time.Time(*e.Timestamp).UTC().Format(time.RFC3339Nano)
	}
	for _, arg := range e.Args {
		if arg == nil {
			continue
		}
		var s string
		if len(arg.Value) > 0 {
			s = string(arg.Value)
		} else if arg.Description != "" {
			s = arg.Description
		} else {
			s = fmt.Sprintf("<%s>", string(arg.Type))
		}
		msg.Args = append(msg.Args, s)
	}
	msg.Text = strings.Join(msg.Args, " ")
	if e.StackTrace != nil && len(e.StackTrace.CallFrames) > 0 {
		f := e.StackTrace.CallFrames[0]
		msg.URL = f.URL
		msg.Line = f.LineNumber
	}
	cap.mu.Lock()
	cap.console = append(cap.console, msg)
	cap.mu.Unlock()
}

func (cap *cdpCapture) onException(e *runtime.EventExceptionThrown) {
	exc := cdpException{}
	if e.Timestamp != nil {
		exc.Timestamp = time.Time(*e.Timestamp).UTC().Format(time.RFC3339Nano)
	}
	if e.ExceptionDetails != nil {
		exc.Text = e.ExceptionDetails.Text
		exc.URL = e.ExceptionDetails.URL
		exc.Line = e.ExceptionDetails.LineNumber
		if e.ExceptionDetails.Exception != nil {
			exc.Stack = e.ExceptionDetails.Exception.Description
		}
	}
	cap.mu.Lock()
	cap.exceptions = append(cap.exceptions, exc)
	cap.mu.Unlock()
}

func (cap *cdpCapture) onLogEntry(e *cdplog.EventEntryAdded) {
	if e.Entry == nil {
		return
	}
	msg := cdpConsoleEntry{
		Level:  string(e.Entry.Level),
		Text:   e.Entry.Text,
		Source: string(e.Entry.Source),
		URL:    e.Entry.URL,
		Line:   e.Entry.LineNumber,
	}
	if e.Entry.Timestamp != nil {
		msg.Timestamp = time.Time(*e.Entry.Timestamp).UTC().Format(time.RFC3339Nano)
	}
	cap.mu.Lock()
	cap.console = append(cap.console, msg)
	cap.mu.Unlock()
}

// waitForBodies blocks until all GetResponseBody goroutines finish.
func (cap *cdpCapture) waitForBodies() {
	cap.bodyWg.Wait()
}

// writeFiles serialises all captured CDP data to JSON files in diagDir.
func (cap *cdpCapture) writeFiles(diagDir string) error {
	if err := os.MkdirAll(diagDir, 0o755); err != nil {
		return err
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()

	for _, s := range []struct {
		name string
		data any
	}{
		{"cdp_network_requests", cap.requests},
		{"cdp_network_responses", cap.responses},
		{"cdp_network_bodies_index", cap.bodies},
		{"cdp_console", cap.console},
		{"cdp_exceptions", cap.exceptions},
		{"cdp_websocket_frames", cap.wsFrames},
	} {
		b, err := json.MarshalIndent(s.data, "", "  ")
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(diagDir, s.name+".json"), b, 0o644)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// CDP domain helpers
// ─────────────────────────────────────────────────────────────────────────────

// installDiagDomains enables Network, Runtime, Log and Page CDP domains.
func installDiagDomains(ctx context.Context) error {
	return chromedp.Run(ctx,
		chromedp.ActionFunc(func(c context.Context) error {
			// MaxPostDataSize > 0 tells Chrome to include request post data in
			// requestWillBeSent events (up to 100 MB).
			return network.Enable().
				WithMaxPostDataSize(100 * 1024 * 1024).
				Do(c)
		}),
		chromedp.ActionFunc(func(c context.Context) error {
			return runtime.Enable().Do(c)
		}),
		chromedp.ActionFunc(func(c context.Context) error {
			return cdplog.Enable().Do(c)
		}),
		chromedp.ActionFunc(func(c context.Context) error {
			return page.Enable().Do(c)
		}),
	)
}

// addInterceptorScript injects the comprehensive in-page diagnostics script
// via Page.addScriptToEvaluateOnNewDocument so it runs before any page JS on
// every navigation and reload.
func addInterceptorScript(ctx context.Context, script string) error {
	return chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(script).Do(c)
		return err
	}))
}

// diagSanitizeID converts a CDP request ID to a safe filename component.
func diagSanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 80 {
		out = out[:80]
	}
	if out == "" {
		return "body"
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Full-diagnostics collection entry point
// ─────────────────────────────────────────────────────────────────────────────

// collectWithFullDiagnostics is the comprehensive capture path used when
// -capture-diagnostics is set.  It:
//  1. Installs CDP listeners (network, console, exceptions, WS, log)
//  2. Injects a comprehensive in-page interceptor script via
//     addScriptToEvaluateOnNewDocument so it survives page reloads
//  3. Navigates to the target URL
//  4. Issues a full hard reload so that capture starts from the very first
//     network request with the interceptor already active
//  5. Runs a scroll-up loop for at least 60 s (or until the scroll position
//     reaches the top of the conversation) to trigger lazy content loading
//  6. Extracts the conversation
//  7. Reads window.__DIAG__ for the JS-side captured data
//  8. Waits for all pending GetResponseBody fetches to complete
//  9. Writes everything to the diagnostics directory
func (c *Collector) collectWithFullDiagnostics(
	ctx context.Context,
	opts collector.Options,
	wsURL, targetURL string,
) (*model.Conversation, error) {
	diagDir := opts.DiagnosticsDir
	if diagDir == "" {
		tmp, err := os.MkdirTemp("", "gemini-fulldiag-*")
		if err != nil {
			return nil, fmt.Errorf("create temp diag dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		diagDir = tmp
	}
	if err := os.MkdirAll(diagDir, 0o755); err != nil {
		return nil, fmt.Errorf("create diag dir: %w", err)
	}

	allocCtx, cancelAllocator := chromedp.NewRemoteAllocator(ctx, wsURL)
	defer cancelAllocator()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	// ── Step 1: CDP capture – must be set up BEFORE any action ───────────────
	cap := newCDPCapture(diagDir)
	cap.listen(browserCtx)

	// ── Step 2: Enable CDP domains ──────────────────────────────────────────
	if err := installDiagDomains(browserCtx); err != nil {
		return nil, fmt.Errorf("enable CDP domains: %w", err)
	}

	// ── Step 3: Inject interceptor script (runs before page JS on every load) ─
	if err := addInterceptorScript(browserCtx, buildInterceptorScript()); err != nil {
		return nil, fmt.Errorf("add interceptor script: %w", err)
	}

	// ── Step 4: Navigate (CDP is already capturing) ──────────────────────────
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("navigate to target: %w", err)
	}

	// ── Step 5: Full hard reload – this is the "start capture from scratch"
	//            moment the user asked for.  The interceptor script will run
	//            again on the fresh document before any page JS fires.  ───────
	if err := chromedp.Run(browserCtx, chromedp.ActionFunc(func(c context.Context) error {
		return page.Reload().WithIgnoreCache(true).Do(c)
	})); err != nil {
		return nil, fmt.Errorf("reload page: %w", err)
	}
	if err := chromedp.Run(browserCtx, chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
		return nil, fmt.Errorf("wait ready after reload: %w", err)
	}
	// Give the SPA framework time to hydrate.
	if err := chromedp.Run(browserCtx, chromedp.Sleep(3*time.Second)); err != nil {
		return nil, fmt.Errorf("post-reload wait: %w", err)
	}

	// ── Step 6: Scroll-up loop (≥60 s, or until scroll reaches top) ──────────
	var scrollJSON string
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(
		buildScrollAndWaitScript(),
		&scrollJSON,
		func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		},
	)); err != nil {
		// Non-fatal: record and continue.
		_ = os.WriteFile(filepath.Join(diagDir, "scroll_error.txt"), []byte(err.Error()), 0o644)
	}
	if scrollJSON != "" {
		_ = os.WriteFile(filepath.Join(diagDir, "scroll_log.json"), []byte(scrollJSON), 0o644)
	}

	// ── Step 7: Extract the conversation ─────────────────────────────────────
	var convPayload string
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(
		// Pass captureDiagnostics=false here; the lightweight JS-side diag
		// section inside extractConversationScript is superseded by what we
		// collect separately.
		extractConversationScript(opts.IncludeMetadata, false),
		&convPayload,
		func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		},
	)); err != nil {
		return nil, fmt.Errorf("extract conversation: %w", err)
	}

	// ── Step 8: Read window.__DIAG__ (JS-side capture) ───────────────────────
	var jsDiagRaw string
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(
		buildReadDiagScript(),
		&jsDiagRaw,
	)); err != nil {
		_ = os.WriteFile(filepath.Join(diagDir, "js_diag_read_error.txt"), []byte(err.Error()), 0o644)
	}
	if jsDiagRaw != "" {
		_ = os.WriteFile(filepath.Join(diagDir, "js_diagnostics.json"), []byte(jsDiagRaw), 0o644)
	}

	// ── Supplementary: DOM snapshot + performance data ────────────────────────
	var domHTML string
	_ = chromedp.Run(browserCtx, chromedp.Evaluate(
		`(function(){ return document.documentElement ? document.documentElement.outerHTML : ''; })()`,
		&domHTML,
	))
	if domHTML != "" {
		_ = os.WriteFile(filepath.Join(diagDir, "dom_snapshot_final.html"), []byte(domHTML), 0o644)
	}

	var perfJSON string
	_ = chromedp.Run(browserCtx, chromedp.Evaluate(
		`JSON.stringify({`+
			`navigation: performance.getEntriesByType('navigation'),`+
			`resources: performance.getEntriesByType('resource'),`+
			`marks: performance.getEntriesByType('mark'),`+
			`measures: performance.getEntriesByType('measure')`+
			`})`,
		&perfJSON,
	))
	if perfJSON != "" {
		_ = os.WriteFile(filepath.Join(diagDir, "performance.json"), []byte(perfJSON), 0o644)
	}

	// ── Step 9: Wait for all in-flight GetResponseBody goroutines ─────────────
	cap.waitForBodies()

	// ── Step 10: Write CDP-captured data to JSON files ────────────────────────
	if err := cap.writeFiles(diagDir); err != nil {
		_ = os.WriteFile(filepath.Join(diagDir, "cdp_write_error.txt"), []byte(err.Error()), 0o644)
	}

	// ── Step 11: Parse the conversation payload ───────────────────────────────
	if strings.TrimSpace(convPayload) == "" {
		return nil, fmt.Errorf("extracted empty conversation payload from Gemini page")
	}
	var conv model.Conversation
	var extracted extractedPayload
	if err := json.Unmarshal([]byte(convPayload), &extracted); err == nil && len(extracted.Conversation.Messages) > 0 {
		conv = extracted.Conversation
	} else if err := json.Unmarshal([]byte(convPayload), &conv); err != nil {
		return nil, fmt.Errorf("decode extracted conversation: %w", err)
	}
	if opts.ConversationID != "" {
		conv.ID = opts.ConversationID
	}
	if len(conv.Messages) == 0 {
		return nil, fmt.Errorf("no messages found in extracted conversation")
	}
	return &conv, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// In-page JavaScript scripts
// ─────────────────────────────────────────────────────────────────────────────

// buildInterceptorScript returns a self-contained IIFE that installs
// comprehensive runtime interceptors before any page JS runs.  It captures:
//   - fetch (request URL/method/headers/body + response status/headers/body)
//   - XMLHttpRequest (same)
//   - console.* (all methods)
//   - localStorage / sessionStorage (read, write, remove, clear)
//   - document.cookie (get / set)
//   - WebSocket (messages in both directions)
//   - window.onerror / unhandledrejection
//   - IndexedDB open / store operations
//   - MutationObserver on the entire document tree
//
// All captured data accumulates in window.__DIAG__.
func buildInterceptorScript() string {
	return `(function() {
  'use strict';
  if (window.__DIAG_INSTALLED__) return;
  window.__DIAG_INSTALLED__ = true;

  var __DIAG__ = {
    fetchEvents:     [],
    xhrEvents:       [],
    consoleMessages: [],
    domMutations:    [],
    storageOps:      [],
    cookieAccess:    [],
    wsEvents:        [],
    errors:          [],
    idbOps:          [],
    startedAt:       new Date().toISOString()
  };
  Object.defineProperty(window, '__DIAG__', {
    value: __DIAG__, writable: false, configurable: true, enumerable: true
  });

  // ── 1. fetch ────────────────────────────────────────────────────────────────
  var _origFetch = window.fetch ? window.fetch.bind(window) : null;
  if (_origFetch) {
    window.fetch = function() {
      var args  = Array.prototype.slice.call(arguments);
      var input = args[0], init = args[1] || {};
      var url    = typeof input === 'string' ? input
                 : (input && typeof input === 'object' && input.url ? input.url : String(input));
      var method = init.method || (input && typeof input === 'object' ? input.method : null) || 'GET';
      var reqBody = ''; try { reqBody = init.body != null ? String(init.body) : ''; } catch(e) {}
      var reqHdr  = ''; try { reqHdr  = init.headers ? JSON.stringify(init.headers) : ''; } catch(e) {}
      var startMs = Date.now();
      var entry = { url: url, method: method, reqBody: reqBody, reqHeaders: reqHdr,
                    status: 0, ok: false, respHeaders: '', respBody: '',
                    durationMs: 0, error: '', ts: new Date().toISOString() };
      __DIAG__.fetchEvents.push(entry);
      return _origFetch.apply(window, args).then(function(resp) {
        entry.status = resp.status; entry.ok = resp.ok;
        try { entry.respHeaders = JSON.stringify(Object.fromEntries(resp.headers.entries())); } catch(e) {}
        resp.clone().text().then(function(t) {
          entry.respBody = t; entry.durationMs = Date.now() - startMs;
        }).catch(function() { entry.durationMs = Date.now() - startMs; });
        return resp;
      }, function(err) {
        entry.error = String(err); entry.durationMs = Date.now() - startMs; throw err;
      });
    };
  }

  // ── 2. XMLHttpRequest ───────────────────────────────────────────────────────
  if (window.XMLHttpRequest) {
    var _xhrOpen    = XMLHttpRequest.prototype.open;
    var _xhrSend    = XMLHttpRequest.prototype.send;
    var _xhrSetHdr  = XMLHttpRequest.prototype.setRequestHeader;
    XMLHttpRequest.prototype.open = function(method, url) {
      this._diagEntry = { method: method || '', url: url || '', reqBody: '', reqHeaders: {},
                          status: 0, respBody: '', respHeaders: '', durationMs: 0,
                          error: '', ts: new Date().toISOString(), _startMs: 0 };
      __DIAG__.xhrEvents.push(this._diagEntry);
      return _xhrOpen.apply(this, arguments);
    };
    XMLHttpRequest.prototype.setRequestHeader = function(name, value) {
      if (this._diagEntry) this._diagEntry.reqHeaders[String(name)] = String(value);
      return _xhrSetHdr.apply(this, arguments);
    };
    XMLHttpRequest.prototype.send = function(body) {
      if (this._diagEntry) {
        this._diagEntry.reqBody    = body != null ? String(body) : '';
        this._diagEntry._startMs   = Date.now();
        var entry = this._diagEntry;
        this.addEventListener('loadend', function() {
          entry.status      = this.status || 0;
          try { entry.respBody = this.responseText || ''; } catch(e) {}
          entry.respHeaders = this.getAllResponseHeaders ? (this.getAllResponseHeaders() || '') : '';
          entry.durationMs  = Date.now() - entry._startMs;
        });
        this.addEventListener('error',  function() { entry.error = 'network error'; entry.durationMs = Date.now() - entry._startMs; });
        this.addEventListener('abort',  function() { entry.error = 'aborted';       entry.durationMs = Date.now() - entry._startMs; });
      }
      return _xhrSend.apply(this, arguments);
    };
  }

  // ── 3. console.* ───────────────────────────────────────────────────────────
  ['log','warn','error','info','debug','trace','dir','dirxml','group',
   'groupCollapsed','groupEnd','table','count','countReset','time',
   'timeLog','timeEnd','assert'].forEach(function(m) {
    if (typeof console[m] !== 'function') return;
    var _orig = console[m].bind(console);
    console[m] = function() {
      var args = Array.prototype.slice.call(arguments);
      __DIAG__.consoleMessages.push({
        level: m,
        args:  args.map(function(a) {
          try { return typeof a === 'object' && a !== null ? JSON.stringify(a) : String(a); }
          catch(e) { return '[unserializable]'; }
        }),
        ts: new Date().toISOString()
      });
      return _orig.apply(console, args);
    };
  });

  // ── 4. localStorage / sessionStorage ───────────────────────────────────────
  function _wrapStorage(storage, name) {
    if (!storage) return;
    try {
      var _getItem    = storage.getItem.bind(storage);
      var _setItem    = storage.setItem.bind(storage);
      var _removeItem = storage.removeItem.bind(storage);
      var _clear      = storage.clear.bind(storage);
      storage.getItem = function(key) {
        var val = _getItem(key);
        __DIAG__.storageOps.push({ storage: name, op: 'getItem', key: String(key || ''), value: val, ts: new Date().toISOString() });
        return val;
      };
      storage.setItem = function(key, value) {
        __DIAG__.storageOps.push({ storage: name, op: 'setItem', key: String(key || ''), value: String(value || ''), ts: new Date().toISOString() });
        return _setItem(key, value);
      };
      storage.removeItem = function(key) {
        __DIAG__.storageOps.push({ storage: name, op: 'removeItem', key: String(key || ''), ts: new Date().toISOString() });
        return _removeItem(key);
      };
      storage.clear = function() {
        __DIAG__.storageOps.push({ storage: name, op: 'clear', ts: new Date().toISOString() });
        return _clear();
      };
    } catch(e) {}
  }
  try { _wrapStorage(window.localStorage,   'localStorage');   } catch(e) {}
  try { _wrapStorage(window.sessionStorage,  'sessionStorage'); } catch(e) {}

  // ── 5. document.cookie ─────────────────────────────────────────────────────
  try {
    var _ckDesc = Object.getOwnPropertyDescriptor(Document.prototype, 'cookie')
               || Object.getOwnPropertyDescriptor(HTMLDocument.prototype, 'cookie');
    if (_ckDesc && _ckDesc.get && _ckDesc.set) {
      Object.defineProperty(document, 'cookie', {
        get: function() {
          var v = _ckDesc.get.call(this);
          __DIAG__.cookieAccess.push({ op: 'get', value: v, ts: new Date().toISOString() });
          return v;
        },
        set: function(v) {
          __DIAG__.cookieAccess.push({ op: 'set', value: String(v || ''), ts: new Date().toISOString() });
          return _ckDesc.set.call(this, v);
        },
        configurable: true
      });
    }
  } catch(e) {}

  // ── 6. WebSocket ────────────────────────────────────────────────────────────
  if (window.WebSocket) {
    var _OrigWS = window.WebSocket;
    var _PatchWS = function(url, protocols) {
      var ws = protocols !== undefined ? new _OrigWS(url, protocols) : new _OrigWS(url);
      var entry = { url: String(url || ''), openedAt: new Date().toISOString(),
                    closedAt: null, closeCode: null, messages: [] };
      __DIAG__.wsEvents.push(entry);
      ws.addEventListener('message', function(e) {
        entry.messages.push({ dir: 'recv', data: String(e.data || ''), ts: new Date().toISOString() });
      });
      ws.addEventListener('close', function(e) {
        entry.closedAt = new Date().toISOString(); entry.closeCode = e.code;
      });
      var _origSend = ws.send.bind(ws);
      ws.send = function(data) {
        entry.messages.push({ dir: 'send', data: String(data || ''), ts: new Date().toISOString() });
        return _origSend(data);
      };
      return ws;
    };
    _PatchWS.prototype   = _OrigWS.prototype;
    _PatchWS.CONNECTING  = _OrigWS.CONNECTING;
    _PatchWS.OPEN        = _OrigWS.OPEN;
    _PatchWS.CLOSING     = _OrigWS.CLOSING;
    _PatchWS.CLOSED      = _OrigWS.CLOSED;
    window.WebSocket     = _PatchWS;
  }

  // ── 7. window.onerror / unhandledrejection ──────────────────────────────────
  window.addEventListener('error', function(e) {
    __DIAG__.errors.push({
      type: 'error', message: e.message || '', source: e.filename || '',
      line: e.lineno || 0, col: e.colno || 0,
      stack: (e.error && e.error.stack) ? e.error.stack : '',
      ts: new Date().toISOString()
    });
  }, true);
  window.addEventListener('unhandledrejection', function(e) {
    __DIAG__.errors.push({
      type: 'unhandledrejection', message: String(e.reason || ''),
      stack: (e.reason && e.reason.stack) ? e.reason.stack : '',
      ts: new Date().toISOString()
    });
  }, true);

  // ── 8. IndexedDB ────────────────────────────────────────────────────────────
  if (window.indexedDB && window.indexedDB.open) {
    var _idbOpen = window.indexedDB.open.bind(window.indexedDB);
    window.indexedDB.open = function(name, version) {
      __DIAG__.idbOps.push({ op: 'open', db: String(name || ''), version: version || null, ts: new Date().toISOString() });
      return _idbOpen.apply(window.indexedDB, arguments);
    };
  }
  if (window.IDBObjectStore) {
    ['add','put','get','getAll','delete','clear','count','openCursor','getKey','getAllKeys','index'].forEach(function(m) {
      if (typeof IDBObjectStore.prototype[m] !== 'function') return;
      var _orig = IDBObjectStore.prototype[m];
      IDBObjectStore.prototype[m] = function() {
        __DIAG__.idbOps.push({ op: m, store: this.name || '', ts: new Date().toISOString() });
        return _orig.apply(this, arguments);
      };
    });
  }

  // ── 9. MutationObserver (full document tree) ────────────────────────────────
  var _mutCfg = {
    childList: true, subtree: true,
    attributes: true, attributeOldValue: true,
    characterData: true, characterDataOldValue: false
  };
  var _mutObs = new MutationObserver(function(mutations) {
    for (var i = 0; i < mutations.length; i++) {
      var m     = mutations[i];
      var entry = {
        type:         m.type,
        targetTag:    m.target ? ((m.target.tagName || '')).toLowerCase() || '#text' : '',
        targetId:     m.target ? (m.target.id || '') : '',
        targetClass:  m.target ? String(m.target.className || '').slice(0, 100) : '',
        addedCount:   m.addedNodes   ? m.addedNodes.length   : 0,
        removedCount: m.removedNodes ? m.removedNodes.length : 0,
        attrName:     m.attributeName || '',
        oldValue:     m.oldValue ? String(m.oldValue).slice(0, 500) : '',
        ts:           new Date().toISOString()
      };
      if (m.type === 'childList' && m.addedNodes && m.addedNodes.length > 0) {
        var first = m.addedNodes[0];
        entry.firstAddedTag  = first.tagName ? first.tagName.toLowerCase() : '#text';
        entry.firstAddedText = first.textContent ? first.textContent.slice(0, 200) : '';
      }
      __DIAG__.domMutations.push(entry);
    }
  });
  var _startObs = function() {
    try { _mutObs.observe(document.documentElement || document, _mutCfg); } catch(e) {}
  };
  if (document.documentElement) {
    _startObs();
  } else {
    document.addEventListener('DOMContentLoaded', _startObs, { once: true });
  }

})();`
}

// buildScrollAndWaitScript returns an async IIFE that scrolls the conversation
// upward to trigger lazy loading of older messages.  It runs for a minimum of
// 60 seconds and keeps going until the scroll position reaches the very top of
// the page, whichever takes longer.  A hard cap of 10 minutes prevents it from
// running forever.
func buildScrollAndWaitScript() string {
	return `(async function() {
  var wait = function(ms) { return new Promise(function(r) { setTimeout(r, ms); }); };
  var MIN_MS  = 60000;   // 60 s minimum — non-negotiable
  var MAX_MS  = 600000;  // 10 min hard cap
  var startMs = Date.now();
  var log     = [];
  var passes  = 0;
  var lastScrollHeight = 0;

  var scroller = document.scrollingElement || document.documentElement || document.body;

  while (true) {
    var elapsed     = Date.now() - startMs;
    var scrollTop   = scroller.scrollTop   || 0;
    var scrollHeight= scroller.scrollHeight|| 0;
    var reachedTop  = scrollTop <= 2;

    log.push({
      pass: passes, scrollTop: scrollTop, scrollHeight: scrollHeight,
      elapsed: elapsed, reachedTop: reachedTop, ts: new Date().toISOString()
    });

    if (reachedTop && elapsed >= MIN_MS) {
      log.push({ event: 'done', reason: 'top-reached-and-min-elapsed',
                 elapsed: elapsed, ts: new Date().toISOString() });
      break;
    }
    if (elapsed >= MAX_MS) {
      log.push({ event: 'done', reason: 'max-duration-exceeded',
                 elapsed: elapsed, ts: new Date().toISOString() });
      break;
    }

    if (!reachedTop) {
      var target = Math.max(0, scrollTop - window.innerHeight);
      scroller.scrollTo({ top: target, behavior: 'smooth' });
      await wait(800);
      var newH = scroller.scrollHeight;
      if (newH > lastScrollHeight) {
        log.push({ event: 'new-content-loaded', oldHeight: lastScrollHeight,
                   newHeight: newH, ts: new Date().toISOString() });
        lastScrollHeight = newH;
        await wait(600);
      }
    } else {
      // Already at top but min duration not met — keep waiting and re-checking.
      await wait(1000);
    }
    passes++;
  }

  if (window.__DIAG__) { window.__DIAG__.scrollLog = log; }
  return JSON.stringify({
    done: true, passes: passes, elapsedMs: Date.now() - startMs, logEntries: log.length
  });
})();`
}

// buildReadDiagScript returns a synchronous IIFE that serialises
// window.__DIAG__ to a JSON string for collection by the Go side.
func buildReadDiagScript() string {
	return `(function() {
  try {
    var d = window.__DIAG__;
    if (!d) return JSON.stringify({ error: '__DIAG__ not available' });
    return JSON.stringify({
      fetchEvents:     d.fetchEvents     || [],
      xhrEvents:       d.xhrEvents       || [],
      consoleMessages: d.consoleMessages || [],
      domMutations:    d.domMutations    || [],
      storageOps:      d.storageOps      || [],
      cookieAccess:    d.cookieAccess    || [],
      wsEvents:        d.wsEvents        || [],
      errors:          d.errors          || [],
      idbOps:          d.idbOps          || [],
      scrollLog:       d.scrollLog       || [],
      startedAt:       d.startedAt       || null
    });
  } catch(e) {
    return JSON.stringify({ error: String(e) });
  }
})();`
}
