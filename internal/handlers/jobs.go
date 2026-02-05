package handlers

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/auth"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/markup"
	"github.com/snappy-loop/stories/internal/models"
	"github.com/snappy-loop/stories/internal/services"
	"github.com/snappy-loop/stories/internal/storage"
)

// Handler contains all HTTP handlers
type Handler struct {
	jobService         *services.JobService
	fileService        *services.FileService
	storage            *storage.Client
	userRepo           *database.UserRepository
	apiKeyRepo         *database.APIKeyRepository
	defaultQuotaChars  int64
	defaultQuotaPeriod string
	maxPicturesCount   int
}

// NewHandler creates a new handler
func NewHandler(
	jobService *services.JobService,
	fileService *services.FileService,
	storage *storage.Client,
	userRepo *database.UserRepository,
	apiKeyRepo *database.APIKeyRepository,
	defaultQuotaChars int64,
	defaultQuotaPeriod string,
	maxPicturesCount int,
) *Handler {
	return &Handler{
		jobService:         jobService,
		fileService:        fileService,
		storage:            storage,
		userRepo:           userRepo,
		apiKeyRepo:         apiKeyRepo,
		defaultQuotaChars:  defaultQuotaChars,
		defaultQuotaPeriod: defaultQuotaPeriod,
		maxPicturesCount:   maxPicturesCount,
	}
}

// Index serves the index page: list of all tasks (jobs) with statuses and view links
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(indexHTML))
}

// Generation serves the generation page: send test request and get job data forms
func (h *Handler) Generation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	maxStr := strconv.Itoa(h.maxPicturesCount)
	html := strings.ReplaceAll(generationHTML, "__MAX_PICTURES_COUNT__", maxStr)
	w.Write([]byte(html))
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
		b.WriteString(`<div class="segment">`)
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

	var b []byte
	b = append(b, viewHTMLHead...)
	b = append(b, bodyHTML...)
	b = append(b, viewHTMLTail...)

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

var viewHTMLHead = []byte(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Job view</title>
  <style>
    * { box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; max-width: 640px; margin: 2rem auto; padding: 0 1rem; }
    .segment { margin-bottom: 2rem; padding-bottom: 2rem; border-bottom: 1px solid #eee; }
    .segment:last-child { border-bottom: none; }
    .segment audio { display: block; margin-bottom: 0.75rem; width: 100%; }
    .segment-text { margin: 0.75rem 0; line-height: 1.5; white-space: pre-wrap; }
    .segment-image { display: block; max-width: 100%; height: auto; margin-top: 0.75rem; border-radius: 6px; }
    .segment-title { font-size: 1.1rem; margin: 0.5rem 0 0.25rem; }
    .source { margin-bottom: 2rem; padding: 1rem; background: #f8f8f8; border-radius: 6px; border-left: 4px solid #ccc; }
    .source h3 { font-size: 0.95rem; margin: 0 0 0.5rem; color: #555; }
    .source-content { margin: 0; font-size: 0.9rem; white-space: pre-wrap; word-break: break-word; }
  </style>
</head>
<body>
`)

var viewHTMLTail = []byte(`
</body>
</html>
`)

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Great Stories — Tasks</title>
  <style>
    * { box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; max-width: 720px; margin: 2rem auto; padding: 0 1rem; }
    h1 { font-size: 1.5rem; margin-bottom: 0.5rem; }
    section { margin-bottom: 1.5rem; }
    label { display: block; margin-bottom: 0.25rem; font-weight: 500; }
    input { padding: 0.5rem; margin-bottom: 0.75rem; border: 1px solid #ccc; border-radius: 4px; max-width: 360px; }
    button { padding: 0.5rem 1rem; background: #333; color: #fff; border: none; border-radius: 4px; cursor: pointer; }
    button:hover { background: #555; }
    .index-api-section input { width: 100%; }
    .index-api-hint { font-size: 0.8rem; color: #666; margin-top: 0.25rem; }
    .tasks-table { width: 100%; border-collapse: collapse; margin-top: 1rem; }
    .tasks-table th, .tasks-table td { text-align: left; padding: 0.5rem 0.75rem; border-bottom: 1px solid #e0e0e0; }
    .tasks-table th { font-weight: 600; }
    .tasks-table a { color: #333; }
    .tasks-table .job-id-cell { max-width: 120px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .tasks-error { color: #c00; margin-top: 0.5rem; }
    .tasks-empty { color: #666; margin-top: 1rem; }
    .nav-link { margin-right: 1rem; }
  </style>
</head>
<body>
  <h1>Great Stories</h1>
  <p><a href="/" class="nav-link">Tasks</a><a href="/generation" class="nav-link">Generation</a></p>

  <section class="index-api-section">
    <label for="index-api-key">API key</label>
    <input type="password" id="index-api-key" placeholder="API key" autocomplete="off">
    <button type="button" id="index-load-tasks">Load tasks</button>
    <p class="index-api-hint">Email me to <a href="mailto:vasily.kulakov@gmail.com">vasily.kulakov@gmail.com</a> to get an API key.</p>
    <p id="index-error" class="tasks-error" style="display:none;"></p>
  </section>

  <table id="index-tasks-table" class="tasks-table" style="display:none;">
    <thead>
      <tr><th>Job ID</th><th>Status</th><th>Type</th><th>Photos</th><th>Speech</th><th>Created</th><th></th></tr>
    </thead>
    <tbody id="index-tasks-body"></tbody>
  </table>
  <p id="index-tasks-empty" class="tasks-empty" style="display:none;">No tasks yet. Enter API key and click Load tasks, or <a href="/generation">create a new job</a>.</p>

  <script>
    function contractId(id) {
      if (!id || id.length <= 12) return id;
      return id.substring(0, 8) + '…' + id.substring(id.length - 4);
    }
    document.getElementById('index-load-tasks').addEventListener('click', async function() {
      const apiKey = document.getElementById('index-api-key').value.trim();
      const errorEl = document.getElementById('index-error');
      const tableEl = document.getElementById('index-tasks-table');
      const bodyEl = document.getElementById('index-tasks-body');
      const emptyEl = document.getElementById('index-tasks-empty');
      errorEl.style.display = 'none';
      emptyEl.style.display = 'none';
      if (!apiKey) {
        errorEl.textContent = 'Please enter an API key.';
        errorEl.style.display = 'block';
        return;
      }
      try {
        const res = await fetch('/v1/jobs', { headers: { 'Authorization': 'Bearer ' + apiKey } });
        const data = await res.json();
        if (!res.ok) {
          errorEl.textContent = data.error || res.statusText || 'Failed to load tasks';
          errorEl.style.display = 'block';
          return;
        }
        const jobs = data.jobs || [];
        bodyEl.innerHTML = '';
        if (jobs.length === 0) {
          emptyEl.style.display = 'block';
          tableEl.style.display = 'none';
        } else {
          tableEl.style.display = 'table';
          jobs.forEach(function(job) {
            const tr = document.createElement('tr');
            const id = job.id || job.job_id || '';
            const shortId = contractId(id);
            const status = job.status || '';
            const type = job.input_type || '';
            const photos = job.pictures_count != null ? job.pictures_count : '';
            const speech = job.audio_type || '';
            const created = job.created_at ? new Date(job.created_at).toLocaleString() : '';
            tr.innerHTML = '<td class="job-id-cell" title="' + id.replace(/"/g, '&quot;') + '"><code style="font-size:0.85em">' + shortId + '</code></td><td>' + status + '</td><td>' + type + '</td><td>' + photos + '</td><td>' + speech + '</td><td>' + created + '</td><td><a href="/view/' + id + '">View</a></td>';
            bodyEl.appendChild(tr);
          });
        }
      } catch (err) {
        errorEl.textContent = err.message;
        errorEl.style.display = 'block';
      }
    });
  </script>
</body>
</html>
`

const generationHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Great Stories — Generation</title>
  <style>
    * { box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; max-width: 560px; margin: 2rem auto; padding: 0 1rem; }
    h1 { font-size: 1.5rem; margin-bottom: 0.5rem; }
    section { margin-bottom: 2rem; padding: 1.25rem; border: 1px solid #e0e0e0; border-radius: 8px; }
    section h2 { font-size: 1.1rem; margin-top: 0; margin-bottom: 1rem; }
    label { display: block; margin-bottom: 0.25rem; font-weight: 500; }
    input, textarea, select { width: 100%; padding: 0.5rem; margin-bottom: 0.75rem; border: 1px solid #ccc; border-radius: 4px; }
    textarea { min-height: 80px; resize: vertical; }
    button { padding: 0.5rem 1rem; background: #333; color: #fff; border: none; border-radius: 4px; cursor: pointer; }
    button:hover { background: #555; }
    button:disabled { opacity: 0.6; cursor: not-allowed; }
    .result { margin-top: 1rem; padding: 0.75rem; background: #f5f5f5; border-radius: 4px; font-size: 0.9rem; white-space: pre-wrap; word-break: break-all; }
    .error { background: #fee; color: #c00; }
    .poll-status { font-size: 0.85rem; color: #666; margin-bottom: 0.5rem; }
    .get-job-loading { display: none; margin-bottom: 0.75rem; }
    .get-job-loading.visible { display: block; }
    .loading-dots::after { content: ''; animation: loading-dots 1.5s steps(4, end) infinite; }
    @keyframes loading-dots { 0%, 20% { content: ''; } 40% { content: '.'; } 60% { content: '..'; } 80%, 100% { content: '...'; } }
    .get-job-view-link { margin-left: 0.25rem; }
    .nav-link { margin-right: 1rem; }
    .api-docs { margin-bottom: 1.5rem; border: 1px solid #e0e0e0; border-radius: 8px; }
    .api-docs summary { padding: 0.75rem 1rem; cursor: pointer; font-weight: 500; list-style: none; }
    .api-docs summary::-webkit-details-marker { display: none; }
    .api-docs summary::before { content: '▶'; display: inline-block; margin-right: 0.5rem; font-size: 0.7em; transition: transform 0.2s; }
    .api-docs[open] summary::before { transform: rotate(90deg); }
    .api-docs-inner { padding: 0 1rem 1rem; font-size: 0.9rem; color: #444; }
    .api-docs-inner h4 { margin: 1rem 0 0.35rem; font-size: 0.95rem; }
    .api-docs-inner h4:first-child { margin-top: 0; }
    .api-docs-inner pre { background: #f5f5f5; padding: 0.5rem; border-radius: 4px; overflow-x: auto; font-size: 0.85em; }
    .api-docs-inner p { margin: 0.25rem 0 0; }
  </style>
</head>
<body>
  <h1>Great Stories — Generation</h1>
  <p><a href="/" class="nav-link">Tasks</a><a href="/generation" class="nav-link">Generation</a></p>
  <p>Send a test job request and check job status.</p>

  <details class="api-docs">
    <summary>API description</summary>
    <div class="api-docs-inner">
      <h4>POST /v1/jobs</h4>
      <p>Create a new enrichment job. Use <code>Authorization: Bearer &lt;api_key&gt;</code>.</p>
      <p><strong>Request:</strong> Provide <code>text</code> and/or <code>file_ids</code> (upload files first via POST /v1/files).</p>
      <pre>{
  "text": "string (optional if file_ids provided)",
  "file_ids": ["uuid", "..."],
  "type": "educational | financial | fictional",
  "pictures_count": "integer (1–max from config)",
  "audio_type": "free_speech | podcast",
  "webhook": { "url": "string (optional)", "secret": "string (optional)" }
}</pre>
      <p><strong>POST /v1/files</strong> — Upload a file (multipart form, field <code>file</code>). Returns <code>file_id</code>. Use these IDs in <code>file_ids</code> when creating a job.</p>
      <p><strong>Response (202):</strong> <code>{ "job_id", "status": "queued", "created_at" }</code></p>
      <h4>GET /v1/jobs/{job_id}</h4>
      <p>Get job status, segments, and assets (with download URLs).</p>
      <h4>GET /v1/jobs</h4>
      <p>List your jobs (with pagination).</p>
      <h4>GET /v1/assets/{asset_id} / GET /v1/assets/{asset_id}/content</h4>
      <p>Asset metadata and pass-through content.</p>
    </div>
  </details>

  <section>
    <h2>API key</h2>
    <label for="api-key">Use this key for both forms below</label>
    <input type="password" id="api-key" name="api_key" placeholder="API key" autocomplete="off" data-1p-ignore>
  </section>

  <section>
    <h2>Send test request</h2>
    <form id="test-request">
      <label for="text">Text (optional if you add files)</label>
      <textarea id="text" name="text" placeholder="Short story or paragraph to enrich..." maxlength="50000"></textarea>
      <p id="text-remaining" style="font-size:0.85rem;color:#666;margin:-0.5rem 0 0.75rem 0;">50000 characters remaining</p>
      <label for="files">Files (optional). PDF or images. Uploaded when you send the request.</label>
      <input type="file" id="files" name="files" multiple accept=".pdf,image/jpeg,image/png,image/gif,image/webp,application/pdf">
      <p id="files-summary" style="font-size:0.85rem;color:#666;margin:-0.5rem 0 0.75rem 0;">No files selected</p>
      <p style="font-size:0.8rem;color:#888;margin:-0.25rem 0 0.75rem 0;">Disclaimer: Files containing copyrighted content (e.g. book pages, articles, comics) may fail to process due to content policy.</p>
      <label for="type">Type</label>
      <select id="type" name="type">
        <option value="educational">Educational</option>
        <option value="financial">Financial</option>
        <option value="fictional">Fictional</option>
      </select>
      <label for="pictures_count">Pictures count</label>
      <input type="number" id="pictures_count" name="pictures_count" value="2" min="1" max="__MAX_PICTURES_COUNT__">
      <label for="audio_type">Audio type</label>
      <select id="audio_type" name="audio_type">
        <option value="free_speech">Free speech</option>
        <option value="podcast">Podcast</option>
      </select>
      <button type="submit" id="send-test-btn">Send test request</button>
    </form>
    <div id="test-request-result" class="result" style="display:none;"></div>
  </section>

  <section>
    <h2>Get job data</h2>
    <div id="get-job-loading" class="get-job-loading">
      <span class="loading-dots">Processing</span>
      <p style="margin: 0.25rem 0 0 0; font-size: 0.85rem; color: #666;">Processing may take up to 10 minutes, depending on the text length.</p>
    </div>
    <p id="get-job-poll-status" class="poll-status" style="display:none;"></p>
    <span id="get-job-view-wrap" style="display:none;"><a id="get-job-view-link" href="#" class="get-job-view-link">View</a></span>   
    <form id="get-job">
      <label for="job-id">Job ID</label>
      <input type="text" id="job-id" name="job_id" placeholder="e.g. 550e8400-e29b-41d4-a716-446655440000" required>
      <button type="submit">Get job</button>
    </form>
    <p id="get-job-copy-wrap" style="display:none; margin-bottom:0.5rem;">
      <button type="button" id="get-job-copy-btn">Copy response</button>
      <span id="get-job-copy-feedback" style="margin-left:0.5rem; font-size:0.9rem; color:#666;"></span>
    </p>
    <div id="get-job-result" class="result" style="display:none;"></div>
  </section>

  <script>
    var MAX_TEXT_LENGTH = 50000;
    var MAX_PICTURES_COUNT = __MAX_PICTURES_COUNT__;
    var textEl = document.getElementById('text');
    var remainingEl = document.getElementById('text-remaining');
    function updateRemaining() {
      var len = textEl.value.length;
      var rem = MAX_TEXT_LENGTH - len;
      remainingEl.textContent = rem + ' characters remaining';
      if (rem < 0) remainingEl.style.color = '#c00';
      else remainingEl.style.color = '#666';
    }
    textEl.addEventListener('input', updateRemaining);
    textEl.addEventListener('paste', function() { setTimeout(updateRemaining, 0); });

    var filesInput = document.getElementById('files');
    var filesSummary = document.getElementById('files-summary');
    function updateFilesSummary() {
      var n = filesInput.files.length;
      filesSummary.textContent = n === 0 ? 'No files selected' : n + ' file(s) selected';
    }
    filesInput.addEventListener('change', updateFilesSummary);

    document.getElementById('test-request').addEventListener('submit', async function(e) {
      e.preventDefault();
      const resultEl = document.getElementById('test-request-result');
      const sendBtn = document.getElementById('send-test-btn');
      resultEl.style.display = 'block';
      resultEl.classList.remove('error');
      const text = document.getElementById('text').value.trim();
      const fileList = filesInput.files;
      const hasFiles = fileList && fileList.length > 0;
      if (!text && !hasFiles) {
        resultEl.textContent = 'Please enter text and/or select at least one file.';
        resultEl.classList.add('error');
        return;
      }
      if (text.length > MAX_TEXT_LENGTH) {
        resultEl.textContent = 'Text is too long. Maximum ' + MAX_TEXT_LENGTH + ' characters.';
        resultEl.classList.add('error');
        return;
      }
      const apiKey = document.getElementById('api-key').value.trim();
      if (!apiKey) {
        resultEl.textContent = 'Please enter an API key.';
        resultEl.classList.add('error');
        return;
      }
      const picturesCount = parseInt(document.getElementById('pictures_count').value, 10) || 2;
      if (picturesCount > MAX_PICTURES_COUNT || picturesCount < 1) {
        resultEl.textContent = 'Pictures count must be between 1 and ' + MAX_PICTURES_COUNT + '.';
        resultEl.classList.add('error');
        return;
      }

      var fileIds = [];
      if (hasFiles) {
        sendBtn.disabled = true;
        sendBtn.textContent = 'Uploading 0/' + fileList.length + '...';
        resultEl.textContent = 'Uploading files...';
        try {
          for (var i = 0; i < fileList.length; i++) {
            sendBtn.textContent = 'Uploading ' + (i + 1) + '/' + fileList.length + '...';
            var fd = new FormData();
            fd.append('file', fileList[i]);
            var uploadRes = await fetch('/v1/files', {
              method: 'POST',
              headers: { 'Authorization': 'Bearer ' + apiKey },
              body: fd
            });
            var uploadData = await uploadRes.json();
            if (!uploadRes.ok) {
              resultEl.textContent = 'Upload failed: ' + (uploadData.error || uploadRes.statusText);
              resultEl.classList.add('error');
              sendBtn.disabled = false;
              sendBtn.textContent = 'Send test request';
              return;
            }
            fileIds.push(uploadData.file_id);
          }
        } catch (err) {
          resultEl.textContent = 'Upload error: ' + err.message;
          resultEl.classList.add('error');
          sendBtn.disabled = false;
          sendBtn.textContent = 'Send test request';
          return;
        }
        sendBtn.textContent = 'Creating job...';
      }

      var payload = {
        type: document.getElementById('type').value,
        pictures_count: picturesCount,
        audio_type: document.getElementById('audio_type').value
      };
      if (text) payload.text = text;
      if (fileIds.length > 0) payload.file_ids = fileIds;

      try {
        const res = await fetch('/v1/jobs', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'Authorization': 'Bearer ' + apiKey
          },
          body: JSON.stringify(payload)
        });
        const data = await res.json();
        if (!res.ok) {
          resultEl.textContent = 'Error: ' + (data.error || res.statusText);
          resultEl.classList.add('error');
          sendBtn.disabled = false;
          sendBtn.textContent = 'Send test request';
          return;
        }
        resultEl.textContent = 'Job created.\njob_id: ' + data.job_id + '\nstatus: ' + data.status;
        document.getElementById('job-id').value = data.job_id || '';
      } catch (err) {
        resultEl.textContent = 'Error: ' + err.message;
        resultEl.classList.add('error');
      }
      sendBtn.disabled = false;
      sendBtn.textContent = 'Send test request';
    });

    var getJobPollTimer = null;
    function stopGetJobPoll() {
      if (getJobPollTimer) {
        clearInterval(getJobPollTimer);
        getJobPollTimer = null;
      }
      document.getElementById('get-job-poll-status').style.display = 'none';
      document.getElementById('get-job-loading').classList.remove('visible');
    }
    function fetchAndShowJob(apiKey, jobId, resultEl, pollStatusEl, isPoll) {
      return fetch('/v1/jobs/' + encodeURIComponent(jobId), {
        method: 'GET',
        headers: { 'Authorization': 'Bearer ' + apiKey }
      }).then(function(res) { return res.json().then(function(data) { return { res: res, data: data }; }); })
        .then(function(_ref) {
          var res = _ref.res, data = _ref.data;
          if (!res.ok) {
            resultEl.textContent = 'Error: ' + (data.error || res.statusText);
            resultEl.classList.add('error');
            if (isPoll) stopGetJobPoll();
            return null;
          }
          resultEl.classList.remove('error');
          resultEl.textContent = JSON.stringify(data, null, 2);
          if (pollStatusEl && data.job) {
            pollStatusEl.style.display = 'block';
            pollStatusEl.textContent = 'Polling every 5s. Status: ' + (data.job.status || '');
          }
          return data.job ? data.job.status : null;
        }).catch(function(err) {
          resultEl.textContent = 'Error: ' + err.message;
          resultEl.classList.add('error');
          if (isPoll) stopGetJobPoll();
          return null;
        });
    }
    document.getElementById('get-job').addEventListener('submit', async function(e) {
      e.preventDefault();
      const resultEl = document.getElementById('get-job-result');
      const copyWrap = document.getElementById('get-job-copy-wrap');
      const pollStatusEl = document.getElementById('get-job-poll-status');
      const loadingEl = document.getElementById('get-job-loading');
      const viewWrap = document.getElementById('get-job-view-wrap');
      const viewLink = document.getElementById('get-job-view-link');
      resultEl.style.display = 'block';
      copyWrap.style.display = 'block';
      resultEl.classList.remove('error');
      const apiKey = document.getElementById('api-key').value.trim();
      const jobId = document.getElementById('job-id').value.trim();
      if (!apiKey) {
        resultEl.textContent = 'Please enter an API key above.';
        resultEl.classList.add('error');
        return;
      }
      if (!jobId) {
        resultEl.textContent = 'Please enter a job ID.';
        resultEl.classList.add('error');
        return;
      }
      stopGetJobPoll();
      viewWrap.style.display = 'none';
      loadingEl.classList.add('visible');
      try {
        const status = await fetchAndShowJob(apiKey, jobId, resultEl, pollStatusEl, false);
        viewLink.setAttribute('href', '/view/' + encodeURIComponent(jobId));
        viewWrap.style.display = 'inline';
        if (status !== 'succeeded' && status !== 'failed' && status !== 'canceled') {
          getJobPollTimer = setInterval(function() {
            fetchAndShowJob(apiKey, jobId, resultEl, pollStatusEl, true).then(function(s) {
              if (s === 'succeeded' || s === 'failed' || s === 'canceled') stopGetJobPoll();
            });
          }, 5000);
        } else {
          loadingEl.classList.remove('visible');
        }
      } catch (err) {
        resultEl.textContent = 'Error: ' + err.message;
        resultEl.classList.add('error');
        loadingEl.classList.remove('visible');
      }
    });

    document.getElementById('get-job-copy-btn').addEventListener('click', function() {
      const resultEl = document.getElementById('get-job-result');
      const feedback = document.getElementById('get-job-copy-feedback');
      var text = resultEl.textContent || '';
      if (!text) {
        feedback.textContent = 'Nothing to copy.';
        setTimeout(function() { feedback.textContent = ''; }, 2000);
        return;
      }
      navigator.clipboard.writeText(text).then(function() {
        feedback.textContent = 'Copied!';
        setTimeout(function() { feedback.textContent = ''; }, 2000);
      }).catch(function() {
        feedback.textContent = 'Copy failed.';
        setTimeout(function() { feedback.textContent = ''; }, 2000);
      });
    });
  </script>
</body>
</html>
`
