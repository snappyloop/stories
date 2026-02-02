package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/googleai"
)

// Client wraps Gemini API client
type Client struct {
	apiKey     string
	modelFlash string
	modelPro   string
	llmFlash   llms.Model
	llmPro     llms.Model
}

// NewClient creates a new LLM client
func NewClient(apiKey, modelFlash, modelPro string) *Client {
	// Initialize Google AI LLM for flash model
	llmFlash, err := googleai.New(
		context.Background(),
		googleai.WithAPIKey(apiKey),
		googleai.WithDefaultModel(modelFlash),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to initialize flash model, using fallback")
	}

	// Initialize Google AI LLM for pro model
	llmPro, err := googleai.New(
		context.Background(),
		googleai.WithAPIKey(apiKey),
		googleai.WithDefaultModel(modelPro),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to initialize pro model, using fallback")
	}

	log.Info().
		Str("model_flash", modelFlash).
		Str("model_pro", modelPro).
		Msg("LLM client initialized")

	return &Client{
		apiKey:     apiKey,
		modelFlash: modelFlash,
		modelPro:   modelPro,
		llmFlash:   llmFlash,
		llmPro:     llmPro,
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

// GenerateAudio generates audio from narration script
func (c *Client) GenerateAudio(ctx context.Context, script, audioType string) (*Audio, error) {
	log.Debug().
		Str("audio_type", audioType).
		Int("script_length", len(script)).
		Msg("Generating audio")

	// TODO: Implement actual audio generation using Gemini/Google TTS
	// For now, return a placeholder

	// Calculate approximate duration (words per minute)
	words := len(script) / 5                  // rough estimate
	duration := float64(words) / 150.0 * 60.0 // 150 WPM

	// Return empty audio data as placeholder
	data := bytes.NewReader([]byte("PLACEHOLDER_AUDIO_DATA"))

	audio := &Audio{
		Data:     data,
		Size:     int64(data.Len()),
		Duration: duration,
		Model:    c.modelFlash,
	}

	log.Info().
		Float64("duration", duration).
		Msg("Audio generation complete")

	return audio, nil
}

// GenerateImagePrompt generates an image generation prompt
func (c *Client) GenerateImagePrompt(ctx context.Context, text, inputType string) (string, error) {
	log.Debug().
		Str("input_type", inputType).
		Msg("Generating image prompt")

	// Use Gemini to create an optimal image generation prompt
	if c.llmFlash == nil {
		return c.fallbackImagePrompt(text, inputType), nil
	}

	// Build style guidance
	var styleGuidance string
	switch inputType {
	case "educational":
		styleGuidance = "Create a clear, informative diagram or illustration suitable for educational purposes. Focus on clarity and accuracy."
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

	// Call Gemini
	response, err := llms.GenerateFromSinglePrompt(ctx, c.llmFlash, prompt,
		llms.WithTemperature(0.8),
		llms.WithMaxTokens(300),
	)
	if err != nil {
		log.Error().Err(err).Msg("Gemini image prompt generation failed, using fallback")
		return c.fallbackImagePrompt(text, inputType), nil
	}

	imagePrompt := strings.TrimSpace(response)

	log.Info().
		Int("prompt_length", len(imagePrompt)).
		Msg("Image prompt generation complete (Gemini)")

	return imagePrompt, nil
}

// fallbackImagePrompt provides simple image prompt fallback
func (c *Client) fallbackImagePrompt(text, inputType string) string {
	var stylePrefix string
	switch inputType {
	case "educational":
		stylePrefix = "Educational diagram illustration: "
	case "financial":
		stylePrefix = "Professional financial chart: "
	case "fictional":
		stylePrefix = "Cinematic scene: "
	}

	// Take first 100 chars of text as base
	textSample := text
	if len(textSample) > 100 {
		textSample = textSample[:100] + "..."
	}

	return stylePrefix + textSample
}

// GenerateImage generates an image from a prompt
func (c *Client) GenerateImage(ctx context.Context, prompt string) (*Image, error) {
	log.Debug().
		Str("prompt", prompt[:min(50, len(prompt))]).
		Msg("Generating image")

	// TODO: Implement actual image generation using Gemini/Imagen
	// For now, return a placeholder

	// Return empty image data as placeholder
	data := bytes.NewReader([]byte("PLACEHOLDER_IMAGE_DATA"))

	image := &Image{
		Data:       data,
		Size:       int64(data.Len()),
		Resolution: "1024x1024",
		Model:      c.modelPro,
	}

	log.Info().
		Str("resolution", image.Resolution).
		Msg("Image generation complete")

	return image, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
