package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/agentsclient"
	"github.com/snappy-loop/stories/internal/auth"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/markup"
	"github.com/snappy-loop/stories/internal/models"
	"github.com/snappy-loop/stories/internal/services"
	"github.com/snappy-loop/stories/internal/storage"
)

// jobService is the subset of JobService used by job handlers (for testability).
type jobService interface {
	CreateJob(ctx context.Context, req *models.CreateJobRequest, userID, apiKeyID uuid.UUID) (*models.CreateJobResponse, error)
	GetJob(ctx context.Context, jobID, userID uuid.UUID) (*models.JobStatusResponse, error)
	GetJobByID(ctx context.Context, jobID uuid.UUID) (*models.JobStatusResponse, error)
	ListJobs(ctx context.Context, userID uuid.UUID, limit int, cursor *time.Time) ([]*models.Job, error)
	GetAsset(ctx context.Context, assetID, userID uuid.UUID) (*models.Asset, error)
	GetAssetByJobID(ctx context.Context, assetID, jobID uuid.UUID) (*models.Asset, error)
}

// Handler contains all HTTP handlers
type Handler struct {
	jobService         jobService
	fileService        *services.FileService
	storage            *storage.Client
	userRepo           *database.UserRepository
	apiKeyRepo         *database.APIKeyRepository
	defaultQuotaChars  int64
	defaultQuotaPeriod string
	maxSegmentsCount   int
	agentsClient       *agentsclient.Client
	agentsGRPCURL      string
	agentsMCPURL       string
}

// NewHandler creates a new handler. agentsClient may be nil if the agents service is not configured.
// agentsGRPCURL and agentsMCPURL are used to show/hide transport options and agent panels on the /agents page.
func NewHandler(
	jobService jobService,
	fileService *services.FileService,
	storage *storage.Client,
	userRepo *database.UserRepository,
	apiKeyRepo *database.APIKeyRepository,
	defaultQuotaChars int64,
	defaultQuotaPeriod string,
	maxSegmentsCount int,
	agentsClient *agentsclient.Client,
	agentsGRPCURL, agentsMCPURL string,
) *Handler {
	return &Handler{
		jobService:         jobService,
		fileService:        fileService,
		storage:            storage,
		userRepo:           userRepo,
		apiKeyRepo:         apiKeyRepo,
		defaultQuotaChars:  defaultQuotaChars,
		defaultQuotaPeriod: defaultQuotaPeriod,
		maxSegmentsCount:   maxSegmentsCount,
		agentsClient:       agentsClient,
		agentsGRPCURL:      agentsGRPCURL,
		agentsMCPURL:       agentsMCPURL,
	}
}

// agentsNavLink returns the Agents nav link HTML, or empty if agents are not configured.
func (h *Handler) agentsNavLink() template.HTML {
	if h.agentsGRPCURL != "" || h.agentsMCPURL != "" {
		return template.HTML(`<a href="/agents" class="nav-link">Agents</a>`)
	}
	return ""
}

// Index serves the index page: list of all tasks (jobs) with statuses and view links
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	data := struct{ NavAgentsLink template.HTML }{NavAgentsLink: h.agentsNavLink()}
	buf, err := executeTemplateToBytes("index", data)
	if err != nil {
		log.Error().Err(err).Msg("index template")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(buf)
}

// Generation serves the generation page: send test request and get job data forms
func (h *Handler) Generation(w http.ResponseWriter, r *http.Request) {
	data := struct {
		NavAgentsLink    template.HTML
		MaxSegmentsCount int
	}{NavAgentsLink: h.agentsNavLink(), MaxSegmentsCount: h.maxSegmentsCount}
	buf, err := executeTemplateToBytes("generation", data)
	if err != nil {
		log.Error().Err(err).Msg("generation template")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(buf)
}

// CreateUser handles POST /users — creates a user and an API key, returns both (API key shown once)
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email *string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user := &models.User{
		ID:        uuid.New(),
		Email:     req.Email,
		CreatedAt: time.Now(),
	}
	if err := h.userRepo.Create(r.Context(), user); err != nil {
		log.Error().Err(err).Msg("Failed to create user")
		writeJSONError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	plainKey, _, err := h.apiKeyRepo.CreateAPIKey(r.Context(), user.ID, h.defaultQuotaChars, h.defaultQuotaPeriod)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create API key")
		writeJSONError(w, http.StatusInternalServerError, "failed to create API key")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"user_id": user.ID.String(),
		"email":   user.Email,
		"api_key": plainKey,
		"message": "Copy the api_key; it will not be shown again.",
	})
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

	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	asset, err := h.jobService.GetAsset(r.Context(), assetID, userID)
	if err != nil {
		log.Error().Err(err).Str("asset_id", assetID.String()).Msg("Failed to get asset")
		writeJSONError(w, http.StatusNotFound, "asset not found")
		return
	}

	writeJSON(w, http.StatusOK, models.AssetResponse{
		Asset:       asset.ToInResponse(),
		DownloadURL: "/v1/assets/" + assetID.String() + "/content",
	})
}

// GetAssetContent handles GET /v1/assets/{id}/content — pass-through stream from S3
func (h *Handler) GetAssetContent(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	assetID, err := uuid.Parse(vars["id"])
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid asset id")
		return
	}

	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	asset, err := h.jobService.GetAsset(r.Context(), assetID, userID)
	if err != nil {
		log.Error().Err(err).Str("asset_id", assetID.String()).Msg("Failed to get asset")
		writeJSONError(w, http.StatusNotFound, "asset not found")
		return
	}

	if h.storage == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "storage not configured")
		return
	}

	body, err := h.storage.GetObject(r.Context(), asset.S3Key)
	if err != nil {
		log.Error().Err(err).Str("asset_id", assetID.String()).Str("s3_key", asset.S3Key).Msg("Failed to get object from storage")
		writeJSONError(w, http.StatusInternalServerError, "failed to load asset")
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", asset.MimeType)
	if asset.SizeBytes > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(asset.SizeBytes, 10))
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, body); err != nil {
		log.Error().Err(err).Str("asset_id", assetID.String()).Msg("Failed to stream asset content")
	}
}

// injectFactChecksIntoHTML inserts fact-check divs into segment divs. For each non-empty fact-check,
// finds the segment div with matching data-segment-id and appends a .fact-check div before its
// outermost closing </div>. Uses string search instead of regex so that nested divs inside the
// segment are handled correctly.
func injectFactChecksIntoHTML(bodyHTML string, factChecks []*models.SegmentFactCheck) string {
	if len(factChecks) == 0 {
		return bodyHTML
	}
	for _, fc := range factChecks {
		if fc.FactCheckText == "" {
			continue
		}
		// Locate the opening tag for this segment.
		openTag := `<div class="segment" data-segment-id="` + fc.SegmentID.String() + `">`
		openIdx := strings.Index(bodyHTML, openTag)
		if openIdx < 0 {
			continue
		}
		afterOpen := openIdx + len(openTag)

		// Walk the HTML after the opening tag and count nested divs to find the
		// matching closing </div> for this segment.
		depth := 1
		pos := afterOpen
		closeIdx := -1
		for depth > 0 && pos < len(bodyHTML) {
			// Find the next div-related tag (opening or closing).
			nextOpen := strings.Index(bodyHTML[pos:], "<div")
			nextClose := strings.Index(bodyHTML[pos:], "</div>")
			if nextClose < 0 {
				break // malformed HTML, bail out
			}
			if nextOpen >= 0 && nextOpen < nextClose {
				depth++
				pos += nextOpen + 4 // skip past "<div"
			} else {
				depth--
				if depth == 0 {
					closeIdx = pos + nextClose
				}
				pos += nextClose + 6 // skip past "</div>"
			}
		}
		if closeIdx < 0 {
			continue
		}
		escaped := html.EscapeString(fc.FactCheckText)
		insert := `<div class="fact-check">` + escaped + `</div>`
		bodyHTML = bodyHTML[:closeIdx] + insert + bodyHTML[closeIdx:]
	}
	return bodyHTML
}

// viewJobFallbackHTML builds HTML from segments and assets when job has no output_markup (e.g. legacy jobs).
func viewJobFallbackHTML(resp *models.JobStatusResponse, jobIDStr string) string {
	type segmentAssets struct {
		audio *models.AssetResponse
		image *models.AssetResponse
	}
	bySegment := make(map[uuid.UUID]*segmentAssets)
	for i := range resp.Assets {
		a := resp.Assets[i]
		if a.Asset.SegmentID == nil {
			continue
		}
		sid := *a.Asset.SegmentID
		if bySegment[sid] == nil {
			bySegment[sid] = &segmentAssets{}
		}
		switch a.Asset.Kind {
		case "audio":
			if bySegment[sid].audio == nil {
				bySegment[sid].audio = a
			}
		case "image":
			if bySegment[sid].image == nil {
				bySegment[sid].image = a
			}
		}
	}
	var b strings.Builder
	for _, seg := range resp.Segments {
		sa := bySegment[seg.ID]
		b.WriteString(`<div class="segment" data-segment-id="`)
		b.WriteString(seg.ID.String())
		b.WriteString(`">`)
		if sa != nil && sa.audio != nil {
			b.WriteString(fmt.Sprintf(`<audio controls preload="metadata" src="/view/asset/%s?job_id=%s"></audio>`, sa.audio.Asset.ID.String(), jobIDStr))
		}
		b.WriteString(`<p class="segment-text">`)
		b.WriteString(html.EscapeString(seg.SegmentText))
		b.WriteString(`</p>`)
		if sa != nil && sa.image != nil {
			b.WriteString(fmt.Sprintf(`<img class="segment-image" src="/view/asset/%s?job_id=%s" alt="">`, sa.image.Asset.ID.String(), jobIDStr))
		}
		b.WriteString(`</div>`)
	}
	return b.String()
}

// ViewJob handles GET /view/{id} — renders job as HTML (from output_markup or fallback from segments)
func (h *Handler) ViewJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobID, err := uuid.Parse(vars["id"])
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	resp, err := h.jobService.GetJobByID(r.Context(), jobID)
	if err != nil {
		log.Error().Err(err).Str("job_id", jobID.String()).Msg("Failed to get job for view")
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	jobIDStr := jobID.String()
	var bodyHTML string
	if resp.Job.OutputMarkup != nil && *resp.Job.OutputMarkup != "" {
		bodyHTML = markup.ToHTML(*resp.Job.OutputMarkup, jobIDStr)
	} else {
		// Fallback: build from segments when no markup (e.g. old jobs)
		bodyHTML = viewJobFallbackHTML(resp, jobIDStr)
	}
	bodyHTML = injectFactChecksIntoHTML(bodyHTML, resp.FactChecks)

	var b []byte
	b = append(b, viewHeadBytes...)
	b = append(b, bodyHTML...)
	b = append(b, viewTailBytes...)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// ViewAsset handles GET /view/asset/{id}?job_id=xxx — pass-through for view page (no auth)
func (h *Handler) ViewAsset(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	assetID, err := uuid.Parse(vars["id"])
	if err != nil {
		http.Error(w, "invalid asset id", http.StatusBadRequest)
		return
	}
	jobIDStr := r.URL.Query().Get("job_id")
	if jobIDStr == "" {
		http.Error(w, "missing job_id", http.StatusBadRequest)
		return
	}
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		http.Error(w, "invalid job_id", http.StatusBadRequest)
		return
	}

	asset, err := h.jobService.GetAssetByJobID(r.Context(), assetID, jobID)
	if err != nil {
		log.Error().Err(err).Str("asset_id", assetID.String()).Str("job_id", jobIDStr).Msg("ViewAsset: asset not found")
		http.Error(w, "asset not found", http.StatusNotFound)
		return
	}

	if h.storage == nil {
		http.Error(w, "storage not configured", http.StatusServiceUnavailable)
		return
	}

	body, err := h.storage.GetObject(r.Context(), asset.S3Key)
	if err != nil {
		log.Error().Err(err).Str("asset_id", assetID.String()).Msg("ViewAsset: failed to get object")
		http.Error(w, "failed to load asset", http.StatusInternalServerError)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", asset.MimeType)
	if asset.SizeBytes > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(asset.SizeBytes, 10))
	}
	w.WriteHeader(http.StatusOK)
	io.Copy(w, body)
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
