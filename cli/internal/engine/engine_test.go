package engine

import (
	"archive/zip"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/voltyh/extension/cli/internal/collector/mock"
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
		if len(f.Name) > len("media/") && f.Name[:len("media/")] == "media/" {
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
