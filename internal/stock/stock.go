package stock

import (
	"context"
	"sync"
)

// Candidate is a stock video result from any source.
type Candidate struct {
	URL          string
	ThumbnailURL string
	Duration     int
	Source       string
	Score        float64 // CLIP similarity (0–1); 0 = unscored
}

// Config holds API keys for all stock sources and the reranker.
type Config struct {
	PexelsAPIKey  string
	PixabayAPIKey string
	JinaAPIKey    string // optional — enables CLIP reranking
}

// SearchAll queries all configured sources in parallel for a visual scene
// description, then reranks the combined pool by CLIP similarity.
// Returns deduplicated candidates, best first.
func SearchAll(ctx context.Context, query string, cfg Config, perPage, minDuration int) []Candidate {
	var mu sync.Mutex
	var all []Candidate
	var wg sync.WaitGroup

	if cfg.PexelsAPIKey != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, err := searchPexels(ctx, query, cfg.PexelsAPIKey, perPage, minDuration)
			if err == nil && len(results) > 0 {
				mu.Lock()
				all = append(all, results...)
				mu.Unlock()
			}
		}()
	}

	if cfg.PixabayAPIKey != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, err := searchPixabay(ctx, query, cfg.PixabayAPIKey, perPage, minDuration)
			if err == nil && len(results) > 0 {
				mu.Lock()
				all = append(all, results...)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(all) == 0 {
		return nil
	}

	// Deduplicate by URL.
	seen := make(map[string]bool, len(all))
	deduped := all[:0]
	for _, c := range all {
		if !seen[c.URL] {
			seen[c.URL] = true
			deduped = append(deduped, c)
		}
	}
	all = deduped

	// Rerank by CLIP visual similarity if a Jina key is configured.
	return Rerank(ctx, query, all, cfg.JinaAPIKey)
}
