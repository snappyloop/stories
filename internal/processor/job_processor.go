package processor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/config"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/kafka"
	"github.com/snappy-loop/stories/internal/llm"
	"github.com/snappy-loop/stories/internal/models"
	"github.com/snappy-loop/stories/internal/storage"
)

// JobProcessor handles job processing pipeline
type JobProcessor struct {
	db              *database.DB
	jobRepo         *database.JobRepository
	segmentRepo     *database.SegmentRepository
	assetRepo       *database.AssetRepository
	jobFileRepo     *database.JobFileRepository
	fileRepo        *database.FileRepository
	inputRegistry   *InputProcessorRegistry
	llmClient       *llm.Client
	storageClient   *storage.Client
	webhookProducer *kafka.Producer
	config          *config.Config
}

// NewJobProcessor creates a new job processor
func NewJobProcessor(
	db *database.DB,
	llmClient *llm.Client,
	storageClient *storage.Client,
	webhookProducer *kafka.Producer,
	cfg *config.Config,
	inputRegistry *InputProcessorRegistry,
	jobFileRepo *database.JobFileRepository,
	fileRepo *database.FileRepository,
) *JobProcessor {
	return &JobProcessor{
		db:              db,
		jobRepo:         database.NewJobRepository(db),
		segmentRepo:     database.NewSegmentRepository(db),
		assetRepo:       database.NewAssetRepository(db),
		jobFileRepo:     jobFileRepo,
		fileRepo:        fileRepo,
		inputRegistry:   inputRegistry,
		llmClient:       llmClient,
		storageClient:   storageClient,
		webhookProducer: webhookProducer,
		config:          cfg,
	}
}

// audioExtension returns the file extension for an audio MIME type (e.g. "audio/wav" -> "wav").
func audioExtension(mimeType string) string {
	switch mimeType {
	case "audio/mpeg":
		return "mp3"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav"
	default:
		return "wav"
	}
}

// imageExtension returns the file extension for an image MIME type (e.g. "image/jpeg" -> "jpg").
func imageExtension(mimeType string) string {
	switch mimeType {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	default:
		return "png"
	}
}

// ProcessJob processes a job end-to-end
func (p *JobProcessor) ProcessJob(ctx context.Context, jobID uuid.UUID) error {
	log.Info().Str("job_id", jobID.String()).Msg("Starting job processing")

	// Get job from database
	job, err := p.jobRepo.GetByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("failed to get job: %w", err)
	}

	// Skip if job already reached a terminal state (idempotent for duplicate Kafka deliveries)
	if job.Status == "succeeded" || job.Status == "failed" || job.Status == "canceled" {
		log.Warn().
			Str("job_id", jobID.String()).
			Str("status", job.Status).
			Msg("Job already processed")
		return nil
	}

	// Idempotent restart: if status is "running", a previous worker may have crashed before
	// finishing. Clear partial segments and assets so we don't create duplicates when we
	// re-run the pipeline (segments table has no unique constraint on (job_id, idx)).
	if job.Status == "running" {
		log.Info().
			Str("job_id", jobID.String()).
			Msg("Job was running; clearing partial state for idempotent restart")
		if err := p.segmentRepo.DeleteByJobID(ctx, jobID); err != nil {
			return fmt.Errorf("failed to clear segments for restart: %w", err)
		}
		if err := p.jobRepo.UpdateMarkup(ctx, jobID, ""); err != nil {
			log.Error().Err(err).Msg("Failed to clear job markup for restart")
		}
	}

	// Update job status to running
	if err := p.updateJobStatus(ctx, jobID, "running", nil, nil); err != nil {
		log.Error().Err(err).Msg("Failed to update job status to running")
	}

	// Process job with error handling
	if err := p.processJobPipeline(ctx, job); err != nil {
		log.Error().
			Err(err).
			Str("job_id", jobID.String()).
			Msg("Job processing failed")

		// Update job status to failed
		errCode := "processing_error"
		errMsg := err.Error()
		if err := p.updateJobStatus(ctx, jobID, "failed", &errCode, &errMsg); err != nil {
			log.Error().Err(err).Msg("Failed to update job status to failed")
		}

		// Publish webhook event for failure
		p.publishWebhookEvent(ctx, jobID, "job_failed")

		return err
	}

	// Update job status to succeeded
	if err := p.updateJobStatus(ctx, jobID, "succeeded", nil, nil); err != nil {
		log.Error().Err(err).Msg("Failed to update job status to succeeded")
	}

	// Publish webhook event for success
	p.publishWebhookEvent(ctx, jobID, "job_completed")

	log.Info().
		Str("job_id", jobID.String()).
		Msg("Job processing completed successfully")

	return nil
}

// processJobPipeline executes the full processing pipeline
func (p *JobProcessor) processJobPipeline(ctx context.Context, job *models.Job) error {
	// Step 0: Resolve input to text (e.g. extract from files via vision)
	textToSegment := job.InputText
	if p.inputRegistry != nil {
		processor := p.inputRegistry.GetProcessor(job.InputSource)
		if processor != nil {
			var jobFiles []*models.JobFile
			if job.InputSource == "files" || job.InputSource == "mixed" {
				var err error
				jobFiles, err = p.jobFileRepo.ListByJob(ctx, job.ID)
				if err != nil {
					return fmt.Errorf("failed to list job files: %w", err)
				}
			}
			combined, err := processor.Process(ctx, job, jobFiles)
			if err != nil {
				return fmt.Errorf("input processing failed: %w", err)
			}
			textToSegment = combined
			if job.InputSource != "text" {
				if err := p.jobRepo.UpdateExtractedText(ctx, job.ID, &combined); err != nil {
					log.Warn().Err(err).Msg("Failed to update job extracted_text")
				}
				job.ExtractedText = &combined
			}
		}
	}

	// Step 1: Segment the text
	log.Info().Str("job_id", job.ID.String()).Msg("Step 1: Segmenting text")
	segments, err := p.llmClient.SegmentText(ctx, textToSegment, job.PicturesCount, job.InputType)
	if err != nil {
		return fmt.Errorf("segmentation failed: %w", err)
	}

	// Save segments to database and keep their IDs for asset foreign keys.
	// Sanitize text to valid UTF-8 so PostgreSQL never sees invalid byte sequences.
	segmentIDs := make([]uuid.UUID, len(segments))
	for i, seg := range segments {
		titleVal := ""
		if seg.Title != nil {
			titleVal = strings.ToValidUTF8(*seg.Title, "\uFFFD")
		}
		if titleVal == "" {
			titleVal = fmt.Sprintf("Part %d", i+1)
		}
		segment := &models.Segment{
			ID:          uuid.New(),
			JobID:       job.ID,
			Idx:         i,
			StartChar:   seg.StartChar,
			EndChar:     seg.EndChar,
			Title:       &titleVal,
			SegmentText: strings.ToValidUTF8(seg.Text, "\uFFFD"),
			Status:      "queued",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		segmentIDs[i] = segment.ID

		if err := p.segmentRepo.Create(ctx, segment); err != nil {
			return fmt.Errorf("failed to save segment %d: %w", i, err)
		}
	}

	log.Info().
		Str("job_id", job.ID.String()).
		Int("segments", len(segments)).
		Msg("Segmentation complete")

	// Step 2: Process each segment asynchronously with limited concurrency
	log.Info().Str("job_id", job.ID.String()).Msg("Step 2: Processing segments (async)")

	concurrency := p.config.MaxConcurrentSegments
	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex

	for i := range segments {
		idx := i
		segCopy := segments[i]
		segmentID := segmentIDs[i]
		wg.Add(1)
		go func(seg *llm.Segment) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			log.Info().
				Str("job_id", job.ID.String()).
				Int("segment", idx+1).
				Int("total", len(segments)).
				Msg("Processing segment")

			if err := p.processSegment(ctx, job, seg, idx, segmentID); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("segment %d: %w", idx, err)
				}
				mu.Unlock()
			}
		}(segCopy)
	}

	wg.Wait()
	if firstErr != nil {
		return firstErr
	}

	// Step 3: Generate output markup
	log.Info().Str("job_id", job.ID.String()).Msg("Step 3: Generating output markup")
	markup, err := p.generateOutputMarkup(ctx, job.ID)
	if err != nil {
		return fmt.Errorf("failed to generate markup: %w", err)
	}

	// Save markup to job
	if err := p.jobRepo.UpdateMarkup(ctx, job.ID, markup); err != nil {
		return fmt.Errorf("failed to save markup: %w", err)
	}

	return nil
}

// processSegment processes a single segment. segmentID is the database segment ID (used for asset FK).
func (p *JobProcessor) processSegment(ctx context.Context, job *models.Job, seg *llm.Segment, idx int, segmentID uuid.UUID) error {
	// Update segment status to running
	if err := p.segmentRepo.UpdateStatus(ctx, job.ID, idx, "running"); err != nil {
		log.Error().Err(err).Msg("Failed to update segment status")
	}

	// Generate narration script
	script, err := p.llmClient.GenerateNarration(ctx, seg.Text, job.AudioType, job.InputType)
	if err != nil {
		p.segmentRepo.UpdateStatus(ctx, job.ID, idx, "failed")
		return fmt.Errorf("narration generation failed: %w", err)
	}

	// Generate audio (Gemini Pro)
	audio, err := p.llmClient.GenerateAudio(ctx, script, job.AudioType)
	if err != nil {
		log.Error().Err(err).
			Str("job_id", job.ID.String()).
			Int("segment", idx).
			Msg("Audio generation failed")
		p.segmentRepo.UpdateStatus(ctx, job.ID, idx, "failed")
		return fmt.Errorf("audio generation failed: %w", err)
	}

	log.Debug().
		Str("job_id", job.ID.String()).
		Int("segment", idx).
		Int64("audio_size_bytes", audio.Size).
		Str("mime_type", audio.MimeType).
		Msg("Audio from Gemini, uploading to S3")

	// TTS output is WAV (see GEMINI_INTEGRATION.md). Use actual format so Content-Type matches payload.
	mimeType := audio.MimeType
	if mimeType == "" {
		mimeType = "audio/wav"
	}
	ext := audioExtension(mimeType)
	audioKey := fmt.Sprintf("jobs/%s/segments/%d/audio.%s", job.ID, idx, ext)
	if err := p.storageClient.Upload(ctx, audioKey, audio.Data, mimeType); err != nil {
		p.segmentRepo.UpdateStatus(ctx, job.ID, idx, "failed")
		return fmt.Errorf("audio upload failed: %w", err)
	}

	// Save audio asset (use DB segment ID for FK)
	audioAsset := &models.Asset{
		ID:        uuid.New(),
		JobID:     job.ID,
		SegmentID: &segmentID,
		Kind:      "audio",
		MimeType:  mimeType,
		S3Bucket:  p.config.S3Bucket,
		S3Key:     audioKey,
		SizeBytes: audio.Size,
		Meta: map[string]any{
			"duration": audio.Duration,
			"model":    audio.Model,
		},
		CreatedAt: time.Now(),
	}

	if err := p.assetRepo.Create(ctx, audioAsset); err != nil {
		return fmt.Errorf("failed to save audio asset: %w", err)
	}

	// Generate image prompt
	imagePrompt, err := p.llmClient.GenerateImagePrompt(ctx, seg.Text, job.InputType)
	if err != nil {
		p.segmentRepo.UpdateStatus(ctx, job.ID, idx, "failed")
		return fmt.Errorf("image prompt generation failed: %w", err)
	}

	// Generate image
	image, err := p.llmClient.GenerateImage(ctx, imagePrompt)
	if err != nil {
		p.segmentRepo.UpdateStatus(ctx, job.ID, idx, "failed")
		return fmt.Errorf("image generation failed: %w", err)
	}

	// Use actual format from Gemini so Content-Type and file extension match payload.
	imgMimeType := image.MimeType
	if imgMimeType == "" {
		imgMimeType = "image/png"
	}
	imgExt := imageExtension(imgMimeType)
	imageKey := fmt.Sprintf("jobs/%s/segments/%d/image.%s", job.ID, idx, imgExt)

	log.Debug().
		Str("job_id", job.ID.String()).
		Int("segment", idx).
		Int64("image_size_bytes", image.Size).
		Str("mime_type", imgMimeType).
		Msg("Image from Gemini, uploading to S3")

	// Upload image to S3
	if err := p.storageClient.Upload(ctx, imageKey, image.Data, imgMimeType); err != nil {
		p.segmentRepo.UpdateStatus(ctx, job.ID, idx, "failed")
		return fmt.Errorf("image upload failed: %w", err)
	}

	// Save image asset (use DB segment ID for FK)
	imageAsset := &models.Asset{
		ID:        uuid.New(),
		JobID:     job.ID,
		SegmentID: &segmentID,
		Kind:      "image",
		MimeType:  imgMimeType,
		S3Bucket:  p.config.S3Bucket,
		S3Key:     imageKey,
		SizeBytes: image.Size,
		Meta: map[string]any{
			"resolution": image.Resolution,
			"model":      image.Model,
		},
		CreatedAt: time.Now(),
	}

	if err := p.assetRepo.Create(ctx, imageAsset); err != nil {
		return fmt.Errorf("failed to save image asset: %w", err)
	}

	// Update segment status to succeeded
	if err := p.segmentRepo.UpdateStatus(ctx, job.ID, idx, "succeeded"); err != nil {
		log.Error().Err(err).Msg("Failed to update segment status to succeeded")
	}

	log.Info().
		Str("job_id", job.ID.String()).
		Int("segment", idx).
		Msg("Segment processing complete")

	return nil
}

// generateOutputMarkup generates the final markup with asset references and file sources
func (p *JobProcessor) generateOutputMarkup(ctx context.Context, jobID uuid.UUID) (string, error) {
	// Get job files (for SOURCE blocks)
	var jobFiles []*models.JobFile
	if p.jobFileRepo != nil {
		var err error
		jobFiles, err = p.jobFileRepo.ListByJob(ctx, jobID)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to list job files for markup")
		}
	}

	// Get all segments
	segments, err := p.segmentRepo.ListByJob(ctx, jobID)
	if err != nil {
		return "", fmt.Errorf("failed to get segments: %w", err)
	}

	// Get all assets
	assets, err := p.assetRepo.ListByJob(ctx, jobID)
	if err != nil {
		return "", fmt.Errorf("failed to get assets: %w", err)
	}

	// Build asset map by segment
	assetsBySegment := make(map[uuid.UUID][]*models.Asset)
	for _, asset := range assets {
		if asset.SegmentID != nil {
			assetsBySegment[*asset.SegmentID] = append(assetsBySegment[*asset.SegmentID], asset)
		}
	}

	// Generate markup: SOURCE blocks first (file extractions)
	markup := ""
	for _, jf := range jobFiles {
		if jf.ExtractedText != nil && *jf.ExtractedText != "" {
			filename := ""
			if p.fileRepo != nil {
				file, err := p.fileRepo.GetByID(ctx, jf.FileID)
				if err == nil {
					filename = file.Filename
				}
			}
			markup += fmt.Sprintf("[[SOURCE file_id=%s filename=%q]]\n", jf.FileID, filename)
			markup += *jf.ExtractedText + "\n[[/SOURCE]]\n\n"
		}
	}

	for _, segment := range segments {
		markup += fmt.Sprintf("[[SEGMENT id=%s]]\n", segment.ID)

		if segment.Title != nil {
			markup += fmt.Sprintf("# %s\n\n", *segment.Title)
		}

		markup += segment.SegmentText + "\n\n"

		// Add asset references
		for _, asset := range assetsBySegment[segment.ID] {
			if asset.Kind == "image" {
				markup += fmt.Sprintf("[[IMAGE asset_id=%s]]\n", asset.ID)
			} else if asset.Kind == "audio" {
				markup += fmt.Sprintf("[[AUDIO asset_id=%s]]\n", asset.ID)
			}
		}

		markup += "[[/SEGMENT]]\n\n"
	}

	return markup, nil
}

// updateJobStatus updates the job status in the database
func (p *JobProcessor) updateJobStatus(ctx context.Context, jobID uuid.UUID, status string, errorCode, errorMessage *string) error {
	return p.jobRepo.UpdateStatus(ctx, jobID, status, errorCode, errorMessage)
}

// publishWebhookEvent publishes a webhook event to Kafka so the dispatcher can deliver webhooks.
func (p *JobProcessor) publishWebhookEvent(ctx context.Context, jobID uuid.UUID, event string) {
	if p.webhookProducer == nil {
		log.Warn().Str("job_id", jobID.String()).Str("event", event).Msg("Webhook producer not configured, skipping publish")
		return
	}
	if err := p.webhookProducer.PublishWebhook(ctx, jobID, event, ""); err != nil {
		log.Error().Err(err).Str("job_id", jobID.String()).Str("event", event).Msg("Failed to publish webhook event to Kafka")
	}
}
