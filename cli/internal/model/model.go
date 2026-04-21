package model

import "time"

const ToolVersion = "0.1.0"

type Conversation struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Messages []Message `json:"messages"`
}

type Message struct {
	ID        string     `json:"id"`
	Role      string     `json:"role"`
	Text      string     `json:"text"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	Media     []MediaRef `json:"media,omitempty"`
}

type MediaRef struct {
	ID         string `json:"id"`
	Filename   string `json:"filename,omitempty"`
	MimeType   string `json:"mimeType,omitempty"`
	URL        string `json:"url,omitempty"`
	DataURI    string `json:"dataUri,omitempty"`
	SourcePath string `json:"sourcePath,omitempty"`

	ExportPath string `json:"exportPath,omitempty"`
	Checksum   string `json:"checksum,omitempty"`
}

type Failure struct {
	Code      string `json:"code"`
	Scope     string `json:"scope"`
	Message   string `json:"message"`
	MessageID string `json:"messageId,omitempty"`
	MediaID   string `json:"mediaId,omitempty"`
}

type Manifest struct {
	ToolVersion string    `json:"toolVersion"`
	Collector   string    `json:"collector"`
	StartedAt   time.Time `json:"startedAt"`
	FinishedAt  time.Time `json:"finishedAt"`

	MessageCount int `json:"messageCount"`
	MediaCount   int `json:"mediaCount"`

	Failures []Failure `json:"failures"`
}

