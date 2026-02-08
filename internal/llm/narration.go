package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/tmc/langchaingo/llms"
)

// GenerateNarration generates narration script for a segment.
// Tries Gemini 3 Pro first; if it returns empty, falls back to 2.5 Flash.
func (c *Client) GenerateNarration(ctx context.Context, text, audioType, inputType string) (string, error) {
	log.Debug().
		Str("audio_type", audioType).
		Str("input_type", inputType).
		Msg("Generating narration")

	// Build style guidance and system prompt once (shared by Pro and Flash)
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

	systemPrompt := fmt.Sprintf(`Generate a narration script for the text provided by the user.

Style: %s
Audio format: %s

Generate a natural narration script that would sound good when read aloud.
Make it engaging and appropriate for the content type.
Return ONLY the narration text, no explanations or formatting.`, styleGuidance, audioStyle)

	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: systemPrompt}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: text}}},
	}
	opts := []llms.CallOption{
		llms.WithTemperature(0.7),
		llms.WithMaxTokens(1000),
	}

	// Try Gemini 3 Pro first
	if c.llmPro != nil {
		resp, err := c.llmPro.GenerateContent(ctx, messages, opts...)
		if err != nil {
			log.Warn().Err(err).Msg("Gemini Pro narration failed, trying 2.5 Flash")
		} else if len(resp.Choices) > 0 {
			response := resp.Choices[0].Content
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
		resp, err := c.llmFlash.GenerateContent(ctx, messages, opts...)
		if err != nil {
			log.Warn().Err(err).Msg("Gemini 2.5 Flash narration failed")
		} else if len(resp.Choices) > 0 {
			response := resp.Choices[0].Content
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
