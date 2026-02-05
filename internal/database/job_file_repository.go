package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/models"
)

// JobFileRepository handles job-file linking database operations
type JobFileRepository struct {
	db *DB
}

// NewJobFileRepository creates a new JobFileRepository
func NewJobFileRepository(db *DB) *JobFileRepository {
	return &JobFileRepository{db: db}
}

// Create creates a new job_file link
func (r *JobFileRepository) Create(ctx context.Context, jf *models.JobFile) error {
	query := `
		INSERT INTO job_files (
			id, job_id, file_id, processing_order, status, created_at
		) VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := r.db.ExecContext(ctx, query,
		jf.ID, jf.JobID, jf.FileID, jf.ProcessingOrder, jf.Status, jf.CreatedAt,
	)
	return err
}

// ListByJob retrieves all job_file links for a job, ordered by processing_order
func (r *JobFileRepository) ListByJob(ctx context.Context, jobID uuid.UUID) ([]*models.JobFile, error) {
	query := `
		SELECT id, job_id, file_id, processing_order, extracted_text, status, created_at
		FROM job_files
		WHERE job_id = $1
		ORDER BY processing_order ASC
	`
	rows, err := r.db.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*models.JobFile
	for rows.Next() {
		jf := &models.JobFile{}
		var extractedText sql.NullString
		err := rows.Scan(
			&jf.ID, &jf.JobID, &jf.FileID, &jf.ProcessingOrder,
			&extractedText, &jf.Status, &jf.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		if extractedText.Valid {
			jf.ExtractedText = &extractedText.String
		}
		list = append(list, jf)
	}
	return list, rows.Err()
}

// UpdateExtraction updates extracted_text and status for a job_file
func (r *JobFileRepository) UpdateExtraction(ctx context.Context, id uuid.UUID, extractedText *string, status string) error {
	query := `
		UPDATE job_files
		SET extracted_text = $1, status = $2
		WHERE id = $3
	`
	result, err := r.db.ExecContext(ctx, query, extractedText, status, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("job_file not found")
	}
	return nil
}
