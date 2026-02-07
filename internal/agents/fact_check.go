package agents

import (
	"context"

	"github.com/snappy-loop/stories/internal/llm"
)

// FactCheckAgentImpl wraps llm.Client for fact-checking.
type FactCheckAgentImpl struct {
	Client *llm.Client
}

// NewFactCheckAgent returns a FactCheckAgent that delegates to the LLM client.
func NewFactCheckAgent(client *llm.Client) FactCheckAgent {
	return &FactCheckAgentImpl{Client: client}
}

// FactCheckSegment delegates to llm.Client.FactCheckSegment.
func (a *FactCheckAgentImpl) FactCheckSegment(ctx context.Context, text string) (string, error) {
	return a.Client.FactCheckSegment(ctx, text)
}
