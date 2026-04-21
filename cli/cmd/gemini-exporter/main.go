package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/voltyh/extension/cli/internal/collector"
	"github.com/voltyh/extension/cli/internal/collector/gemini"
	"github.com/voltyh/extension/cli/internal/collector/mock"
	"github.com/voltyh/extension/cli/internal/engine"
)

func main() {
	var (
		collectorName = flag.String("collector", "mock", "collector to use: mock|gemini")
		fixturePath   = flag.String("fixture", "", "path to mock fixture json (required for collector=mock)")
		outputZip     = flag.String("output", "", "output zip file path")
		workdir       = flag.String("workdir", "", "workspace directory (optional, enables resume-friendly runs)")
		resume        = flag.Bool("resume", false, "reuse workspace if it already exists")
		convID        = flag.String("conversation-id", "", "gemini conversation id (collector-dependent)")
		timeout       = flag.Duration("timeout", 20*time.Second, "network timeout per request")
		retries       = flag.Int("retries", 3, "download retry attempts")
		concurrency   = flag.Int("concurrency", 4, "media download concurrency")
	)
	flag.Parse()

	if *outputZip == "" {
		*outputZip = filepath.Join(".", "gemini-export.zip")
	}

	var c collector.Collector
	switch *collectorName {
	case "mock":
		if *fixturePath == "" {
			exitf("collector=mock requires -fixture")
		}
		c = &mock.Collector{FixturePath: *fixturePath}
	case "gemini":
		c = &gemini.Collector{}
	default:
		exitf("unknown collector: %s", *collectorName)
	}

	cfg := engine.Config{
		Collector:               c,
		OutputZip:               *outputZip,
		WorkDir:                 *workdir,
		Resume:                  *resume,
		DownloadTimeout:         *timeout,
		DownloadRetries:         *retries,
		DownloadConcurrency:     *concurrency,
		CollectorConversationID: *convID,
	}

	m, err := engine.Run(context.Background(), cfg)
	if err != nil {
		exitf("export failed: %v", err)
	}
	fmt.Printf("export complete: %s\n", *outputZip)
	fmt.Printf("collector: %s\n", m.Collector)
	fmt.Printf("messages: %d\n", m.MessageCount)
	fmt.Printf("media: %d\n", m.MediaCount)
	fmt.Printf("failures: %d\n", len(m.Failures))
}

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
