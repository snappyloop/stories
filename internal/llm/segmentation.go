package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/rivo/uniseg"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/tmc/langchaingo/llms"
)

// SegmentText segments text into logical parts.
// Uses 3.0 flash first, then 2.5 flash; if both fail or return no valid response, returns one segment (whole text).
// segmentsCount is normalized to at least 1 to avoid division-by-zero in merge logic; callers may pass 0 from gRPC/jobs.
func (c *Client) SegmentText(ctx context.Context, text string, segmentsCount int, inputType string) ([]*Segment, error) {
	if segmentsCount < 1 {
		segmentsCount = 1
	}
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

	systemPrompt := c.buildSegmentSystemPrompt(segmentsCount, inputType)

	// Log segmentation request (system prompt + user message length)
	log.Info().
		Str("caller", "SegmentText").
		Int("system_prompt_len", len(systemPrompt)).
		Str("segment_system_prompt", systemPrompt).
		Int("user_text_len", len(text)).
		Msg("SegmentText LLM request (system + user message)")

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
		segments, err := c.trySegmentWithModel(ctx, tier.name, tier.modelName, tier.langModel, systemPrompt, text, segmentsCount, inputType)
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

// buildSegmentSystemPrompt returns the system prompt for segmentation (instructions only).
// The text to analyze is sent separately as a user message, as-is.
func (c *Client) buildSegmentSystemPrompt(segmentsCount int, inputType string) string {
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

Task: Identify ALL natural breakpoints in the text where the content logically divides. %s

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

A text to analyze will be provided by the user.`, styleGuidance, segmentsCount-1)
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
// requestedCount must be at least 1; values < 1 are treated as 1 to avoid division by zero.
func mergeBoundariesIntoSegments(boundaries []int, byteOffsets []int, text string, requestedCount int) []*Segment {
	if requestedCount < 1 {
		requestedCount = 1
	}
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
// System prompt holds instructions; user message is the text to analyze, sent as-is.
// When genaiClient is available and modelName is set, uses genai with ResponseSchema; otherwise uses langchaingo with JSON MIME type.
func (c *Client) trySegmentWithModel(ctx context.Context, modelTier string, modelName string, langModel llms.Model, systemPrompt, userText string, requestedCount int, inputType string) ([]*Segment, error) {
	var response string

	if c.genaiClient != nil && modelName != "" {
		// Use genai client with response schema for structured JSON output
		model := c.genaiClient.GenerativeModel(modelName)
		model.SetTemperature(0.3)
		model.SetMaxOutputTokens(2000)
		model.ResponseMIMEType = "application/json"
		model.ResponseSchema = segmentResponseSchema()
		model.SystemInstruction = genai.NewUserContent(genai.Text(systemPrompt))

		resp, err := model.GenerateContent(ctx, genai.Text(userText))
		if err != nil {
			return nil, err
		}
		response = c.extractTextFromGenaiResponse(resp)
	} else if langModel != nil {
		// Fallback: langchaingo with system + user messages and JSON MIME type (no schema)
		messages := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: systemPrompt}}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: userText}}},
		}
		resp, err := langModel.GenerateContent(ctx, messages,
			llms.WithTemperature(0.3),
			llms.WithMaxTokens(2000),
			llms.WithResponseMIMEType("application/json"),
		)
		if err != nil {
			return nil, err
		}
		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("empty response from model")
		}
		response = resp.Choices[0].Content
	} else {
		return nil, fmt.Errorf("no segment model available")
	}

	// Log segmentation response output
	log.Info().
		Str("caller", "SegmentText").
		Str("model_tier", modelTier).
		Str("input_type", inputType).
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
	byteOffsets := runeToByteOffsets(userText)
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
		Int("text_bytes", len(userText)).
		Int("text_graphemes", numGraphemes).
		Int("boundaries_returned", len(result.Boundaries)).
		Int("requested_segments", requestedCount).
		Interface("boundaries_graphemes", result.Boundaries).
		Msg("LLM returned boundaries")

	// Validate boundaries are at sentence endings (. ! ?)
	validatedBoundaries := validateAndAdjustBoundaries(result.Boundaries, userText, byteOffsets)

	log.Info().
		Str("caller", "SegmentText").
		Interface("validated_boundaries", validatedBoundaries).
		Msg("Boundaries after validation")

	// Cache the validated boundaries for future use
	if c.boundaryCache != nil {
		textHash := database.TextHash(userText)
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
	segments := mergeBoundariesIntoSegments(validatedBoundaries, byteOffsets, userText, requestedCount)

	log.Info().
		Str("caller", "SegmentText").
		Str("model_tier", modelTier).
		Str("input_type", inputType).
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
