package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/tmc/langchaingo/llms"
)

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
