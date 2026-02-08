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
	model.SystemInstruction = genai.NewUserContent(genai.Text(c.buildExtractionSystemPrompt(inputType, mimeType)))

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
// The document or image to summarize is sent by the user as a separate message, as-is.
func (c *Client) buildExtractionSystemPrompt(inputType, mimeType string) string {
	fileType := "document"
	if strings.HasPrefix(mimeType, "image/") {
		fileType = "image"
	}

	base := fmt.Sprintf("Summarize the %s provided by the user in your own words. Describe the main content, ideas, and structure. Do not quote or transcribe long passages verbatim; paraphrase and condense so the summary is useful for creating an enriched story version.", fileType)

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
