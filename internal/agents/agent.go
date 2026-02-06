package agents

import (
	"context"
	"io"

	"github.com/snappy-loop/stories/internal/llm"
)

// SegmentationAgent segments text into logical parts.
type SegmentationAgent interface {
	SegmentText(ctx context.Context, text string, segmentsCount int, inputType string) ([]*llm.Segment, error)
}

// AudioAgent generates narration scripts and TTS audio.
type AudioAgent interface {
	GenerateNarration(ctx context.Context, text, audioType, inputType string) (string, error)
	GenerateAudio(ctx context.Context, script, audioType string) (*llm.Audio, error)
}

// ImageAgent generates image prompts and images.
type ImageAgent interface {
	GenerateImagePrompt(ctx context.Context, text, inputType string) (string, error)
	GenerateImage(ctx context.Context, prompt string) (*llm.Image, error)
}

// AudioData reads the full audio bytes from llm.Audio (for gRPC/MCP which need bytes).
func AudioData(a *llm.Audio) ([]byte, error) {
	if a == nil || a.Data == nil {
		return nil, nil
	}
	return io.ReadAll(a.Data)
}
