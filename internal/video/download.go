package video

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// DownloadVideo fetches a video URL and saves it to dir, returning the file path.
func DownloadVideo(ctx context.Context, videoURL, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating download dir: %w", err)
	}

	outPath := filepath.Join(dir, uuid.New().String()+".mp4")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating download request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading video: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(outPath)
		return "", fmt.Errorf("writing video file: %w", err)
	}

	return outPath, nil
}
