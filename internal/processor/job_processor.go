package processor

import (
	"context"
	"fmt"
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
) *JobProcessor {
	return &JobProcessor{
		db:              db,
		jobRepo:         database.NewJobRepository(db),
		segmentRepo:     database.NewSegmentRepository(db),
		assetRepo:       database.NewAssetRepository(db),
		llmClient:       llmClient,
		storageClient:   storageClient,
		webhookProducer: webhookProducer,
		config:          cfg,
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

	// Check if job is already processed
	if job.Status == "succeeded" || job.Status == "failed" {
		log.Warn().
			Str("job_id", jobID.String()).
			Str("status", job.Status).
			Msg("Job already processed")
		return nil
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
	// Step 1: Segment the text
	log.Info().Str("job_id", job.ID.String()).Msg("Step 1: Segmenting text")
	segments, err := p.llmClient.SegmentText(ctx, job.InputText, job.PicturesCount, job.InputType)
	if err != nil {
		return fmt.Errorf("segmentation failed: %w", err)
	}

	// Save segments to database and keep their IDs for asset foreign keys
	segmentIDs := make([]uuid.UUID, len(segments))
	for i, seg := range segments {
		segment := &models.Segment{
			ID:          uuid.New(),
			JobID:       job.ID,
			Idx:         i,
			StartChar:   seg.StartChar,
			EndChar:     seg.EndChar,
			Title:       seg.Title,
			SegmentText: seg.Text,
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

	// Step 2: Process each segment (with limited concurrency)
	log.Info().Str("job_id", job.ID.String()).Msg("Step 2: Processing segments")

	// For now, process sequentially. In production, use semaphore for concurrency control
	for i, seg := range segments {
		log.Info().
			Str("job_id", job.ID.String()).
			Int("segment", i+1).
			Int("total", len(segments)).
			Msg("Processing segment")

		if err := p.processSegment(ctx, job, seg, i, segmentIDs[i]); err != nil {
			return fmt.Errorf("failed to process segment %d: %w", i, err)
		}
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

	// Generate audio (placeholder - actual implementation would use Gemini/Google TTS)
	audio, err := p.llmClient.GenerateAudio(ctx, script, job.AudioType)
	if err != nil {
		p.segmentRepo.UpdateStatus(ctx, job.ID, idx, "failed")
		return fmt.Errorf("audio generation failed: %w", err)
	}

	// Upload audio to S3
	audioKey := fmt.Sprintf("jobs/%s/segments/%d/audio.mp3", job.ID, idx)
	if err := p.storageClient.Upload(ctx, audioKey, audio.Data, "audio/mpeg"); err != nil {
		p.segmentRepo.UpdateStatus(ctx, job.ID, idx, "failed")
		return fmt.Errorf("audio upload failed: %w", err)
	}

	// Save audio asset (use DB segment ID for FK)
	audioAsset := &models.Asset{
		ID:        uuid.New(),
		JobID:     job.ID,
		SegmentID: &segmentID,
		Kind:      "audio",
		MimeType:  "audio/mpeg",
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

	// Upload image to S3
	imageKey := fmt.Sprintf("jobs/%s/segments/%d/image.png", job.ID, idx)
	if err := p.storageClient.Upload(ctx, imageKey, image.Data, "image/png"); err != nil {
		p.segmentRepo.UpdateStatus(ctx, job.ID, idx, "failed")
		return fmt.Errorf("image upload failed: %w", err)
	}

	// Save image asset (use DB segment ID for FK)
	imageAsset := &models.Asset{
		ID:        uuid.New(),
		JobID:     job.ID,
		SegmentID: &segmentID,
		Kind:      "image",
		MimeType:  "image/png",
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

// generateOutputMarkup generates the final markup with asset references
func (p *JobProcessor) generateOutputMarkup(ctx context.Context, jobID uuid.UUID) (string, error) {
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

	// Generate markup
	markup := ""
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

// publishWebhookEvent publishes a webhook event to Kafka
func (p *JobProcessor) publishWebhookEvent(ctx context.Context, jobID uuid.UUID, event string) {
	// TODO: Publish actual webhook event to Kafka
	// This would use webhookProducer to publish to the webhooks topic

	log.Info().
		Str("job_id", jobID.String()).
		Str("event", event).
		Msg("Publishing webhook event")
}
