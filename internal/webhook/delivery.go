package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	retryWorker  *RetryWorker
}

// NewDeliveryService creates a new webhook delivery service
func NewDeliveryService(db *database.DB, cfg *config.Config) *DeliveryService {
	service := &DeliveryService{
		db: db,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		config:       cfg,
		jobRepo:      database.NewJobRepository(db),
		deliveryRepo: database.NewWebhookDeliveryRepository(db),
	}

	// Initialize retry worker
	service.retryWorker = NewRetryWorker(service, cfg)

	return service
}

// Start starts the background retry worker
func (s *DeliveryService) Start(ctx context.Context) {
	s.retryWorker.Start(ctx)
}

// Stop stops the background retry worker
func (s *DeliveryService) Stop() {
	s.retryWorker.Stop()
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

// DeliveryError wraps webhook delivery errors with HTTP status code
type DeliveryError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *DeliveryError) Error() string {
	return e.Message
}

// IsRetryable determines if an error should be retried
func (e *DeliveryError) IsRetryable() bool {
	// Retry on 5xx server errors
	if e.StatusCode >= 500 && e.StatusCode < 600 {
		return true
	}
	// Retry on 429 Too Many Requests
	if e.StatusCode == 429 {
		return true
	}
	// Don't retry on 4xx client errors (except 429)
	if e.StatusCode >= 400 && e.StatusCode < 500 {
		return false
	}
	// Retry on other errors (network issues, timeouts, etc.)
	return true
}

// DeliverWebhook delivers a webhook for a completed job
// Makes one immediate attempt, schedules retries asynchronously if it fails
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
	finishedAt := time.Now()
	if job.FinishedAt != nil {
		finishedAt = *job.FinishedAt
	}

	payload := WebhookPayload{
		JobID:        job.ID,
		Status:       job.Status,
		FinishedAt:   finishedAt,
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

	// Make one immediate attempt (non-blocking for consumer)
	delivery.Attempts = 1
	now := time.Now()
	delivery.LastAttemptAt = &now

	err = s.sendWebhook(ctx, *job.WebhookURL, payload, job.WebhookSecret)

	if err == nil {
		// Success on first attempt
		delivery.Status = "sent"
		if err := s.deliveryRepo.Update(ctx, delivery); err != nil {
			log.Error().Err(err).Msg("Failed to update delivery record")
		}

		log.Info().
			Str("job_id", job.ID.String()).
			Str("url", *job.WebhookURL).
			Msg("Webhook delivered successfully on first attempt")

		return nil
	}

	// First attempt failed - check if retryable
	errMsg := err.Error()
	delivery.LastError = &errMsg

	var deliveryErr *DeliveryError
	if errors.As(err, &deliveryErr) && !deliveryErr.IsRetryable() {
		// Permanent error - don't schedule retries
		delivery.Status = "failed"
		if err := s.deliveryRepo.Update(ctx, delivery); err != nil {
			log.Error().Err(err).Msg("Failed to update delivery record")
		}

		log.Error().
			Err(err).
			Str("job_id", job.ID.String()).
			Str("url", *job.WebhookURL).
			Int("status_code", deliveryErr.StatusCode).
			Msg("Webhook delivery failed with permanent error - not retrying")

		// Return nil to not block consumer - error is logged and recorded
		return nil
	}

	// Transient error - schedule for retry
	delivery.Status = "pending"
	if err := s.deliveryRepo.Update(ctx, delivery); err != nil {
		log.Error().Err(err).Msg("Failed to update delivery record")
	}

	log.Warn().
		Err(err).
		Str("job_id", job.ID.String()).
		Str("url", *job.WebhookURL).
		Msg("Webhook delivery failed on first attempt - scheduled for retry")

	// Return nil to not block consumer - retries will be handled by background worker
	return nil
}

// RetryWorker handles background retry of failed webhook deliveries
type RetryWorker struct {
	service  *DeliveryService
	config   *config.Config
	stopChan chan struct{}
	ticker   *time.Ticker
}

// NewRetryWorker creates a new retry worker
func NewRetryWorker(service *DeliveryService, cfg *config.Config) *RetryWorker {
	return &RetryWorker{
		service:  service,
		config:   cfg,
		stopChan: make(chan struct{}),
	}
}

// Start starts the retry worker
func (w *RetryWorker) Start(ctx context.Context) {
	// Check for pending deliveries every 10 seconds
	w.ticker = time.NewTicker(10 * time.Second)

	go func() {
		log.Info().Msg("Retry worker started")

		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("Retry worker context cancelled, stopping")
				return
			case <-w.stopChan:
				log.Info().Msg("Retry worker stopped")
				return
			case <-w.ticker.C:
				w.processPendingDeliveries(ctx)
			}
		}
	}()
}

// Stop stops the retry worker
func (w *RetryWorker) Stop() {
	if w.ticker != nil {
		w.ticker.Stop()
	}
	close(w.stopChan)
}

// processPendingDeliveries processes pending webhook deliveries
func (w *RetryWorker) processPendingDeliveries(ctx context.Context) {
	// Get pending deliveries
	deliveries, err := w.service.deliveryRepo.GetPendingDeliveries(ctx, 100)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get pending deliveries")
		return
	}

	if len(deliveries) == 0 {
		return
	}

	log.Info().Int("count", len(deliveries)).Msg("Processing pending webhook deliveries")

	for _, delivery := range deliveries {
		// Check if it's time to retry based on exponential backoff
		if !w.shouldRetry(delivery) {
			continue
		}

		// Get job details
		job, err := w.service.jobRepo.GetByID(ctx, delivery.JobID)
		if err != nil {
			log.Error().
				Err(err).
				Str("delivery_id", delivery.ID.String()).
				Str("job_id", delivery.JobID.String()).
				Msg("Failed to get job for delivery")
			continue
		}

		// Build payload
		finishedAt := time.Now()
		if job.FinishedAt != nil {
			finishedAt = *job.FinishedAt
		}

		payload := WebhookPayload{
			JobID:        job.ID,
			Status:       job.Status,
			FinishedAt:   finishedAt,
			OutputMarkup: job.OutputMarkup,
		}

		if job.ErrorCode != nil && job.ErrorMessage != nil {
			payload.Error = &ErrorInfo{
				Code:    *job.ErrorCode,
				Message: *job.ErrorMessage,
			}
		}

		// Attempt delivery
		w.retryDelivery(ctx, job, delivery, payload)
	}
}

// shouldRetry determines if a delivery should be retried based on exponential backoff
func (w *RetryWorker) shouldRetry(delivery *models.WebhookDelivery) bool {
	// Check if max retries exceeded
	if delivery.Attempts >= w.config.WebhookMaxRetries {
		// Mark as permanently failed
		delivery.Status = "failed"
		ctx := context.Background()
		if err := w.service.deliveryRepo.Update(ctx, delivery); err != nil {
			log.Error().Err(err).Msg("Failed to update delivery status to failed")
		}

		log.Error().
			Str("delivery_id", delivery.ID.String()).
			Str("job_id", delivery.JobID.String()).
			Int("attempts", delivery.Attempts).
			Msg("Webhook delivery failed permanently after max retries")

		return false
	}

	// Calculate next retry time based on exponential backoff
	if delivery.LastAttemptAt == nil {
		return true // First retry
	}

	baseDelay := w.config.WebhookRetryBaseDelay
	maxDelay := w.config.WebhookRetryMaxDelay

	// Calculate backoff: baseDelay * 2^(attempt-1)
	// attempt-1 because first attempt was immediate
	backoffDelay := baseDelay * time.Duration(1<<uint(delivery.Attempts-1))
	if backoffDelay > maxDelay {
		backoffDelay = maxDelay
	}

	nextRetryTime := delivery.LastAttemptAt.Add(backoffDelay)
	return time.Now().After(nextRetryTime)
}

// retryDelivery attempts to redeliver a webhook
func (w *RetryWorker) retryDelivery(ctx context.Context, job *models.Job, delivery *models.WebhookDelivery, payload WebhookPayload) {
	// Update attempt count
	delivery.Attempts++
	now := time.Now()
	delivery.LastAttemptAt = &now

	// Attempt delivery
	err := w.service.sendWebhook(ctx, delivery.URL, payload, job.WebhookSecret)

	if err == nil {
		// Success
		delivery.Status = "sent"
		if err := w.service.deliveryRepo.Update(ctx, delivery); err != nil {
			log.Error().Err(err).Msg("Failed to update delivery record")
		}

		log.Info().
			Str("job_id", job.ID.String()).
			Str("url", delivery.URL).
			Int("attempts", delivery.Attempts).
			Msg("Webhook delivered successfully after retry")

		return
	}

	// Delivery failed
	errMsg := err.Error()
	delivery.LastError = &errMsg

	log.Warn().
		Err(err).
		Str("job_id", job.ID.String()).
		Str("url", delivery.URL).
		Int("attempt", delivery.Attempts).
		Int("max_retries", w.config.WebhookMaxRetries).
		Msg("Webhook retry failed")

	// Check if error is retryable
	var deliveryErr *DeliveryError
	if errors.As(err, &deliveryErr) && !deliveryErr.IsRetryable() {
		// Permanent error - don't retry
		delivery.Status = "failed"
		log.Error().
			Err(err).
			Str("job_id", job.ID.String()).
			Str("url", delivery.URL).
			Int("status_code", deliveryErr.StatusCode).
			Msg("Webhook delivery failed with permanent error - not retrying")
	}

	// Update delivery record
	if err := w.service.deliveryRepo.Update(ctx, delivery); err != nil {
		log.Error().Err(err).Msg("Failed to update delivery record")
	}
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
		// Network error - retryable
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for logging
	respBody, _ := io.ReadAll(resp.Body)

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &DeliveryError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("webhook returned status %d", resp.StatusCode),
			Body:       string(respBody),
		}
	}

	return nil
}

// generateSignature generates HMAC-SHA256 signature for the payload
func generateSignature(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}
