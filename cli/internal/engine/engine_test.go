package engine

import (
	"archive/zip"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/voltyh/extension/cli/internal/collector"
	"github.com/voltyh/extension/cli/internal/collector/mock"
	"github.com/voltyh/extension/cli/internal/model"
)

func TestRunProducesZipContract(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	outZip := filepath.Join(tmp, "out.zip")
	fixture := filepath.Join("..", "..", "testdata", "mock_conversation.json")

	manifest, err := Run(context.Background(), Config{
		Collector: &mock.Collector{
			FixturePath: fixture,
		},
		OutputZip:               outZip,
		WorkDir:                 filepath.Join(tmp, "workspace"),
		Resume:                  false,
		DownloadTimeout:         2 * time.Second,
		DownloadRetries:         1,
		DownloadConcurrency:     2,
		CollectorConversationID: "",
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if manifest.MessageCount != 2 {
		t.Fatalf("expected 2 messages, got %d", manifest.MessageCount)
	}

	zr, err := zip.OpenReader(outZip)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	required := map[string]bool{
		"conversation.json": false,
		"manifest.json":     false,
		"index.html":        false,
		"logs/export.log":   false,
	}
	mediaCount := 0
	for _, f := range zr.File {
		if _, ok := required[f.Name]; ok {
			required[f.Name] = true
		}
		if strings.HasPrefix(f.Name, "media/") {
			mediaCount++
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("missing required file in zip: %s", name)
		}
	}
	if mediaCount != 1 {
		t.Fatalf("expected deduped media count 1, got %d", mediaCount)
	}
}

type stubCollector struct {
	conv *model.Conversation
}

func (s stubCollector) Name() string { return "stub" }

func (s stubCollector) Collect(context.Context, collector.Options) (*model.Conversation, error) {
	return s.conv, nil
}

func TestRunPreservesTextAttachmentContent(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	outZip := filepath.Join(tmp, "text.zip")
	conv := &model.Conversation{
		ID:    "conv-1",
		Title: "Text attachment",
		Messages: []model.Message{
			{
				ID:   "msg-1",
				Role: "user",
				Text: "see attached",
				Media: []model.MediaRef{
					{
						ID:          "media-1",
						Filename:    "notes.txt",
						MimeType:    "text/plain",
						DataURI:     "data:text/plain;base64,SGVsbG8gZnJvbSBhdHRhY2htZW50",
						TextContent: "Hello from attachment",
					},
				},
			},
		},
	}

	_, err := Run(context.Background(), Config{
		Collector:           stubCollector{conv: conv},
		OutputZip:           outZip,
		WorkDir:             filepath.Join(tmp, "workspace"),
		DownloadTimeout:     2 * time.Second,
		DownloadRetries:     1,
		DownloadConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	zr, err := zip.OpenReader(outZip)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	var sawTextFile bool
	var sawHTML bool
	for _, f := range zr.File {
		switch {
		case strings.HasPrefix(f.Name, "media/") && strings.HasSuffix(f.Name, "notes.txt"):
			sawTextFile = true
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open media file: %v", err)
			}
			body, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatalf("read media file: %v", err)
			}
			if string(body) != "Hello from attachment" {
				t.Fatalf("unexpected attachment body: %q", string(body))
			}
		case f.Name == "index.html":
			sawHTML = true
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open index html: %v", err)
			}
			body, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatalf("read index html: %v", err)
			}
			html := string(body)
			if !strings.Contains(html, "Hello from attachment") {
				t.Fatalf("expected rendered attachment content in html")
			}
		}
	}
	if !sawTextFile {
		t.Fatalf("expected notes.txt media file in zip")
	}
	if !sawHTML {
		t.Fatalf("expected index.html in zip")
	}
}
