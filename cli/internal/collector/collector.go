package collector

import (
	"context"

	"github.com/voltyh/extension/cli/internal/model"
)

type Options struct {
	ConversationID string
}

type Collector interface {
	Name() string
	Collect(ctx context.Context, opts Options) (*model.Conversation, error)
}
