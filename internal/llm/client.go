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
	"github.com/rs/zerolog/log"
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
	llmSegmentPrimary    llms.Model           // primary for segmentation
	llmSegmentFallback   llms.Model           // fallback for segmentation
	genaiClient          *genai.Client        // for image modality and segment schema
	unifiedClient        *unifiedgenai.Client // unified genai SDK for TTS
}

// NewClient creates a new LLM client.
// apiEndpoint: optional Gemini API base URL (e.g. http://host.docker.internal:31300/gemini); when set, all Gemini calls use this endpoint.
// modelSegmentPrimary/modelSegmentFallback: models for segmentation (e.g. gemini-3.0-flash, gemini-2.5-flash-lite).
func NewClient(apiKey, modelFlash, modelPro, modelImage, modelTTS, ttsVoice, apiEndpoint, modelSegmentPrimary, modelSegmentFallback string) *Client {
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
		modelSegmentPrimary = "gemini-3.0-flash"
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
func (c *Client) SegmentText(ctx context.Context, text string, picturesCount int, inputType string) ([]*Segment, error) {
	log.Info().
		Int("pictures_count", picturesCount).
		Str("type", inputType).
		Int("text_length", len(text)).
		Msg("Segmenting text")

	prompt := c.buildSegmentPrompt(text, picturesCount, inputType)

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
		segments, err := c.trySegmentWithModel(ctx, tier.name, tier.modelName, tier.langModel, prompt, text)
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

// buildSegmentPrompt builds the segmentation prompt with structured response instructions.
func (c *Client) buildSegmentPrompt(text string, picturesCount int, inputType string) string {
	var styleGuidance string
	switch inputType {
	case "educational":
		styleGuidance = "Focus on logical learning progression and key concepts."
	case "financial":
		styleGuidance = "Separate by distinct financial topics or time periods."
	case "fictional":
		styleGuidance = "Segment by narrative beats, scenes, or chapters."
	default:
		styleGuidance = "Break at natural boundaries (paragraphs, sentences)."
	}

	return fmt.Sprintf(`You are an expert at analyzing and segmenting text.

Your task: segment the following text into exactly %d logical parts. %s

Important: The text may contain multiple blocks (e.g. main content and then "---" followed by image or file descriptions). You MUST segment the ENTIRE text from start_char 0 to the last character—every part of the text must be included in some segment.

Structured response requirements:
- You must respond with a single JSON object only. No markdown, no code fences, no explanation before or after.
- The JSON must have exactly one key: "segments", an array of objects.
- Each object must have: "start_char" (number, 0-based byte index), "end_char" (number, exclusive), "title" (string).
- Rules: segments cover the entire text with no gaps; no overlaps; start_char of segment N+1 equals end_char of segment N; break at natural boundaries.

Example valid response:
{"segments":[{"start_char":0,"end_char":150,"title":"Introduction"},{"start_char":150,"end_char":350,"title":"Main Point"}]}

TEXT TO SEGMENT:
%s`, picturesCount, styleGuidance, text)
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

// segmentResponseSchema returns the genai.Schema for segmentation JSON: {"segments": [{"start_char", "end_char", "title"}, ...]}.
func segmentResponseSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"segments": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"start_char": {Type: genai.TypeInteger, Description: "0-based byte index where segment starts"},
						"end_char":   {Type: genai.TypeInteger, Description: "Byte index where segment ends (exclusive)"},
						"title":      {Type: genai.TypeString, Description: "Short descriptive title for the segment"},
					},
					Required: []string{"start_char", "end_char", "title"},
				},
			},
		},
		Required: []string{"segments"},
	}
}

// trySegmentWithModel calls the given model and parses the response into segments. Returns (nil, err) on failure, (segments, nil) on success.
// When genaiClient is available and modelName is set, uses genai with ResponseSchema; otherwise uses langchaingo with JSON MIME type.
func (c *Client) trySegmentWithModel(ctx context.Context, modelTier string, modelName string, langModel llms.Model, prompt, text string) ([]*Segment, error) {
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
		Segments []struct {
			StartChar int    `json:"start_char"`
			EndChar   int    `json:"end_char"`
			Title     string `json:"title"`
		} `json:"segments"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	if len(result.Segments) == 0 {
		return nil, fmt.Errorf("no segments in response")
	}

	// Validate and convert to Segment objects
	segments := make([]*Segment, 0, len(result.Segments)+1)
	for _, seg := range result.Segments {
		if seg.StartChar < 0 || seg.EndChar > len(text) || seg.StartChar >= seg.EndChar {
			return nil, fmt.Errorf("invalid segment bounds start=%d end=%d", seg.StartChar, seg.EndChar)
		}
		segmentText := text[seg.StartChar:seg.EndChar]
		title := seg.Title
		segments = append(segments, &Segment{
			ID:        uuid.New(),
			StartChar: seg.StartChar,
			EndChar:   seg.EndChar,
			Title:     &title,
			Text:      segmentText,
		})
	}

	// If the model did not cover the full text (e.g. only segmented the first block and ignored
	// content after "\n\n---\n\n" such as image/file descriptions), append one segment for the remainder.
	lastEnd := 0
	if len(segments) > 0 {
		lastEnd = segments[len(segments)-1].EndChar
	}
	if lastEnd < len(text) {
		tail := strings.TrimSpace(text[lastEnd:])
		if len(tail) > 0 {
			title := "Additional content"
			segments = append(segments, &Segment{
				ID:        uuid.New(),
				StartChar: lastEnd,
				EndChar:   len(text),
				Title:     &title,
				Text:      text[lastEnd:],
			})
			log.Info().
				Str("caller", "SegmentText").
				Int("trailing_chars", len(text)-lastEnd).
				Msg("Appended segment for trailing content (e.g. image/file description)")
		}
	}

	log.Info().
		Str("caller", "SegmentText").
		Str("model_tier", modelTier).
		Int("segments_created", len(segments)).
		Msg("Text segmentation complete (Gemini)")

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
