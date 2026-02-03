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
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/models"
	"github.com/snappy-loop/stories/internal/services"
	"github.com/snappy-loop/stories/internal/storage"
)

// Handler contains all HTTP handlers
type Handler struct {
	jobService         *services.JobService
	storage            *storage.Client
	userRepo           *database.UserRepository
	apiKeyRepo         *database.APIKeyRepository
	defaultQuotaChars  int64
	defaultQuotaPeriod string
}

// NewHandler creates a new handler
func NewHandler(
	jobService *services.JobService,
	storage *storage.Client,
	userRepo *database.UserRepository,
	apiKeyRepo *database.APIKeyRepository,
	defaultQuotaChars int64,
	defaultQuotaPeriod string,
) *Handler {
	return &Handler{
		jobService:         jobService,
		storage:            storage,
		userRepo:           userRepo,
		apiKeyRepo:         apiKeyRepo,
		defaultQuotaChars:  defaultQuotaChars,
		defaultQuotaPeriod: defaultQuotaPeriod,
	}
}

// Index serves the index page with forms to create a user and send a test request
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(indexHTML))
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

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Great Stories — API</title>
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
    .result { margin-top: 1rem; padding: 0.75rem; background: #f5f5f5; border-radius: 4px; font-size: 0.9rem; white-space: pre-wrap; word-break: break-all; }
    .error { background: #fee; color: #c00; }
  </style>
</head>
<body>
  <h1>Great Stories — API</h1>
  <p>Create a user and send a test job request.</p>

  <section>
    <h2>Create user</h2>
    <form id="create-user">
      <label for="email">Email (optional)</label>
      <input type="email" id="email" name="email" placeholder="you@example.com">
      <button type="submit">Create user</button>
    </form>
    <div id="create-user-result" class="result" style="display:none;"></div>
  </section>

  <section>
    <h2>Send test request</h2>
    <form id="test-request">
      <label for="api-key">API key</label>
      <input type="password" id="api-key" name="api_key" placeholder="Paste api_key from above" autocomplete="off">
      <label for="text">Text</label>
      <textarea id="text" name="text" placeholder="Short story or paragraph to enrich..." required></textarea>
      <label for="type">Type</label>
      <select id="type" name="type">
        <option value="educational">Educational</option>
        <option value="financial">Financial</option>
        <option value="fictional">Fictional</option>
      </select>
      <label for="pictures_count">Pictures count</label>
      <input type="number" id="pictures_count" name="pictures_count" value="2" min="1" max="20">
      <label for="audio_type">Audio type</label>
      <select id="audio_type" name="audio_type">
        <option value="free_speech">Free speech</option>
        <option value="podcast">Podcast</option>
      </select>
      <button type="submit">Send test request</button>
    </form>
    <div id="test-request-result" class="result" style="display:none;"></div>
  </section>

  <script>
    document.getElementById('create-user').addEventListener('submit', async function(e) {
      e.preventDefault();
      const resultEl = document.getElementById('create-user-result');
      resultEl.style.display = 'block';
      resultEl.classList.remove('error');
      const email = document.getElementById('email').value.trim() || null;
      try {
        const res = await fetch('/users', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ email: email || null })
        });
        const data = await res.json();
        if (!res.ok) {
          resultEl.textContent = 'Error: ' + (data.error || res.statusText);
          resultEl.classList.add('error');
          return;
        }
        resultEl.textContent = 'User created.\nuser_id: ' + data.user_id + '\napi_key: ' + data.api_key + '\n\n' + (data.message || '');
        document.getElementById('api-key').value = data.api_key || '';
      } catch (err) {
        resultEl.textContent = 'Error: ' + err.message;
        resultEl.classList.add('error');
      }
    });

    document.getElementById('test-request').addEventListener('submit', async function(e) {
      e.preventDefault();
      const resultEl = document.getElementById('test-request-result');
      resultEl.style.display = 'block';
      resultEl.classList.remove('error');
      const apiKey = document.getElementById('api-key').value.trim();
      if (!apiKey) {
        resultEl.textContent = 'Please enter an API key (create a user first).';
        resultEl.classList.add('error');
        return;
      }
      const payload = {
        text: document.getElementById('text').value,
        type: document.getElementById('type').value,
        pictures_count: parseInt(document.getElementById('pictures_count').value, 10) || 2,
        audio_type: document.getElementById('audio_type').value
      };
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
          return;
        }
        resultEl.textContent = 'Job created.\njob_id: ' + data.job_id + '\nstatus: ' + data.status;
      } catch (err) {
        resultEl.textContent = 'Error: ' + err.message;
        resultEl.classList.add('error');
      }
    });
  </script>
</body>
</html>
`
