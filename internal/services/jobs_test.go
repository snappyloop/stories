package services

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/config"
	"github.com/snappy-loop/stories/internal/models"
)

// noopJobPublisher is a mock that does nothing (no real Kafka).
type noopJobPublisher struct{}

func (noopJobPublisher) PublishJob(context.Context, uuid.UUID, string) error { return nil }

// fakeJobRepo is an in-memory job repository for tests.
type fakeJobRepo struct {
	mu     sync.Mutex
	jobs   map[uuid.UUID]*models.Job
	byUser map[uuid.UUID][]*models.Job
}

func newFakeJobRepo() *fakeJobRepo {
	return &fakeJobRepo{
		jobs:   make(map[uuid.UUID]*models.Job),
		byUser: make(map[uuid.UUID][]*models.Job),
	}
}

func (f *fakeJobRepo) Create(ctx context.Context, job *models.Job) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	clone := *job
	f.jobs[job.ID] = &clone
	f.byUser[job.UserID] = append(f.byUser[job.UserID], &clone)
	return nil
}

func (f *fakeJobRepo) GetByID(ctx context.Context, jobID uuid.UUID) (*models.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[jobID]
	if !ok {
		return nil, nil
	}
	clone := *j
	return &clone, nil
}

var errNotFound = func() error { e := "job not found"; return &errT{msg: e} }()

type errT struct{ msg string }

func (e *errT) Error() string { return e.msg }

func (f *fakeJobRepo) ListByUser(ctx context.Context, userID uuid.UUID, limit int, cursor *time.Time) ([]*models.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.byUser[userID]
	if list == nil {
		return []*models.Job{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]*models.Job, len(list))
	for i, j := range list {
		clone := *j
		out[i] = &clone
	}
	return out, nil
}

// fakeSegmentRepo returns empty segments.
type fakeSegmentRepo struct{}

func (fakeSegmentRepo) ListByJob(context.Context, uuid.UUID) ([]*models.Segment, error) {
	return nil, nil
}

// fakeAssetRepo returns empty list; GetByID returns not found.
type fakeAssetRepo struct{}

func (fakeAssetRepo) ListByJob(context.Context, uuid.UUID) ([]*models.Asset, error) {
	return nil, nil
}

func (fakeAssetRepo) GetByID(context.Context, uuid.UUID) (*models.Asset, error) {
	return nil, errNotFound
}

// fakeJobFileRepo does nothing for Create; ListByJob returns empty.
type fakeJobFileRepo struct{}

func (fakeJobFileRepo) Create(context.Context, *models.JobFile) error { return nil }

func (fakeJobFileRepo) ListByJob(context.Context, uuid.UUID) ([]*models.JobFile, error) {
	return nil, nil
}

// fakeFileRepo returns not found unless we need a specific file for a test.
type fakeFileRepo struct {
	byID map[uuid.UUID]*models.File
}

func newFakeFileRepo() *fakeFileRepo { return &fakeFileRepo{byID: make(map[uuid.UUID]*models.File)} }

func (f *fakeFileRepo) GetByID(ctx context.Context, fileID uuid.UUID) (*models.File, error) {
	if f.byID != nil {
		if file, ok := f.byID[fileID]; ok {
			return file, nil
		}
	}
	return nil, nil
}

func (f *fakeFileRepo) GetByIDAndUser(ctx context.Context, fileID, userID uuid.UUID) (*models.File, error) {
	file, _ := f.GetByID(ctx, fileID)
	if file == nil || file.UserID != userID {
		return nil, errNotFound
	}
	return file, nil
}

// fakeAPIKeyRepo returns a pre-set key for GetByID; UpdateUsage no-op; CreateAPIKey not used in these tests.
type fakeAPIKeyRepo struct {
	key *models.APIKey
}

func newFakeAPIKeyRepo(key *models.APIKey) *fakeAPIKeyRepo {
	return &fakeAPIKeyRepo{key: key}
}

func (f *fakeAPIKeyRepo) GetByID(ctx context.Context, keyID uuid.UUID) (*models.APIKey, error) {
	if f.key != nil && f.key.ID == keyID {
		return f.key, nil
	}
	return nil, nil
}

func (f *fakeAPIKeyRepo) UpdateUsage(ctx context.Context, keyID uuid.UUID, chars int64, periodStartedAt time.Time) error {
	return nil
}

func (f *fakeAPIKeyRepo) CreateAPIKey(ctx context.Context, userID uuid.UUID, quotaChars int64, quotaPeriod string) (string, *models.APIKey, error) {
	key := &models.APIKey{
		ID:                uuid.New(),
		UserID:            userID,
		QuotaChars:        quotaChars,
		UsedCharsInPeriod: 0,
		PeriodStartedAt:   time.Now(),
		CreatedAt:         time.Now(),
	}
	f.key = key
	return "sk_test", key, nil
}

func TestCreateJob_ValidationErrors(t *testing.T) {
	cfg := &config.Config{
		MaxFilesPerJob:     10,
		MaxInputLength:     50000,
		MaxSegmentsCount:   5,
		CharsPerFile:       1000,
		DefaultQuotaChars:  100000,
		DefaultQuotaPeriod: "monthly",
	}

	// Use fakes that are never called (validation fails first)
	jobRepo := newFakeJobRepo()
	apiKey := &models.APIKey{ID: uuid.New(), UserID: uuid.New(), QuotaChars: 100000, UsedCharsInPeriod: 0, PeriodStartedAt: time.Now(), QuotaPeriod: "monthly"}
	svc := NewJobService(
		jobRepo,
		fakeSegmentRepo{},
		fakeAssetRepo{},
		fakeJobFileRepo{},
		newFakeFileRepo(),
		newFakeAPIKeyRepo(apiKey),
		noopJobPublisher{},
		cfg,
	)
	ctx := context.Background()
	userID := uuid.New()
	apiKeyID := apiKey.ID

	tests := []struct {
		name string
		req  *models.CreateJobRequest
		want string
	}{
		{"empty text and no file_ids", &models.CreateJobRequest{Text: "", Type: "educational", SegmentsCount: 2, AudioType: "free_speech"}, "either text or file_ids is required"},
		{"invalid type", &models.CreateJobRequest{Text: "Some text", Type: "invalid", SegmentsCount: 2, AudioType: "free_speech"}, "invalid type"},
		{"segments_count too low", &models.CreateJobRequest{Text: "Some text", Type: "educational", SegmentsCount: 0, AudioType: "free_speech"}, "segments_count must be between 1 and 20"},
		{"segments_count too high", &models.CreateJobRequest{Text: "Some text", Type: "educational", SegmentsCount: 100, AudioType: "free_speech"}, "segments_count must be between 1 and"},
		{"invalid audio_type", &models.CreateJobRequest{Text: "Some text", Type: "educational", SegmentsCount: 2, AudioType: "invalid"}, "invalid audio_type"},
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

func TestCreateJob_Success(t *testing.T) {
	cfg := &config.Config{
		MaxFilesPerJob:     10,
		MaxInputLength:     50000,
		MaxSegmentsCount:   20,
		CharsPerFile:       1000,
		DefaultQuotaChars:  100000,
		DefaultQuotaPeriod: "monthly",
	}

	userID := uuid.New()
	apiKey := &models.APIKey{
		ID:                uuid.New(),
		UserID:            userID,
		QuotaChars:        100000,
		UsedCharsInPeriod: 0,
		PeriodStartedAt:   time.Now(),
		QuotaPeriod:       "monthly",
		CreatedAt:         time.Now(),
	}

	jobRepo := newFakeJobRepo()
	// GetByID must return job when found - fakeJobRepo returns nil for missing, but we need "job not found" error for GetByID. So we need a wrapper that returns an error when job is not in map. Actually the real repo returns fmt.Errorf("job not found"). So for GetJob to work we need GetByID to return the job. Our fakeJobRepo.GetByID returns (nil, nil) when not found. But the service does: job, err := s.jobRepo.GetByID(...); if err != nil { return ..., err }; if job == nil { we don't handle that }. So the service expects either (job, nil) or (nil, err). So we need our fake to return an error when not found. Let me check - in database/repositories GetByID returns (nil, fmt.Errorf("job not found")) on sql.ErrNoRows. So we need our fake to return (nil, someErr) when not found. I'll add a helper that returns error when job is nil.
	svc := NewJobService(
		&fakeJobRepoGetByIDErr{fakeJobRepo: jobRepo},
		fakeSegmentRepo{},
		fakeAssetRepo{},
		fakeJobFileRepo{},
		newFakeFileRepo(),
		newFakeAPIKeyRepo(apiKey),
		noopJobPublisher{},
		cfg,
	)
	ctx := context.Background()

	req := &models.CreateJobRequest{
		Text:          "Short test text for job creation.",
		Type:          "educational",
		SegmentsCount: 1,
		AudioType:     "free_speech",
	}

	resp, err := svc.CreateJob(ctx, req, userID, apiKey.ID)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if resp.JobID == uuid.Nil {
		t.Error("expected non-nil job_id")
	}
	if resp.Status != "queued" {
		t.Errorf("expected status queued, got %s", resp.Status)
	}

	got, err := svc.GetJob(ctx, resp.JobID, userID)
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

// fakeJobRepoGetByIDErr wraps fakeJobRepo and returns error when job not found (like real DB).
type fakeJobRepoGetByIDErr struct {
	*fakeJobRepo
}

func (f *fakeJobRepoGetByIDErr) GetByID(ctx context.Context, jobID uuid.UUID) (*models.Job, error) {
	j, _ := f.fakeJobRepo.GetByID(ctx, jobID)
	if j == nil {
		return nil, errNotFound
	}
	return j, nil
}

func TestGetJob_AccessDenied(t *testing.T) {
	user1ID := uuid.New()
	key1ID := uuid.New()
	jobID := uuid.New()

	jobRepo := newFakeJobRepo()
	// Pre-insert a job owned by user1
	jobRepo.Create(context.Background(), &models.Job{
		ID: jobID, UserID: user1ID, APIKeyID: key1ID, Status: "queued",
		InputType: "educational", SegmentsCount: 1, AudioType: "free_speech",
		InputText: "test", InputSource: "text", CreatedAt: time.Now(),
	})

	svc := NewJobService(
		&fakeJobRepoGetByIDErr{fakeJobRepo: jobRepo},
		fakeSegmentRepo{},
		fakeAssetRepo{},
		fakeJobFileRepo{},
		newFakeFileRepo(),
		newFakeAPIKeyRepo(nil),
		noopJobPublisher{},
		config.Load(),
	)
	ctx := context.Background()
	otherUserID := uuid.New()

	_, err := svc.GetJob(ctx, jobID, otherUserID)
	if err == nil {
		t.Fatal("expected access denied error")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("expected access denied, got: %v", err)
	}
}

func TestListJobs_LimitClamping(t *testing.T) {
	jobRepo := newFakeJobRepo()
	svc := NewJobService(
		jobRepo,
		fakeSegmentRepo{},
		fakeAssetRepo{},
		fakeJobFileRepo{},
		newFakeFileRepo(),
		newFakeAPIKeyRepo(nil),
		noopJobPublisher{},
		config.Load(),
	)
	ctx := context.Background()
	userID := uuid.New()

	jobs, err := svc.ListJobs(ctx, userID, 0, nil)
	if err != nil {
		t.Fatalf("ListJobs(0): %v", err)
	}
	if jobs == nil {
		t.Error("ListJobs(0) returned nil slice")
	}

	jobs, err = svc.ListJobs(ctx, userID, 500, nil)
	if err != nil {
		t.Fatalf("ListJobs(500): %v", err)
	}
	if jobs == nil {
		t.Error("ListJobs(500) returned nil slice")
	}
}
