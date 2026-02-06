package mcpserver

import (
	"net/http"
	"strings"

	"github.com/snappy-loop/stories/internal/auth"
)

// AuthMiddleware returns an http middleware that validates Authorization: Bearer <key>
// using auth.Service. On failure it responds with 401 JSON and does not call next.
func AuthMiddleware(authService *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
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
			apiKey := strings.TrimSpace(parts[1])
			if apiKey == "" {
				writeJSONError(w, http.StatusUnauthorized, "empty api key")
				return
			}
			_, err := authService.ValidateAPIKey(r.Context(), apiKey)
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "invalid api key")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
