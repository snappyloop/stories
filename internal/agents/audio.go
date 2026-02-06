package agents

import (
	"context"

	"github.com/snappy-loop/stories/internal/llm"
)

// AudioAgentImpl wraps llm.Client for narration and TTS.
type AudioAgentImpl struct {
	Client *llm.Client
}

// NewAudioAgent returns an AudioAgent that delegates to the LLM client.
func NewAudioAgent(client *llm.Client) AudioAgent {
	return &AudioAgentImpl{Client: client}
}

// GenerateNarration delegates to llm.Client.GenerateNarration.
func (a *AudioAgentImpl) GenerateNarration(ctx context.Context, text, audioType, inputType string) (string, error) {
	return a.Client.GenerateNarration(ctx, text, audioType, inputType)
}

// GenerateAudio delegates to llm.Client.GenerateAudio.
func (a *AudioAgentImpl) GenerateAudio(ctx context.Context, script, audioType string) (*llm.Audio, error) {
	return a.Client.GenerateAudio(ctx, script, audioType)
}
