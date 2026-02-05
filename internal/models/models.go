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
	InputSource   string     `json:"input_source"`   // text, files, mixed
	ExtractedText *string    `json:"extracted_text,omitempty"`
	OutputMarkup  *string    `json:"output_markup,omitempty"`
	WebhookURL    *string    `json:"webhook_url,omitempty"`
	WebhookSecret *string    `json:"webhook_secret,omitempty"`
	ErrorCode     *string    `json:"error_code,omitempty"`
	ErrorMessage  *string    `json:"error_message,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// File represents an uploaded file available for job processing
type File struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Filename  string    `json:"filename"`
	MimeType  string    `json:"mime_type"`
	SizeBytes int64     `json:"size_bytes"`
	S3Bucket  string    `json:"s3_bucket"`
	S3Key     string    `json:"s3_key"`
	Status    string    `json:"status"` // pending, ready, failed, expired
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// FileInResponse is File without S3 private fields for API responses (e.g. list files)
func (f File) ToInResponse() FileInResponse {
	return FileInResponse{
		ID:        f.ID,
		UserID:    f.UserID,
		Filename:  f.Filename,
		MimeType:  f.MimeType,
		SizeBytes: f.SizeBytes,
		Status:    f.Status,
		ExpiresAt: f.ExpiresAt,
		CreatedAt: f.CreatedAt,
	}
}

type FileInResponse struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Filename  string    `json:"filename"`
	MimeType  string    `json:"mime_type"`
	SizeBytes int64     `json:"size_bytes"`
	Status    string    `json:"status"` // pending, ready, failed, expired
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// JobFile links jobs to files
type JobFile struct {
	ID              uuid.UUID  `json:"id"`
	JobID           uuid.UUID  `json:"job_id"`
	FileID          uuid.UUID  `json:"file_id"`
	ProcessingOrder int        `json:"processing_order"`
	ExtractedText   *string    `json:"extracted_text,omitempty"`
	Status          string     `json:"status"` // pending, processing, succeeded, failed
	CreatedAt       time.Time  `json:"created_at"`
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

// AssetInResponse is Asset without S3 private fields for API responses
func (a Asset) ToInResponse() AssetInResponse {
	return AssetInResponse{
		ID:        a.ID,
		JobID:     a.JobID,
		SegmentID: a.SegmentID,
		Kind:      a.Kind,
		MimeType:  a.MimeType,
		SizeBytes: a.SizeBytes,
		Checksum:  a.Checksum,
		Meta:      a.Meta,
		CreatedAt: a.CreatedAt,
	}
}

type AssetInResponse struct {
	ID        uuid.UUID      `json:"id"`
	JobID     uuid.UUID      `json:"job_id"`
	SegmentID *uuid.UUID     `json:"segment_id,omitempty"`
	Kind      string         `json:"kind"` // image, audio
	MimeType  string         `json:"mime_type"`
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
	Text          string         `json:"text,omitempty"`
	FileIDs       []uuid.UUID    `json:"file_ids,omitempty"`
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

// UploadFileResponse returned after file upload
type UploadFileResponse struct {
	FileID    uuid.UUID `json:"file_id"`
	Filename  string    `json:"filename"`
	MimeType  string    `json:"mime_type"`
	SizeBytes int64     `json:"size_bytes"`
	ExpiresAt time.Time `json:"expires_at"`
}

// JobFileResponse represents file extraction info in job status
type JobFileResponse struct {
	FileID        uuid.UUID `json:"file_id"`
	Filename      string    `json:"filename"`
	MimeType      string    `json:"mime_type"`
	ExtractedText *string   `json:"extracted_text,omitempty"`
	Status        string    `json:"status"`
}

// JobStatusResponse represents detailed job status
type JobStatusResponse struct {
	Job      Job                `json:"job"`
	Segments []*Segment         `json:"segments"`
	Assets   []*AssetResponse   `json:"assets"`
	Files    []*JobFileResponse `json:"files"`
}

// AssetResponse represents asset metadata with download URL (S3 fields excluded)
type AssetResponse struct {
	Asset       AssetInResponse `json:"asset"`
	DownloadURL string          `json:"download_url"`
}
