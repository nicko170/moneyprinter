package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

var tiktokEndpoints = []string{
	"https://tiktok-tts.weilnet.workers.dev/api/generation",
	"https://tiktoktts.com/api/tiktok-tts",
}

const tiktokCharLimit = 300

// TikTok implements Provider using the TikTok TTS API.
type TikTok struct{}

func (t *TikTok) Synthesize(ctx context.Context, text, voice, outPath string) error {
	endpoint, err := findAvailableEndpoint(ctx)
	if err != nil {
		return err
	}

	if len(text) <= tiktokCharLimit {
		audioData, err := tiktokGenerate(ctx, endpoint, text, voice)
		if err != nil {
			return err
		}
		return os.WriteFile(outPath, audioData, 0644)
	}

	// Split long text into chunks and process concurrently.
	chunks := splitText(text, tiktokCharLimit-1)
	results := make([][]byte, len(chunks))
	errs := make([]error, len(chunks))
	var wg sync.WaitGroup

	for i, chunk := range chunks {
		wg.Add(1)
		go func(idx int, txt string) {
			defer wg.Done()
			data, err := tiktokGenerate(ctx, endpoint, txt, voice)
			if err != nil {
				// Retry with alternate endpoint.
				for _, alt := range tiktokEndpoints {
					if alt != endpoint {
						data, err = tiktokGenerate(ctx, alt, txt, voice)
						break
					}
				}
			}
			if err != nil {
				errs[idx] = err
				return
			}
			results[idx] = data
		}(i, chunk)
	}
	wg.Wait()

	// Check for any chunk failures.
	for i, err := range errs {
		if err != nil {
			return fmt.Errorf("TTS chunk %d failed: %w", i, err)
		}
	}

	// Concatenate all audio chunks.
	var combined []byte
	for _, data := range results {
		combined = append(combined, data...)
	}
	return os.WriteFile(outPath, combined, 0644)
}

func findAvailableEndpoint(ctx context.Context) (string, error) {
	for _, ep := range tiktokEndpoints {
		// Check the base URL (everything before /api).
		baseURL := strings.Split(ep, "/a")[0]
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
		if err != nil {
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return ep, nil
		}
	}
	return "", fmt.Errorf("no TikTok TTS endpoints available")
}

func tiktokGenerate(ctx context.Context, endpoint, text, voice string) ([]byte, error) {
	payload, _ := json.Marshal(map[string]string{
		"text":  text,
		"voice": voice,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating TTS request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("TTS request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading TTS response: %w", err)
	}

	// Both endpoints return JSON with base64-encoded audio.
	// Endpoint 0: {"data": "base64..."}
	// Endpoint 1: {"data": "audio/mpeg;base64,base64..."}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing TTS response: %w", err)
	}

	dataStr, ok := result["data"].(string)
	if !ok || dataStr == "" || dataStr == "error" {
		return nil, fmt.Errorf("TTS returned error or empty data")
	}

	// Strip data URI prefix if present.
	if idx := strings.Index(dataStr, ","); idx != -1 && strings.Contains(dataStr[:idx], "base64") {
		dataStr = dataStr[idx+1:]
	}

	decoded, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		return nil, fmt.Errorf("decoding TTS audio: %w", err)
	}

	return decoded, nil
}

// splitText splits text into chunks at word boundaries, each at most maxLen chars.
func splitText(text string, maxLen int) []string {
	words := strings.Fields(text)
	var chunks []string
	var current strings.Builder

	for _, word := range words {
		if current.Len() > 0 && current.Len()+1+len(word) > maxLen {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(word)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}
