package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/config"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/models"
)

// DeliveryService handles webhook delivery with retries
type DeliveryService struct {
	db           *database.DB
	httpClient   *http.Client
	config       *config.Config
	jobRepo      *database.JobRepository
	deliveryRepo *database.WebhookDeliveryRepository
}

// NewDeliveryService creates a new webhook delivery service
func NewDeliveryService(db *database.DB, cfg *config.Config) *DeliveryService {
	return &DeliveryService{
		db: db,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		config:       cfg,
		jobRepo:      database.NewJobRepository(db),
		deliveryRepo: database.NewWebhookDeliveryRepository(db),
	}
}

// WebhookPayload represents the webhook payload
type WebhookPayload struct {
	JobID        uuid.UUID  `json:"job_id"`
	Status       string     `json:"status"`
	FinishedAt   time.Time  `json:"finished_at"`
	OutputMarkup *string    `json:"output_markup,omitempty"`
	Error        *ErrorInfo `json:"error,omitempty"`
}

// ErrorInfo represents error information in the webhook
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// DeliverWebhook delivers a webhook for a completed job
func (s *DeliveryService) DeliverWebhook(ctx context.Context, jobID uuid.UUID) error {
	// Get job details
	job, err := s.jobRepo.GetByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("failed to get job: %w", err)
	}

	// Check if webhook is configured
	if job.WebhookURL == nil || *job.WebhookURL == "" {
		log.Debug().Str("job_id", jobID.String()).Msg("No webhook configured for job")
		return nil
	}

	// Create webhook payload
	payload := WebhookPayload{
		JobID:        job.ID,
		Status:       job.Status,
		FinishedAt:   time.Now(),
		OutputMarkup: job.OutputMarkup,
	}

	if job.ErrorCode != nil && job.ErrorMessage != nil {
		payload.Error = &ErrorInfo{
			Code:    *job.ErrorCode,
			Message: *job.ErrorMessage,
		}
	}

	// Create delivery record
	delivery := &models.WebhookDelivery{
		ID:        uuid.New(),
		JobID:     job.ID,
		URL:       *job.WebhookURL,
		Status:    "pending",
		Attempts:  0,
		CreatedAt: time.Now(),
	}

	if err := s.deliveryRepo.Create(ctx, delivery); err != nil {
		log.Error().Err(err).Msg("Failed to create delivery record")
		// Continue with delivery attempt
	}

	// Attempt delivery with retries
	return s.deliverWithRetries(ctx, job, delivery, payload)
}

// deliverWithRetries attempts to deliver the webhook with exponential backoff
func (s *DeliveryService) deliverWithRetries(ctx context.Context, job *models.Job, delivery *models.WebhookDelivery, payload WebhookPayload) error {
	maxRetries := s.config.WebhookMaxRetries
	baseDelay := s.config.WebhookRetryBaseDelay

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Update attempt count
		delivery.Attempts = attempt + 1
		now := time.Now()
		delivery.LastAttemptAt = &now

		// Attempt delivery
		err := s.sendWebhook(ctx, *job.WebhookURL, payload, job.WebhookSecret)

		if err == nil {
			// Success
			delivery.Status = "sent"
			if err := s.deliveryRepo.Update(ctx, delivery); err != nil {
				log.Error().Err(err).Msg("Failed to update delivery record")
			}

			log.Info().
				Str("job_id", job.ID.String()).
				Str("url", *job.WebhookURL).
				Int("attempts", delivery.Attempts).
				Msg("Webhook delivered successfully")

			return nil
		}

		// Log error
		errMsg := err.Error()
		delivery.LastError = &errMsg

		log.Warn().
			Err(err).
			Str("job_id", job.ID.String()).
			Str("url", *job.WebhookURL).
			Int("attempt", attempt+1).
			Int("max_retries", maxRetries).
			Msg("Webhook delivery failed")

		// Update delivery record
		if err := s.deliveryRepo.Update(ctx, delivery); err != nil {
			log.Error().Err(err).Msg("Failed to update delivery record")
		}

		// Check if we should retry
		if attempt < maxRetries-1 {
			// Calculate backoff delay (exponential with jitter)
			delay := baseDelay * time.Duration(1<<uint(attempt))
			if delay > s.config.WebhookRetryMaxDelay {
				delay = s.config.WebhookRetryMaxDelay
			}

			log.Info().
				Dur("delay", delay).
				Int("next_attempt", attempt+2).
				Msg("Retrying webhook delivery")

			// Wait before retry
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
				// Continue to next attempt
			}
		}
	}

	// All retries exhausted
	delivery.Status = "failed"
	if err := s.deliveryRepo.Update(ctx, delivery); err != nil {
		log.Error().Err(err).Msg("Failed to update delivery record")
	}

	return fmt.Errorf("webhook delivery failed after %d attempts", maxRetries)
}

// sendWebhook sends the webhook HTTP request
func (s *DeliveryService) sendWebhook(ctx context.Context, url string, payload WebhookPayload, secret *string) error {
	// Marshal payload
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Stories-Webhook/1.0")
	req.Header.Set("X-GS-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

	// Add signature if secret is provided
	if secret != nil && *secret != "" {
		signature := generateSignature(body, *secret)
		req.Header.Set("X-GS-Signature", signature)
	}

	// Send request
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for logging
	respBody, _ := io.ReadAll(resp.Body)

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// generateSignature generates HMAC-SHA256 signature for the payload
func generateSignature(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}
