package database

import (
	"context"

	"github.com/google/uuid"
)

// UpdateStatus updates a job's status and error information
func (r *JobRepository) UpdateStatus(ctx context.Context, jobID uuid.UUID, status string, errorCode, errorMessage *string) error {
	query := `
		UPDATE jobs
		SET status = $1::job_status,
		    error_code = $2,
		    error_message = $3,
		    started_at = CASE WHEN status = 'queued' AND ($1::job_status = 'running') THEN NOW() ELSE started_at END,
		    finished_at = CASE WHEN $1::job_status IN ('succeeded', 'failed', 'canceled') THEN NOW() ELSE finished_at END
		WHERE id = $4
	`

	_, err := r.db.ExecContext(ctx, query, status, errorCode, errorMessage, jobID)
	return err
}

// UpdateMarkup updates a job's output markup
func (r *JobRepository) UpdateMarkup(ctx context.Context, jobID uuid.UUID, markup string) error {
	query := `
		UPDATE jobs
		SET output_markup = $1
		WHERE id = $2
	`

	_, err := r.db.ExecContext(ctx, query, markup, jobID)
	return err
}
