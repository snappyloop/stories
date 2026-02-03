package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/models"
	"golang.org/x/crypto/bcrypt"
)

// ContextKey is the type for context keys
type ContextKey string

const (
	// UserIDKey is the context key for user ID
	UserIDKey ContextKey = "user_id"
	// APIKeyIDKey is the context key for API key ID
	APIKeyIDKey ContextKey = "api_key_id"
)

// Service handles authentication
type Service struct {
	apiKeyRepo *database.APIKeyRepository
}

// NewService creates a new auth service
func NewService(db *database.DB) *Service {
	return &Service{
		apiKeyRepo: database.NewAPIKeyRepository(db),
	}
}

// Middleware creates an authentication middleware
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			writeJSONError(w, http.StatusUnauthorized, "invalid authorization header format")
			return
		}

		apiKey := parts[1]
		if apiKey == "" {
			writeJSONError(w, http.StatusUnauthorized, "empty api key")
			return
		}

		// Look up by key_lookup (sha256 hex) for keys created via API; fallback to legacy key_hash lookup
		storedKey, err := s.apiKeyRepo.GetByKeyLookup(r.Context(), database.KeyLookupHash(apiKey))
		if err != nil {
			// Legacy: try lookup by raw key (key_hash stored as plain key)
			storedKey, err = s.apiKeyRepo.GetByKeyHash(r.Context(), hashAPIKeySimple(apiKey))
		}
		if err != nil {
			log.Debug().Msg("API key not found")
			writeJSONError(w, http.StatusUnauthorized, "invalid api key")
			return
		}

		// Check if key is active
		if storedKey.Status != "active" {
			log.Warn().Str("key_id", storedKey.ID.String()).Msg("API key is not active")
			writeJSONError(w, http.StatusUnauthorized, "api key is disabled")
			return
		}

		// Verify key: bcrypt for new keys; legacy keys store plain key in KeyHash
		if err := bcrypt.CompareHashAndPassword([]byte(storedKey.KeyHash), []byte(apiKey)); err != nil {
			if storedKey.KeyHash != apiKey {
				writeJSONError(w, http.StatusUnauthorized, "invalid api key")
				return
			}
		}

		// Add user ID and API key ID to context
		ctx := context.WithValue(r.Context(), UserIDKey, storedKey.UserID)
		ctx = context.WithValue(ctx, APIKeyIDKey, storedKey.ID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetUserID retrieves the user ID from context
func GetUserID(ctx context.Context) (uuid.UUID, error) {
	userID, ok := ctx.Value(UserIDKey).(uuid.UUID)
	if !ok {
		return uuid.Nil, fmt.Errorf("user id not found in context")
	}
	return userID, nil
}

// GetAPIKeyID retrieves the API key ID from context
func GetAPIKeyID(ctx context.Context) (uuid.UUID, error) {
	keyID, ok := ctx.Value(APIKeyIDKey).(uuid.UUID)
	if !ok {
		return uuid.Nil, fmt.Errorf("api key id not found in context")
	}
	return keyID, nil
}

// ValidateAPIKey validates an API key and returns the associated key info
func (s *Service) ValidateAPIKey(ctx context.Context, apiKey string) (*models.APIKey, error) {
	storedKey, err := s.apiKeyRepo.GetByKeyLookup(ctx, database.KeyLookupHash(apiKey))
	if err != nil {
		storedKey, err = s.apiKeyRepo.GetByKeyHash(ctx, hashAPIKeySimple(apiKey))
	}
	if err != nil {
		return nil, fmt.Errorf("api key not found: %w", err)
	}

	if storedKey.Status != "active" {
		return nil, fmt.Errorf("api key is disabled")
	}

	// Verify: bcrypt for new keys; legacy keys store plain key in KeyHash
	if err := bcrypt.CompareHashAndPassword([]byte(storedKey.KeyHash), []byte(apiKey)); err != nil {
		if storedKey.KeyHash != apiKey {
			return nil, fmt.Errorf("invalid api key")
		}
	}

	return storedKey, nil
}

// hashAPIKeySimple creates a lookup hash for the API key
// The actual bcrypt hash is stored in the database
func hashAPIKeySimple(apiKey string) string {
	// For lookup, we use crypt() function in SQL
	// Here we just return a placeholder since we'll use bcrypt compare
	return apiKey
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
