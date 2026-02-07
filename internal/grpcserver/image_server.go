package grpcserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/agents"
	"github.com/snappy-loop/stories/internal/storage"
	imagev1 "github.com/snappy-loop/stories/gen/image/v1"
)

// ImageServer implements image.v1.ImageServiceServer.
type ImageServer struct {
	imagev1.UnimplementedImageServiceServer
	agent   agents.ImageAgent
	storage *storage.Client
}

// NewImageServer returns a new ImageServer. storageClient may be nil; then image is returned inline (may hit gRPC size limits).
func NewImageServer(agent agents.ImageAgent, storageClient *storage.Client) *ImageServer {
	return &ImageServer{agent: agent, storage: storageClient}
}

// GenerateImagePrompt delegates to the image agent.
func (s *ImageServer) GenerateImagePrompt(ctx context.Context, req *imagev1.GenerateImagePromptRequest) (*imagev1.GenerateImagePromptResponse, error) {
	prompt, err := s.agent.GenerateImagePrompt(ctx, req.GetText(), req.GetInputType())
	if err != nil {
		return nil, err
	}
	return &imagev1.GenerateImagePromptResponse{Prompt: prompt}, nil
}

// GenerateImage delegates to the image agent. If storage is configured, uploads to S3 and returns URL to avoid gRPC message size limits.
func (s *ImageServer) GenerateImage(ctx context.Context, req *imagev1.GenerateImageRequest) (*imagev1.GenerateImageResponse, error) {
	img, err := s.agent.GenerateImage(ctx, req.GetPrompt())
	if err != nil {
		return nil, err
	}
	if img == nil {
		return nil, fmt.Errorf("image agent returned nil result")
	}
	var data []byte
	if img.Data != nil {
		data, err = io.ReadAll(img.Data)
		if err != nil {
			return nil, err
		}
	}
	mimeType := img.MimeType
	if mimeType == "" {
		mimeType = "image/png"
	}
	resp := &imagev1.GenerateImageResponse{
		Size:       img.Size,
		Resolution: img.Resolution,
		MimeType:   mimeType,
		Model:      img.Model,
	}
	if s.storage != nil && len(data) > 0 {
		userID := userIDFromContext(ctx)
		key := "agents/" + userID + "/image/" + uuid.New().String() + imageExtensionForMime(mimeType)
		if err := s.storage.Upload(ctx, key, bytes.NewReader(data), mimeType, int64(len(data))); err != nil {
			return nil, fmt.Errorf("upload image to S3: %w", err)
		}
		if url := s.storage.PublicURL(key); url != "" {
			resp.Url = url
		} else {
			url, err := s.storage.GeneratePresignedURL(key, 24*time.Hour)
			if err != nil {
				return nil, fmt.Errorf("presign image URL: %w", err)
			}
			resp.Url = url
		}
	} else {
		resp.Data = data
	}
	return resp, nil
}

func imageExtensionForMime(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".bin"
	}
}
