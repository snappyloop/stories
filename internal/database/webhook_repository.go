package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/models"
)

// WebhookDeliveryRepository handles webhook delivery operations
type WebhookDeliveryRepository struct {
	db *DB
}

// NewWebhookDeliveryRepository creates a new WebhookDeliveryRepository
func NewWebhookDeliveryRepository(db *DB) *WebhookDeliveryRepository {
	return &WebhookDeliveryRepository{db: db}
}

// Create creates a new webhook delivery record
func (r *WebhookDeliveryRepository) Create(ctx context.Context, delivery *models.WebhookDelivery) error {
	query := `
		INSERT INTO webhook_deliveries (
			id, job_id, url, status, attempts, last_attempt_at, last_error, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := r.db.ExecContext(ctx, query,
		delivery.ID, delivery.JobID, delivery.URL, delivery.Status,
		delivery.Attempts, delivery.LastAttemptAt, delivery.LastError,
		delivery.CreatedAt,
	)

	return err
}

// Update updates a webhook delivery record
func (r *WebhookDeliveryRepository) Update(ctx context.Context, delivery *models.WebhookDelivery) error {
	query := `
		UPDATE webhook_deliveries
		SET status = $1, attempts = $2, last_attempt_at = $3, last_error = $4
		WHERE id = $5
	`

	_, err := r.db.ExecContext(ctx, query,
		delivery.Status, delivery.Attempts, delivery.LastAttemptAt,
		delivery.LastError, delivery.ID,
	)

	return err
}

// GetByJobID retrieves webhook deliveries for a job
func (r *WebhookDeliveryRepository) GetByJobID(ctx context.Context, jobID uuid.UUID) ([]*models.WebhookDelivery, error) {
	query := `
		SELECT id, job_id, url, status, attempts, last_attempt_at, last_error, created_at
		FROM webhook_deliveries
		WHERE job_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.db.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []*models.WebhookDelivery
	for rows.Next() {
		delivery := &models.WebhookDelivery{}
		err := rows.Scan(
			&delivery.ID, &delivery.JobID, &delivery.URL, &delivery.Status,
			&delivery.Attempts, &delivery.LastAttemptAt, &delivery.LastError,
			&delivery.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, delivery)
	}

	return deliveries, rows.Err()
}

// GetPendingDeliveries retrieves pending webhook deliveries
func (r *WebhookDeliveryRepository) GetPendingDeliveries(ctx context.Context, limit int) ([]*models.WebhookDelivery, error) {
	query := `
		SELECT id, job_id, url, status, attempts, last_attempt_at, last_error, created_at
		FROM webhook_deliveries
		WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT $1
	`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []*models.WebhookDelivery
	for rows.Next() {
		delivery := &models.WebhookDelivery{}
		err := rows.Scan(
			&delivery.ID, &delivery.JobID, &delivery.URL, &delivery.Status,
			&delivery.Attempts, &delivery.LastAttemptAt, &delivery.LastError,
			&delivery.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, delivery)
	}

	return deliveries, rows.Err()
}

// GetByID retrieves a webhook delivery by ID
func (r *WebhookDeliveryRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.WebhookDelivery, error) {
	query := `
		SELECT id, job_id, url, status, attempts, last_attempt_at, last_error, created_at
		FROM webhook_deliveries
		WHERE id = $1
	`

	delivery := &models.WebhookDelivery{}
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&delivery.ID, &delivery.JobID, &delivery.URL, &delivery.Status,
		&delivery.Attempts, &delivery.LastAttemptAt, &delivery.LastError,
		&delivery.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("webhook delivery not found")
	}

	return delivery, err
}
