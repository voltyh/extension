package engine

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/voltyh/extension/cli/internal/collector"
	"github.com/voltyh/extension/cli/internal/model"
	"github.com/voltyh/extension/cli/internal/render"
	"github.com/voltyh/extension/cli/internal/zipper"
)

type Config struct {
	Collector collector.Collector
	OutputZip string
	WorkDir   string
	Resume    bool

	CollectorConversationID string
	AttachActiveSession     bool
	SingleConversationRun   bool
	IncludeMetadata         bool
	MediaMode               string
	DownloadTimeout         time.Duration
	DownloadRetries         int
	DownloadConcurrency     int
}

type mediaTask struct {
	msgIdx   int
	msgID    string
	mediaIdx int
	ref      model.MediaRef
}

type mediaResult struct {
	task       mediaTask
	exportPath string
	checksum   string
	mimeType   string
	failure    *model.Failure
}

func Run(ctx context.Context, cfg Config) (*model.Manifest, error) {
	if cfg.Collector == nil {
		return nil, errors.New("collector is required")
	}
	if cfg.OutputZip == "" {
		return nil, errors.New("output zip is required")
	}
	if cfg.DownloadTimeout <= 0 {
		cfg.DownloadTimeout = 20 * time.Second
	}
	if cfg.DownloadRetries <= 0 {
		cfg.DownloadRetries = 3
	}
	if cfg.DownloadConcurrency <= 0 {
		cfg.DownloadConcurrency = 4
	}

	started := time.Now().UTC()
	manifest := &model.Manifest{
		ToolVersion: model.ToolVersion,
		Collector:   cfg.Collector.Name(),
		StartedAt:   started,
	}

	conv, err := cfg.Collector.Collect(ctx, collector.Options{
		ConversationID:        cfg.CollectorConversationID,
		AttachActiveSession:   cfg.AttachActiveSession,
		SingleConversationRun: cfg.SingleConversationRun,
		IncludeMetadata:       cfg.IncludeMetadata,
		MediaMode:             cfg.MediaMode,
	})
	if err != nil {
		return nil, err
	}
	normalizeConversation(conv)
	manifest.Failures = append(manifest.Failures, validateConversation(conv)...)

	workDir, cleanup, err := resolveWorkDir(cfg.WorkDir, cfg.Resume)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if err := os.MkdirAll(filepath.Join(workDir, "media"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(workDir, "logs"), 0o755); err != nil {
		return nil, err
	}

	logPath := filepath.Join(workDir, "logs", "export.log")
	_ = os.WriteFile(logPath, []byte{}, 0o644)
	logf := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		_ = appendLog(logPath, msg)
	}
	logf("collector=%s", cfg.Collector.Name())
	logf("conversation=%s", conv.ID)

	tasks := collectMediaTasks(conv)
	results := downloadAllMedia(ctx, tasks, workDir, cfg, logf)
	for _, r := range results {
		if r.failure != nil {
			manifest.Failures = append(manifest.Failures, *r.failure)
			continue
		}
		msg := &conv.Messages[r.task.msgIdx]
		media := &msg.Media[r.task.mediaIdx]
		media.ExportPath = r.exportPath
		media.Checksum = r.checksum
		if media.MimeType == "" {
			media.MimeType = r.mimeType
		}
	}

	convJSON, err := marshalStableJSON(conv)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(workDir, "conversation.json"), convJSON, 0o644); err != nil {
		return nil, err
	}

	htmlDoc, err := render.Render(conv)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(workDir, "index.html"), htmlDoc, 0o644); err != nil {
		return nil, err
	}

	manifest.MessageCount = len(conv.Messages)
	manifest.MediaCount = countMedia(conv)
	manifest.FinishedAt = time.Now().UTC()
	manifestJSON, err := marshalStableJSON(manifest)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(workDir, "manifest.json"), manifestJSON, 0o644); err != nil {
		return nil, err
	}

	if err := zipper.CreateFromDir(workDir, cfg.OutputZip); err != nil {
		return nil, err
	}
	return manifest, nil
}

func resolveWorkDir(path string, resume bool) (string, func(), error) {
	if path != "" {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", nil, err
		}
		if !resume {
			if err := os.RemoveAll(path); err != nil {
				return "", nil, err
			}
			if err := os.MkdirAll(path, 0o755); err != nil {
				return "", nil, err
			}
		}
		return path, func() {}, nil
	}
	tmp, err := os.MkdirTemp("", "gemini-export-*")
	if err != nil {
		return "", nil, err
	}
	return tmp, func() { _ = os.RemoveAll(tmp) }, nil
}

func appendLog(path, line string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(time.Now().UTC().Format(time.RFC3339) + " " + line + "\n")
	return err
}

func normalizeConversation(conv *model.Conversation) {
	if conv.ID == "" {
		conv.ID = "conversation-" + hashString(conv.Title)
	}
	for i := range conv.Messages {
		msg := &conv.Messages[i]
		if msg.ID == "" {
			msg.ID = "msg-" + hashString(msg.Role+"\x00"+msg.Text+"\x00"+conv.ID+"\x00"+fmt.Sprintf("%d", i))
		}
		for j := range msg.Media {
			m := &msg.Media[j]
			if m.ID == "" {
				m.ID = "media-" + hashString(msg.ID+"\x00"+m.URL+"\x00"+m.Filename+"\x00"+fmt.Sprintf("%d", j))
			}
		}
	}
}

func validateConversation(conv *model.Conversation) []model.Failure {
	var out []model.Failure
	for _, msg := range conv.Messages {
		if msg.Role == "" {
			out = append(out, model.Failure{
				Code:      "missing_role",
				Scope:     "message",
				MessageID: msg.ID,
				Message:   "message role is empty",
			})
		}
		for _, m := range msg.Media {
			srcCount := 0
			if m.URL != "" {
				srcCount++
			}
			if m.DataURI != "" {
				srcCount++
			}
			if m.SourcePath != "" {
				srcCount++
			}
			if srcCount == 0 {
				out = append(out, model.Failure{
					Code:      "missing_media_source",
					Scope:     "media",
					MessageID: msg.ID,
					MediaID:   m.ID,
					Message:   "media has no source (url, dataUri, sourcePath)",
				})
			}
			if m.URL != "" {
				u, err := url.Parse(m.URL)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
					out = append(out, model.Failure{
						Code:      "invalid_media_url",
						Scope:     "media",
						MessageID: msg.ID,
						MediaID:   m.ID,
						Message:   "media url must be http/https",
					})
				}
			}
		}
	}
	return out
}

func collectMediaTasks(conv *model.Conversation) []mediaTask {
	var tasks []mediaTask
	for i := range conv.Messages {
		msg := &conv.Messages[i]
		for j := range msg.Media {
			tasks = append(tasks, mediaTask{
				msgIdx:   i,
				msgID:    msg.ID,
				mediaIdx: j,
				ref:      msg.Media[j],
			})
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		ai := tasks[i].ref.ID
		aj := tasks[j].ref.ID
		return ai < aj
	})
	return tasks
}

func downloadAllMedia(ctx context.Context, tasks []mediaTask, workDir string, cfg Config, logf func(string, ...any)) []mediaResult {
	if len(tasks) == 0 {
		return nil
	}
	in := make(chan mediaTask)
	out := make(chan mediaResult)
	var wg sync.WaitGroup

	sumToPath := &sync.Map{}
	keyToPath := &sync.Map{}

	worker := func() {
		defer wg.Done()
		for task := range in {
			ref := task.ref
			key := sourceKey(ref)
			if v, ok := keyToPath.Load(key); ok {
				ex := v.(mediaResult)
				out <- mediaResult{
					task:       task,
					exportPath: ex.exportPath,
					checksum:   ex.checksum,
					mimeType:   ex.mimeType,
				}
				continue
			}
			b, mimeType, err := fetchMedia(ctx, ref, cfg)
			if err != nil {
				out <- mediaResult{
					task: task,
					failure: &model.Failure{
						Code:      classifyMediaError(err),
						Scope:     "media",
						MessageID: task.msgID,
						MediaID:   task.ref.ID,
						Message:   err.Error(),
					},
				}
				continue
			}
			sum := sha256.Sum256(b)
			checksum := hex.EncodeToString(sum[:])
			if v, ok := sumToPath.Load(checksum); ok {
				rel := v.(string)
				res := mediaResult{task: task, exportPath: rel, checksum: checksum, mimeType: mimeType}
				keyToPath.Store(key, res)
				out <- res
				continue
			}
			filename := chooseFileName(ref, mimeType, checksum)
			relPath := filepath.ToSlash(filepath.Join("media", filename))
			absPath := filepath.Join(workDir, "media", filename)
			if _, err := os.Stat(absPath); err != nil {
				if !os.IsNotExist(err) {
					out <- mediaResult{
						task: task,
						failure: &model.Failure{
							Code:    "write_failed",
							Scope:   "media",
							MediaID: ref.ID,
							Message: err.Error(),
						},
					}
					continue
				}
				if err := os.WriteFile(absPath, b, 0o644); err != nil {
					out <- mediaResult{
						task: task,
						failure: &model.Failure{
							Code:    "write_failed",
							Scope:   "media",
							MediaID: ref.ID,
							Message: err.Error(),
						},
					}
					continue
				}
			}
			logf("media saved id=%s file=%s", ref.ID, relPath)
			res := mediaResult{task: task, exportPath: relPath, checksum: checksum, mimeType: mimeType}
			sumToPath.Store(checksum, relPath)
			keyToPath.Store(key, res)
			out <- res
		}
	}

	workers := cfg.DownloadConcurrency
	if workers < 1 {
		workers = 1
	}
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go worker()
	}

	go func() {
		for _, t := range tasks {
			in <- t
		}
		close(in)
		wg.Wait()
		close(out)
	}()

	results := make([]mediaResult, 0, len(tasks))
	for r := range out {
		results = append(results, r)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].task.msgIdx != results[j].task.msgIdx {
			return results[i].task.msgIdx < results[j].task.msgIdx
		}
		return results[i].task.mediaIdx < results[j].task.mediaIdx
	})
	return results
}

func classifyMediaError(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.Contains(msg, "blocked"):
		return "blocked"
	case strings.Contains(msg, "404"), strings.Contains(msg, "410"):
		return "missing"
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"):
		return "expired"
	default:
		return "download_failed"
	}
}

func sourceKey(ref model.MediaRef) string {
	return hashString(ref.URL + "\x00" + ref.DataURI + "\x00" + ref.SourcePath)
}

func fetchMedia(ctx context.Context, ref model.MediaRef, cfg Config) ([]byte, string, error) {
	if ref.DataURI != "" {
		return decodeDataURI(ref.DataURI)
	}
	if ref.SourcePath != "" {
		b, err := os.ReadFile(ref.SourcePath)
		return b, mime.TypeByExtension(filepath.Ext(ref.SourcePath)), err
	}
	if ref.URL == "" {
		return nil, "", errors.New("missing media source")
	}

	var lastErr error
	var lastData []byte
	var lastMime string
	for i := 0; i < cfg.DownloadRetries; i++ {
		reqCtx, cancel := context.WithTimeout(ctx, cfg.DownloadTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, ref.URL, nil)
		if err != nil {
			cancel()
			return nil, "", err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		func() {
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode > 299 {
				lastErr = fmt.Errorf("http status %d", resp.StatusCode)
				return
			}
			ct := resp.Header.Get("Content-Type")
			b, rerr := io.ReadAll(resp.Body)
			if rerr != nil {
				lastErr = rerr
				return
			}
			lastErr = nil
			lastMime = ct
			lastData = b
		}()
		cancel()
		if lastErr == nil {
			return lastData, lastMime, nil
		}
	}
	if lastErr == nil {
		lastErr = errors.New("download failed")
	}
	return nil, "", lastErr
}

func decodeDataURI(in string) ([]byte, string, error) {
	if !strings.HasPrefix(in, "data:") {
		return nil, "", errors.New("invalid data uri")
	}
	parts := strings.SplitN(in, ",", 2)
	if len(parts) != 2 {
		return nil, "", errors.New("invalid data uri payload")
	}
	meta := parts[0]
	payload := parts[1]
	mimeType := strings.TrimPrefix(strings.SplitN(meta, ";", 2)[0], "data:")
	if strings.Contains(meta, ";base64") {
		b, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, "", err
		}
		return b, mimeType, nil
	}
	b, err := url.QueryUnescape(payload)
	if err != nil {
		return nil, "", err
	}
	return []byte(b), mimeType, nil
}

func chooseFileName(ref model.MediaRef, mimeType, checksum string) string {
	base := ref.Filename
	if strings.TrimSpace(base) == "" {
		base = ref.ID
	}
	base = sanitizeFileName(base)
	ext := filepath.Ext(base)
	if ext == "" {
		if exts, _ := mime.ExtensionsByType(mimeType); len(exts) > 0 {
			base += exts[0]
		} else {
			base += ".bin"
		}
	}
	prefix := checksum
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return prefix + "_" + base
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "file"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return replacer.Replace(name)
}

func marshalStableJSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func hashString(in string) string {
	sum := sha256.Sum256([]byte(in))
	return hex.EncodeToString(sum[:])[:12]
}

func countMedia(conv *model.Conversation) int {
	n := 0
	for _, msg := range conv.Messages {
		n += len(msg.Media)
	}
	return n
}
