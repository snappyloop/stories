package llm

import (
	"bytes"
	"context"
	"fmt"
	"reflect"

	"github.com/google/generative-ai-go/genai"
	"github.com/rs/zerolog/log"
)

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
