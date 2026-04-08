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
	PixabayAPIKey   string `json:"pixabay_api_key"`
	JinaAPIKey      string `json:"jina_api_key"`

	// TTS configuration
	TTSProvider          string `json:"tts_provider"`            // "elevenlabs", "chatterbox", or "tiktok"
	TTSElevenLabsAPIKey  string `json:"tts_elevenlabs_api_key"`
	TTSTikTokSessionID   string `json:"tts_tiktok_session_id"`
	TTSChatterboxURL     string `json:"tts_chatterbox_url"`
	TTSChatterboxVoice   string `json:"tts_chatterbox_voice_ref"`

	BraveSearchAPIKey string `json:"brave_search_api_key"`

	// YouTube publishing
	YouTubeClientID     string `json:"youtube_client_id"`
	YouTubeClientSecret string `json:"youtube_client_secret"`
	YouTubeRefreshToken string `json:"youtube_refresh_token"`
	YouTubeChannelID    string `json:"youtube_channel_id"`
	YouTubeAutoPublish  bool   `json:"youtube_auto_publish"`

	// Image generation
	ImageGenProvider string `json:"imagegen_provider"` // "vllm", "replicate", or "openai"
	ImageGenURL      string `json:"imagegen_url"`      // base URL for vLLM (e.g. "http://localhost:8000")
	ImageGenAPIKey   string `json:"imagegen_api_key"`
	ImageGenModel    string `json:"imagegen_model"`    // e.g. "flux-2", "black-forest-labs/flux-1.1-pro"
	AssemblyAIKey     string `json:"assembly_ai_api_key"`
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
		s.TTSProvider = "elevenlabs"
	}
	if s.ImageGenProvider == "" {
		s.ImageGenProvider = "vllm"
	}
	if s.ImageGenURL == "" {
		s.ImageGenURL = "http://localhost:8000"
	}
	if s.ImageGenModel == "" {
		s.ImageGenModel = "flux-2"
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
	r.PixabayAPIKey = redact(r.PixabayAPIKey)
	r.JinaAPIKey = redact(r.JinaAPIKey)
	r.YouTubeClientSecret = redact(r.YouTubeClientSecret)
	r.YouTubeRefreshToken = redact(r.YouTubeRefreshToken)
	r.ImageGenAPIKey = redact(r.ImageGenAPIKey)
	r.TTSElevenLabsAPIKey = redact(r.TTSElevenLabsAPIKey)
	r.TTSTikTokSessionID = redact(r.TTSTikTokSessionID)
	r.BraveSearchAPIKey = redact(r.BraveSearchAPIKey)
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
