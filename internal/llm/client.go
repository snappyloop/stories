package llm

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
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

// logGeminiResponse logs Gemini response text, truncating if over maxGeminiResponseLogBytes.
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
