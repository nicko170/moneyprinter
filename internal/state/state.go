package state

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type State struct {
	InferenceURL    string `json:"inference_url"`
	InferenceAPIKey string `json:"inference_api_key"`
	InferenceModel  string `json:"inference_model"`
	PexelsAPIKey    string `json:"pexels_api_key"`

	// TTS configuration
	TTSProvider         string `json:"tts_provider"`           // "tiktok" or "chatterbox"
	TTSTikTokSessionID  string `json:"tts_tiktok_session_id"`
	TTSChatterboxURL    string `json:"tts_chatterbox_url"`
	TTSChatterboxVoice  string `json:"tts_chatterbox_voice_ref"`

	AssemblyAIKey string `json:"assembly_ai_api_key"`
	ImageMagick   string `json:"imagemagick_binary"`
	OutputDir     string `json:"output_dir"`
	TempDir       string `json:"temp_dir"`
	FontsDir      string `json:"fonts_dir"`
	SongsDir      string `json:"songs_dir"`
}

func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading state file: %w", err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}

	s.applyDefaults()
	return &s, nil
}

func (s *State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling state: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func (s *State) applyDefaults() {
	if s.TTSProvider == "" {
		s.TTSProvider = "tiktok"
	}
	if s.TTSChatterboxURL == "" {
		s.TTSChatterboxURL = "http://localhost:7860"
	}
	if s.OutputDir == "" {
		s.OutputDir = "./output"
	}
	if s.TempDir == "" {
		s.TempDir = "./temp"
	}
	if s.FontsDir == "" {
		s.FontsDir = "./fonts"
	}
	if s.SongsDir == "" {
		s.SongsDir = "./songs"
	}
}

// Redacted returns a copy with sensitive fields masked for API responses.
func (s *State) Redacted() State {
	r := *s
	r.InferenceAPIKey = redact(r.InferenceAPIKey)
	r.PexelsAPIKey = redact(r.PexelsAPIKey)
	r.TTSTikTokSessionID = redact(r.TTSTikTokSessionID)
	r.AssemblyAIKey = redact(r.AssemblyAIKey)
	return r
}

func redact(val string) string {
	if val == "" {
		return ""
	}
	if len(val) <= 8 {
		return strings.Repeat("*", len(val))
	}
	return val[:4] + strings.Repeat("*", len(val)-8) + val[len(val)-4:]
}
