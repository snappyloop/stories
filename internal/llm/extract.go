package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/generative-ai-go/genai"
)

// ExtractContent uses Gemini 3 Pro vision to extract text from images/PDFs.
// System prompt holds instructions; user message is the document/image, sent as-is.
func (c *Client) ExtractContent(ctx context.Context, data []byte, mimeType, inputType string) (string, error) {
	if c.genaiClient == nil {
		return "", fmt.Errorf("genai client not initialized")
	}

	model := c.genaiClient.GenerativeModel(c.modelPro)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(c.buildExtractionSystemPrompt(inputType, mimeType))},
		Role:  "system",
	}

	resp, err := model.GenerateContent(ctx, genai.Blob{MIMEType: mimeType, Data: data})
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

// buildExtractionSystemPrompt returns the system prompt for extraction (instructions only).
// The document or image to extract from is sent by the user as a separate message, as-is.
func (c *Client) buildExtractionSystemPrompt(inputType, mimeType string) string {
	fileType := "document"
	if strings.HasPrefix(mimeType, "image/") {
		fileType = "image"
	}

	// Ask for the essence: what the picture/document explains or teaches, written as that
	// explanation itself—not a description of the picture (e.g. no "this set lists", "the second
	// group centers on"). Output should read like the script or lesson the picture conveys.
	base := fmt.Sprintf("From the %s provided by the user, extract and output only what it explains or teaches. Do not describe the %s (e.g. do not say \"this graphic shows\", \"the second group centers on\", \"this set lists\"). Instead, write the actual message: the lesson, the vocabulary, the explanation—as if you were teaching it or saying it aloud. Paraphrase in your own words where needed; keep the substance and flow so it is useful for creating an enriched story.", fileType, fileType)

	switch inputType {
	case "educational":
		return base + " For educational content: write the concepts and facts as you would explain them (e.g. \"Bee is la abeja, worm is el gusano\" not \"This set lists bee (la abeja)...\"). Keep the logical flow."
	case "financial":
		return base + " For financial content: state the main points, figures, and conclusions as the document presents them. Note any disclaimers or risk warnings briefly."
	case "fictional":
		return base + " For fiction: tell the plot, key characters, and story beats in your own words as a narrative, not as a description of what the image contains."
	default:
		return base + " Keep the overall meaning and flow clear."
	}
}
