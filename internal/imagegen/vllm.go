package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// VLLM implements Provider using a local vLLM instance with OpenAI-compatible
// image generation API (POST /v1/images/generations).
type VLLM struct {
	BaseURL string // e.g. "http://localhost:8000"
	APIKey  string // optional, for authenticated endpoints
	Model   string // e.g. "flux-2"
}

func (v *VLLM) Name() string { return "vllm" }

type vllmRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	Size           string `json:"size"`
	N              int    `json:"n"`
	ResponseFormat string `json:"response_format"`
}

type vllmImageData struct {
	B64JSON string `json:"b64_json"`
	URL     string `json:"url"`
}

type vllmResponse struct {
	Data []vllmImageData `json:"data"`
}

func (v *VLLM) Generate(ctx context.Context, req Request, outputDir string) (Result, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return Result{}, fmt.Errorf("creating output dir: %w", err)
	}

	count := req.Count
	if count <= 0 {
		count = 1
	}
	width := req.Width
	if width <= 0 {
		width = 1080
	}
	height := req.Height
	if height <= 0 {
		height = 1350
	}

	// Dimensions must be divisible by 16 for diffusion models.
	width = roundTo16(width)
	height = roundTo16(height)

	model := v.Model
	if model == "" {
		model = "flux-2"
	}

	baseURL := v.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:8000"
	}

	body, _ := json.Marshal(vllmRequest{
		Model:          model,
		Prompt:         req.Prompt,
		Size:           fmt.Sprintf("%dx%d", width, height),
		N:              count,
		ResponseFormat: "b64_json",
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/v1/images/generations", bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if v.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+v.APIKey)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("vllm request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("vllm returned %d: %s", resp.StatusCode, string(respBody))
	}

	var vResp vllmResponse
	if err := json.Unmarshal(respBody, &vResp); err != nil {
		return Result{}, fmt.Errorf("parsing response: %w", err)
	}

	if len(vResp.Data) == 0 {
		return Result{}, fmt.Errorf("no images in response")
	}

	var paths []string
	for i, img := range vResp.Data {
		outPath := filepath.Join(outputDir, fmt.Sprintf("image_%03d.png", i+1))

		if img.B64JSON != "" {
			data, err := base64.StdEncoding.DecodeString(img.B64JSON)
			if err != nil {
				return Result{}, fmt.Errorf("decoding base64 image %d: %w", i+1, err)
			}
			if err := os.WriteFile(outPath, data, 0644); err != nil {
				return Result{}, fmt.Errorf("writing image %d: %w", i+1, err)
			}
		} else if img.URL != "" {
			if err := downloadFile(ctx, img.URL, outPath); err != nil {
				return Result{}, fmt.Errorf("downloading image %d: %w", i+1, err)
			}
		} else {
			continue
		}

		paths = append(paths, outPath)
	}

	if len(paths) == 0 {
		return Result{}, fmt.Errorf("no images decoded from response")
	}

	return Result{ImagePaths: paths}, nil
}

func roundTo16(n int) int {
	return ((n + 15) / 16) * 16
}
