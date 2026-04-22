package gemini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveDevToolsEndpointsHTTP(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/test"}`))
	}))
	defer srv.Close()

	httpBase, wsURL, err := resolveDevToolsEndpoints(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("resolveDevToolsEndpoints returned error: %v", err)
	}
	if httpBase != srv.URL {
		t.Fatalf("unexpected http base: %q", httpBase)
	}
	if wsURL != "ws://127.0.0.1:9222/devtools/browser/test" {
		t.Fatalf("unexpected ws url: %q", wsURL)
	}
}

func TestSelectGeminiTarget(t *testing.T) {
	t.Parallel()

	targets := []devToolsTarget{
		{Type: "page", URL: "https://example.com"},
		{Type: "page", URL: "https://gemini.google.com/app/conv-123"},
		{Type: "page", URL: "https://gemini.google.com/app/conv-999"},
	}

	selected, err := selectGeminiTarget(targets, "conv-999")
	if err != nil {
		t.Fatalf("selectGeminiTarget returned error: %v", err)
	}
	if selected.URL != "https://gemini.google.com/app/conv-999" {
		t.Fatalf("unexpected target selected: %s", selected.URL)
	}
}
