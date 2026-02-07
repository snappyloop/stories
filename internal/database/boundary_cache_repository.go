package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// BoundaryCacheRepository handles segment boundary caching
type BoundaryCacheRepository struct {
	db *DB
}

// NewBoundaryCacheRepository creates a new BoundaryCacheRepository
func NewBoundaryCacheRepository(db *DB) *BoundaryCacheRepository {
	return &BoundaryCacheRepository{db: db}
}

// TextHash computes SHA-256 hash of text for cache key.
// Text is normalized (trimmed and lowercased) before hashing for better cache hits.
func TextHash(text string) string {
	normalized := strings.ToLower(strings.TrimSpace(text))
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:])
}

// Get retrieves cached boundaries for a text hash and input type
func (r *BoundaryCacheRepository) Get(ctx context.Context, textHash, inputType string) ([]int, error) {
	query := `
		SELECT boundaries
		FROM segment_boundaries_cache
		WHERE text_hash = $1 AND input_type = $2
	`

	var boundariesJSON []byte
	err := r.db.QueryRowContext(ctx, query, textHash, inputType).Scan(&boundariesJSON)

	if err == sql.ErrNoRows {
		return nil, nil // Cache miss
	}

	if err != nil {
		return nil, fmt.Errorf("query cache: %w", err)
	}

	var boundaries []int
	if err := json.Unmarshal(boundariesJSON, &boundaries); err != nil {
		return nil, fmt.Errorf("unmarshal boundaries: %w", err)
	}

	return boundaries, nil
}

// Set stores boundaries in cache for a text hash and input type
func (r *BoundaryCacheRepository) Set(ctx context.Context, textHash, inputType string, boundaries []int) error {
	boundariesJSON, err := json.Marshal(boundaries)
	if err != nil {
		return fmt.Errorf("marshal boundaries: %w", err)
	}

	query := `
		INSERT INTO segment_boundaries_cache (text_hash, input_type, boundaries, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (text_hash) DO UPDATE
		SET boundaries = EXCLUDED.boundaries,
		    input_type = EXCLUDED.input_type,
		    created_at = EXCLUDED.created_at
	`

	_, err = r.db.ExecContext(ctx, query, textHash, inputType, boundariesJSON, time.Now())
	if err != nil {
		return fmt.Errorf("insert cache: %w", err)
	}

	return nil
}
