package processor

import (
	"context"

	"github.com/snappy-loop/stories/internal/models"
)

// TextProcessor processes text-only input (pass-through)
type TextProcessor struct{}

// NewTextProcessor creates a new TextProcessor
func NewTextProcessor() *TextProcessor {
	return &TextProcessor{}
}

// Name returns the processor name
func (p *TextProcessor) Name() string {
	return "TextProcessor"
}

// CanProcess returns true for "text" input source
func (p *TextProcessor) CanProcess(inputSource string) bool {
	return inputSource == "text"
}

// Process returns the job's input text unchanged
func (p *TextProcessor) Process(ctx context.Context, job *models.Job, _ []*models.JobFile) (string, error) {
	return job.InputText, nil
}
