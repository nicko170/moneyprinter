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
	"time"
)

// Replicate implements Provider using the Replicate HTTP API.
type Replicate struct {
	APIToken string
	Model    string // e.g. "black-forest-labs/flux-1.1-pro"
}

func (r *Replicate) Name() string { return "replicate" }

type replicatePrediction struct {
	ID     string `json:"id"`
	Status string `json:"status"` // starting, processing, succeeded, failed, canceled
	Output any    `json:"output"` // string URL or []string URLs depending on model
	Error  any    `json:"error"`
}

func (r *Replicate) Generate(ctx context.Context, req Request, outputDir string) (Result, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return Result{}, fmt.Errorf("creating output dir: %w", err)
	}

	model := r.Model
	if model == "" {
		model = "black-forest-labs/flux-1.1-pro"
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

	// Build input — include ref images as data URIs if provided.
	input := map[string]any{
		"prompt":       req.Prompt,
		"width":        width,
		"height":       height,
		"num_outputs":  count,
		"output_format": "png",
	}

	// Attach first reference image if available (for models that support it).
	if len(req.RefImages) > 0 {
		dataURI, err := fileToDataURI(req.RefImages[0])
		if err == nil {
			input["image"] = dataURI
		}
	}

	body, _ := json.Marshal(map[string]any{
		"model": model,
		"input": input,
	})

	// Create prediction.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.replicate.com/v1/predictions", bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+r.APIToken)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Prefer", "wait") // try synchronous wait (up to 60s)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("replicate request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return Result{}, fmt.Errorf("replicate returned %d: %s", resp.StatusCode, string(respBody))
	}

	var pred replicatePrediction
	if err := json.Unmarshal(respBody, &pred); err != nil {
		return Result{}, fmt.Errorf("parsing prediction: %w", err)
	}

	// Poll until complete if not already done.
	if pred.Status != "succeeded" && pred.Status != "failed" {
		pred, err = r.pollPrediction(ctx, pred.ID)
		if err != nil {
			return Result{}, err
		}
	}

	if pred.Status == "failed" {
		return Result{}, fmt.Errorf("replicate prediction failed: %v", pred.Error)
	}

	// Extract output URLs.
	urls := extractOutputURLs(pred.Output)
	if len(urls) == 0 {
		return Result{}, fmt.Errorf("no images in prediction output")
	}

	// Download images.
	var paths []string
	for i, u := range urls {
		outPath := filepath.Join(outputDir, fmt.Sprintf("image_%03d.png", i+1))
		if err := downloadFile(ctx, u, outPath); err != nil {
			return Result{}, fmt.Errorf("downloading image %d: %w", i+1, err)
		}
		paths = append(paths, outPath)
	}

	return Result{ImagePaths: paths}, nil
}

func (r *Replicate) pollPrediction(ctx context.Context, id string) (replicatePrediction, error) {
	url := fmt.Sprintf("https://api.replicate.com/v1/predictions/%s", id)
	for {
		select {
		case <-ctx.Done():
			return replicatePrediction{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}

		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+r.APIToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return replicatePrediction{}, fmt.Errorf("polling: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var pred replicatePrediction
		json.Unmarshal(body, &pred)

		if pred.Status == "succeeded" || pred.Status == "failed" || pred.Status == "canceled" {
			return pred, nil
		}
	}
}

func extractOutputURLs(output any) []string {
	switch v := output.(type) {
	case string:
		return []string{v}
	case []any:
		var urls []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				urls = append(urls, s)
			}
		}
		return urls
	}
	return nil
}

func downloadFile(ctx context.Context, url, outPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func fileToDataURI(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	mime := "image/png"
	ext := filepath.Ext(path)
	switch ext {
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".webp":
		mime = "image/webp"
	}
	return fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data)), nil
}
