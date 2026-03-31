package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Chatterbox implements Provider by calling a Chatterbox Gradio server.
type Chatterbox struct {
	BaseURL      string // e.g. "http://localhost:7860"
	VoiceRefPath string // path to reference audio WAV for voice cloning
}

// gradioResponse represents the Gradio API predict response format.
type gradioResponse struct {
	Data []interface{} `json:"data"`
}

func (c *Chatterbox) Synthesize(ctx context.Context, text, voice, outPath string) error {
	return c.synthesizeViaJSON(ctx, text, outPath)
}

func (c *Chatterbox) synthesizeViaJSON(ctx context.Context, text, outPath string) error {
	url := strings.TrimRight(c.BaseURL, "/") + "/api/predict"

	// Gradio JSON API: send fn_index and data array.
	payload := map[string]interface{}{
		"fn_index": 0,
		"data": []interface{}{
			nil, // model_state (loaded server-side)
			text,
			c.VoiceRefPath, // reference audio file path
			0.8,            // temperature
			0,              // seed (0 = random)
			0.0,            // min_p
			0.95,           // top_p
			1000,           // top_k
			1.2,            // repetition_penalty
			true,           // normalize loudness
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling gradio request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("creating gradio request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("gradio request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading gradio response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gradio returned %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse the response to get the audio file path or data.
	var result gradioResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parsing gradio response: %w", err)
	}

	if len(result.Data) == 0 {
		return fmt.Errorf("gradio returned no data")
	}

	// Gradio audio output is typically a tuple (sample_rate, filepath) or a file path.
	// Try to extract the file info.
	audioData, ok := result.Data[0].(map[string]interface{})
	if ok {
		// Gradio 4+ returns {"name": "/tmp/gradio/xxx.wav", "data": null, ...}
		if name, exists := audioData["name"].(string); exists {
			return c.downloadFile(ctx, name, outPath)
		}
	}

	return fmt.Errorf("unexpected gradio response format: %s", string(respBody))
}

func (c *Chatterbox) downloadFile(ctx context.Context, gradioPath, outPath string) error {
	// Gradio serves temp files at /file=<path>
	url := strings.TrimRight(c.BaseURL, "/") + "/file=" + gradioPath

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading from gradio: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gradio file download returned %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return err
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

func toGradioPayload(text, voicePath string) string {
	data, _ := json.Marshal([]interface{}{text, voicePath})
	return string(data)
}
