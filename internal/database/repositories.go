package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/models"
	"golang.org/x/crypto/bcrypt"
)

// JobRepository handles job-related database operations
type JobRepository struct {
	db *DB
}

// NewJobRepository creates a new JobRepository
func NewJobRepository(db *DB) *JobRepository {
	return &JobRepository{db: db}
}

// Create creates a new job
func (r *JobRepository) Create(ctx context.Context, job *models.Job) error {
	query := `
		INSERT INTO jobs (
			id, user_id, api_key_id, status, input_type, pictures_count, 
			audio_type, input_text, input_source, extracted_text, webhook_url, webhook_secret, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`

	_, err := r.db.ExecContext(ctx, query,
		job.ID, job.UserID, job.APIKeyID, job.Status, job.InputType,
		job.PicturesCount, job.AudioType, job.InputText, job.InputSource, job.ExtractedText,
		job.WebhookURL, job.WebhookSecret, job.CreatedAt,
	)

	return err
}

// GetByID retrieves a job by ID
func (r *JobRepository) GetByID(ctx context.Context, jobID uuid.UUID) (*models.Job, error) {
	query := `
		SELECT id, user_id, api_key_id, status, input_type, pictures_count,
			audio_type, input_text, input_source, extracted_text, output_markup, webhook_url, webhook_secret,
			error_code, error_message, created_at, started_at, finished_at
		FROM jobs WHERE id = $1
	`

	job := &models.Job{}
	err := r.db.QueryRowContext(ctx, query, jobID).Scan(
		&job.ID, &job.UserID, &job.APIKeyID, &job.Status, &job.InputType,
		&job.PicturesCount, &job.AudioType, &job.InputText, &job.InputSource, &job.ExtractedText,
		&job.OutputMarkup, &job.WebhookURL, &job.WebhookSecret, &job.ErrorCode, &job.ErrorMessage,
		&job.CreatedAt, &job.StartedAt, &job.FinishedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job not found")
	}

	return job, err
}

// ListByUser retrieves jobs for a user with pagination
func (r *JobRepository) ListByUser(ctx context.Context, userID uuid.UUID, limit int, cursor *time.Time) ([]*models.Job, error) {
	query := `
		SELECT id, user_id, api_key_id, status, input_type, pictures_count,
			audio_type, input_text, input_source, extracted_text, output_markup, webhook_url, webhook_secret,
			error_code, error_message, created_at, started_at, finished_at
		FROM jobs 
		WHERE user_id = $1 AND ($2::timestamptz IS NULL OR created_at < $2)
		ORDER BY created_at DESC
		LIMIT $3
	`

	rows, err := r.db.QueryContext(ctx, query, userID, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*models.Job
	for rows.Next() {
		job := &models.Job{}
		err := rows.Scan(
			&job.ID, &job.UserID, &job.APIKeyID, &job.Status, &job.InputType,
			&job.PicturesCount, &job.AudioType, &job.InputText, &job.InputSource, &job.ExtractedText,
			&job.OutputMarkup, &job.WebhookURL, &job.WebhookSecret, &job.ErrorCode, &job.ErrorMessage,
			&job.CreatedAt, &job.StartedAt, &job.FinishedAt,
		)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}

	return jobs, rows.Err()
}

// SegmentRepository handles segment-related database operations
type SegmentRepository struct {
	db *DB
}

// NewSegmentRepository creates a new SegmentRepository
func NewSegmentRepository(db *DB) *SegmentRepository {
	return &SegmentRepository{db: db}
}

// ListByJob retrieves segments for a job
func (r *SegmentRepository) ListByJob(ctx context.Context, jobID uuid.UUID) ([]*models.Segment, error) {
	query := `
		SELECT id, job_id, idx, start_char, end_char, title, segment_text,
			status, created_at, updated_at
		FROM segments
		WHERE job_id = $1
		ORDER BY idx ASC
	`

	rows, err := r.db.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var segments []*models.Segment
	for rows.Next() {
		segment := &models.Segment{}
		err := rows.Scan(
			&segment.ID, &segment.JobID, &segment.Idx, &segment.StartChar,
			&segment.EndChar, &segment.Title, &segment.SegmentText,
			&segment.Status, &segment.CreatedAt, &segment.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}

	return segments, rows.Err()
}

// AssetRepository handles asset-related database operations
type AssetRepository struct {
	db *DB
}

// NewAssetRepository creates a new AssetRepository
func NewAssetRepository(db *DB) *AssetRepository {
	return &AssetRepository{db: db}
}

// GetByID retrieves an asset by ID
func (r *AssetRepository) GetByID(ctx context.Context, assetID uuid.UUID) (*models.Asset, error) {
	query := `
		SELECT id, job_id, segment_id, kind, mime_type, s3_bucket, s3_key,
			size_bytes, checksum, meta, created_at
		FROM assets
		WHERE id = $1
	`

	asset := &models.Asset{}
	var metaJSON []byte

	err := r.db.QueryRowContext(ctx, query, assetID).Scan(
		&asset.ID, &asset.JobID, &asset.SegmentID, &asset.Kind,
		&asset.MimeType, &asset.S3Bucket, &asset.S3Key, &asset.SizeBytes,
		&asset.Checksum, &metaJSON, &asset.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("asset not found")
	}

	if err != nil {
		return nil, err
	}

	if len(metaJSON) > 0 {
		if err := json.Unmarshal(metaJSON, &asset.Meta); err != nil {
			return nil, fmt.Errorf("failed to unmarshal meta: %w", err)
		}
	}

	return asset, nil
}

// ListByJob retrieves assets for a job
func (r *AssetRepository) ListByJob(ctx context.Context, jobID uuid.UUID) ([]*models.Asset, error) {
	query := `
		SELECT id, job_id, segment_id, kind, mime_type, s3_bucket, s3_key,
			size_bytes, checksum, meta, created_at
		FROM assets
		WHERE job_id = $1
		ORDER BY created_at ASC
	`

	rows, err := r.db.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var assets []*models.Asset
	for rows.Next() {
		asset := &models.Asset{}
		var metaJSON []byte

		err := rows.Scan(
			&asset.ID, &asset.JobID, &asset.SegmentID, &asset.Kind,
			&asset.MimeType, &asset.S3Bucket, &asset.S3Key, &asset.SizeBytes,
			&asset.Checksum, &metaJSON, &asset.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		if len(metaJSON) > 0 {
			if err := json.Unmarshal(metaJSON, &asset.Meta); err != nil {
				return nil, fmt.Errorf("failed to unmarshal meta: %w", err)
			}
		}

		assets = append(assets, asset)
	}

	return assets, rows.Err()
}

// APIKeyRepository handles API key operations
type APIKeyRepository struct {
	db *DB
}

// NewAPIKeyRepository creates a new APIKeyRepository
func NewAPIKeyRepository(db *DB) *APIKeyRepository {
	return &APIKeyRepository{db: db}
}

// KeyLookupHash returns the lookup hash for an API key (sha256 hex).
// Used for secure lookup without storing the plain key.
func KeyLookupHash(apiKey string) string {
	h := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(h[:])
}

// GetByID retrieves an API key by ID
func (r *APIKeyRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.APIKey, error) {
	query := `
		SELECT id, user_id, key_hash, status, quota_period, quota_chars,
			used_chars_in_period, period_started_at, created_at
		FROM api_keys
		WHERE id = $1
	`
	key := &models.APIKey{}
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&key.ID, &key.UserID, &key.KeyHash, &key.Status, &key.QuotaPeriod,
		&key.QuotaChars, &key.UsedCharsInPeriod, &key.PeriodStartedAt,
		&key.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("api key not found")
	}
	return key, err
}

// GetByKeyHash retrieves an API key by its hash (legacy lookup by raw key)
func (r *APIKeyRepository) GetByKeyHash(ctx context.Context, keyHash string) (*models.APIKey, error) {
	query := `
		SELECT id, user_id, key_hash, status, quota_period, quota_chars,
			used_chars_in_period, period_started_at, created_at
		FROM api_keys
		WHERE key_hash = $1
	`

	key := &models.APIKey{}
	err := r.db.QueryRowContext(ctx, query, keyHash).Scan(
		&key.ID, &key.UserID, &key.KeyHash, &key.Status, &key.QuotaPeriod,
		&key.QuotaChars, &key.UsedCharsInPeriod, &key.PeriodStartedAt,
		&key.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("api key not found")
	}

	return key, err
}

// GetByKeyLookup retrieves an API key by its lookup hash (sha256 hex of the plain key)
func (r *APIKeyRepository) GetByKeyLookup(ctx context.Context, lookup string) (*models.APIKey, error) {
	query := `
		SELECT id, user_id, key_hash, status, quota_period, quota_chars,
			used_chars_in_period, period_started_at, created_at
		FROM api_keys
		WHERE key_lookup = $1
	`

	key := &models.APIKey{}
	err := r.db.QueryRowContext(ctx, query, lookup).Scan(
		&key.ID, &key.UserID, &key.KeyHash, &key.Status, &key.QuotaPeriod,
		&key.QuotaChars, &key.UsedCharsInPeriod, &key.PeriodStartedAt,
		&key.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("api key not found")
	}

	return key, err
}

// CreateAPIKey creates a new API key for a user and returns the plain key (shown only once).
func (r *APIKeyRepository) CreateAPIKey(ctx context.Context, userID uuid.UUID, quotaChars int64, quotaPeriod string) (plainKey string, key *models.APIKey, err error) {
	const keyLen = 32
	b := make([]byte, keyLen)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("generate key: %w", err)
	}
	plainKey = "sk_" + hex.EncodeToString(b)

	hash, err := bcrypt.GenerateFromPassword([]byte(plainKey), bcrypt.DefaultCost)
	if err != nil {
		return "", nil, fmt.Errorf("hash key: %w", err)
	}
	lookup := KeyLookupHash(plainKey)

	key = &models.APIKey{
		ID:                uuid.New(),
		UserID:            userID,
		KeyHash:           string(hash),
		Status:            "active",
		QuotaPeriod:       quotaPeriod,
		QuotaChars:        quotaChars,
		UsedCharsInPeriod: 0,
		PeriodStartedAt:   time.Now(),
		CreatedAt:         time.Now(),
	}

	query := `
		INSERT INTO api_keys (id, user_id, key_hash, key_lookup, status, quota_period, quota_chars,
			used_chars_in_period, period_started_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err = r.db.ExecContext(ctx, query,
		key.ID, key.UserID, key.KeyHash, lookup, key.Status, key.QuotaPeriod,
		key.QuotaChars, key.UsedCharsInPeriod, key.PeriodStartedAt, key.CreatedAt,
	)
	if err != nil {
		return "", nil, err
	}
	return plainKey, key, nil
}

// UpdateUsage updates the usage for an API key
func (r *APIKeyRepository) UpdateUsage(ctx context.Context, keyID uuid.UUID, chars int64, periodStartedAt time.Time) error {
	query := `
		UPDATE api_keys
		SET used_chars_in_period = used_chars_in_period + $1,
			period_started_at = $2
		WHERE id = $3
	`

	_, err := r.db.ExecContext(ctx, query, chars, periodStartedAt, keyID)
	return err
}
