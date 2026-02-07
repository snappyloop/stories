package llm

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"
	unifiedgenai "google.golang.org/genai"
)

const maxFactCheckLen = 1024 // made larger than requested in case LLM returns more than requested

const factCheckPrompt = `You are a fact-checker. Analyze the following text and check all factual claims against well-known and trusted sources using web search.

If any fact is incorrect or misleading: briefly describe the issue only (max 512 characters total).
If all facts are correct: your entire response must be exactly the single character 0. Do not add any explanation, summary, or other text—just 0.`

// FactCheckSegment checks the given segment text for factual accuracy using Google Search grounding.
// Returns empty string if all facts are correct (or model returned "0"), or a short description (up to 1024 chars) otherwise.
func (c *Client) FactCheckSegment(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	if c.unifiedClient == nil {
		log.Debug().Msg("FactCheckSegment: unified client not configured, skipping")
		return "", nil
	}

	contents := unifiedgenai.Text(factCheckPrompt + "\n\nText to check:\n" + text)
	config := &unifiedgenai.GenerateContentConfig{
		Tools: []*unifiedgenai.Tool{
			{GoogleSearch: &unifiedgenai.GoogleSearch{}},
		},
	}

	log.Debug().Str("model", c.modelFlash).Int("text_len", len(text)).Msg("Fact-checking segment with Google Search grounding")
	result, err := c.unifiedClient.Models.GenerateContent(ctx, c.modelFlash, contents, config)
	if err != nil {
		return "", err
	}

	out := strings.TrimSpace(result.Text())
	// Treat empty, "0", or responses that only confirm no issues (e.g. end with "0" or say no inaccuracies) as no issue
	if out == "" || out == "0" {
		return "", nil
	}
	if strings.HasSuffix(out, " 0") || strings.HasSuffix(out, ". 0") {
		return "", nil
	}
	if len(out) > maxFactCheckLen {
		out = out[:maxFactCheckLen]
	}
	return out, nil
}
