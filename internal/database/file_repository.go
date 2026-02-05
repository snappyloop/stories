package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/models"
)

// FileRepository handles file-related database operations
type FileRepository struct {
	db *DB
}

// NewFileRepository creates a new FileRepository
func NewFileRepository(db *DB) *FileRepository {
	return &FileRepository{db: db}
}

// Create creates a new file record
func (r *FileRepository) Create(ctx context.Context, file *models.File) error {
	query := `
		INSERT INTO files (
			id, user_id, filename, mime_type, size_bytes, s3_bucket, s3_key,
			status, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.db.ExecContext(ctx, query,
		file.ID, file.UserID, file.Filename, file.MimeType, file.SizeBytes,
		file.S3Bucket, file.S3Key, file.Status, file.ExpiresAt, file.CreatedAt,
	)
	return err
}

// GetByID retrieves a file by ID
func (r *FileRepository) GetByID(ctx context.Context, fileID uuid.UUID) (*models.File, error) {
	query := `
		SELECT id, user_id, filename, mime_type, size_bytes, s3_bucket, s3_key,
			status, expires_at, created_at
		FROM files
		WHERE id = $1
	`
	file := &models.File{}
	err := r.db.QueryRowContext(ctx, query, fileID).Scan(
		&file.ID, &file.UserID, &file.Filename, &file.MimeType, &file.SizeBytes,
		&file.S3Bucket, &file.S3Key, &file.Status, &file.ExpiresAt, &file.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("file not found")
	}
	if err != nil {
		return nil, err
	}
	return file, nil
}

// GetByIDAndUser retrieves a file by ID and user ID (for ownership check)
func (r *FileRepository) GetByIDAndUser(ctx context.Context, fileID, userID uuid.UUID) (*models.File, error) {
	query := `
		SELECT id, user_id, filename, mime_type, size_bytes, s3_bucket, s3_key,
			status, expires_at, created_at
		FROM files
		WHERE id = $1 AND user_id = $2
	`
	file := &models.File{}
	err := r.db.QueryRowContext(ctx, query, fileID, userID).Scan(
		&file.ID, &file.UserID, &file.Filename, &file.MimeType, &file.SizeBytes,
		&file.S3Bucket, &file.S3Key, &file.Status, &file.ExpiresAt, &file.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("file not found")
	}
	if err != nil {
		return nil, err
	}
	return file, nil
}

// ListByUser retrieves files for a user, optionally filtered by status
func (r *FileRepository) ListByUser(ctx context.Context, userID uuid.UUID, status string) ([]*models.File, error) {
	query := `
		SELECT id, user_id, filename, mime_type, size_bytes, s3_bucket, s3_key,
			status, expires_at, created_at
		FROM files
		WHERE user_id = $1 AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query, userID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []*models.File
	for rows.Next() {
		file := &models.File{}
		err := rows.Scan(
			&file.ID, &file.UserID, &file.Filename, &file.MimeType, &file.SizeBytes,
			&file.S3Bucket, &file.S3Key, &file.Status, &file.ExpiresAt, &file.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

// Delete deletes a file by ID
func (r *FileRepository) Delete(ctx context.Context, fileID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM files WHERE id = $1`, fileID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("file not found")
	}
	return nil
}

// DeleteByIDAndUser deletes a file by ID and user ID (for ownership check)
func (r *FileRepository) DeleteByIDAndUser(ctx context.Context, fileID, userID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM files WHERE id = $1 AND user_id = $2`, fileID, userID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("file not found")
	}
	return nil
}
