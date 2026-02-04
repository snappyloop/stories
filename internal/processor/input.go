package processor

import (
	"context"

	"github.com/snappy-loop/stories/internal/models"
)

// InputProcessor processes different input types into text for segmentation.
// Designed for future agent architecture.
type InputProcessor interface {
	Name() string
	CanProcess(inputSource string) bool
	Process(ctx context.Context, job *models.Job, jobFiles []*models.JobFile) (string, error)
}

// InputProcessorRegistry manages available input processors
type InputProcessorRegistry struct {
	processors []InputProcessor
}

// NewInputProcessorRegistry creates a registry with the given processors
func NewInputProcessorRegistry(processors ...InputProcessor) *InputProcessorRegistry {
	return &InputProcessorRegistry{processors: processors}
}

// GetProcessor returns the first processor that can handle the input source
func (r *InputProcessorRegistry) GetProcessor(inputSource string) InputProcessor {
	for _, p := range r.processors {
		if p.CanProcess(inputSource) {
			return p
		}
	}
	return nil
}
