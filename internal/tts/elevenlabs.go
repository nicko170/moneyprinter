package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const elevenLabsBaseURL = "https://api.elevenlabs.io/v1/text-to-speech"

// ElevenLabs implements Provider using the ElevenLabs TTS API.
type ElevenLabs struct {
	APIKey string
}

type elevenLabsRequest struct {
	Text    string                 `json:"text"`
	ModelID string                 `json:"model_id"`
	VoiceSettings map[string]any `json:"voice_settings,omitempty"`
}

func (e *ElevenLabs) Synthesize(ctx context.Context, text, voice, outPath string) error {
	if voice == "" {
		voice = "21m00Tcm4TlvDq8ikWAM" // Rachel (default)
	}

	payload := elevenLabsRequest{
		Text:    text,
		ModelID: "eleven_multilingual_v2",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling ElevenLabs request: %w", err)
	}

	url := elevenLabsBaseURL + "/" + voice
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating ElevenLabs request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", e.APIKey)
	req.Header.Set("Accept", "audio/mpeg")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ElevenLabs request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ElevenLabs returned %d: %s", resp.StatusCode, string(errBody))
	}

	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading ElevenLabs response: %w", err)
	}

	return os.WriteFile(outPath, audio, 0644)
}
