package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/auth"
	"github.com/snappy-loop/stories/internal/models"
	"github.com/snappy-loop/stories/internal/services"
	"github.com/snappy-loop/stories/internal/storage"
)

// Handler contains all HTTP handlers
type Handler struct {
	jobService *services.JobService
	storage    *storage.Client
}

// NewHandler creates a new handler
func NewHandler(jobService *services.JobService, storage *storage.Client) *Handler {
	return &Handler{
		jobService: jobService,
		storage:    storage,
	}
}

// CreateJob handles POST /v1/jobs
func (h *Handler) CreateJob(w http.ResponseWriter, r *http.Request) {
	var req models.CreateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Get user ID from context
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	apiKeyID, err := auth.GetAPIKeyID(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Create job
	resp, err := h.jobService.CreateJob(r.Context(), &req, userID, apiKeyID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create job")
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, resp)
}

// GetJob handles GET /v1/jobs/{id}
func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobID, err := uuid.Parse(vars["id"])
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	resp, err := h.jobService.GetJob(r.Context(), jobID, userID)
	if err != nil {
		log.Error().Err(err).Str("job_id", jobID.String()).Msg("Failed to get job")
		writeJSONError(w, http.StatusNotFound, "job not found")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// ListJobs handles GET /v1/jobs
func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil {
			limit = parsedLimit
		}
	}

	var cursor *time.Time
	cursorStr := r.URL.Query().Get("cursor")
	if cursorStr != "" {
		if parsedCursor, err := time.Parse(time.RFC3339, cursorStr); err == nil {
			cursor = &parsedCursor
		}
	}

	jobs, err := h.jobService.ListJobs(r.Context(), userID, limit, cursor)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list jobs")
		writeJSONError(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobs": jobs,
	})
}

// GetAsset handles GET /v1/assets/{id}
func (h *Handler) GetAsset(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	assetID, err := uuid.Parse(vars["id"])
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid asset id")
		return
	}

	// TODO: Get asset from database and verify ownership
	_ = assetID

	writeJSONError(w, http.StatusNotImplemented, "not implemented yet")
}

// GetAssetContent handles GET /v1/assets/{id}/content
func (h *Handler) GetAssetContent(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	assetID, err := uuid.Parse(vars["id"])
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid asset id")
		return
	}

	// TODO: Get asset from database, verify ownership, and stream from S3
	_ = assetID

	writeJSONError(w, http.StatusNotImplemented, "not implemented yet")
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Error().Err(err).Msg("Failed to encode JSON response")
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
