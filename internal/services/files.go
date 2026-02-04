package services

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/config"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/models"
	"github.com/snappy-loop/stories/internal/storage"
)

// Allowed MIME types for file uploads
var allowedMimeTypes = map[string]bool{
	"image/jpeg":       true,
	"image/png":        true,
	"image/gif":        true,
	"image/webp":       true,
	"application/pdf":  true,
}

// FileService handles file upload and management
type FileService struct {
	fileRepo   *database.FileRepository
	storage    *storage.Client
	bucket     string
	config     *config.Config
}

// NewFileService creates a new FileService
func NewFileService(
	fileRepo *database.FileRepository,
	storage *storage.Client,
	bucket string,
	cfg *config.Config,
) *FileService {
	return &FileService{
		fileRepo: fileRepo,
		storage:  storage,
		bucket:   bucket,
		config:   cfg,
	}
}

// UploadFile uploads a file to S3 and creates a file record
func (s *FileService) UploadFile(ctx context.Context, userID uuid.UUID, filename, mimeType string, data io.Reader, sizeBytes int64) (*models.UploadFileResponse, error) {
	if sizeBytes > s.config.MaxFileSize {
		return nil, fmt.Errorf("file size exceeds maximum of %d bytes", s.config.MaxFileSize)
	}
	if !allowedMimeTypes[mimeType] {
		return nil, fmt.Errorf("unsupported mime type: %s", mimeType)
	}

	// Sanitize filename
	filename = filepath.Base(filename)
	if filename == "" || filename == "." {
		filename = "upload"
	}

	fileID := uuid.New()
	expiresAt := time.Now().Add(time.Duration(s.config.FileExpirationHrs) * time.Hour)
	s3Key := fmt.Sprintf("files/%s/%s", userID.String(), fileID.String()+getExtension(filename, mimeType))

	if err := s.storage.Upload(ctx, s3Key, data, mimeType); err != nil {
		return nil, fmt.Errorf("failed to upload to storage: %w", err)
	}

	file := &models.File{
		ID:        fileID,
		UserID:    userID,
		Filename:  filename,
		MimeType:  mimeType,
		SizeBytes: sizeBytes,
		S3Bucket:  s.bucket,
		S3Key:     s3Key,
		Status:    "ready",
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}

	if err := s.fileRepo.Create(ctx, file); err != nil {
		_ = s.storage.Delete(ctx, s3Key)
		return nil, fmt.Errorf("failed to create file record: %w", err)
	}

	log.Info().
		Str("file_id", fileID.String()).
		Str("user_id", userID.String()).
		Str("filename", filename).
		Int64("size", sizeBytes).
		Msg("File uploaded")

	return &models.UploadFileResponse{
		FileID:    file.ID,
		Filename:  file.Filename,
		MimeType:  file.MimeType,
		SizeBytes: file.SizeBytes,
		ExpiresAt: file.ExpiresAt,
	}, nil
}

// ListFiles returns files for a user, optionally filtered by status
func (s *FileService) ListFiles(ctx context.Context, userID uuid.UUID, status string) ([]*models.File, error) {
	return s.fileRepo.ListByUser(ctx, userID, status)
}

// DeleteFile deletes a file (S3 object and DB record) if owned by user
func (s *FileService) DeleteFile(ctx context.Context, fileID, userID uuid.UUID) error {
	file, err := s.fileRepo.GetByIDAndUser(ctx, fileID, userID)
	if err != nil {
		return err
	}
	if err := s.storage.Delete(ctx, file.S3Key); err != nil {
		log.Warn().Err(err).Str("key", file.S3Key).Msg("Failed to delete file from S3")
	}
	return s.fileRepo.DeleteByIDAndUser(ctx, fileID, userID)
}

// GetFileByIDAndUser returns a file if owned by user (for handlers)
func (s *FileService) GetFileByIDAndUser(ctx context.Context, fileID, userID uuid.UUID) (*models.File, error) {
	return s.fileRepo.GetByIDAndUser(ctx, fileID, userID)
}

// GetFileByID returns a file by ID (for worker/processor)
func (s *FileService) GetFileByID(ctx context.Context, fileID uuid.UUID) (*models.File, error) {
	return s.fileRepo.GetByID(ctx, fileID)
}

func getExtension(filename, mimeType string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext != "" {
		return ext
	}
	switch mimeType {
	case "application/pdf":
		return ".pdf"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}
