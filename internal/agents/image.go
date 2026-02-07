package agents

import (
	"context"

	"github.com/snappy-loop/stories/internal/llm"
)

// ImageAgentImpl wraps llm.Client for image prompt and image generation.
type ImageAgentImpl struct {
	Client *llm.Client
}

// NewImageAgent returns an ImageAgent that delegates to the LLM client.
func NewImageAgent(client *llm.Client) ImageAgent {
	return &ImageAgentImpl{Client: client}
}

// GenerateImagePrompt delegates to llm.Client.GenerateImagePrompt.
func (a *ImageAgentImpl) GenerateImagePrompt(ctx context.Context, text, inputType string) (string, error) {
	return a.Client.GenerateImagePrompt(ctx, text, inputType)
}

// GenerateImage delegates to llm.Client.GenerateImage.
func (a *ImageAgentImpl) GenerateImage(ctx context.Context, prompt string) (*llm.Image, error) {
	return a.Client.GenerateImage(ctx, prompt)
}
