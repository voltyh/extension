package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/voltyh/extension/cli/internal/collector"
	"github.com/voltyh/extension/cli/internal/model"
)

const (
	defaultDebugURL   = "http://127.0.0.1:9222"
	defaultGeminiHome = "https://gemini.google.com/app"
)

type Collector struct {
	RemoteDebugURL string
}

type devToolsVersion struct {
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type devToolsTarget struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

func (c *Collector) Name() string { return "gemini" }

func (c *Collector) Collect(ctx context.Context, opts collector.Options) (*model.Conversation, error) {
	if !opts.AttachActiveSession {
		return nil, errors.New("gemini collector requires -attach-active-session=true")
	}
	if opts.MediaMode != "" && opts.MediaMode != "files" {
		return nil, fmt.Errorf("unsupported media mode %q (only \"files\" is supported)", opts.MediaMode)
	}

	debugURL := strings.TrimSpace(c.RemoteDebugURL)
	if debugURL == "" {
		debugURL = strings.TrimSpace(os.Getenv("GEMINI_REMOTE_DEBUGGING_URL"))
	}
	if debugURL == "" {
		debugURL = defaultDebugURL
	}

	httpBase, wsURL, err := resolveDevToolsEndpoints(ctx, debugURL)
	if err != nil {
		return nil, err
	}
	targets, err := listDevToolsTargets(ctx, httpBase)
	if err != nil {
		return nil, err
	}
	selected, err := selectGeminiTarget(targets, opts.ConversationID)
	if err != nil {
		return nil, err
	}

	allocCtx, cancelAllocator := chromedp.NewRemoteAllocator(ctx, wsURL)
	defer cancelAllocator()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx, chromedp.WithTargetID(target.ID(selected.ID)))
	defer cancelBrowser()

	actions := []chromedp.Action{chromedp.WaitReady("body", chromedp.ByQuery)}
	if opts.ConversationID != "" {
		actions = append(actions,
			chromedp.Navigate(defaultGeminiHome+"/"+opts.ConversationID),
			chromedp.WaitReady("body", chromedp.ByQuery),
		)
	}

	var payload string
	actions = append(actions, chromedp.Evaluate(extractConversationScript(opts.IncludeMetadata), &payload))
	if err := chromedp.Run(browserCtx, actions...); err != nil {
		return nil, fmt.Errorf("extract from browser: %w", err)
	}
	if strings.TrimSpace(payload) == "" {
		return nil, errors.New("extracted empty payload from Gemini page")
	}

	var conv model.Conversation
	if err := json.Unmarshal([]byte(payload), &conv); err != nil {
		return nil, fmt.Errorf("decode extracted conversation: %w", err)
	}
	if opts.ConversationID != "" {
		conv.ID = opts.ConversationID
	}
	if len(conv.Messages) == 0 {
		return nil, errors.New("no messages found; open a Gemini chat tab in the remote-debug browser and ensure it is fully loaded")
	}
	return &conv, nil
}

func resolveDevToolsEndpoints(ctx context.Context, raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid remote debugging URL: %w", err)
	}
	switch u.Scheme {
	case "ws", "wss":
		httpScheme := "http"
		if u.Scheme == "wss" {
			httpScheme = "https"
		}
		httpBase := fmt.Sprintf("%s://%s", httpScheme, u.Host)
		return strings.TrimRight(httpBase, "/"), raw, nil
	case "http", "https":
		base := strings.TrimRight(raw, "/")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/json/version", nil)
		if err != nil {
			return "", "", err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", "", fmt.Errorf("connect to remote debugging endpoint %s: %w", base, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return "", "", fmt.Errorf("remote debugging endpoint returned status %d", resp.StatusCode)
		}
		var v devToolsVersion
		if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
			return "", "", fmt.Errorf("decode /json/version: %w", err)
		}
		if strings.TrimSpace(v.WebSocketDebuggerURL) == "" {
			return "", "", errors.New("missing webSocketDebuggerUrl in /json/version")
		}
		return base, v.WebSocketDebuggerURL, nil
	default:
		return "", "", fmt.Errorf("unsupported remote debugging URL scheme %q", u.Scheme)
	}
}

func listDevToolsTargets(ctx context.Context, httpBase string) ([]devToolsTarget, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(httpBase, "/")+"/json/list", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list browser targets: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("list browser targets status %d", resp.StatusCode)
	}
	var targets []devToolsTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, fmt.Errorf("decode browser target list: %w", err)
	}
	return targets, nil
}

func selectGeminiTarget(targets []devToolsTarget, conversationID string) (*devToolsTarget, error) {
	var geminiPages []devToolsTarget
	for _, t := range targets {
		if t.Type != "page" {
			continue
		}
		if strings.Contains(strings.ToLower(t.URL), "gemini.google.com") {
			geminiPages = append(geminiPages, t)
		}
	}
	if len(geminiPages) == 0 {
		return nil, errors.New("no gemini.google.com page target found on the remote-debug browser")
	}
	if conversationID != "" {
		for i := range geminiPages {
			if strings.Contains(geminiPages[i].URL, "/"+conversationID) {
				return &geminiPages[i], nil
			}
		}
	}
	return &geminiPages[0], nil
}

func extractConversationScript(includeMetadata bool) string {
	flagLiteral := "false"
	if includeMetadata {
		flagLiteral = "true"
	}
	return `(function() {
  const includeMetadata = ` + flagLiteral + `;
  const asAbsURL = (s) => {
    try { return new URL(s, location.href).href; } catch (_) { return s || ""; }
  };
  const fileNameFromURL = (u) => {
    try {
      const p = new URL(u).pathname.split('/').filter(Boolean).pop() || "";
      return p || "";
    } catch (_) {
      return "";
    }
  };
  const mimeFromURL = (u) => {
    const x = (u || "").toLowerCase();
    if (x.includes("data:image/")) return "image/*";
    if (x.includes("data:video/")) return "video/*";
    if (/\.(png|jpg|jpeg|gif|webp|svg)(\?|$)/.test(x)) return "image/*";
    if (/\.(mp4|webm|mov|m4v)(\?|$)/.test(x)) return "video/*";
    return "";
  };
  const roleFor = (el) => {
    const tag = (el.tagName || "").toLowerCase();
    const roleAttr = (el.getAttribute("data-message-author-role") || el.getAttribute("data-author") || el.getAttribute("data-role") || "").toLowerCase();
    const cls = (el.className || "").toString().toLowerCase();
    if (tag.includes("user-query") || roleAttr.includes("user") || cls.includes("user-query") || cls.includes("from-user")) return "user";
    if (tag.includes("model-response") || roleAttr.includes("model") || roleAttr.includes("assistant") || cls.includes("model-response") || cls.includes("assistant")) return "assistant";
    return "assistant";
  };
  const messageNodes = Array.from(document.querySelectorAll([
    "user-query",
    "model-response",
    "[data-message-author-role]",
    "[data-author]",
    ".user-query",
    ".model-response"
  ].join(",")));
  const seen = new Set();
  const messages = [];
  messageNodes.forEach((el, i) => {
    if (seen.has(el)) return;
    seen.add(el);
    const role = roleFor(el);
    const text = (el.innerText || "").trim();
    const media = [];
    const mediaNodes = el.querySelectorAll("img[src],video[src],video source[src],a[href]");
    let mediaCount = 0;
    mediaNodes.forEach((m) => {
      const tag = (m.tagName || "").toLowerCase();
      const src = m.getAttribute("src");
      const href = m.getAttribute("href");
      const raw = src || href || "";
      if (!raw) return;
      const abs = asAbsURL(raw);
      const isData = abs.startsWith("data:");
      const looksLikeMediaLink = tag === "img" || tag === "video" || tag === "source" || /\.(png|jpg|jpeg|gif|webp|svg|mp4|webm|mov|m4v)(\?|$)/i.test(abs);
      if (!looksLikeMediaLink) return;
      const mime = tag === "img" ? "image/*" : tag === "video" || tag === "source" ? "video/*" : mimeFromURL(abs);
      media.push({
        id: "media-" + i + "-" + (mediaCount++),
        filename: fileNameFromURL(abs),
        mimeType: mime,
        url: isData ? "" : abs,
        dataUri: isData ? abs : ""
      });
    });
    if (!text && media.length === 0) return;
    messages.push({
      id: "msg-" + i,
      role,
      text,
      media
    });
  });
  const pathParts = location.pathname.split("/").filter(Boolean);
  const maybeConvID = pathParts.length >= 2 && pathParts[0] === "app" ? pathParts[1] : "";
  let title = document.title || "Gemini Conversation";
  if (includeMetadata) {
    const lang = document.documentElement?.lang || "";
    if (lang) title = title + " [" + lang + "]";
  }
  return JSON.stringify({
    id: maybeConvID,
    title,
    messages
  });
})();`
}
