package models

import (
	"time"

	"github.com/google/uuid"
)

// User represents a user in the system
type User struct {
	ID        uuid.UUID `json:"id"`
	Email     *string   `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// APIKey represents an API key for authentication
type APIKey struct {
	ID                uuid.UUID `json:"id"`
	UserID            uuid.UUID `json:"user_id"`
	KeyHash           string    `json:"-"`
	Status            string    `json:"status"`       // active, disabled
	QuotaPeriod       string    `json:"quota_period"` // daily, weekly, monthly, yearly
	QuotaChars        int64     `json:"quota_chars"`
	UsedCharsInPeriod int64     `json:"used_chars_in_period"`
	PeriodStartedAt   time.Time `json:"period_started_at"`
	CreatedAt         time.Time `json:"created_at"`
}

// Job represents an enrichment job
type Job struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	APIKeyID      uuid.UUID  `json:"api_key_id"`
	Status        string     `json:"status"`     // queued, running, succeeded, failed, canceled
	InputType     string     `json:"input_type"` // educational, financial, fictional
	PicturesCount int        `json:"pictures_count"`
	AudioType     string     `json:"audio_type"` // free_speech, podcast
	InputText     string     `json:"input_text"`
	OutputMarkup  *string    `json:"output_markup,omitempty"`
	WebhookURL    *string    `json:"webhook_url,omitempty"`
	WebhookSecret *string    `json:"webhook_secret,omitempty"`
	ErrorCode     *string    `json:"error_code,omitempty"`
	ErrorMessage  *string    `json:"error_message,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// Segment represents a text segment within a job
type Segment struct {
	ID          uuid.UUID `json:"id"`
	JobID       uuid.UUID `json:"job_id"`
	Idx         int       `json:"idx"`
	StartChar   int       `json:"start_char"`
	EndChar     int       `json:"end_char"`
	Title       *string   `json:"title,omitempty"`
	SegmentText string    `json:"segment_text"`
	Status      string    `json:"status"` // queued, running, succeeded, failed
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Asset represents a generated asset (image or audio)
type Asset struct {
	ID        uuid.UUID      `json:"id"`
	JobID     uuid.UUID      `json:"job_id"`
	SegmentID *uuid.UUID     `json:"segment_id,omitempty"`
	Kind      string         `json:"kind"` // image, audio
	MimeType  string         `json:"mime_type"`
	S3Bucket  string         `json:"s3_bucket"`
	S3Key     string         `json:"s3_key"`
	SizeBytes int64          `json:"size_bytes"`
	Checksum  *string        `json:"checksum,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// WebhookDelivery represents a webhook delivery attempt
type WebhookDelivery struct {
	ID            uuid.UUID  `json:"id"`
	JobID         uuid.UUID  `json:"job_id"`
	URL           string     `json:"url"`
	Status        string     `json:"status"` // pending, sent, failed
	Attempts      int        `json:"attempts"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastError     *string    `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// CreateJobRequest represents a request to create a new job
type CreateJobRequest struct {
	Text          string         `json:"text"`
	Type          string         `json:"type"` // educational, financial, fictional
	PicturesCount int            `json:"pictures_count"`
	AudioType     string         `json:"audio_type"` // free_speech, podcast
	Webhook       *WebhookConfig `json:"webhook,omitempty"`
}

// WebhookConfig represents webhook configuration for a job
type WebhookConfig struct {
	URL    string  `json:"url"`
	Secret *string `json:"secret,omitempty"`
}

// CreateJobResponse represents the response when creating a job
type CreateJobResponse struct {
	JobID     uuid.UUID `json:"job_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// JobStatusResponse represents detailed job status
type JobStatusResponse struct {
	Job      Job              `json:"job"`
	Segments []*Segment       `json:"segments"`
	Assets   []*AssetResponse `json:"assets"`
}

// AssetResponse represents asset metadata with download URL
type AssetResponse struct {
	Asset       Asset  `json:"asset"`
	DownloadURL string `json:"download_url"`
}
