package agents

import (
	"context"

	"github.com/snappy-loop/stories/internal/llm"
)

// SegmentationAgentImpl wraps llm.Client for segmentation.
type SegmentationAgentImpl struct {
	Client *llm.Client
}

// NewSegmentationAgent returns a SegmentationAgent that delegates to the LLM client.
func NewSegmentationAgent(client *llm.Client) SegmentationAgent {
	return &SegmentationAgentImpl{Client: client}
}

// SegmentText delegates to llm.Client.SegmentText.
func (a *SegmentationAgentImpl) SegmentText(ctx context.Context, text string, segmentsCount int, inputType string) ([]*llm.Segment, error) {
	return a.Client.SegmentText(ctx, text, segmentsCount, inputType)
}
