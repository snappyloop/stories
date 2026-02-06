package grpcserver

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/agents"
	"github.com/snappy-loop/stories/internal/storage"
	audiov1 "github.com/snappy-loop/stories/gen/audio/v1"
)

// AudioServer implements audio.v1.AudioServiceServer.
type AudioServer struct {
	audiov1.UnimplementedAudioServiceServer
	agent   agents.AudioAgent
	storage *storage.Client
}

// NewAudioServer returns a new AudioServer. storageClient may be nil; then audio is returned inline (may hit gRPC size limits).
func NewAudioServer(agent agents.AudioAgent, storageClient *storage.Client) *AudioServer {
	return &AudioServer{agent: agent, storage: storageClient}
}

// GenerateNarration delegates to the audio agent.
func (s *AudioServer) GenerateNarration(ctx context.Context, req *audiov1.GenerateNarrationRequest) (*audiov1.GenerateNarrationResponse, error) {
	script, err := s.agent.GenerateNarration(ctx, req.GetText(), req.GetAudioType(), req.GetInputType())
	if err != nil {
		return nil, err
	}
	return &audiov1.GenerateNarrationResponse{Script: script}, nil
}

// GenerateAudio delegates to the audio agent. If storage is configured, uploads to S3 and returns URL to avoid gRPC message size limits.
func (s *AudioServer) GenerateAudio(ctx context.Context, req *audiov1.GenerateAudioRequest) (*audiov1.GenerateAudioResponse, error) {
	audio, err := s.agent.GenerateAudio(ctx, req.GetScript(), req.GetAudioType())
	if err != nil {
		return nil, err
	}
	data, err := agents.AudioData(audio)
	if err != nil {
		return nil, err
	}
	mimeType := audio.MimeType
	if mimeType == "" {
		mimeType = "audio/wav"
	}
	resp := &audiov1.GenerateAudioResponse{
		Size:     audio.Size,
		Duration: audio.Duration,
		MimeType: mimeType,
		Model:    audio.Model,
	}
	if s.storage != nil && len(data) > 0 {
		userID := userIDFromContext(ctx)
		key := "agents/" + userID + "/audio/" + uuid.New().String() + extensionForMime(mimeType)
		if err := s.storage.Upload(ctx, key, bytes.NewReader(data), mimeType, int64(len(data))); err != nil {
			return nil, fmt.Errorf("upload audio to S3: %w", err)
		}
		if url := s.storage.PublicURL(key); url != "" {
			resp.Url = url
		} else {
			url, err := s.storage.GeneratePresignedURL(key, 24*time.Hour)
			if err != nil {
				return nil, fmt.Errorf("presign audio URL: %w", err)
			}
			resp.Url = url
		}
	} else {
		resp.Data = data
	}
	return resp, nil
}

func extensionForMime(mime string) string {
	switch mime {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav":
		return ".wav"
	case "audio/webm":
		return ".webm"
	default:
		return ".bin"
	}
}
