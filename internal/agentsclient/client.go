package agentsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	audiov1 "github.com/snappy-loop/stories/gen/audio/v1"
	imagev1 "github.com/snappy-loop/stories/gen/image/v1"
	segmentationv1 "github.com/snappy-loop/stories/gen/segmentation/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const apiKeyRedacted = "***"

// Client calls the agents service via gRPC or MCP.
type Client struct {
	grpcConn *grpc.ClientConn
	segCli   segmentationv1.SegmentationServiceClient
	audioCli audiov1.AudioServiceClient
	imageCli imagev1.ImageServiceClient
	mcpURL   string
	httpCli  *http.Client
}

// NewClient dials the gRPC server and prepares MCP URL. Call Close when done.
func NewClient(grpcURL, mcpURL string) (*Client, error) {
	conn, err := grpc.NewClient(grpcURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("grpc dial: %w", err)
	}
	return &Client{
		grpcConn: conn,
		segCli:   segmentationv1.NewSegmentationServiceClient(conn),
		audioCli: audiov1.NewAudioServiceClient(conn),
		imageCli: imagev1.NewImageServiceClient(conn),
		mcpURL:   mcpURL,
		httpCli:  &http.Client{Timeout: 120 * time.Second},
	}, nil
}

// Close closes the gRPC connection.
func (c *Client) Close() error {
	return c.grpcConn.Close()
}

// RedactRequest returns a copy of the request map with api_key set to "***".
func RedactRequest(req map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(req))
	for k, v := range req {
		if k == "api_key" {
			out[k] = apiKeyRedacted
		} else {
			out[k] = v
		}
	}
	return out
}

// Call invokes the agents service. transport is "grpc" or "mcp". action is one of:
// segment_text, generate_narration, generate_audio, generate_image_prompt, generate_image.
// params must contain the action-specific fields plus "api_key".
// Returns (redacted request, response, error). Response is a map or struct for JSON encoding.
func (c *Client) Call(ctx context.Context, apiKey, transport, action string, params map[string]interface{}) (requestRedacted map[string]interface{}, response interface{}, err error) {
	requestRedacted = RedactRequest(params)

	switch transport {
	case "grpc":
		response, err = c.callGRPC(ctx, apiKey, action, params)
	case "mcp":
		response, err = c.callMCP(ctx, apiKey, action, params)
	default:
		return requestRedacted, nil, fmt.Errorf("unknown transport: %s", transport)
	}
	return requestRedacted, response, err
}

func (c *Client) ctxWithAuth(ctx context.Context, apiKey string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+apiKey)
}

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getInt(m map[string]interface{}, key string) int32 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int32(n)
		case int:
			return int32(n)
		case int32:
			return n
		}
	}
	return 0
}

func (c *Client) callGRPC(ctx context.Context, apiKey, action string, params map[string]interface{}) (interface{}, error) {
	ctx = c.ctxWithAuth(ctx, apiKey)
	switch action {
	case "segment_text":
		req := &segmentationv1.SegmentTextRequest{
			Text:          getStr(params, "text"),
			SegmentsCount: getInt(params, "segments_count"),
			InputType:     getStr(params, "input_type"),
		}
		if req.SegmentsCount < 1 {
			req.SegmentsCount = 1
		}
		resp, err := c.segCli.SegmentText(ctx, req)
		if err != nil {
			return nil, err
		}
		return segmentResponseToMap(resp), nil
	case "generate_narration":
		req := &audiov1.GenerateNarrationRequest{
			Text:      getStr(params, "text"),
			AudioType: getStr(params, "audio_type"),
			InputType: getStr(params, "input_type"),
		}
		resp, err := c.audioCli.GenerateNarration(ctx, req)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"script": resp.GetScript()}, nil
	case "generate_audio":
		req := &audiov1.GenerateAudioRequest{
			Script:    getStr(params, "script"),
			AudioType: getStr(params, "audio_type"),
		}
		resp, err := c.audioCli.GenerateAudio(ctx, req)
		if err != nil {
			return nil, err
		}
		ct := resp.GetMimeType()
		if ct == "" {
			ct = "audio/wav"
		}
		out := map[string]interface{}{
			"size":         resp.GetSize(),
			"duration":     resp.GetDuration(),
			"mime_type":    ct,
			"content_type": ct,
			"model":        resp.GetModel(),
		}
		if u := resp.GetUrl(); u != "" {
			out["url"] = u
		}
		return out, nil
	case "generate_image_prompt":
		req := &imagev1.GenerateImagePromptRequest{
			Text:      getStr(params, "text"),
			InputType: getStr(params, "input_type"),
		}
		resp, err := c.imageCli.GenerateImagePrompt(ctx, req)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"prompt": resp.GetPrompt()}, nil
	case "generate_image":
		req := &imagev1.GenerateImageRequest{
			Prompt: getStr(params, "prompt"),
		}
		resp, err := c.imageCli.GenerateImage(ctx, req)
		if err != nil {
			return nil, err
		}
		ct := resp.GetMimeType()
		if ct == "" {
			ct = "image/png"
		}
		out := map[string]interface{}{
			"size":         resp.GetSize(),
			"resolution":  resp.GetResolution(),
			"mime_type":   ct,
			"content_type": ct,
			"model":       resp.GetModel(),
		}
		if u := resp.GetUrl(); u != "" {
			out["url"] = u
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

func segmentResponseToMap(resp *segmentationv1.SegmentTextResponse) map[string]interface{} {
	segs := make([]map[string]interface{}, len(resp.GetSegments()))
	for i, s := range resp.GetSegments() {
		segs[i] = map[string]interface{}{
			"start_char": s.GetStartChar(),
			"end_char":   s.GetEndChar(),
			"title":      s.GetTitle(),
			"text":       s.GetText(),
		}
	}
	return map[string]interface{}{"segments": segs}
}

// MCP tools/call request and response
type mcpCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

func (c *Client) callMCP(ctx context.Context, apiKey, action string, params map[string]interface{}) (interface{}, error) {
	mcpAction := action
	args := make(map[string]interface{})
	switch action {
	case "segment_text":
		args["text"] = getStr(params, "text")
		sc := getInt(params, "segments_count")
		if sc < 1 {
			sc = 1
		}
		args["segments_count"] = sc
		it := getStr(params, "input_type")
		if it == "" {
			it = "educational"
		}
		args["input_type"] = it
	case "generate_narration":
		mcpAction = "generate_narration"
		args["text"] = getStr(params, "text")
		at := getStr(params, "audio_type")
		if at == "" {
			at = "free_speech"
		}
		args["audio_type"] = at
		it := getStr(params, "input_type")
		if it == "" {
			it = "educational"
		}
		args["input_type"] = it
	case "generate_audio":
		mcpAction = "generate_audio"
		args["script"] = getStr(params, "script")
		at := getStr(params, "audio_type")
		if at == "" {
			at = "free_speech"
		}
		args["audio_type"] = at
	case "generate_image_prompt", "generate_image":
		// MCP server only has segment_text, generate_narration, generate_audio
		return nil, fmt.Errorf("MCP does not support action %s", action)
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  mcpCallParams{Name: mcpAction, Arguments: args},
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.mcpURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var mcpResp struct {
		JSONRPC string      `json:"jsonrpc"`
		ID      interface{} `json:"id"`
		Result  interface{} `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&mcpResp); err != nil {
		return nil, fmt.Errorf("decode MCP response: %w", err)
	}
	if mcpResp.Error != nil {
		return nil, fmt.Errorf("MCP error: %s", mcpResp.Error.Message)
	}
	return mcpResp.Result, nil
}
