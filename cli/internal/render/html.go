package render

import (
	"bytes"
	"html/template"
	"strings"

	"github.com/voltyh/extension/cli/internal/model"
)

const pageTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{ .Title }}</title>
  <style>
    body { font-family: sans-serif; max-width: 900px; margin: 24px auto; padding: 0 16px; }
    .m { border: 1px solid #ddd; border-radius: 8px; padding: 12px; margin: 12px 0; }
    .u { border-left: 5px solid #7e57c2; }
    .a { border-left: 5px solid #26a69a; }
    .meta { color: #666; font-size: 12px; margin-bottom: 8px; }
    img, video { max-width: 100%; border-radius: 6px; margin-top: 8px; }
    .attachment { margin-top: 8px; }
    pre { white-space: pre-wrap; background: #f6f8fa; padding: 10px; border-radius: 6px; overflow-x: auto; }
  </style>
</head>
<body>
  <h1>{{ .Title }}</h1>
  {{ range .Messages }}
    <section class="m {{ if eq .Role "user" }}u{{ else }}a{{ end }}">
      <div class="meta">{{ .Role }}{{ if .CreatedAt }} · {{ .CreatedAt }}{{ end }}</div>
      <div>{{ .Text }}</div>
      {{ range .Media }}
        {{ if .ExportPath }}
          {{ if .TextContent }}
            <div class="attachment">
              <div><a href="{{ .ExportPath }}">{{ .Filename }}</a></div>
              <pre>{{ .TextContent }}</pre>
            </div>
          {{ else if isImage .MimeType }}
            <img src="{{ .ExportPath }}" alt="{{ .Filename }}">
          {{ else if isVideo .MimeType }}
            <video controls src="{{ .ExportPath }}"></video>
          {{ else }}
            <div><a href="{{ .ExportPath }}">{{ .Filename }}</a></div>
          {{ end }}
        {{ end }}
      {{ end }}
    </section>
  {{ end }}
</body>
</html>`

func Render(conv *model.Conversation) ([]byte, error) {
	tpl, err := template.New("index").Funcs(template.FuncMap{
		"isImage": func(mime string) bool { return strings.HasPrefix(mime, "image/") },
		"isVideo": func(mime string) bool { return strings.HasPrefix(mime, "video/") },
	}).Parse(pageTemplate)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, conv); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
