package database

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/models"
)

// FactCheckRepository handles segment fact-check database operations
type FactCheckRepository struct {
	db *DB
}

// NewFactCheckRepository creates a new FactCheckRepository
func NewFactCheckRepository(db *DB) *FactCheckRepository {
	return &FactCheckRepository{db: db}
}

// Create inserts a segment fact-check record
func (r *FactCheckRepository) Create(ctx context.Context, fc *models.SegmentFactCheck) error {
	query := `
		INSERT INTO segment_fact_checks (id, segment_id, job_id, fact_check_text, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := r.db.ExecContext(ctx, query,
		fc.ID, fc.SegmentID, fc.JobID, fc.FactCheckText, fc.CreatedAt,
	)
	return err
}

// ListByJob returns all fact-checks for a job, ordered by segment
func (r *FactCheckRepository) ListByJob(ctx context.Context, jobID uuid.UUID) ([]*models.SegmentFactCheck, error) {
	query := `
		SELECT id, segment_id, job_id, fact_check_text, created_at
		FROM segment_fact_checks
		WHERE job_id = $1
		ORDER BY created_at ASC
	`
	rows, err := r.db.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("list fact checks: %w", err)
	}
	defer rows.Close()

	var list []*models.SegmentFactCheck
	for rows.Next() {
		fc := &models.SegmentFactCheck{}
		err := rows.Scan(&fc.ID, &fc.SegmentID, &fc.JobID, &fc.FactCheckText, &fc.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan fact check: %w", err)
		}
		list = append(list, fc)
	}
	return list, rows.Err()
}
