package llm

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/rivo/uniseg"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/googleai"
	"google.golang.org/api/option"
	unifiedgenai "google.golang.org/genai"
)

// maxGeminiResponseLogBytes is the max length of a Gemini response body to log in full (to avoid huge logs).
const maxGeminiResponseLogBytes = 8192

// maxSegmentInputLogBytes is the max length of segmentation prompt to log (input to SegmentText LLM call).
const maxSegmentInputLogBytes = 4096

// httpClientForEndpoint returns an http.Client that rewrites request URLs to the given base endpoint (e.g. http://host.docker.internal:31300/gemini).
func httpClientForEndpoint(baseEndpoint string) *http.Client {
	base, err := url.Parse(baseEndpoint)
	if err != nil {
		log.Warn().Err(err).Str("endpoint", baseEndpoint).Msg("Invalid GEMINI_API_ENDPOINT, using default")
		return nil
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	return &http.Client{
		Transport: &endpointRoundTripper{base: base, next: http.DefaultTransport},
	}
}

// endpointRoundTripper rewrites request URLs to a custom base (scheme, host, path prefix).
type endpointRoundTripper struct {
	base *url.URL
	next http.RoundTripper
}

func (e *endpointRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = e.base.Scheme
	req2.URL.Host = e.base.Host
	req2.URL.Path = path.Join(e.base.Path, strings.TrimPrefix(req.URL.Path, "/"))
	if req.URL.RawQuery != "" {
		req2.URL.RawQuery = req.URL.RawQuery
	}
	return e.next.RoundTrip(req2)
}

func logGeminiResponse(caller, raw string) {
	if len(raw) <= maxGeminiResponseLogBytes {
		log.Info().Str("caller", caller).Str("gemini_response", raw).Msg("Gemini response")
		return
	}
	log.Info().
		Str("caller", caller).
		Str("gemini_response", raw[:maxGeminiResponseLogBytes]+"... [truncated]").
		Int("gemini_response_len", len(raw)).
		Msg("Gemini response")
}

// Client wraps Gemini API client
type Client struct {
	apiKey               string
	modelFlash           string
	modelPro             string
	modelImage           string // image generation, e.g. gemini-3-pro-image-preview
	modelTTS             string // TTS model, e.g. gemini-2.5-pro-preview-tts
	ttsVoice             string // TTS voice name, e.g. Zephyr, Puck, Aoede
	modelSegmentPrimary  string // e.g. gemini-3.0-flash
	modelSegmentFallback string // e.g. gemini-2.5-flash-lite
	llmFlash             llms.Model
	llmPro               llms.Model
	llmSegmentPrimary    llms.Model                       // primary for segmentation
	llmSegmentFallback   llms.Model                       // fallback for segmentation
	genaiClient          *genai.Client                    // for image modality and segment schema
	unifiedClient        *unifiedgenai.Client             // unified genai SDK for TTS
	boundaryCache        *database.BoundaryCacheRepository // cache for segmentation boundaries
}

// NewClient creates a new LLM client.
// apiEndpoint: optional Gemini API base URL (e.g. http://host.docker.internal:31300/gemini); when set, all Gemini calls use this endpoint.
// modelSegmentPrimary/modelSegmentFallback: models for segmentation (e.g. gemini-3.0-flash, gemini-2.5-flash-lite).
// boundaryCache: optional repository for caching segmentation boundaries; if nil, caching is disabled.
func NewClient(apiKey, modelFlash, modelPro, modelImage, modelTTS, ttsVoice, apiEndpoint, modelSegmentPrimary, modelSegmentFallback string, boundaryCache *database.BoundaryCacheRepository) *Client {
	if modelImage == "" {
		modelImage = "gemini-3-pro-image-preview"
	}
	if modelTTS == "" {
		modelTTS = "gemini-2.5-pro-preview-tts"
	}
	if ttsVoice == "" {
		ttsVoice = "Zephyr"
	}
	if modelSegmentPrimary == "" {
		modelSegmentPrimary = "gemini-3-flash-preview"
	}
	if modelSegmentFallback == "" {
		modelSegmentFallback = "gemini-2.5-flash-lite"
	}

	// Optional custom HTTP client for langchaingo when using a custom endpoint
	var langchaingoHTTPClient *http.Client
	if apiEndpoint != "" {
		langchaingoHTTPClient = httpClientForEndpoint(apiEndpoint)
	}

	// Initialize Google AI LLM for flash model
	flashOpts := []googleai.Option{googleai.WithAPIKey(apiKey), googleai.WithDefaultModel(modelFlash)}
	if langchaingoHTTPClient != nil {
		flashOpts = append(flashOpts, googleai.WithHTTPClient(langchaingoHTTPClient))
	}
	llmFlash, err := googleai.New(context.Background(), flashOpts...)
	if err != nil {
		log.Error().Err(err).Msg("Failed to initialize flash model, using fallback")
	}

	// Initialize Google AI LLM for pro model
	proOpts := []googleai.Option{googleai.WithAPIKey(apiKey), googleai.WithDefaultModel(modelPro)}
	if langchaingoHTTPClient != nil {
		proOpts = append(proOpts, googleai.WithHTTPClient(langchaingoHTTPClient))
	}
	llmPro, err := googleai.New(context.Background(), proOpts...)
	if err != nil {
		log.Error().Err(err).Msg("Failed to initialize pro model, using fallback")
	}

	// Segment primary (e.g. 3.0 flash)
	segPrimaryOpts := []googleai.Option{googleai.WithAPIKey(apiKey), googleai.WithDefaultModel(modelSegmentPrimary)}
	if langchaingoHTTPClient != nil {
		segPrimaryOpts = append(segPrimaryOpts, googleai.WithHTTPClient(langchaingoHTTPClient))
	}
	llmSegmentPrimary, err := googleai.New(context.Background(), segPrimaryOpts...)
	if err != nil {
		log.Error().Err(err).Str("model", modelSegmentPrimary).Msg("Failed to initialize segment primary model")
	}

	// Segment fallback (e.g. 2.5 flash)
	segFallbackOpts := []googleai.Option{googleai.WithAPIKey(apiKey), googleai.WithDefaultModel(modelSegmentFallback)}
	if langchaingoHTTPClient != nil {
		segFallbackOpts = append(segFallbackOpts, googleai.WithHTTPClient(langchaingoHTTPClient))
	}
	llmSegmentFallback, err := googleai.New(context.Background(), segFallbackOpts...)
	if err != nil {
		log.Error().Err(err).Str("model", modelSegmentFallback).Msg("Failed to initialize segment fallback model")
	}

	// genai client for strict modality (IMAGE); requires API key
	var genaiClient *genai.Client
	if apiKey != "" {
		genaiOpts := []option.ClientOption{option.WithAPIKey(apiKey)}
		if apiEndpoint != "" {
			genaiOpts = append(genaiOpts, option.WithEndpoint(apiEndpoint))
		}
		genaiClient, err = genai.NewClient(context.Background(), genaiOpts...)
		if err != nil {
			log.Error().Err(err).Msg("Failed to initialize genai client for image generation")
		}
	}

	// Unified genai client for TTS with response_modalities: audio
	var unifiedClient *unifiedgenai.Client
	if apiKey != "" {
		unifiedCfg := &unifiedgenai.ClientConfig{APIKey: apiKey}
		if apiEndpoint != "" {
			unifiedCfg.HTTPOptions = unifiedgenai.HTTPOptions{BaseURL: apiEndpoint}
		}
		unifiedClient, err = unifiedgenai.NewClient(context.Background(), unifiedCfg)
		if err != nil {
			log.Error().Err(err).Msg("Failed to initialize unified genai client for TTS")
		}
	}

	log.Info().
		Str("model_flash", modelFlash).
		Str("model_pro", modelPro).
		Str("model_segment_primary", modelSegmentPrimary).
		Str("model_segment_fallback", modelSegmentFallback).
		Str("model_image", modelImage).
		Str("model_tts", modelTTS).
		Str("tts_voice", ttsVoice).
		Str("api_endpoint", apiEndpoint).
		Bool("genai_client", genaiClient != nil).
		Bool("unified_tts", unifiedClient != nil).
		Msg("LLM client initialized")

	return &Client{
		apiKey:               apiKey,
		modelFlash:           modelFlash,
		modelPro:             modelPro,
		modelImage:           modelImage,
		modelTTS:             modelTTS,
		ttsVoice:             ttsVoice,
		modelSegmentPrimary:  modelSegmentPrimary,
		modelSegmentFallback: modelSegmentFallback,
		llmFlash:             llmFlash,
		llmPro:               llmPro,
		llmSegmentPrimary:    llmSegmentPrimary,
		llmSegmentFallback:   llmSegmentFallback,
		genaiClient:          genaiClient,
		unifiedClient:        unifiedClient,
		boundaryCache:        boundaryCache,
	}
}

// Segment represents a text segment
type Segment struct {
	ID        uuid.UUID
	StartChar int
	EndChar   int
	Title     *string
	Text      string
}

// Narration represents generated narration
type Narration struct {
	Text     string
	Duration float64
}

// Audio represents generated audio
type Audio struct {
	Data     io.Reader
	Size     int64
	Duration float64
	Model    string
	MimeType string // e.g. "audio/wav" (TTS output is WAV per GEMINI_INTEGRATION.md)
}

// ImagePrompt represents an image generation prompt
type ImagePrompt struct {
	Prompt string
	Style  string
}

// Image represents a generated image
type Image struct {
	Data       io.Reader
	Size       int64
	Resolution string
	Model      string
	MimeType   string // e.g. "image/png", "image/jpeg" (from Gemini blob.MIMEType)
}

// SegmentText segments text into logical parts.
// Uses 3.0 flash first, then 2.5 flash; if both fail or return no valid response, returns one segment (whole text).
func (c *Client) SegmentText(ctx context.Context, text string, segmentsCount int, inputType string) ([]*Segment, error) {
	text = strings.TrimSpace(text)
	log.Info().
		Int("segments_count", segmentsCount).
		Str("type", inputType).
		Int("text_length", len(text)).
		Msg("Segmenting text")

	// Check cache first
	var cachedBoundaries []int
	textHash := database.TextHash(text)
	if c.boundaryCache != nil {
		cached, err := c.boundaryCache.Get(ctx, textHash)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to get from boundary cache, proceeding with LLM")
		} else if cached != nil {
			log.Info().
				Str("text_hash", textHash).
				Int("cached_boundaries", len(cached)).
				Msg("Using cached boundaries")
			cachedBoundaries = cached
		}
	}

	// If we have cached boundaries, skip LLM and go straight to merging
	if cachedBoundaries != nil {
		byteOffsets := runeToByteOffsets(text)
		
		// Validate cached boundaries
		validatedBoundaries := validateAndAdjustBoundaries(cachedBoundaries, text, byteOffsets)
		
		segments := mergeBoundariesIntoSegments(validatedBoundaries, byteOffsets, text, segmentsCount)
		
		log.Info().
			Str("caller", "SegmentText").
			Int("final_segments", len(segments)).
			Msg("Text segmentation complete (from cache)")
		
		return segments, nil
	}

	prompt := c.buildSegmentPrompt(text, segmentsCount, inputType)

	// Log segmentation request input
	if len(prompt) <= maxSegmentInputLogBytes {
		log.Info().
			Str("caller", "SegmentText").
			Int("prompt_len", len(prompt)).
			Str("segment_request_input", prompt).
			Msg("SegmentText LLM request input")
	} else {
		log.Info().
			Str("caller", "SegmentText").
			Int("prompt_len", len(prompt)).
			Str("segment_request_input", prompt[:maxSegmentInputLogBytes]+"... [truncated]").
			Msg("SegmentText LLM request input")
	}

	// Try primary (3.0 flash), then fallback (2.5 flash)
	for _, tier := range []struct {
		name      string
		modelName string
		langModel llms.Model
	}{
		{"primary", c.modelSegmentPrimary, c.llmSegmentPrimary},
		{"fallback", c.modelSegmentFallback, c.llmSegmentFallback},
	} {
		if tier.modelName == "" && tier.langModel == nil {
			continue
		}
		segments, err := c.trySegmentWithModel(ctx, tier.name, tier.modelName, tier.langModel, prompt, text, segmentsCount, inputType)
		if err != nil {
			log.Warn().Err(err).Str("model_tier", tier.name).Msg("Segment model failed, trying next")
			continue
		}
		if segments != nil {
			return segments, nil
		}
	}

	// No response from both: create one segment made of the whole text
	log.Info().Msg("No valid response from segment models, using single-segment fallback")
	return c.oneSegmentFallback(text), nil
}

// buildSegmentPrompt builds the segmentation prompt asking for ALL logical boundaries.
func (c *Client) buildSegmentPrompt(text string, segmentsCount int, inputType string) string {
	var styleGuidance string
	switch inputType {
	case "educational":
		styleGuidance = "Identify boundaries between concepts, subtopics, or learning units."
	case "financial":
		styleGuidance = "Identify boundaries between financial topics, time periods, or categories."
	case "fictional":
		styleGuidance = "Identify boundaries between scenes, plot points, or narrative beats."
	default:
		styleGuidance = "Identify boundaries at paragraph breaks or major topic changes."
	}

	return fmt.Sprintf(`You are an expert at analyzing text structure and identifying logical segment boundaries.

Task: Identify ALL natural breakpoints in the following text where the content logically divides. %s

Rules:
1. Return a list of character positions (indices) where segments should END
2. Each position must be at a sentence boundary (ending with . ! ? etc.) - NEVER mid-sentence
3. Identify at least %d breakpoints, but return MORE if the text has more natural divisions
4. Prefer positions at paragraph breaks (\n\n) or section headers
5. The final position in your list should be the end of the text (last character index)
6. Count characters as visual units: emoji 🙋‍♂️ = 1 character (not bytes)
7. Positions must be in ascending order

Response format (STRICT):
- JSON object only (no markdown, no code fences)
- One key "boundaries" (array of integers)
- Each integer is a character position where a segment ends (0-based, exclusive end)

Example for text with 500 characters and 5 natural breakpoints:
{"boundaries":[120, 245, 350, 420, 500]}

This means: segment 1 is chars 0-120, segment 2 is chars 120-245, etc.

TEXT TO ANALYZE:
%s`, styleGuidance, segmentsCount-1, text)
}

// runeToByteOffsets returns a slice where offsets[i] is the byte index of the i-th grapheme cluster
// (visual character) in s, and offsets[len-1] == len(s). This matches how LLMs count "characters"
// (e.g., 🙋‍♂️ is 1 grapheme cluster, not 3 runes). Used to convert LLM character indices to byte positions.
func runeToByteOffsets(s string) []int {
	offsets := make([]int, 0, len(s)/2) // Rough estimate
	byteOffset := 0
	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		offsets = append(offsets, byteOffset)
		byteOffset += len(gr.Bytes())
	}
	offsets = append(offsets, len(s))
	return offsets
}

// validateAndAdjustBoundaries checks if boundaries are at sentence endings and adjusts them if not.
func validateAndAdjustBoundaries(boundaries []int, text string, byteOffsets []int) []int {
	adjusted := make([]int, 0, len(boundaries))
	numGraphemes := len(byteOffsets) - 1
	
	for _, graphemeBoundary := range boundaries {
		if graphemeBoundary >= numGraphemes {
			if len(adjusted) == 0 || numGraphemes > adjusted[len(adjusted)-1] {
				adjusted = append(adjusted, numGraphemes)
			}
			continue
		}
		
		bytePos := byteOffsets[graphemeBoundary]
		
		// Check if we're at a sentence boundary (looking back for . ! ?)
		if isSentenceBoundary(text, bytePos) {
			if len(adjusted) == 0 || graphemeBoundary > adjusted[len(adjusted)-1] {
				adjusted = append(adjusted, graphemeBoundary)
			}
			continue
		}
		
		// Not at sentence boundary - search backward for nearest sentence ending
		newBytePos := findPreviousSentenceBoundary(text, bytePos)
		if newBytePos < 0 {
			// No sentence boundary found, use original
			log.Warn().
				Int("grapheme_boundary", graphemeBoundary).
				Int("byte_pos", bytePos).
				Msg("Could not find sentence boundary, using original position")
			if len(adjusted) == 0 || graphemeBoundary > adjusted[len(adjusted)-1] {
				adjusted = append(adjusted, graphemeBoundary)
			}
			continue
		}
		
		// Convert byte position back to grapheme position
		newGrapheme := findGraphemeForBytePos(byteOffsets, newBytePos)
		log.Debug().
			Int("original_grapheme", graphemeBoundary).
			Int("adjusted_grapheme", newGrapheme).
			Int("original_byte", bytePos).
			Int("adjusted_byte", newBytePos).
			Msg("Adjusted boundary to sentence ending")
		if len(adjusted) == 0 || newGrapheme > adjusted[len(adjusted)-1] {
			adjusted = append(adjusted, newGrapheme)
		}
	}
	
	// Ensure last boundary is the end of the text so the full text is always covered
	if len(adjusted) == 0 || adjusted[len(adjusted)-1] != numGraphemes {
		adjusted = append(adjusted, numGraphemes)
	}
	
	return adjusted
}

// isSentenceBoundary checks if the position is right after sentence-ending punctuation
func isSentenceBoundary(text string, bytePos int) bool {
	if bytePos <= 0 || bytePos > len(text) {
		return false
	}
	
	// Look back for sentence-ending punctuation: . ! ?
	// Allow for closing quotes, parentheses, etc.
	checkPos := bytePos - 1
	for checkPos >= 0 && (text[checkPos] == ' ' || text[checkPos] == '\n' || text[checkPos] == ')' || text[checkPos] == '"' || text[checkPos] == '*') {
		checkPos--
	}
	
	if checkPos < 0 {
		return false
	}
	
	return text[checkPos] == '.' || text[checkPos] == '!' || text[checkPos] == '?'
}

// findPreviousSentenceBoundary searches backward from bytePos for a sentence-ending punctuation
func findPreviousSentenceBoundary(text string, bytePos int) int {
	if bytePos <= 0 {
		return -1
	}
	
	for i := bytePos - 1; i >= 0; i-- {
		if text[i] == '.' || text[i] == '!' || text[i] == '?' {
			// Found sentence ending - return position after it (and any trailing whitespace/punctuation)
			j := i + 1
			for j < len(text) && (text[j] == ' ' || text[j] == '\n' || text[j] == ')' || text[j] == '"' || text[j] == '*') {
				j++
			}
			return j
		}
	}
	
	return -1
}

// findGraphemeForBytePos finds the grapheme index for a given byte position
func findGraphemeForBytePos(byteOffsets []int, targetByte int) int {
	// byteOffsets[i] = byte position of grapheme i
	// Find largest i where byteOffsets[i] <= targetByte
	for i := len(byteOffsets) - 1; i >= 0; i-- {
		if byteOffsets[i] <= targetByte {
			return i
		}
	}
	return 0
}

// mergeBoundariesIntoSegments takes LLM-identified boundaries and merges them into the requested number of segments.
// If LLM returned fewer boundaries than requested, returns all boundaries as segments.
// If LLM returned more, merges logical segments by distributing them evenly.
func mergeBoundariesIntoSegments(boundaries []int, byteOffsets []int, text string, requestedCount int) []*Segment {
	numBoundaries := len(boundaries)

	// If LLM returned fewer or equal boundaries than requested, use all of them
	if numBoundaries <= requestedCount {
		segments := make([]*Segment, numBoundaries)
		startGrapheme := 0
		for i, endGrapheme := range boundaries {
			startByte := byteOffsets[startGrapheme]
			endByte := byteOffsets[endGrapheme]
			title := fmt.Sprintf("Part %d", i+1)
			segments[i] = &Segment{
				ID:        uuid.New(),
				StartChar: startByte,
				EndChar:   endByte,
				Title:     &title,
				Text:      text[startByte:endByte],
			}
			startGrapheme = endGrapheme
		}
		return segments
	}

	// LLM returned more boundaries than requested: merge them
	// Strategy: distribute boundaries evenly, with remainder going to first segments
	// Example: 11 boundaries → 3 segments: 11/3=3 remainder 2, so [4,4,3] boundaries per segment
	boundariesPerSegment := numBoundaries / requestedCount
	remainder := numBoundaries % requestedCount

	log.Debug().
		Int("num_boundaries", numBoundaries).
		Int("requested_count", requestedCount).
		Int("boundaries_per_segment", boundariesPerSegment).
		Int("remainder", remainder).
		Msg("Merging boundaries")

	segments := make([]*Segment, requestedCount)
	boundaryIdx := 0

	for i := 0; i < requestedCount; i++ {
		// This segment gets boundariesPerSegment boundaries, plus 1 if we have remainder left
		count := boundariesPerSegment
		if i < remainder {
			count++
		}

		startGrapheme := 0
		if boundaryIdx > 0 {
			startGrapheme = boundaries[boundaryIdx-1]
		}
		endGrapheme := boundaries[boundaryIdx+count-1]

		startByte := byteOffsets[startGrapheme]
		endByte := byteOffsets[endGrapheme]
		
		log.Debug().
			Int("segment", i+1).
			Int("boundaries_used", count).
			Int("start_grapheme", startGrapheme).
			Int("end_grapheme", endGrapheme).
			Int("start_byte", startByte).
			Int("end_byte", endByte).
			Str("text_preview", text[startByte:min(startByte+50, endByte)]+"..."+text[max(startByte, endByte-50):endByte]).
			Msg("Creating segment")
		
		title := fmt.Sprintf("Part %d", i+1)

		segments[i] = &Segment{
			ID:        uuid.New(),
			StartChar: startByte,
			EndChar:   endByte,
			Title:     &title,
			Text:      text[startByte:endByte],
		}

		boundaryIdx += count
	}

	return segments
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// extractTextFromGenaiResponse returns the concatenated text from the first candidate's parts.
func (c *Client) extractTextFromGenaiResponse(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if text, ok := part.(genai.Text); ok {
			b.WriteString(string(text))
		}
	}
	return b.String()
}

// segmentResponseSchema returns the genai.Schema for segmentation JSON: {"boundaries": [120, 245, 500]}.
func segmentResponseSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"boundaries": {
				Type:        genai.TypeArray,
				Description: "Array of character positions where segments end (0-based, exclusive, ascending order)",
				Items: &genai.Schema{
					Type:        genai.TypeInteger,
					Description: "Character position (visual character count, emojis = 1 char) where a segment ends",
				},
			},
		},
		Required: []string{"boundaries"},
	}
}

// trySegmentWithModel calls the given model and parses the response into segments. Returns (nil, err) on failure, (segments, nil) on success.
// When genaiClient is available and modelName is set, uses genai with ResponseSchema; otherwise uses langchaingo with JSON MIME type.
func (c *Client) trySegmentWithModel(ctx context.Context, modelTier string, modelName string, langModel llms.Model, prompt, text string, requestedCount int, inputType string) ([]*Segment, error) {
	var response string

	if c.genaiClient != nil && modelName != "" {
		// Use genai client with response schema for structured JSON output
		model := c.genaiClient.GenerativeModel(modelName)
		model.SetTemperature(0.3)
		model.SetMaxOutputTokens(2000)
		model.ResponseMIMEType = "application/json"
		model.ResponseSchema = segmentResponseSchema()

		resp, err := model.GenerateContent(ctx, genai.Text(prompt))
		if err != nil {
			return nil, err
		}
		response = c.extractTextFromGenaiResponse(resp)
	} else if langModel != nil {
		// Fallback: langchaingo with JSON MIME type (no schema)
		var err error
		response, err = llms.GenerateFromSinglePrompt(ctx, langModel, prompt,
			llms.WithTemperature(0.3),
			llms.WithMaxTokens(2000),
			llms.WithResponseMIMEType("application/json"),
		)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("no segment model available")
	}

	// Log segmentation response output
	log.Info().
		Str("caller", "SegmentText").
		Str("model_tier", modelTier).
		Int("response_len", len(response)).
		Msg("SegmentText LLM response output")
	logGeminiResponse("SegmentText", response)

	response = strings.TrimSpace(response)
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	if response == "" {
		return nil, fmt.Errorf("empty response")
	}

	var result struct {
		Boundaries []int `json:"boundaries"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	if len(result.Boundaries) == 0 {
		return nil, fmt.Errorf("no boundaries in response")
	}

	// LLM returns grapheme indices; convert to byte positions for correct slicing (handles emojis and multi-byte UTF-8).
	byteOffsets := runeToByteOffsets(text)
	numGraphemes := len(byteOffsets) - 1

	// Validate boundaries
	for i, boundary := range result.Boundaries {
		if boundary < 0 || boundary > numGraphemes {
			return nil, fmt.Errorf("boundary %d out of range: %d (text has %d graphemes)", i, boundary, numGraphemes)
		}
		if i > 0 && boundary <= result.Boundaries[i-1] {
			return nil, fmt.Errorf("boundaries must be in ascending order: %d <= %d", boundary, result.Boundaries[i-1])
		}
	}

	// Ensure last boundary is the end of text
	if result.Boundaries[len(result.Boundaries)-1] != numGraphemes {
		result.Boundaries = append(result.Boundaries, numGraphemes)
	}

	log.Info().
		Str("caller", "SegmentText").
		Int("text_bytes", len(text)).
		Int("text_graphemes", numGraphemes).
		Int("boundaries_returned", len(result.Boundaries)).
		Int("requested_segments", requestedCount).
		Interface("boundaries_graphemes", result.Boundaries).
		Msg("LLM returned boundaries")

	// Validate boundaries are at sentence endings (. ! ?)
	validatedBoundaries := validateAndAdjustBoundaries(result.Boundaries, text, byteOffsets)
	
	log.Info().
		Str("caller", "SegmentText").
		Interface("validated_boundaries", validatedBoundaries).
		Msg("Boundaries after validation")

	// Cache the validated boundaries for future use
	if c.boundaryCache != nil {
		textHash := database.TextHash(text)
		if err := c.boundaryCache.Set(ctx, textHash, validatedBoundaries); err != nil {
			log.Warn().Err(err).Msg("Failed to cache boundaries")
		} else {
			log.Info().
				Str("text_hash", textHash).
				Int("boundaries_cached", len(validatedBoundaries)).
				Msg("Cached boundaries for future use")
		}
	}

	// Merge boundaries into requested number of segments
	segments := mergeBoundariesIntoSegments(validatedBoundaries, byteOffsets, text, requestedCount)

	log.Info().
		Str("caller", "SegmentText").
		Str("model_tier", modelTier).
		Int("final_segments", len(segments)).
		Msg("Text segmentation complete")

	return segments, nil
}

// oneSegmentFallback returns a single segment containing the entire text (used when both segment models fail).
func (c *Client) oneSegmentFallback(text string) []*Segment {
	title := "Part 1"
	return []*Segment{{
		ID:        uuid.New(),
		StartChar: 0,
		EndChar:   len(text),
		Title:     &title,
		Text:      text,
	}}
}

// GenerateNarration generates narration script for a segment.
// Tries Gemini 3 Pro first; if it returns empty, falls back to 2.5 Flash.
func (c *Client) GenerateNarration(ctx context.Context, text, audioType, inputType string) (string, error) {
	log.Debug().
		Str("audio_type", audioType).
		Str("input_type", inputType).
		Msg("Generating narration")

	// Build style guidance and prompt once (shared by Pro and Flash)
	var styleGuidance string
	switch inputType {
	case "educational":
		styleGuidance = "Create clear, engaging educational narration suitable for learning. Use conversational tone."
	case "financial":
		styleGuidance = "Create professional, measured narration for financial content. Include appropriate disclaimers. Avoid hype or promises."
	case "fictional":
		styleGuidance = "Create immersive, dramatic narration suitable for storytelling."
	default:
		styleGuidance = "Create clear, engaging narration."
	}

	var audioStyle string
	switch audioType {
	case "free_speech":
		audioStyle = "Natural speaking style, as if explaining to a friend."
	case "podcast":
		audioStyle = "Professional podcast style with good pacing and emphasis."
	default:
		audioStyle = "Natural speaking style."
	}

	prompt := fmt.Sprintf(`Generate a narration script for the following text.

Style: %s
Audio format: %s

Original text:
%s

Generate a natural narration script that would sound good when read aloud. 
Make it engaging and appropriate for the content type.
Return ONLY the narration text, no explanations or formatting.`, styleGuidance, audioStyle, text)

	// Try Gemini 3 Pro first
	if c.llmPro != nil {
		response, err := llms.GenerateFromSinglePrompt(ctx, c.llmPro, prompt,
			llms.WithTemperature(0.7),
			llms.WithMaxTokens(1000),
		)
		if err != nil {
			log.Warn().Err(err).Msg("Gemini Pro narration failed, trying 2.5 Flash")
		} else {
			logGeminiResponse("GenerateNarration", response)
			narration := strings.TrimSpace(response)
			if narration != "" {
				log.Info().Msg("Narration generation complete (Gemini Pro)")
				return narration, nil
			}
			log.Warn().Msg("Gemini Pro returned empty narration, trying 2.5 Flash")
		}
	}

	// Fallback: 2.5 Flash
	if c.llmFlash != nil {
		response, err := llms.GenerateFromSinglePrompt(ctx, c.llmFlash, prompt,
			llms.WithTemperature(0.7),
			llms.WithMaxTokens(1000),
		)
		if err != nil {
			log.Warn().Err(err).Msg("Gemini 2.5 Flash narration failed")
		} else {
			logGeminiResponse("GenerateNarration", response)
			narration := strings.TrimSpace(response)
			if narration != "" {
				log.Info().Msg("Narration generation complete (Gemini 2.5 Flash)")
				return narration, nil
			}
		}
	}

	// No narration from either model: return empty so caller skips TTS
	log.Info().Msg("Narration not generated, returning empty (TTS will be skipped)")
	return "", nil
}

// GenerateAudio generates audio from narration script using the unified genai SDK.
// Uses gemini-2.5-pro-preview-tts with response_modalities: ["audio"] and SpeechConfig.
// If script is empty, skips TTS and returns placeholder (avoids unnecessary API call and zero-length audio).
func (c *Client) GenerateAudio(ctx context.Context, script, audioType string) (*Audio, error) {
	log.Debug().
		Str("audio_type", audioType).
		Int("script_length", len(script)).
		Msg("Generating audio")

	if len(script) == 0 {
		log.Debug().Msg("Script length is zero, skipping TTS and using placeholder")
		return c.placeholderAudio(script)
	}

	if c.unifiedClient != nil {
		audio, err := c.generateAudioUnified(ctx, script, audioType)
		if err != nil {
			log.Warn().Err(err).
				Str("model", c.modelTTS).
				Int("script_length", len(script)).
				Msg("TTS generation failed, falling back to placeholder")
			return c.placeholderAudio(script)
		}
		if audio != nil {
			return audio, nil
		}
	}

	return c.placeholderAudio(script)
}

// generateAudioUnified uses the unified genai SDK with response_modalities: ["audio"] for TTS.
func (c *Client) generateAudioUnified(ctx context.Context, script, audioType string) (*Audio, error) {
	// Build prompt with tone direction
	toneHint := ttsToneHint(audioType)
	promptText := script
	if toneHint != "" {
		promptText = "[tone: " + toneHint + "] " + script
	}

	contents := []*unifiedgenai.Content{
		{
			Role: "user",
			Parts: []*unifiedgenai.Part{
				unifiedgenai.NewPartFromText(promptText),
			},
		},
	}

	temp := float32(1.0)
	config := &unifiedgenai.GenerateContentConfig{
		Temperature:        &temp,
		ResponseModalities: []string{"audio"},
		SpeechConfig: &unifiedgenai.SpeechConfig{
			VoiceConfig: &unifiedgenai.VoiceConfig{
				PrebuiltVoiceConfig: &unifiedgenai.PrebuiltVoiceConfig{
					VoiceName: c.ttsVoice,
				},
			},
		},
	}

	log.Debug().
		Str("model", c.modelTTS).
		Str("voice", c.ttsVoice).
		Str("audio_type", audioType).
		Msg("Calling unified genai TTS GenerateContentStream")

	// Collect audio data from streaming response
	var audioBuffer bytes.Buffer
	var lastMimeType string

	for resp, err := range c.unifiedClient.Models.GenerateContentStream(ctx, c.modelTTS, contents, config) {
		if err != nil {
			return nil, fmt.Errorf("TTS stream error: %w", err)
		}
		if resp.Candidates == nil || len(resp.Candidates) == 0 {
			continue
		}
		cand := resp.Candidates[0]
		if cand.Content == nil || cand.Content.Parts == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			if part.InlineData != nil && len(part.InlineData.Data) > 0 {
				audioBuffer.Write(part.InlineData.Data)
				if part.InlineData.MIMEType != "" {
					lastMimeType = part.InlineData.MIMEType
				}
			}
		}
	}

	if audioBuffer.Len() == 0 {
		return nil, fmt.Errorf("TTS returned no audio data")
	}

	// Convert to WAV if raw PCM (per GEMINI_INTEGRATION.md: "Output: WAV format (converted from raw PCM)")
	audioBytes := audioBuffer.Bytes()
	outMime := lastMimeType
	if lastMimeType != "" && strings.HasPrefix(lastMimeType, "audio/L") {
		log.Debug().Str("mime_type", lastMimeType).Msg("Converting raw PCM to WAV")
		audioBytes = convertToWAV(audioBytes, lastMimeType)
		outMime = "audio/wav"
	}
	if outMime == "" {
		outMime = "audio/wav"
	}

	size := int64(len(audioBytes))
	words := len(script) / 5
	duration := float64(words) / 150.0 * 60.0

	log.Info().
		Str("caller", "GenerateAudio").
		Int64("audio_size_bytes", size).
		Str("voice", c.ttsVoice).
		Str("mime_type", outMime).
		Msg("TTS audio generated")

	audio := &Audio{
		Data:     bytes.NewReader(audioBytes),
		Size:     size,
		Duration: duration,
		Model:    c.modelTTS,
		MimeType: outMime,
	}

	if err := c.validateAudio(audio); err != nil {
		log.Error().Err(err).Msg("Audio validation failed")
		return nil, err
	}

	return audio, nil
}

// ttsToneHint returns a tone hint for TTS based on audio type.
func ttsToneHint(audioType string) string {
	switch audioType {
	case "podcast":
		return "professional and measured, good pacing"
	case "free_speech":
		return "warm, natural and conversational"
	default:
		return "clear and engaging"
	}
}

// convertToWAV converts raw PCM audio data to WAV format.
func convertToWAV(audioData []byte, mimeType string) []byte {
	params := parseAudioMimeType(mimeType)
	bitsPerSample := params.bitsPerSample
	sampleRate := params.rate
	numChannels := 1
	dataSize := len(audioData)
	bytesPerSample := bitsPerSample / 8
	blockAlign := numChannels * bytesPerSample
	byteRate := sampleRate * blockAlign
	chunkSize := 36 + dataSize

	header := new(bytes.Buffer)
	binary.Write(header, binary.LittleEndian, []byte("RIFF"))
	binary.Write(header, binary.LittleEndian, uint32(chunkSize))
	binary.Write(header, binary.LittleEndian, []byte("WAVE"))
	binary.Write(header, binary.LittleEndian, []byte("fmt "))
	binary.Write(header, binary.LittleEndian, uint32(16))
	binary.Write(header, binary.LittleEndian, uint16(1))
	binary.Write(header, binary.LittleEndian, uint16(numChannels))
	binary.Write(header, binary.LittleEndian, uint32(sampleRate))
	binary.Write(header, binary.LittleEndian, uint32(byteRate))
	binary.Write(header, binary.LittleEndian, uint16(blockAlign))
	binary.Write(header, binary.LittleEndian, uint16(bitsPerSample))
	binary.Write(header, binary.LittleEndian, []byte("data"))
	binary.Write(header, binary.LittleEndian, uint32(dataSize))

	return append(header.Bytes(), audioData...)
}

type audioParams struct {
	bitsPerSample int
	rate          int
}

// parseAudioMimeType parses bits per sample and rate from an audio MIME type.
func parseAudioMimeType(mimeType string) audioParams {
	params := audioParams{bitsPerSample: 16, rate: 24000}

	parts := strings.Split(mimeType, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "rate=") {
			if rate, err := strconv.Atoi(strings.Split(part, "=")[1]); err == nil {
				params.rate = rate
			}
		} else if strings.HasPrefix(part, "audio/L") {
			re := regexp.MustCompile(`audio/L(\d+)`)
			if matches := re.FindStringSubmatch(part); len(matches) > 1 {
				if bits, err := strconv.Atoi(matches[1]); err == nil {
					params.bitsPerSample = bits
				}
			}
		}
	}
	return params
}

func (c *Client) placeholderAudio(script string) (*Audio, error) {
	audioBytes := []byte("PLACEHOLDER_AUDIO_DATA")
	data := bytes.NewReader(audioBytes)
	words := len(script) / 5
	duration := float64(words) / 150.0 * 60.0
	audio := &Audio{
		Data:     data,
		Size:     int64(len(audioBytes)),
		Duration: duration,
		Model:    c.modelTTS,
		MimeType: "audio/wav",
	}
	log.Info().
		Str("caller", "GenerateAudio").
		Str("gemini_response", "placeholder").
		Int64("audio_size_bytes", audio.Size).
		Msg("Gemini response (audio placeholder)")
	if err := c.validateAudio(audio); err != nil {
		return nil, err
	}
	return audio, nil
}

// validateAudio checks that audio result is valid (non-nil, has data, positive size).
func (c *Client) validateAudio(audio *Audio) error {
	if audio == nil {
		return fmt.Errorf("audio is nil")
	}
	if audio.Data == nil {
		return fmt.Errorf("audio data is nil")
	}
	if audio.Size <= 0 {
		return fmt.Errorf("audio size is invalid: %d", audio.Size)
	}
	return nil
}

// GenerateImagePrompt generates an image generation prompt using Gemini (Flash; Pro can return empty with langchaingo).
func (c *Client) GenerateImagePrompt(ctx context.Context, text, inputType string) (string, error) {
	log.Debug().
		Str("input_type", inputType).
		Msg("Generating image prompt")

	// Use Flash for image prompt generation (same as SegmentText/GenerateNarration); Pro often returns empty via langchaingo.
	model := c.llmFlash
	if model == nil {
		return c.fallbackImagePrompt(text, inputType), nil
	}

	// Build style guidance
	var styleGuidance string
	switch inputType {
	case "educational":
		styleGuidance = "Create a clear, easy-to-read illustration or reference table suitable for learning. Prefer diagrams, simple charts, step-by-step visuals, or tables that are easy to understand. Focus on clarity and accuracy."
	case "financial":
		styleGuidance = "Create a professional, restrained visual suitable for financial content. Avoid flashy or misleading imagery."
	case "fictional":
		styleGuidance = "Create a cinematic, atmospheric scene that captures the mood and setting of the story."
	}

	prompt := fmt.Sprintf(`You are an expert at creating image generation prompts for AI models like Midjourney or DALL-E.

Based on the following text, create a detailed, effective image generation prompt.

Content type: %s
Style guidance: %s

Text to visualize:
%s

Generate a concise but detailed image generation prompt (max 150 words).
Focus on:
- Visual elements, composition, style
- Mood, lighting, atmosphere
- Specific details that would create an effective image

Return ONLY the image prompt, no explanations.`, inputType, styleGuidance, text)

	// Call Gemini Pro (or Flash fallback)
	response, err := llms.GenerateFromSinglePrompt(ctx, model, prompt,
		llms.WithTemperature(0.8),
		llms.WithMaxTokens(300),
	)
	if err != nil {
		log.Error().Err(err).Msg("Gemini image prompt generation failed, using fallback")
		return c.fallbackImagePrompt(text, inputType), nil
	}

	logGeminiResponse("GenerateImagePrompt", response)

	imagePrompt := strings.TrimSpace(response)
	if imagePrompt == "" {
		log.Warn().Msg("Gemini returned empty image prompt, using fallback")
		imagePrompt = c.fallbackImagePrompt(text, inputType)
	}

	log.Info().
		Int("prompt_length", len(imagePrompt)).
		Msg("Image prompt generation complete (Gemini)")

	return imagePrompt, nil
}

// fallbackImagePrompt provides simple image prompt fallback (used when Gemini returns empty or model is unavailable).
func (c *Client) fallbackImagePrompt(text, inputType string) string {
	var stylePrefix string
	switch inputType {
	case "educational":
		stylePrefix = "Clear, easy-to-read educational illustration or reference table: "
	case "financial":
		stylePrefix = "Professional financial chart: "
	case "fictional":
		stylePrefix = "Cinematic scene: "
	default:
		stylePrefix = "Illustration: "
	}

	// Take first 200 chars of text as base so the prompt is substantive
	textSample := strings.TrimSpace(text)
	if textSample == "" {
		textSample = "key concepts and ideas"
	} else if len(textSample) > 200 {
		textSample = textSample[:200] + "..."
	}

	return stylePrefix + textSample
}

// GenerateImage generates an image from a prompt using Gemini Pro with strict IMAGE modality.
// Uses genai client and GenerateContent; when the SDK supports it, set model.ResponseModality = []string{"IMAGE"}.
func (c *Client) GenerateImage(ctx context.Context, prompt string) (*Image, error) {
	log.Debug().
		Str("prompt", prompt[:min(50, len(prompt))]+"...").
		Msg("Generating image")

	if c.genaiClient != nil {
		img, err := c.generateImageGenai(ctx, prompt)
		if err != nil {
			log.Error().Err(err).
				Str("model", c.modelPro).
				Str("prompt_preview", prompt[:min(80, len(prompt))]).
				Msg("Genai image generation failed (strict modality: no fallback)")
			return nil, err
		}
		if img != nil {
			return img, nil
		}
	}

	return c.placeholderImage(prompt)
}

// generateImageGenai calls Gemini with an image prompt and expects image Blob in response (strict modality).
// Uses model gemini-3-pro-image-preview (or GeminiModelImage) with ResponseModality = []string{"IMAGE"}.
func (c *Client) generateImageGenai(ctx context.Context, prompt string) (*Image, error) {
	model := c.genaiClient.GenerativeModel(c.modelImage)
	// Strict modality: request native image output (required for gemini-3-pro-image-preview)
	setResponseModality(model, []string{"IMAGE"})

	reqPrompt := genai.Text(prompt)
	resp, err := model.GenerateContent(ctx, reqPrompt)
	if err != nil {
		return nil, err
	}

	logGeminiResponse("GenerateImage", fmt.Sprintf("candidates=%d", len(resp.Candidates)))
	for i, cand := range resp.Candidates {
		if cand.Content == nil {
			continue
		}
		for j, part := range cand.Content.Parts {
			blob, ok := part.(genai.Blob)
			if !ok || len(blob.Data) == 0 {
				continue
			}
			log.Info().
				Str("caller", "GenerateImage").
				Str("gemini_response", "blob").
				Int64("image_size_bytes", int64(len(blob.Data))).
				Str("mime_type", blob.MIMEType).
				Int("candidate", i).
				Int("part", j).
				Msg("Gemini response (image blob)")
			imageBytes := blob.Data
			size := int64(len(imageBytes))
			mimeType := blob.MIMEType
			if mimeType == "" {
				mimeType = "image/png"
			}
			return &Image{
				Data:       bytes.NewReader(imageBytes),
				Size:       size,
				Resolution: "1024x1024",
				Model:      c.modelImage,
				MimeType:   mimeType,
			}, nil
		}
	}

	log.Warn().
		Str("model", c.modelImage).
		Int("candidates", len(resp.Candidates)).
		Msg("No image blob in Gemini response; ensure ResponseModality is IMAGE for strict image generation")
	return nil, fmt.Errorf("no image blob in response (strict modality: expected IMAGE)")
}

// setResponseModality sets model.ResponseModality when the genai SDK exposes it (e.g. for Gemini 3).
// Uses reflection so it no-ops on older SDKs that don't have the field.
func setResponseModality(model *genai.GenerativeModel, modalities []string) {
	v := reflect.ValueOf(model).Elem()
	f := v.FieldByName("ResponseModality")
	if !f.IsValid() || !f.CanSet() {
		log.Debug().Msg("ResponseModality not available on GenerativeModel (SDK may not support it yet)")
		return
	}
	// ResponseModality is []string
	if f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String {
		f.Set(reflect.ValueOf(modalities))
		log.Debug().Strs("modality", modalities).Msg("Set ResponseModality on GenerativeModel")
	}
}

func (c *Client) placeholderImage(prompt string) (*Image, error) {
	imageBytes := []byte("PLACEHOLDER_IMAGE_DATA")
	image := &Image{
		Data:       bytes.NewReader(imageBytes),
		Size:       int64(len(imageBytes)),
		Resolution: "1024x1024",
		Model:      c.modelPro,
		MimeType:   "image/png",
	}
	log.Info().
		Str("caller", "GenerateImage").
		Str("gemini_response", "placeholder").
		Int64("image_size_bytes", image.Size).
		Str("model", c.modelPro).
		Msg("Gemini response (image placeholder)")
	return image, nil
}

// ExtractContent uses Gemini 3 Pro vision to extract text from images/PDFs
func (c *Client) ExtractContent(ctx context.Context, data []byte, mimeType, inputType string) (string, error) {
	if c.genaiClient == nil {
		return "", fmt.Errorf("genai client not initialized")
	}

	model := c.genaiClient.GenerativeModel(c.modelPro)
	prompt := c.buildExtractionPrompt(inputType, mimeType)

	resp, err := model.GenerateContent(ctx, genai.Text(prompt), genai.Blob{MIMEType: mimeType, Data: data})
	if err != nil {
		return "", fmt.Errorf("gemini vision failed: %w", err)
	}

	var result strings.Builder
	for _, cand := range resp.Candidates {
		if cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			if text, ok := part.(genai.Text); ok {
				result.WriteString(string(text))
			}
		}
	}

	return result.String(), nil
}

// buildExtractionPrompt asks for a transformative summary (not verbatim extraction) to avoid
// FinishReasonRecitation / copyright blocks. We want paraphrased content suitable for downstream
// segmentation and narration, not word-for-word transcription.
func (c *Client) buildExtractionPrompt(inputType, mimeType string) string {
	fileType := "document"
	if strings.HasPrefix(mimeType, "image/") {
		fileType = "image"
	}

	base := fmt.Sprintf("Summarize this %s in your own words. Describe the main content, ideas, and structure. Do not quote or transcribe long passages verbatim; paraphrase and condense so the summary is useful for creating an enriched story version.", fileType)

	switch inputType {
	case "educational":
		return base + " Focus on the main concepts, facts, and how they are organized. Keep the logical flow clear."
	case "financial":
		return base + " Summarize the main points, figures, and conclusions. Note the presence of any disclaimers or risk warnings without quoting them in full."
	case "fictional":
		return base + " Summarize the plot, key characters, and story beats in your own words. Capture the tone and main events without copying dialogue or text verbatim."
	default:
		return base + " Keep the overall structure and meaning clear in your summary."
	}
}
