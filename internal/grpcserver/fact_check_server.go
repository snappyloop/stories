package grpcserver

import (
	"context"

	"github.com/snappy-loop/stories/internal/agents"
	factcheckv1 "github.com/snappy-loop/stories/gen/factcheck/v1"
)

// FactCheckServer implements factcheck.v1.FactCheckServiceServer.
type FactCheckServer struct {
	factcheckv1.UnimplementedFactCheckServiceServer
	agent agents.FactCheckAgent
}

// NewFactCheckServer returns a new FactCheckServer.
func NewFactCheckServer(agent agents.FactCheckAgent) *FactCheckServer {
	return &FactCheckServer{agent: agent}
}

// FactCheckSegment delegates to the fact-check agent.
func (s *FactCheckServer) FactCheckSegment(ctx context.Context, req *factcheckv1.FactCheckSegmentRequest) (*factcheckv1.FactCheckSegmentResponse, error) {
	text, err := s.agent.FactCheckSegment(ctx, req.GetText())
	if err != nil {
		return nil, err
	}
	return &factcheckv1.FactCheckSegmentResponse{FactCheckText: text}, nil
}
