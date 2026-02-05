package processor

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/llm"
	"github.com/snappy-loop/stories/internal/models"
	"github.com/snappy-loop/stories/internal/storage"
)

// MultiFileProcessor processes multiple files with Gemini 3 Pro vision
type MultiFileProcessor struct {
	llmClient     *llm.Client
	storageClient *storage.Client
	fileRepo      *database.FileRepository
	jobFileRepo   *database.JobFileRepository
}

// NewMultiFileProcessor creates a new MultiFileProcessor
func NewMultiFileProcessor(
	llmClient *llm.Client,
	storageClient *storage.Client,
	fileRepo *database.FileRepository,
	jobFileRepo *database.JobFileRepository,
) *MultiFileProcessor {
	return &MultiFileProcessor{
		llmClient:     llmClient,
		storageClient: storageClient,
		fileRepo:      fileRepo,
		jobFileRepo:   jobFileRepo,
	}
}

// Name returns the processor name
func (p *MultiFileProcessor) Name() string {
	return "MultiFileProcessor"
}

// CanProcess returns true for "files" or "mixed" input source
func (p *MultiFileProcessor) CanProcess(inputSource string) bool {
	return inputSource == "files" || inputSource == "mixed"
}

// Process extracts text from each file via Gemini vision and combines with optional text input
func (p *MultiFileProcessor) Process(ctx context.Context, job *models.Job, jobFiles []*models.JobFile) (string, error) {
	var parts []string

	if job.InputText != "" && job.InputText != "[pending extraction]" {
		parts = append(parts, job.InputText)
	}

	// Process each file in order
	sort.Slice(jobFiles, func(i, j int) bool {
		return jobFiles[i].ProcessingOrder < jobFiles[j].ProcessingOrder
	})

	for _, jf := range jobFiles {
		file, err := p.fileRepo.GetByID(ctx, jf.FileID)
		if err != nil {
			log.Error().Err(err).Str("file_id", jf.FileID.String()).Msg("Failed to get file for extraction")
			_ = p.jobFileRepo.UpdateExtraction(ctx, jf.ID, nil, "failed")
			return "", fmt.Errorf("file %s: %w", jf.FileID.String(), err)
		}

		rc, err := p.storageClient.GetObject(ctx, file.S3Key)
		if err != nil {
			log.Error().Err(err).Str("s3_key", file.S3Key).Msg("Failed to download file from S3")
			_ = p.jobFileRepo.UpdateExtraction(ctx, jf.ID, nil, "failed")
			return "", fmt.Errorf("download file %s: %w", file.Filename, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			_ = p.jobFileRepo.UpdateExtraction(ctx, jf.ID, nil, "failed")
			return "", fmt.Errorf("read file %s: %w", file.Filename, err)
		}

		extracted, err := p.llmClient.ExtractContent(ctx, data, file.MimeType, job.InputType)
		if err != nil {
			log.Error().Err(err).Str("file_id", jf.FileID.String()).Msg("Gemini vision extraction failed")
			_ = p.jobFileRepo.UpdateExtraction(ctx, jf.ID, nil, "failed")
			return "", fmt.Errorf("extract %s: %w", file.Filename, err)
		}

		jf.ExtractedText = &extracted
		jf.Status = "succeeded"
		if err := p.jobFileRepo.UpdateExtraction(ctx, jf.ID, &extracted, "succeeded"); err != nil {
			log.Warn().Err(err).Str("job_file_id", jf.ID.String()).Msg("Failed to update job_file extraction")
		}

		parts = append(parts, extracted)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}
