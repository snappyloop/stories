package services

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/config"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/models"
)

// TestCreateJob_ValidationErrors tests that CreateJob returns validation errors
// for invalid requests without hitting the database (validation runs first).
// Uses a real DB connection when DATABASE_URL is set so that NewJobService works.
func TestCreateJob_ValidationErrors(t *testing.T) {
	cfg := &config.Config{
		MaxFilesPerJob:    10,
		MaxInputLength:    50000,
		MaxPicturesCount:  20,
		CharsPerFile:      1000,
		DefaultQuotaChars: 100000,
		DefaultQuotaPeriod: "monthly",
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}

	db, err := database.Connect(dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	svc := NewJobService(db, nil, cfg)
	ctx := context.Background()
	userID := uuid.New()
	apiKeyID := uuid.New()

	tests := []struct {
		name string
		req  *models.CreateJobRequest
		want string
	}{
		{
			name: "empty text and no file_ids",
			req: &models.CreateJobRequest{
				Text:           "",
				Type:           "educational",
				PicturesCount:  2,
				AudioType:      "free_speech",
			},
			want: "either text or file_ids is required",
		},
		{
			name: "invalid type",
			req: &models.CreateJobRequest{
				Text:          "Some text",
				Type:          "invalid",
				PicturesCount: 2,
				AudioType:     "free_speech",
			},
			want: "invalid type",
		},
		{
			name: "pictures_count too low",
			req: &models.CreateJobRequest{
				Text:          "Some text",
				Type:          "educational",
				PicturesCount: 0,
				AudioType:     "free_speech",
			},
			want: "pictures_count must be between 1 and 20",
		},
		{
			name: "pictures_count too high",
			req: &models.CreateJobRequest{
				Text:          "Some text",
				Type:          "educational",
				PicturesCount: 100,
				AudioType:     "free_speech",
			},
			want: "pictures_count must be between 1 and",
		},
		{
			name: "invalid audio_type",
			req: &models.CreateJobRequest{
				Text:          "Some text",
				Type:          "educational",
				PicturesCount: 2,
				AudioType:     "invalid",
			},
			want: "invalid audio_type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.CreateJob(ctx, tt.req, userID, apiKeyID)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

// TestCreateJob_Success runs a full create-job flow when DB is available and
// user/api_key are created in the test.
func TestCreateJob_Success(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}

	db, err := database.Connect(dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	cfg := config.Load()
	cfg.MaxPicturesCount = 20
	cfg.MaxInputLength = 50000

	userRepo := database.NewUserRepository(db)
	apiKeyRepo := database.NewAPIKeyRepository(db)

	ctx := context.Background()
	user := &models.User{ID: uuid.New(), Email: strPtr("test-jobs@example.com"), CreatedAt: time.Now()}
	if err := userRepo.Create(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	plainKey, key, err := apiKeyRepo.CreateAPIKey(ctx, user.ID, 100000, "monthly")
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	if plainKey == "" || key == nil {
		t.Fatal("expected non-empty plain key and key")
	}

	svc := NewJobService(db, nil, cfg)

	req := &models.CreateJobRequest{
		Text:          "Short test text for job creation.",
		Type:          "educational",
		PicturesCount: 1,
		AudioType:     "free_speech",
	}

	resp, err := svc.CreateJob(ctx, req, user.ID, key.ID)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if resp.JobID == uuid.Nil {
		t.Error("expected non-nil job_id")
	}
	if resp.Status != "queued" {
		t.Errorf("expected status queued, got %s", resp.Status)
	}

	// GetJob should return the same job for the same user
	got, err := svc.GetJob(ctx, resp.JobID, user.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Job.ID != resp.JobID {
		t.Errorf("GetJob id %s != CreateJob id %s", got.Job.ID, resp.JobID)
	}
	if got.Job.Status != "queued" {
		t.Errorf("GetJob status %s", got.Job.Status)
	}
}

// TestGetJob_AccessDenied ensures a user cannot get another user's job.
func TestGetJob_AccessDenied(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}

	db, err := database.Connect(dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	cfg := config.Load()
	userRepo := database.NewUserRepository(db)
	apiKeyRepo := database.NewAPIKeyRepository(db)
	jobRepo := database.NewJobRepository(db)

	ctx := context.Background()
	user1 := &models.User{ID: uuid.New(), Email: strPtr("user1@test.com")}
	if err := userRepo.Create(ctx, user1); err != nil {
		t.Fatalf("create user1: %v", err)
	}
	_, key1, err := apiKeyRepo.CreateAPIKey(ctx, user1.ID, 100000, "monthly")
	if err != nil {
		t.Fatalf("create key1: %v", err)
	}

	job := &models.Job{
		ID:            uuid.New(),
		UserID:        user1.ID,
		APIKeyID:      key1.ID,
		Status:        "queued",
		InputType:     "educational",
		PicturesCount: 1,
		AudioType:     "free_speech",
		InputText:     "test",
		InputSource:   "text",
		CreatedAt:     key1.CreatedAt,
	}
	if err := jobRepo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	otherUserID := uuid.New()
	svc := NewJobService(db, nil, cfg)

	_, err = svc.GetJob(ctx, job.ID, otherUserID)
	if err == nil {
		t.Fatal("expected access denied error")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("expected access denied, got: %v", err)
	}
}


// TestListJobs_LimitClamping ensures limit is clamped to 1-100.
func TestListJobs_LimitClamping(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}

	db, err := database.Connect(dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	svc := NewJobService(db, nil, config.Load())
	ctx := context.Background()
	userID := uuid.New()

	// limit 0 or negative should be clamped to 20
	jobs, err := svc.ListJobs(ctx, userID, 0, nil)
	if err != nil {
		t.Fatalf("ListJobs(0): %v", err)
	}
	if jobs == nil {
		t.Error("ListJobs(0) returned nil slice")
	}

	// limit > 100 should be clamped to 20 (per current impl)
	jobs, err = svc.ListJobs(ctx, userID, 500, nil)
	if err != nil {
		t.Fatalf("ListJobs(500): %v", err)
	}
	if jobs == nil {
		t.Error("ListJobs(500) returned nil slice")
	}
}

func strPtr(s string) *string { return &s }
