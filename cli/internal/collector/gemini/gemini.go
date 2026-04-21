package gemini

import (
	"context"
	"errors"

	"github.com/voltyh/extension/cli/internal/collector"
	"github.com/voltyh/extension/cli/internal/model"
)

type Collector struct{}

func (c *Collector) Name() string { return "gemini" }

func (c *Collector) Collect(_ context.Context, opts collector.Options) (*model.Conversation, error) {
	_ = opts
	return nil, errors.New(
		"gemini collector v1 is not implemented yet; planned mode is active-session attach, one-chat-per-run, media/* file references, and metadata capture where available",
	)
}
