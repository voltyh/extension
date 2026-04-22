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

	"github.com/chromedp/cdproto/runtime"
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

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	targetURL := selected.URL
	if opts.ConversationID != "" {
		targetURL = defaultGeminiHome + "/" + opts.ConversationID
	}
	actions := []chromedp.Action{
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	}

	var payload string
	actions = append(actions, chromedp.Evaluate(
		extractConversationScript(opts.IncludeMetadata),
		&payload,
		func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		},
	))
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
	return `(async function() {
  const includeMetadata = ` + flagLiteral + `;
  const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const asAbsURL = (s) => {
    try { return new URL(s, location.href).href; } catch (_) { return s || ""; }
  };
  const filePreviewSelector = 'user-query-file-preview, file-preview, mat-chip, .attachment-chip, [data-test-id="file-preview"], [data-test-id="uploaded-file"]';
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
    if (x.includes("data:text/")) return "text/plain";
    if (/\.(png|jpg|jpeg|gif|webp|svg)(\?|$)/.test(x)) return "image/*";
    if (/\.(mp4|webm|mov|m4v)(\?|$)/.test(x)) return "video/*";
    if (/\.(txt|md|json|csv|log|py|js|ts|tsx|go|java|c|cc|cpp|h|hpp|rs|rb|php|cs|swift|kt|sql|sh|yaml|yml|xml|html)(\?|$)/.test(x)) return "text/plain";
    return "";
  };
  const mimeFromDataURI = (u) => {
    const m = /^data:([^;,]+)/i.exec(u || "");
    return m ? m[1] : "";
  };
  const extForMime = (mime) => {
    const x = (mime || "").toLowerCase();
    if (x.includes("png")) return ".png";
    if (x.includes("jpeg") || x.includes("jpg")) return ".jpg";
    if (x.includes("gif")) return ".gif";
    if (x.includes("webp")) return ".webp";
    if (x.includes("svg")) return ".svg";
    if (x.includes("mp4")) return ".mp4";
    if (x.includes("webm")) return ".webm";
    if (x.includes("quicktime") || x.includes("mov")) return ".mov";
    if (x.includes("json")) return ".json";
    if (x.includes("markdown")) return ".md";
    if (x.includes("csv")) return ".csv";
    if (x.includes("html")) return ".html";
    if (x.includes("xml")) return ".xml";
    if (x.includes("pdf")) return ".pdf";
    if (x.includes("plain")) return ".txt";
    return "";
  };
  const ensureFilename = (name, mime, fallback) => {
    let out = (name || "").trim();
    if (!out || out === "Unknown_Attachment") out = fallback || "attachment";
    if (!/\.[A-Za-z0-9]{2,8}$/.test(out)) {
      const ext = extForMime(mime);
      if (ext) out += ext;
    }
    return out;
  };
  const base64FromBytes = (bytes) => {
    let binary = "";
    const chunk = 0x8000;
    for (let i = 0; i < bytes.length; i += chunk) {
      binary += String.fromCharCode.apply(null, bytes.slice(i, i + chunk));
    }
    return btoa(binary);
  };
  const textToDataURI = (text, mime) => {
    const bytes = new TextEncoder().encode(text || "");
    return "data:" + (mime || "text/plain") + ";base64," + base64FromBytes(bytes);
  };
  const blobToDataURI = async (blob) => await new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onloadend = () => resolve(reader.result || "");
    reader.onerror = () => reject(reader.error || new Error("blob read failed"));
    reader.readAsDataURL(blob);
  });
  const fetchAsDataURI = async (u) => {
    const raw = asAbsURL(u);
    if (!raw) return "";
    if (raw.startsWith("data:")) return raw;
    try {
      const resp = await fetch(raw, { credentials: "include" });
      if (!resp.ok) return "";
      return await blobToDataURI(await resp.blob());
    } catch (_) {
      return "";
    }
  };
  const getFullFileName = (previewEl) => {
    let text = previewEl.innerText || previewEl.textContent || "";
    text = text.replace(/Remove file/gi, "").replace(/\n/g, "").trim();
    const match = text.match(/([a-zA-Z0-9_\-\s\(\)]+\.[a-zA-Z0-9]{2,8})/);
    if (match) return match[1].trim();
    const attr = previewEl.getAttribute("aria-label") || previewEl.getAttribute("mattooltip") || previewEl.title;
    if (attr) return attr.replace(/Remove file/gi, "").replace(/Attachment:/gi, "").trim();
    return text.substring(0, 60) || "Unknown_Attachment";
  };
  const uniqueTopLevel = (nodes) => nodes.filter((node, idx) => !nodes.some((parent, parentIdx) => parentIdx !== idx && parent.contains(node)));
  const expandMessage = (el) => {
    el.querySelectorAll("button").forEach((b) => {
      const btnLabel = (b.getAttribute("aria-label") || "").toLowerCase();
      const tooltip = (b.getAttribute("mattooltip") || "").toLowerCase();
      const hasPopup = b.getAttribute("aria-haspopup");
      if (hasPopup === "menu" || hasPopup === "true" || hasPopup === "dialog") return;
      if (btnLabel.includes("tool") || tooltip.includes("tool")) return;
      if (btnLabel.includes("share") || btnLabel.includes("export") || btnLabel.includes("more action")) return;
      if ((btnLabel.includes("expand") || btnLabel.includes("show more")) && !b.dataset.cliExpanded) {
        b.dataset.cliExpanded = "1";
        try { b.click(); } catch (_) {}
      }
    });
  };
  const waitForElement = async (selector, timeoutMs) => {
    const deadline = Date.now() + (timeoutMs || 4000);
    while (Date.now() < deadline) {
      const el = document.querySelector(selector);
      if (el) return el;
      await wait(100);
    }
    return null;
  };
  const closeActiveViewers = async () => {
    const closers = Array.from(document.querySelectorAll('button[aria-label*="close" i], button[mattooltip*="close" i], [role="button"][aria-label*="close" i]'));
    for (const btn of closers) {
      try { btn.click(); } catch (_) {}
    }
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    document.dispatchEvent(new KeyboardEvent("keyup", { key: "Escape", bubbles: true }));
    await wait(150);
  };
  const materializeConversationDOM = async () => {
    const scroller = document.scrollingElement || document.documentElement || document.body;
    if (!scroller) return;
    const step = Math.max(260, Math.floor(window.innerHeight * 0.85));
    const settleAt = async (targetTop) => {
      scroller.scrollTo({ top: targetTop, behavior: "auto" });
      await wait(120);
      expandMessage(document.body);
      await wait(60);
    };
    for (let i = 0; i < 4; i += 1) {
      await settleAt(scroller.scrollHeight);
      await settleAt(0);
    }
    const maxPasses = 80;
    for (let pass = 0; pass < maxPasses; pass += 1) {
      const top = scroller.scrollTop || 0;
      const next = Math.min(scroller.scrollHeight, top + step);
      await settleAt(next);
      if (Math.abs(next - top) < 2 || next >= scroller.scrollHeight - 2) break;
    }
    await settleAt(0);
  };
  const captureImageByURL = async (rawURL) => {
    const src = asAbsURL(rawURL);
    if (!src) return null;
    if (src.startsWith("data:")) {
      return { dataUri: src, mimeType: mimeFromDataURI(src) || "image/*", src };
    }
    const fetched = await fetchAsDataURI(src);
    if (fetched) {
      return { dataUri: fetched, mimeType: mimeFromDataURI(fetched) || mimeFromURL(src) || "image/*", src };
    }
    return { url: src, mimeType: mimeFromURL(src) || "image/*", src };
  };
  const getImageCapture = async (img) => {
    if (!img) return null;
    const src = asAbsURL(img.currentSrc || img.src || "");
    if (!src) return null;
    if (src.startsWith("data:")) {
      return { dataUri: src, mimeType: mimeFromDataURI(src) || "image/*" };
    }
    try {
      if (typeof img.decode === "function") {
        await img.decode();
      }
    } catch (_) {}
    try {
      try {
        const canvas = document.createElement("canvas");
        canvas.width = img.naturalWidth || img.width || 800;
        canvas.height = img.naturalHeight || img.height || 600;
        const ctx = canvas.getContext("2d");
        if (ctx) {
          ctx.drawImage(img, 0, 0);
          return { dataUri: canvas.toDataURL("image/png"), mimeType: "image/png" };
        }
      } catch (_) {}
    } catch (_) {}
    const fetched = await fetchAsDataURI(src);
    if (fetched) {
      return { dataUri: fetched, mimeType: mimeFromDataURI(fetched) || mimeFromURL(src) || "image/*" };
    }
    return { url: src, mimeType: mimeFromURL(src) || "image/*" };
  };
  const getVideoCapture = async (node) => {
    if (!node) return null;
    const src = asAbsURL(node.currentSrc || node.src || (node.querySelector && node.querySelector("source[src]") ? node.querySelector("source[src]").src : ""));
    if (!src) return null;
    if (src.startsWith("data:")) {
      return { dataUri: src, mimeType: mimeFromDataURI(src) || "video/*" };
    }
    const fetched = await fetchAsDataURI(src);
    if (fetched) {
      return { dataUri: fetched, mimeType: mimeFromDataURI(fetched) || mimeFromURL(src) || "video/*" };
    }
    return { url: src, mimeType: mimeFromURL(src) || "video/*" };
  };
  const capturePreview = async (preview, msgIndex, mediaIndex) => {
    let label = getFullFileName(preview);
    const fallbackHref = asAbsURL(preview.getAttribute("href") || (preview.querySelector("a[href]") ? preview.querySelector("a[href]").getAttribute("href") : ""));
    const clickTarget = preview.querySelector("button, a, [role='button']") || preview;
    try { clickTarget.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true })); } catch (_) {}
    await wait(250);
    const viewer = await waitForElement(".file-preview-sidebar, mat-sidenav, .mat-drawer-opened, .mat-drawer, dialog, [role='dialog'], .fullscreen-preview", 3500);
    let media = null;
    try {
      if (viewer) {
        const img = viewer.querySelector("img:not(.avatar)");
        if (img) {
          const captured = await getImageCapture(img);
          if (captured) {
            media = {
              id: "media-" + msgIndex + "-" + mediaIndex,
              filename: ensureFilename(label, captured.mimeType, "attachment-image-" + (mediaIndex + 1)),
              mimeType: captured.mimeType,
              url: captured.url || "",
              dataUri: captured.dataUri || ""
            };
          }
        }
        if (!media) {
          const video = viewer.querySelector("video, source[src]");
          if (video) {
            const captured = await getVideoCapture(video);
            if (captured) {
              media = {
                id: "media-" + msgIndex + "-" + mediaIndex,
                filename: ensureFilename(label, captured.mimeType, "attachment-video-" + (mediaIndex + 1)),
                mimeType: captured.mimeType,
                url: captured.url || "",
                dataUri: captured.dataUri || ""
              };
            }
          }
        }
        if (!media) {
          let extractedText = "";
          const directTextNode = viewer.querySelector("pre, code, .text-content, [contenteditable='true']");
          if (directTextNode && (directTextNode.innerText || "").trim()) {
            extractedText = directTextNode.innerText.trim();
          }
          if (!extractedText) {
            const iframe = viewer.querySelector("iframe");
            if (iframe) {
              try {
                const iframeDoc = iframe.contentDocument || iframe.contentWindow.document;
                extractedText = (iframeDoc && iframeDoc.body && iframeDoc.body.innerText ? iframeDoc.body.innerText.trim() : "");
              } catch (_) {}
            }
          }
          if (extractedText) {
            media = {
              id: "media-" + msgIndex + "-" + mediaIndex,
              filename: ensureFilename(label, "text/plain", "attachment-text-" + (mediaIndex + 1)),
              mimeType: "text/plain",
              dataUri: textToDataURI(extractedText, "text/plain;charset=utf-8"),
              textContent: extractedText
            };
          }
        }
        if (!media) {
          const downloadHref = asAbsURL(
            (viewer.querySelector("a[href][download]") && viewer.querySelector("a[href][download]").getAttribute("href")) ||
            (viewer.querySelector("a[href*='googleusercontent'], a[href*='drive'], a[href*='docs.google.com']") && viewer.querySelector("a[href*='googleusercontent'], a[href*='drive'], a[href*='docs.google.com']").getAttribute("href")) ||
            fallbackHref
          );
          if (downloadHref) {
            const fetched = await fetchAsDataURI(downloadHref);
            const mimeType = mimeFromDataURI(fetched) || mimeFromURL(downloadHref) || "application/octet-stream";
            media = {
              id: "media-" + msgIndex + "-" + mediaIndex,
              filename: ensureFilename(label || fileNameFromURL(downloadHref), mimeType, "attachment-file-" + (mediaIndex + 1)),
              mimeType,
              url: fetched ? "" : downloadHref,
              dataUri: fetched || ""
            };
          }
        }
      }
      if (!media && fallbackHref) {
        const fetched = await fetchAsDataURI(fallbackHref);
        const mimeType = mimeFromDataURI(fetched) || mimeFromURL(fallbackHref) || "application/octet-stream";
        media = {
          id: "media-" + msgIndex + "-" + mediaIndex,
          filename: ensureFilename(label || fileNameFromURL(fallbackHref), mimeType, "attachment-file-" + (mediaIndex + 1)),
          mimeType,
          url: fetched ? "" : fallbackHref,
          dataUri: fetched || ""
        };
      }
      return media;
    } finally {
      await closeActiveViewers();
    }
  };
  const roleFor = (el) => {
    const tag = (el.tagName || "").toLowerCase();
    const roleAttr = (el.getAttribute("data-message-author-role") || el.getAttribute("data-author") || el.getAttribute("data-role") || "").toLowerCase();
    const cls = (el.className || "").toString().toLowerCase();
    if (tag.includes("user-query") || roleAttr.includes("user") || cls.includes("user-query") || cls.includes("from-user")) return "user";
    if (tag.includes("model-response") || roleAttr.includes("model") || roleAttr.includes("assistant") || cls.includes("model-response") || cls.includes("assistant")) return "assistant";
    return "assistant";
  };
  await materializeConversationDOM();
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
  for (let i = 0; i < messageNodes.length; i += 1) {
    const el = messageNodes[i];
    if (seen.has(el)) continue;
    seen.add(el);
    expandMessage(el);
    await wait(120);
    const role = roleFor(el);
    const targetContent = el.querySelector(".message-content") || el;
    const text = (targetContent.innerText || "").trim();
    const media = [];
    const allFilePreviews = Array.from(targetContent.querySelectorAll(filePreviewSelector));
    const filePreviews = uniqueTopLevel(allFilePreviews);
    const validImages = Array.from(targetContent.querySelectorAll("img:not(.avatar):not([src*='avatar'])"))
      .filter((img) => !img.closest(filePreviewSelector) && (img.naturalWidth || 0) > 20 && !/icon|\/32\/type\//i.test(img.currentSrc || img.src || ""));
    const seenImageSources = new Set();
    let mediaCount = 0;
    for (const img of validImages) {
      const captured = await getImageCapture(img);
      if (!captured) continue;
      const src = asAbsURL(img.currentSrc || img.src || "");
      if (src && seenImageSources.has(src)) continue;
      if (src) seenImageSources.add(src);
      const mime = captured.mimeType || mimeFromURL(src) || "image/*";
      media.push({
        id: "media-" + i + "-" + (mediaCount++),
        filename: ensureFilename(img.getAttribute("alt") || fileNameFromURL(src), mime, "image-" + (mediaCount)),
        mimeType: mime,
        url: captured.url || "",
        dataUri: captured.dataUri || ""
      });
    }
    const imageLinkMatches = [];
    const mdRegex = /!\[[^\]]*\]\(([^)\s]+(?:\s+"[^"]*")?)\)/g;
    let md;
    while ((md = mdRegex.exec(text)) !== null) {
      const raw = (md[1] || "").replace(/\s+"[^"]*"$/, "");
      if (raw) imageLinkMatches.push(raw);
    }
    const linkedImageURLs = Array.from(targetContent.querySelectorAll("a[href]"))
      .map((a) => asAbsURL(a.getAttribute("href") || ""))
      .filter((u) => /\.(png|jpe?g|gif|webp|svg)(\?|#|$)/i.test(u));
    for (const linked of [...imageLinkMatches, ...linkedImageURLs]) {
      if (!linked || seenImageSources.has(linked)) continue;
      const captured = await captureImageByURL(linked);
      if (!captured) continue;
      seenImageSources.add(linked);
      const mime = captured.mimeType || mimeFromURL(linked) || "image/*";
      media.push({
        id: "media-" + i + "-" + (mediaCount++),
        filename: ensureFilename(fileNameFromURL(linked), mime, "image-link-" + (mediaCount)),
        mimeType: mime,
        url: captured.url || "",
        dataUri: captured.dataUri || ""
      });
    }
    for (const preview of filePreviews) {
      const captured = await capturePreview(preview, i, mediaCount);
      if (!captured) continue;
      captured.id = "media-" + i + "-" + (mediaCount++);
      media.push(captured);
    }
    if (!text && media.length === 0) continue;
    messages.push({
      id: "msg-" + messages.length,
      role,
      text,
      media
    });
  }
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
