package llm

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
	unifiedgenai "google.golang.org/genai"
)

// GenerateAudio generates audio from narration script using the unified genai SDK.
// Uses gemini-2.5-pro-preview-tts with response_modalities: ["audio"] and SpeechConfig.
// If script is empty, skips TTS and returns placeholder (avoids unnecessary API call and zero-length audio).
func (c *Client) GenerateAudio(ctx context.Context, script, audioType string) (*Audio, error) {
	log.Debug().
		Str("audio_type", audioType).
		Int("script_length", len(script)).
		Msg("Generating audio")

	if len(script) == 0 {
		log.Debug().Msg("Script length is zero, skipping TTS and using placeholder")
		return c.placeholderAudio(script)
	}

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
