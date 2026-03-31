package pexels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type videoFile struct {
	Link   string `json:"link"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type video struct {
	Duration   int         `json:"duration"`
	VideoFiles []videoFile `json:"video_files"`
}

type searchResponse struct {
	Videos []video `json:"videos"`
}

// SearchVideos queries the Pexels API and returns direct download URLs for the
// highest-resolution version of each matching video.
func SearchVideos(ctx context.Context, query, apiKey string, perPage, minDuration int) ([]string, error) {
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

	var result searchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing pexels response: %w", err)
	}

	var urls []string
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
			urls = append(urls, bestURL)
		}
	}

	return urls, nil
}
