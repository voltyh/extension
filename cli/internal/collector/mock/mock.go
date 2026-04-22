package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/voltyh/extension/cli/internal/collector"
	"github.com/voltyh/extension/cli/internal/model"
)

type Collector struct {
	FixturePath string
}

func (c *Collector) Name() string { return "mock" }

func (c *Collector) Collect(_ context.Context, _ collector.Options) (*model.Conversation, error) {
	b, err := os.ReadFile(c.FixturePath)
	if err != nil {
		return nil, fmt.Errorf("read fixture: %w", err)
	}
	var conv model.Conversation
	if err := json.Unmarshal(b, &conv); err != nil {
		return nil, fmt.Errorf("parse fixture: %w", err)
	}
	return &conv, nil
}
