package stock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type pixabayVideoVariant struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type pixabayVideoSizes struct {
	Large  pixabayVideoVariant `json:"large"`
	Medium pixabayVideoVariant `json:"medium"`
	Small  pixabayVideoVariant `json:"small"`
	Tiny   pixabayVideoVariant `json:"tiny"`
}

type pixabayHit struct {
	Duration  int               `json:"duration"`
	PictureID string            `json:"picture_id"`
	Videos    pixabayVideoSizes `json:"videos"`
}

type pixabayResponse struct {
	Hits []pixabayHit `json:"hits"`
}

func searchPixabay(ctx context.Context, query, apiKey string, perPage, minDuration int) ([]Candidate, error) {
	u := fmt.Sprintf("https://pixabay.com/api/videos/?key=%s&q=%s&per_page=%d&min_duration=%d",
		url.QueryEscape(apiKey), url.QueryEscape(query), perPage, minDuration)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating pixabay request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pixabay request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading pixabay response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pixabay returned %d: %s", resp.StatusCode, string(body))
	}

	var result pixabayResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing pixabay response: %w", err)
	}

	var candidates []Candidate
	for _, hit := range result.Hits {
		// Pick highest-resolution available URL.
		videoURL := ""
		for _, v := range []pixabayVideoVariant{hit.Videos.Large, hit.Videos.Medium, hit.Videos.Small, hit.Videos.Tiny} {
			if v.URL != "" {
				videoURL = v.URL
				break
			}
		}
		if videoURL == "" {
			continue
		}

		thumbnailURL := ""
		if hit.PictureID != "" {
			thumbnailURL = fmt.Sprintf("https://i.vimeocdn.com/video/%s_295x166.jpg", hit.PictureID)
		}

		candidates = append(candidates, Candidate{
			URL:          videoURL,
			ThumbnailURL: thumbnailURL,
			Duration:     hit.Duration,
			Source:       "pixabay",
		})
	}

	return candidates, nil
}
