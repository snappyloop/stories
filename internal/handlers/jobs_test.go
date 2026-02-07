package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/snappy-loop/stories/internal/auth"
	"github.com/snappy-loop/stories/internal/models"
)

// fakeJobService is a minimal jobService for tests.
type fakeJobService struct {
	createJob func(context.Context, *models.CreateJobRequest, uuid.UUID, uuid.UUID) (*models.CreateJobResponse, error)
	getJob    func(context.Context, uuid.UUID, uuid.UUID) (*models.JobStatusResponse, error)
}

func (f *fakeJobService) CreateJob(ctx context.Context, req *models.CreateJobRequest, userID, apiKeyID uuid.UUID) (*models.CreateJobResponse, error) {
	if f.createJob != nil {
		return f.createJob(ctx, req, userID, apiKeyID)
	}
	return &models.CreateJobResponse{JobID: uuid.New(), Status: "queued", CreatedAt: time.Now()}, nil
}

func (f *fakeJobService) GetJob(ctx context.Context, jobID, userID uuid.UUID) (*models.JobStatusResponse, error) {
	if f.getJob != nil {
		return f.getJob(ctx, jobID, userID)
	}
	return nil, nil
}

func (f *fakeJobService) GetJobByID(ctx context.Context, jobID uuid.UUID) (*models.JobStatusResponse, error) {
	return nil, nil
}

func (f *fakeJobService) ListJobs(ctx context.Context, userID uuid.UUID, limit int, cursor *time.Time) ([]*models.Job, error) {
	return nil, nil
}

func (f *fakeJobService) GetAsset(ctx context.Context, assetID, userID uuid.UUID) (*models.Asset, error) {
	return nil, nil
}

func (f *fakeJobService) GetAssetByJobID(ctx context.Context, assetID, jobID uuid.UUID) (*models.Asset, error) {
	return nil, nil
}

// TestCreateJob_Unauthorized asserts 401 when request context has no user/key.
func TestCreateJob_Unauthorized(t *testing.T) {
	h := NewHandler(
		&fakeJobService{},
		nil, nil, nil, nil,
		100000, "monthly", 20, nil, "", "",
	)

	body := bytes.NewBufferString(`{"text":"Hi","type":"educational","segments_count":2,"audio_type":"free_speech"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", body)
	req.Header.Set("Content-Type", "application/json")
	// Do not add auth to context
	rec := httptest.NewRecorder()

	h.CreateJob(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateJob_InvalidBody asserts 400 for invalid JSON.
func TestCreateJob_InvalidBody(t *testing.T) {
	userID := uuid.New()
	apiKeyID := uuid.New()

	h := NewHandler(
		&fakeJobService{},
		nil, nil, nil, nil,
		100000, "monthly", 20, nil, "", "",
	)

	body := bytes.NewBufferString(`{invalid json`)
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", body)
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), auth.UserIDKey, userID)
	ctx = context.WithValue(ctx, auth.APIKeyIDKey, apiKeyID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.CreateJob(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateJob_ValidationErrorFromService asserts 400 when service returns validation error.
func TestCreateJob_ValidationErrorFromService(t *testing.T) {
	userID := uuid.New()
	apiKeyID := uuid.New()

	h := NewHandler(
		&fakeJobService{
			createJob: func(context.Context, *models.CreateJobRequest, uuid.UUID, uuid.UUID) (*models.CreateJobResponse, error) {
				return nil, fmt.Errorf("validation error: either text or file_ids is required")
			},
		},
		nil, nil, nil, nil,
		100000, "monthly", 20, nil, "", "",
	)

	body := bytes.NewBufferString(`{"type":"educational","segments_count":2,"audio_type":"free_speech"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", body)
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), auth.UserIDKey, userID)
	ctx = context.WithValue(ctx, auth.APIKeyIDKey, apiKeyID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.CreateJob(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateJob_Success asserts 202 and job_id when service succeeds.
func TestCreateJob_Success(t *testing.T) {
	userID := uuid.New()
	apiKeyID := uuid.New()
	jobID := uuid.New()

	h := NewHandler(
		&fakeJobService{
			createJob: func(context.Context, *models.CreateJobRequest, uuid.UUID, uuid.UUID) (*models.CreateJobResponse, error) {
				return &models.CreateJobResponse{JobID: jobID, Status: "queued", CreatedAt: time.Now()}, nil
			},
		},
		nil, nil, nil, nil,
		100000, "monthly", 20, nil, "", "",
	)

	body := bytes.NewBufferString(`{"text":"Hello","type":"educational","segments_count":2,"audio_type":"free_speech"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", body)
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), auth.UserIDKey, userID)
	ctx = context.WithValue(ctx, auth.APIKeyIDKey, apiKeyID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.CreateJob(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp models.CreateJobResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.JobID != jobID {
		t.Errorf("job_id %s != expected %s", resp.JobID, jobID)
	}
	if resp.Status != "queued" {
		t.Errorf("status %s", resp.Status)
	}
}

// TestGetJob_InvalidID asserts 400 for invalid job UUID.
func TestGetJob_InvalidID(t *testing.T) {
	userID := uuid.New()
	h := NewHandler(&fakeJobService{}, nil, nil, nil, nil, 100000, "monthly", 20, nil, "", "")

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/not-a-uuid", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "not-a-uuid"})
	ctx := context.WithValue(req.Context(), auth.UserIDKey, userID)
	ctx = context.WithValue(ctx, auth.APIKeyIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.GetJob(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
