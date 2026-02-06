package grpcserver

import (
	"context"

	"github.com/snappy-loop/stories/internal/agents"
	segmentationv1 "github.com/snappy-loop/stories/gen/segmentation/v1"
)

// SegmentationServer implements segmentation.v1.SegmentationServiceServer.
type SegmentationServer struct {
	segmentationv1.UnimplementedSegmentationServiceServer
	agent agents.SegmentationAgent
}

// NewSegmentationServer returns a new SegmentationServer.
func NewSegmentationServer(agent agents.SegmentationAgent) *SegmentationServer {
	return &SegmentationServer{agent: agent}
}

// SegmentText delegates to the segmentation agent and maps the response to proto.
func (s *SegmentationServer) SegmentText(ctx context.Context, req *segmentationv1.SegmentTextRequest) (*segmentationv1.SegmentTextResponse, error) {
	segments, err := s.agent.SegmentText(ctx, req.GetText(), int(req.GetSegmentsCount()), req.GetInputType())
	if err != nil {
		return nil, err
	}
	out := make([]*segmentationv1.Segment, len(segments))
	for i, seg := range segments {
		title := ""
		if seg.Title != nil {
			title = *seg.Title
		}
		out[i] = &segmentationv1.Segment{
			StartChar: int32(seg.StartChar),
			EndChar:   int32(seg.EndChar),
			Title:     title,
			Text:      seg.Text,
		}
	}
	return &segmentationv1.SegmentTextResponse{Segments: out}, nil
}
