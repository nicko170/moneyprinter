package stock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
)

type jinaInput struct {
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
}

type jinaRequest struct {
	Model string      `json:"model"`
	Input []jinaInput `json:"input"`
}

type jinaEmbedding struct {
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type jinaResponse struct {
	Data []jinaEmbedding `json:"data"`
}

// Rerank scores each candidate by CLIP visual similarity to the query using
// Jina AI's jina-clip-v2 model, then returns candidates sorted best-first.
// If jinaAPIKey is empty or fewer than 2 candidates exist, the pool is
// returned as-is (already deduplicated).
func Rerank(ctx context.Context, query string, candidates []Candidate, jinaAPIKey string) []Candidate {
	if jinaAPIKey == "" || len(candidates) < 2 {
		return candidates
	}

	// Build request: first input is the query text, rest are image URLs.
	// Only include candidates that have a thumbnail URL — others keep score 0.
	type indexed struct {
		jinaIdx int
		candIdx int
	}
	var withThumb []indexed
	inputs := []jinaInput{{Text: query}}
	for i, c := range candidates {
		if c.ThumbnailURL != "" {
			inputs = append(inputs, jinaInput{URL: c.ThumbnailURL})
			withThumb = append(withThumb, indexed{len(inputs) - 1, i})
		}
	}

	if len(withThumb) == 0 {
		return candidates
	}

	body, _ := json.Marshal(jinaRequest{
		Model: "jina-clip-v2",
		Input: inputs,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.jina.ai/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return candidates
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jinaAPIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return candidates
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		return candidates
	}

	var jinaResp jinaResponse
	if err := json.Unmarshal(respBody, &jinaResp); err != nil {
		return candidates
	}

	// Build index → embedding map.
	embMap := make(map[int][]float64, len(jinaResp.Data))
	for _, d := range jinaResp.Data {
		embMap[d.Index] = d.Embedding
	}

	queryEmb, ok := embMap[0]
	if !ok || len(queryEmb) == 0 {
		return candidates
	}

	// Score candidates that have thumbnails.
	for _, idx := range withThumb {
		imgEmb, ok := embMap[idx.jinaIdx]
		if !ok {
			continue
		}
		candidates[idx.candIdx].Score = cosine(queryEmb, imgEmb)
	}

	// Sort by score descending (unscored candidates with Score=0 go last).
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	return candidates
}

func cosine(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// scoreLabel returns a human-readable description for a CLIP score — useful for logs.
func scoreLabel(s float64) string {
	switch {
	case s >= 0.35:
		return fmt.Sprintf("%.2f (great)", s)
	case s >= 0.25:
		return fmt.Sprintf("%.2f (good)", s)
	case s >= 0.15:
		return fmt.Sprintf("%.2f (fair)", s)
	default:
		return fmt.Sprintf("%.2f (weak)", s)
	}
}

var _ = scoreLabel // exported for use in logging if needed
