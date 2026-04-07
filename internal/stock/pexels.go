package stock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type pexelsVideoFile struct {
	Link   string `json:"link"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type pexelsVideo struct {
	Duration   int               `json:"duration"`
	Image      string            `json:"image"` // thumbnail
	VideoFiles []pexelsVideoFile `json:"video_files"`
}

type pexelsResponse struct {
	Videos []pexelsVideo `json:"videos"`
}

func searchPexels(ctx context.Context, query, apiKey string, perPage, minDuration int) ([]Candidate, error) {
	u := fmt.Sprintf("https://api.pexels.com/videos/search?query=%s&per_page=%d",
		url.QueryEscape(query), perPage)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating pexels request: %w", err)
	}
	req.Header.Set("Authorization", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pexels request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading pexels response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pexels returned %d: %s", resp.StatusCode, string(body))
	}

	var result pexelsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing pexels response: %w", err)
	}

	var candidates []Candidate
	for _, v := range result.Videos {
		if v.Duration < minDuration {
			continue
		}
		bestURL, bestRes := "", 0
		for _, f := range v.VideoFiles {
			res := f.Width * f.Height
			if res > bestRes && f.Link != "" {
				bestURL = f.Link
				bestRes = res
			}
		}
		if bestURL != "" {
			candidates = append(candidates, Candidate{
				URL:          bestURL,
				ThumbnailURL: v.Image,
				Duration:     v.Duration,
				Source:       "pexels",
			})
		}
	}

	return candidates, nil
}
