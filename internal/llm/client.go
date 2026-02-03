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
	apiKey        string
	modelFlash    string
	modelPro      string
	modelImage    string // image generation, e.g. gemini-3-pro-image-preview
	modelTTS      string // TTS model, e.g. gemini-2.5-pro-preview-tts
	ttsVoice      string // TTS voice name, e.g. Zephyr, Puck, Aoede
	llmFlash      llms.Model
	llmPro        llms.Model
	genaiClient   *genai.Client        // for image modality via genai SDK
	unifiedClient *unifiedgenai.Client // unified genai SDK for TTS
}

// NewClient creates a new LLM client.
// apiEndpoint: optional Gemini API base URL (e.g. http://host.docker.internal:31300/gemini); when set, all Gemini calls use this endpoint.
// modelImage: image generation model (e.g. gemini-3-pro-image-preview); empty => default.
// modelTTS: TTS model (e.g. gemini-2.5-pro-preview-tts); empty => default.
// ttsVoice: TTS voice name (e.g. Zephyr, Puck, Aoede); empty => Zephyr.
func NewClient(apiKey, modelFlash, modelPro, modelImage, modelTTS, ttsVoice, apiEndpoint string) *Client {
	if modelImage == "" {
		modelImage = "gemini-3-pro-image-preview"
	}
	if modelTTS == "" {
		modelTTS = "gemini-2.5-pro-preview-tts"
	}
	if ttsVoice == "" {
		ttsVoice = "Zephyr"
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
		Str("model_image", modelImage).
		Str("model_tts", modelTTS).
		Str("tts_voice", ttsVoice).
		Str("api_endpoint", apiEndpoint).
		Bool("genai_client", genaiClient != nil).
		Bool("unified_tts", unifiedClient != nil).
		Msg("LLM client initialized")

	return &Client{
		apiKey:        apiKey,
		modelFlash:    modelFlash,
		modelPro:      modelPro,
		modelImage:    modelImage,
		modelTTS:      modelTTS,
		ttsVoice:      ttsVoice,
		llmFlash:      llmFlash,
		llmPro:        llmPro,
		genaiClient:   genaiClient,
		unifiedClient: unifiedClient,
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

// SegmentText segments text into logical parts
func (c *Client) SegmentText(ctx context.Context, text string, picturesCount int, inputType string) ([]*Segment, error) {
	log.Info().
		Int("pictures_count", picturesCount).
		Str("type", inputType).
		Int("text_length", len(text)).
		Msg("Segmenting text")

	// Use Gemini to intelligently segment the text
	if c.llmFlash == nil {
		return c.fallbackSegmentation(text, picturesCount)
	}

	// Build prompt based on content type
	var styleGuidance string
	switch inputType {
	case "educational":
		styleGuidance = "Focus on logical learning progression and key concepts."
	case "financial":
		styleGuidance = "Separate by distinct financial topics or time periods."
	case "fictional":
		styleGuidance = "Segment by narrative beats, scenes, or chapters."
	}

	prompt := fmt.Sprintf(`You are an expert at analyzing and segmenting text. 

Analyze the following text and segment it into exactly %d logical parts. %s

For each segment, provide:
- start_char: The character position where the segment starts (0-indexed)
- end_char: The character position where the segment ends
- title: A short descriptive title for the segment

Important rules:
1. Segments must cover the entire text with no gaps
2. Segments must not overlap
3. start_char of segment N+1 must equal end_char of segment N
4. Try to break at natural boundaries (paragraphs, sentences)
5. Return ONLY valid JSON, no explanation

Return JSON in this exact format:
{
  "segments": [
    {"start_char": 0, "end_char": 150, "title": "Introduction"},
    {"start_char": 150, "end_char": 350, "title": "Main Point"}
  ]
}

TEXT TO SEGMENT:
%s`, picturesCount, styleGuidance, text)

	// Call Gemini
	response, err := llms.GenerateFromSinglePrompt(ctx, c.llmFlash, prompt,
		llms.WithTemperature(0.3),
		llms.WithMaxTokens(2000),
	)
	if err != nil {
		log.Error().Err(err).Msg("Gemini segmentation failed, using fallback")
		return c.fallbackSegmentation(text, picturesCount)
	}

	logGeminiResponse("SegmentText", response)

	// Parse JSON response
	response = strings.TrimSpace(response)
	// Remove markdown code blocks if present
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	var result struct {
		Segments []struct {
			StartChar int    `json:"start_char"`
			EndChar   int    `json:"end_char"`
			Title     string `json:"title"`
		} `json:"segments"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		log.Error().Err(err).Str("response", response).Msg("Failed to parse segmentation JSON, using fallback")
		return c.fallbackSegmentation(text, picturesCount)
	}

	// Validate and convert to Segment objects
	segments := make([]*Segment, 0, len(result.Segments))
	for _, seg := range result.Segments {
		// Validate bounds
		if seg.StartChar < 0 || seg.EndChar > len(text) || seg.StartChar >= seg.EndChar {
			log.Warn().
				Int("start", seg.StartChar).
				Int("end", seg.EndChar).
				Msg("Invalid segment bounds, using fallback")
			return c.fallbackSegmentation(text, picturesCount)
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

	log.Info().
		Int("segments_created", len(segments)).
		Msg("Text segmentation complete (Gemini)")

	return segments, nil
}

// fallbackSegmentation provides simple character-based segmentation
func (c *Client) fallbackSegmentation(text string, picturesCount int) ([]*Segment, error) {
	segments := make([]*Segment, 0, picturesCount)

	charsPerSegment := len(text) / picturesCount
	if charsPerSegment == 0 {
		charsPerSegment = len(text)
	}

	for i := 0; i < picturesCount; i++ {
		start := i * charsPerSegment
		end := (i + 1) * charsPerSegment

		if i == picturesCount-1 {
			end = len(text)
		}

		if start >= len(text) {
			break
		}

		if end > len(text) {
			end = len(text)
		}

		title := fmt.Sprintf("Part %d", i+1)
		segmentText := text[start:end]

		segments = append(segments, &Segment{
			ID:        uuid.New(),
			StartChar: start,
			EndChar:   end,
			Title:     &title,
			Text:      segmentText,
		})
	}

	log.Info().
		Int("segments_created", len(segments)).
		Msg("Text segmentation complete (fallback)")

	return segments, nil
}

// GenerateNarration generates narration script for a segment
func (c *Client) GenerateNarration(ctx context.Context, text, audioType, inputType string) (string, error) {
	log.Debug().
		Str("audio_type", audioType).
		Str("input_type", inputType).
		Msg("Generating narration")

	// Use Gemini to generate natural narration
	if c.llmFlash == nil {
		return c.fallbackNarration(text, audioType, inputType), nil
	}

	// Build style guidance
	var styleGuidance string
	switch inputType {
	case "educational":
		styleGuidance = "Create clear, engaging educational narration suitable for learning. Use conversational tone."
	case "financial":
		styleGuidance = "Create professional, measured narration for financial content. Include appropriate disclaimers. Avoid hype or promises."
	case "fictional":
		styleGuidance = "Create immersive, dramatic narration suitable for storytelling."
	}

	var audioStyle string
	switch audioType {
	case "free_speech":
		audioStyle = "Natural speaking style, as if explaining to a friend."
	case "podcast":
		audioStyle = "Professional podcast style with good pacing and emphasis."
	}

	prompt := fmt.Sprintf(`Generate a narration script for the following text.

Style: %s
Audio format: %s

Original text:
%s

Generate a natural narration script that would sound good when read aloud. 
Make it engaging and appropriate for the content type.
Return ONLY the narration text, no explanations or formatting.`, styleGuidance, audioStyle, text)

	// Call Gemini
	response, err := llms.GenerateFromSinglePrompt(ctx, c.llmFlash, prompt,
		llms.WithTemperature(0.7),
		llms.WithMaxTokens(1000),
	)
	if err != nil {
		log.Error().Err(err).Msg("Gemini narration generation failed, using fallback")
		return c.fallbackNarration(text, audioType, inputType), nil
	}

	logGeminiResponse("GenerateNarration", response)

	narration := strings.TrimSpace(response)

	log.Info().Msg("Narration generation complete (Gemini)")

	return narration, nil
}

// fallbackNarration provides simple narration fallback
func (c *Client) fallbackNarration(text, audioType, inputType string) string {
	var prefix string
	switch inputType {
	case "financial":
		prefix = "[Disclaimer: This is for informational purposes only. Not financial advice.] "
	case "educational":
		prefix = "Let's learn about this: "
	case "fictional":
		prefix = ""
	}

	return prefix + text
}

// GenerateAudio returns placeholder audio (audio generation not implemented).
// GenerateAudio generates audio from narration script using the unified genai SDK.
// Uses gemini-2.5-pro-preview-tts with response_modalities: ["audio"] and SpeechConfig.
func (c *Client) GenerateAudio(ctx context.Context, script, audioType string) (*Audio, error) {
	log.Debug().
		Str("audio_type", audioType).
		Int("script_length", len(script)).
		Msg("Generating audio")

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
