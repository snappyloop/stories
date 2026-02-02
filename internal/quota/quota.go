package quota

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/models"
)

// Service handles quota management
type Service struct {
	apiKeyRepo *database.APIKeyRepository
}

// NewService creates a new quota service
func NewService(db *database.DB) *Service {
	return &Service{
		apiKeyRepo: database.NewAPIKeyRepository(db),
	}
}

// CheckAndConsume checks if quota is available and consumes it
func (s *Service) CheckAndConsume(ctx context.Context, apiKeyID uuid.UUID, charsNeeded int64) error {
	// Get API key
	apiKey, err := s.getAPIKeyByID(ctx, apiKeyID)
	if err != nil {
		return fmt.Errorf("failed to get api key: %w", err)
	}

	// Check if period needs to be reset
	now := time.Now()
	periodDuration := s.getPeriodDuration(apiKey.QuotaPeriod)

	if now.Sub(apiKey.PeriodStartedAt) > periodDuration {
		// Reset period
		apiKey.UsedCharsInPeriod = 0
		apiKey.PeriodStartedAt = now
	}

	// Check quota
	if apiKey.UsedCharsInPeriod+charsNeeded > apiKey.QuotaChars {
		return fmt.Errorf("quota exceeded: %d/%d chars used", apiKey.UsedCharsInPeriod, apiKey.QuotaChars)
	}

	// Update usage
	if err := s.apiKeyRepo.UpdateUsage(ctx, apiKey.ID, charsNeeded, apiKey.PeriodStartedAt); err != nil {
		return fmt.Errorf("failed to update quota: %w", err)
	}

	return nil
}

func (s *Service) getAPIKeyByID(ctx context.Context, apiKeyID uuid.UUID) (*models.APIKey, error) {
	// This would need a new repository method
	// For now, return a placeholder error
	return nil, fmt.Errorf("not implemented")
}

func (s *Service) getPeriodDuration(period string) time.Duration {
	switch period {
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	case "yearly":
		return 365 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}
