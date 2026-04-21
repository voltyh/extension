package gemini

import (
	"context"
	"errors"

	"github.com/voltyh/extension/cli/internal/collector"
	"github.com/voltyh/extension/cli/internal/model"
)

type Collector struct{}

func (c *Collector) Name() string { return "gemini" }

func (c *Collector) Collect(_ context.Context, _ collector.Options) (*model.Conversation, error) {
	return nil, errors.New("gemini collector v1 is not implemented yet; collector boundary is in place")
}

