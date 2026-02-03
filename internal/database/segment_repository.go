package database

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/models"
)

// Create creates a new segment
func (r *SegmentRepository) Create(ctx context.Context, segment *models.Segment) error {
	query := `
		INSERT INTO segments (
			id, job_id, idx, start_char, end_char, title, segment_text,
			status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err := r.db.ExecContext(ctx, query,
		segment.ID, segment.JobID, segment.Idx, segment.StartChar,
		segment.EndChar, segment.Title, segment.SegmentText,
		segment.Status, segment.CreatedAt, segment.UpdatedAt,
	)

	return err
}

// UpdateStatus updates a segment's status
func (r *SegmentRepository) UpdateStatus(ctx context.Context, jobID uuid.UUID, idx int, status string) error {
	query := `
		UPDATE segments
		SET status = $1, updated_at = NOW()
		WHERE job_id = $2 AND idx = $3
	`

	result, err := r.db.ExecContext(ctx, query, status, jobID, idx)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return fmt.Errorf("segment not found: job_id=%s, idx=%d", jobID, idx)
	}

	return nil
}

// DeleteByJobID deletes all segments for a job. Assets are cascade-deleted by the DB.
// Used for idempotent restart when a job was left in "running" after a worker crash.
func (r *SegmentRepository) DeleteByJobID(ctx context.Context, jobID uuid.UUID) error {
	query := `DELETE FROM segments WHERE job_id = $1`
	_, err := r.db.ExecContext(ctx, query, jobID)
	return err
}
