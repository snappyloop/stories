package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/config"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/kafka"
	"github.com/snappy-loop/stories/internal/models"
)

// JobService handles job-related business logic
type JobService struct {
	jobRepo       *database.JobRepository
	segmentRepo   *database.SegmentRepository
	assetRepo     *database.AssetRepository
	jobFileRepo   *database.JobFileRepository
	fileRepo      *database.FileRepository
	apiKeyRepo    *database.APIKeyRepository
	kafkaProducer *kafka.Producer
	config        *config.Config
}

// NewJobService creates a new JobService
func NewJobService(
	db *database.DB,
	kafkaProducer *kafka.Producer,
	cfg *config.Config,
) *JobService {
	return &JobService{
		jobRepo:       database.NewJobRepository(db),
		segmentRepo:   database.NewSegmentRepository(db),
		assetRepo:     database.NewAssetRepository(db),
		jobFileRepo:   database.NewJobFileRepository(db),
		fileRepo:      database.NewFileRepository(db),
		apiKeyRepo:    database.NewAPIKeyRepository(db),
		kafkaProducer: kafkaProducer,
		config:        cfg,
	}
}

// CreateJob creates a new job
func (s *JobService) CreateJob(ctx context.Context, req *models.CreateJobRequest, userID, apiKeyID uuid.UUID) (*models.CreateJobResponse, error) {
	// Validate request
	if err := s.validateCreateJobRequest(req); err != nil {
		return nil, fmt.Errorf("validation error: %w", err)
	}

	// Determine input source and input text
	inputSource := "text"
	inputText := req.Text
	if len(req.FileIDs) > 0 {
		if inputText != "" {
			inputSource = "mixed"
		} else {
			inputSource = "files"
			inputText = "[pending extraction]"
		}
	}

	// Validate files exist and belong to user
	for _, fileID := range req.FileIDs {
		file, err := s.fileRepo.GetByIDAndUser(ctx, fileID, userID)
		if err != nil {
			return nil, fmt.Errorf("file %s not found or not owned by you", fileID.String())
		}
		if file.Status != "ready" {
			return nil, fmt.Errorf("file %s is not available (status: %s)", fileID.String(), file.Status)
		}
	}

	// Quota: text chars + 1000 per file
	charsNeeded := int64(len(req.Text)) + int64(len(req.FileIDs))*int64(s.config.CharsPerFile)
	apiKey, err := s.apiKeyRepo.GetByID(ctx, apiKeyID)
	if err == nil {
		if err := s.checkAndUpdateQuota(ctx, apiKey, charsNeeded); err != nil {
			return nil, err
		}
	}
	log.Info().
		Str("api_key_id", apiKeyID.String()).
		Int64("chars_needed", charsNeeded).
		Msg("Creating job")

	// Create job
	job := &models.Job{
		ID:            uuid.New(),
		UserID:        userID,
		APIKeyID:      apiKeyID,
		Status:        "queued",
		InputType:     req.Type,
		PicturesCount: req.PicturesCount,
		AudioType:     req.AudioType,
		InputText:     inputText,
		InputSource:   inputSource,
		CreatedAt:     time.Now(),
	}

	if req.Webhook != nil {
		job.WebhookURL = &req.Webhook.URL
		job.WebhookSecret = req.Webhook.Secret
	}

	// Save to database
	if err := s.jobRepo.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("failed to create job: %w", err)
	}

	// Create job_files links
	for order, fileID := range req.FileIDs {
		jf := &models.JobFile{
			ID:              uuid.New(),
			JobID:           job.ID,
			FileID:          fileID,
			ProcessingOrder: order,
			Status:          "pending",
			CreatedAt:       time.Now(),
		}
		if err := s.jobFileRepo.Create(ctx, jf); err != nil {
			return nil, fmt.Errorf("failed to link file to job: %w", err)
		}
	}

	// Publish to Kafka
	traceID := uuid.New().String()
	if err := s.kafkaProducer.PublishJob(ctx, job.ID, traceID); err != nil {
		log.Error().Err(err).Str("job_id", job.ID.String()).Msg("Failed to publish job to Kafka")
	}

	log.Info().
		Str("job_id", job.ID.String()).
		Str("user_id", userID.String()).
		Str("type", req.Type).
		Int("pictures", req.PicturesCount).
		Msg("Job created")

	return &models.CreateJobResponse{
		JobID:     job.ID,
		Status:    job.Status,
		CreatedAt: job.CreatedAt,
	}, nil
}

// GetJob retrieves a job with its segments and assets (assets include public URLs)
func (s *JobService) GetJob(ctx context.Context, jobID, userID uuid.UUID) (*models.JobStatusResponse, error) {
	job, err := s.jobRepo.GetByID(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("job not found: %w", err)
	}

	// Verify ownership
	if job.UserID != userID {
		return nil, fmt.Errorf("access denied")
	}

	// Get segments
	segments, err := s.segmentRepo.ListByJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get segments: %w", err)
	}

	// Get assets and attach public URLs
	assets, err := s.assetRepo.ListByJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get assets: %w", err)
	}

	// Get job files (file extraction info)
	jobFiles, err := s.jobFileRepo.ListByJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job files: %w", err)
	}
	filesResp := s.buildJobFileResponses(ctx, jobFiles)

	return &models.JobStatusResponse{
		Job:      *job,
		Segments: segments,
		Assets:   s.buildAssetResponses(assets),
		Files:    filesResp,
	}, nil
}

// buildAssetResponses converts assets to response objects with download URLs.
func (s *JobService) buildAssetResponses(assets []*models.Asset) []*models.AssetResponse {
	out := make([]*models.AssetResponse, len(assets))
	for i, a := range assets {
		out[i] = &models.AssetResponse{
			Asset:       *a,
			DownloadURL: "/v1/assets/" + a.ID.String() + "/content",
		}
	}
	return out
}

// publicAssetURL returns the public URL for an asset (S3PublicURL from config or default S3 style)
func (s *JobService) publicAssetURL(bucket, key string) string {
	if s.config.S3PublicURL != "" {
		return fmt.Sprintf("%s/%s", s.config.S3PublicURL, key)
	}
	return fmt.Sprintf("https://%s.s3.amazonaws.com/%s", bucket, key)
}

// GetJobByID returns job with segments and assets by job ID (no ownership check, for view route)
func (s *JobService) GetJobByID(ctx context.Context, jobID uuid.UUID) (*models.JobStatusResponse, error) {
	job, err := s.jobRepo.GetByID(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("job not found: %w", err)
	}
	segments, err := s.segmentRepo.ListByJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get segments: %w", err)
	}
	assets, err := s.assetRepo.ListByJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get assets: %w", err)
	}
	jobFiles, _ := s.jobFileRepo.ListByJob(ctx, jobID)
	filesResp := s.buildJobFileResponses(ctx, jobFiles)
	return &models.JobStatusResponse{
		Job:      *job,
		Segments: segments,
		Assets:   s.buildAssetResponses(assets),
		Files:    filesResp,
	}, nil
}

// buildJobFileResponses converts job files to response with file metadata
func (s *JobService) buildJobFileResponses(ctx context.Context, jobFiles []*models.JobFile) []*models.JobFileResponse {
	out := make([]*models.JobFileResponse, len(jobFiles))
	for i, jf := range jobFiles {
		resp := &models.JobFileResponse{
			FileID:        jf.FileID,
			ExtractedText: jf.ExtractedText,
			Status:        jf.Status,
		}
		file, err := s.fileRepo.GetByID(ctx, jf.FileID)
		if err == nil {
			resp.Filename = file.Filename
			resp.MimeType = file.MimeType
		}
		out[i] = resp
	}
	return out
}

// GetAsset returns an asset by ID if the user owns the job it belongs to
func (s *JobService) GetAsset(ctx context.Context, assetID, userID uuid.UUID) (*models.Asset, error) {
	asset, err := s.assetRepo.GetByID(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("asset not found: %w", err)
	}
	job, err := s.jobRepo.GetByID(ctx, asset.JobID)
	if err != nil {
		return nil, fmt.Errorf("job not found: %w", err)
	}
	if job.UserID != userID {
		return nil, fmt.Errorf("access denied")
	}
	return asset, nil
}

// GetAssetByJobID returns an asset by ID if it belongs to the given job (for view route, no user check)
func (s *JobService) GetAssetByJobID(ctx context.Context, assetID, jobID uuid.UUID) (*models.Asset, error) {
	asset, err := s.assetRepo.GetByID(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("asset not found: %w", err)
	}
	if asset.JobID != jobID {
		return nil, fmt.Errorf("asset not found")
	}
	return asset, nil
}

// ListJobs lists jobs for a user
func (s *JobService) ListJobs(ctx context.Context, userID uuid.UUID, limit int, cursor *time.Time) ([]*models.Job, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	jobs, err := s.jobRepo.ListByUser(ctx, userID, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}

	return jobs, nil
}

// validateCreateJobRequest validates a create job request
func (s *JobService) validateCreateJobRequest(req *models.CreateJobRequest) error {
	if req.Text == "" && len(req.FileIDs) == 0 {
		return fmt.Errorf("either text or file_ids is required")
	}

	if len(req.FileIDs) > s.config.MaxFilesPerJob {
		return fmt.Errorf("file_ids exceeds maximum of %d files", s.config.MaxFilesPerJob)
	}

	// Check for duplicate file IDs to prevent UNIQUE constraint violation on job_files table
	if len(req.FileIDs) > 0 {
		seen := make(map[uuid.UUID]bool, len(req.FileIDs))
		for _, fileID := range req.FileIDs {
			if seen[fileID] {
				return fmt.Errorf("duplicate file_id: %s", fileID.String())
			}
			seen[fileID] = true
		}
	}

	if len(req.Text) > s.config.MaxInputLength {
		return fmt.Errorf("text exceeds maximum length of %d characters", s.config.MaxInputLength)
	}

	if req.Type != "educational" && req.Type != "financial" && req.Type != "fictional" {
		return fmt.Errorf("invalid type: must be educational, financial, or fictional")
	}

	if req.PicturesCount < 1 || req.PicturesCount > s.config.MaxPicturesCount {
		return fmt.Errorf("pictures_count must be between 1 and %d", s.config.MaxPicturesCount)
	}

	if req.AudioType != "free_speech" && req.AudioType != "podcast" {
		return fmt.Errorf("invalid audio_type: must be free_speech or podcast")
	}

	return nil
}

// checkAndUpdateQuota checks if user has enough quota and updates usage
func (s *JobService) checkAndUpdateQuota(ctx context.Context, apiKey *models.APIKey, charsNeeded int64) error {
	// Check if period needs to be reset
	now := time.Now()
	periodDuration := s.getPeriodDuration(apiKey.QuotaPeriod)

	if now.Sub(apiKey.PeriodStartedAt) > periodDuration {
		// Reset period
		apiKey.UsedCharsInPeriod = 0
		apiKey.PeriodStartedAt = now
	}

	// Check quota
	if apiKey.UsedCharsInPeriod+charsNeeded > apiKey.QuotaChars {
		return fmt.Errorf("quota exceeded: %d/%d chars used", apiKey.UsedCharsInPeriod, apiKey.QuotaChars)
	}

	// Update usage
	if err := s.apiKeyRepo.UpdateUsage(ctx, apiKey.ID, charsNeeded, apiKey.PeriodStartedAt); err != nil {
		return fmt.Errorf("failed to update quota: %w", err)
	}

	return nil
}

func (s *JobService) getPeriodDuration(period string) time.Duration {
	switch period {
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	case "yearly":
		return 365 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}
