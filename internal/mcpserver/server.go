package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"

	"github.com/snappy-loop/stories/internal/agents"
)

// JSON-RPC 2.0 request
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// JSON-RPC 2.0 response
type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP tools/list result
type toolsListResult struct {
	Tools      []mcpTool `json:"tools"`
	NextCursor *string   `json:"nextCursor,omitempty"`
}

type mcpTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]schemaProp  `json:"properties"`
	Required   []string               `json:"required,omitempty"`
}

type schemaProp struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// MCP tools/call result
type toolsCallResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError"`
}

type contentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// Server implements MCP JSON-RPC 2.0 over HTTP (tools/list and tools/call).
type Server struct {
	segmentAgent   agents.SegmentationAgent
	imageAgent     agents.ImageAgent
	factCheckAgent agents.FactCheckAgent
}

// NewServer returns a new MCP server that uses the given agents.
func NewServer(segmentAgent agents.SegmentationAgent, imageAgent agents.ImageAgent, factCheckAgent agents.FactCheckAgent) *Server {
	return &Server{
		segmentAgent:   segmentAgent,
		imageAgent:     imageAgent,
		factCheckAgent: factCheckAgent,
	}
}

// Handler returns the HTTP handler for JSON-RPC requests.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveJSONRPC)
}

func (s *Server) serveJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, req.ID, -32700, "Parse error")
		return
	}
	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, -32600, "Invalid Request")
		return
	}

	var result interface{}
	var rpcErr *rpcError
	switch req.Method {
	case "tools/list":
		result, rpcErr = s.handleToolsList()
	case "tools/call":
		result, rpcErr = s.handleToolsCall(r.Context(), req.Params)
	default:
		writeRPCError(w, req.ID, -32601, "Method not found")
		return
	}

	if rpcErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
}

func (s *Server) handleToolsList() (interface{}, *rpcError) {
	return &toolsListResult{
		Tools: []mcpTool{
			{
				Name:        "segment_text",
				Description: "Segment text into logical parts with titles",
				InputSchema: inputSchema{
					Type: "object",
					Properties: map[string]schemaProp{
						"text":           {Type: "string", Description: "Full text to segment"},
						"segments_count": {Type: "number", Description: "Target number of segments"},
						"input_type":     {Type: "string", Description: "educational, financial, or fictional"},
					},
					Required: []string{"text", "segments_count", "input_type"},
				},
			},
			{
				Name:        "generate_image_prompt",
				Description: "Generate an image generation prompt from text",
				InputSchema: inputSchema{
					Type: "object",
					Properties: map[string]schemaProp{
						"text":       {Type: "string", Description: "Text to describe as image"},
						"input_type": {Type: "string", Description: "educational, financial, or fictional"},
					},
					Required: []string{"text", "input_type"},
				},
			},
			{
				Name:        "generate_image",
				Description: "Generate an image from a prompt",
				InputSchema: inputSchema{
					Type: "object",
					Properties: map[string]schemaProp{
						"prompt": {Type: "string", Description: "Image generation prompt"},
					},
					Required: []string{"prompt"},
				},
			},
			{
				Name:        "fact_check",
				Description: "Fact-check segment text using search grounding; returns empty string if no issues",
				InputSchema: inputSchema{
					Type: "object",
					Properties: map[string]schemaProp{
						"text": {Type: "string", Description: "Segment text to fact-check"},
					},
					Required: []string{"text"},
				},
			},
		},
	}, nil
}

type toolsCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

func (s *Server) handleToolsCall(ctx context.Context, paramsRaw json.RawMessage) (interface{}, *rpcError) {
	var params toolsCallParams
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		return nil, &rpcError{Code: -32602, Message: "Invalid params"}
	}
	switch params.Name {
	case "segment_text":
		return s.callSegmentText(ctx, params.Arguments)
	case "generate_image_prompt":
		return s.callGenerateImagePrompt(ctx, params.Arguments)
	case "generate_image":
		return s.callGenerateImage(ctx, params.Arguments)
	case "fact_check":
		return s.callFactCheck(ctx, params.Arguments)
	default:
		return nil, &rpcError{Code: -32602, Message: "Unknown tool: " + params.Name}
	}
}

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getNum(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

func (s *Server) callSegmentText(ctx context.Context, args map[string]interface{}) (interface{}, *rpcError) {
	text := getStr(args, "text")
	segmentsCount := getNum(args, "segments_count")
	if segmentsCount < 1 {
		segmentsCount = 1
	}
	inputType := getStr(args, "input_type")
	if inputType == "" {
		inputType = "educational"
	}
	segments, err := s.segmentAgent.SegmentText(ctx, text, segmentsCount, inputType)
	if err != nil {
		return &toolsCallResult{
			Content: []contentItem{{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}
	// Return segments as JSON text content
	type segOut struct {
		StartChar int    `json:"start_char"`
		EndChar   int    `json:"end_char"`
		Title     string `json:"title"`
		Text      string `json:"text"`
	}
	out := make([]segOut, len(segments))
	for i, seg := range segments {
		title := ""
		if seg.Title != nil {
			title = *seg.Title
		}
		out[i] = segOut{StartChar: seg.StartChar, EndChar: seg.EndChar, Title: title, Text: seg.Text}
	}
	raw, _ := json.Marshal(out)
	return &toolsCallResult{
		Content: []contentItem{{Type: "text", Text: string(raw)}},
		IsError: false,
	}, nil
}

func (s *Server) callGenerateImagePrompt(ctx context.Context, args map[string]interface{}) (interface{}, *rpcError) {
	text := getStr(args, "text")
	inputType := getStr(args, "input_type")
	if inputType == "" {
		inputType = "educational"
	}
	prompt, err := s.imageAgent.GenerateImagePrompt(ctx, text, inputType)
	if err != nil {
		return &toolsCallResult{
			Content: []contentItem{{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}
	return &toolsCallResult{
		Content: []contentItem{{Type: "text", Text: prompt}},
		IsError: false,
	}, nil
}

func (s *Server) callGenerateImage(ctx context.Context, args map[string]interface{}) (interface{}, *rpcError) {
	prompt := getStr(args, "prompt")
	img, err := s.imageAgent.GenerateImage(ctx, prompt)
	if err != nil {
		return &toolsCallResult{
			Content: []contentItem{{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}
	if img == nil || img.Data == nil {
		return &toolsCallResult{
			Content: []contentItem{{Type: "text", Text: "image agent returned nil result"}},
			IsError: true,
		}, nil
	}
	data, err := io.ReadAll(img.Data)
	if err != nil {
		return &toolsCallResult{
			Content: []contentItem{{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}
	mimeType := img.MimeType
	if mimeType == "" {
		mimeType = "image/png"
	}
	meta := map[string]interface{}{
		"data_base64": base64.StdEncoding.EncodeToString(data),
		"mime_type":   mimeType,
		"size":        img.Size,
		"resolution":  img.Resolution,
		"model":       img.Model,
	}
	metaJSON, _ := json.Marshal(meta)
	return &toolsCallResult{
		Content: []contentItem{{Type: "text", Text: string(metaJSON)}},
		IsError: false,
	}, nil
}

func (s *Server) callFactCheck(ctx context.Context, args map[string]interface{}) (interface{}, *rpcError) {
	if s.factCheckAgent == nil {
		return &toolsCallResult{
			Content: []contentItem{{Type: "text", Text: "fact-check agent not configured"}},
			IsError: true,
		}, nil
	}
	text := getStr(args, "text")
	text, err := s.factCheckAgent.FactCheckSegment(ctx, text)
	if err != nil {
		return &toolsCallResult{
			Content: []contentItem{{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}
	return &toolsCallResult{
		Content: []contentItem{{Type: "text", Text: text}},
		IsError: false,
	}, nil
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeRPCError(w http.ResponseWriter, id interface{}, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	})
}
