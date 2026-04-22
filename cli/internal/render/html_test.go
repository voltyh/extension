package render

import (
	"strings"
	"testing"

	"github.com/voltyh/extension/cli/internal/model"
)

func TestRenderIncludesTextAttachmentContent(t *testing.T) {
	conv := &model.Conversation{
		Title: "Example",
		Messages: []model.Message{
			{
				ID:   "msg-1",
				Role: "user",
				Text: "hello",
				Media: []model.MediaRef{
					{
						ID:          "media-1",
						Filename:    "notes.txt",
						MimeType:    "text/plain",
						ExportPath:  "media/notes.txt",
						TextContent: "line one\nline two",
					},
				},
			},
		},
	}

	out, err := Render(conv)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	html := string(out)
	for _, want := range []string{"notes.txt", "line one", "line two"} {
		if !strings.Contains(html, want) {
			t.Fatalf("render output missing %q", want)
		}
	}
	if strings.Contains(html, "media/notes.txt") {
		t.Fatalf("render output should not include direct attachment href for text attachment")
	}
	if strings.Contains(html, "<a href=") {
		t.Fatalf("render output should not contain attachment hyperlinks")
	}
}
