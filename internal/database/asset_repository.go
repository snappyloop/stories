package database

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/snappy-loop/stories/internal/models"
)

// Create creates a new asset
func (r *AssetRepository) Create(ctx context.Context, asset *models.Asset) error {
	var metaJSON []byte
	var err error

	if asset.Meta != nil {
		metaJSON, err = json.Marshal(asset.Meta)
		if err != nil {
			return fmt.Errorf("failed to marshal meta: %w", err)
		}
	}

	query := `
		INSERT INTO assets (
			id, job_id, segment_id, kind, mime_type, s3_bucket, s3_key,
			size_bytes, checksum, meta, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err = r.db.ExecContext(ctx, query,
		asset.ID, asset.JobID, asset.SegmentID, asset.Kind,
		asset.MimeType, asset.S3Bucket, asset.S3Key, asset.SizeBytes,
		asset.Checksum, metaJSON, asset.CreatedAt,
	)

	return err
}
