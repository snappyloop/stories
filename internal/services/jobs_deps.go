package services

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/models"
)

// JobPublisher publishes job messages (e.g. to Kafka). May be nil to skip publishing.
type JobPublisher interface {
	PublishJob(ctx context.Context, jobID uuid.UUID, traceID string) error
}

// jobRepository is the subset of job DB operations used by JobService.
type jobRepository interface {
	Create(ctx context.Context, job *models.Job) error
	GetByID(ctx context.Context, jobID uuid.UUID) (*models.Job, error)
	ListByUser(ctx context.Context, userID uuid.UUID, limit int, cursor *time.Time) ([]*models.Job, error)
}

// segmentRepository is the subset of segment DB operations used by JobService.
type segmentRepository interface {
	ListByJob(ctx context.Context, jobID uuid.UUID) ([]*models.Segment, error)
}

// assetRepository is the subset of asset DB operations used by JobService.
type assetRepository interface {
	GetByID(ctx context.Context, assetID uuid.UUID) (*models.Asset, error)
	ListByJob(ctx context.Context, jobID uuid.UUID) ([]*models.Asset, error)
}

// jobFileRepository is the subset of job_file DB operations used by JobService.
type jobFileRepository interface {
	Create(ctx context.Context, jf *models.JobFile) error
	ListByJob(ctx context.Context, jobID uuid.UUID) ([]*models.JobFile, error)
}

// fileRepository is the subset of file DB operations used by JobService.
type fileRepository interface {
	GetByID(ctx context.Context, fileID uuid.UUID) (*models.File, error)
	GetByIDAndUser(ctx context.Context, fileID, userID uuid.UUID) (*models.File, error)
}

// apiKeyRepository is the subset of API key DB operations used by JobService.
type apiKeyRepository interface {
	GetByID(ctx context.Context, keyID uuid.UUID) (*models.APIKey, error)
	UpdateUsage(ctx context.Context, keyID uuid.UUID, chars int64, periodStartedAt time.Time) error
	CreateAPIKey(ctx context.Context, userID uuid.UUID, quotaChars int64, quotaPeriod string) (plainKey string, key *models.APIKey, err error)
}

// factCheckRepository is the subset of fact-check DB operations used by JobService.
type factCheckRepository interface {
	ListByJob(ctx context.Context, jobID uuid.UUID) ([]*models.SegmentFactCheck, error)
}
