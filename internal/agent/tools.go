package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/moneyprinter/internal/inference"
)

// Source is a web source found during research.
type Source struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Tool definitions for the research agent.
var (
	WebSearchTool = inference.Tool{
		Type: "function",
		Function: inference.ToolFunction{
			Name:        "web_search",
			Description: "Search the web for current information on a topic. Use this to find facts, statistics, and recent news.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"The search query"}},"required":["query"]}`),
		},
	}

	FetchURLTool = inference.Tool{
		Type: "function",
		Function: inference.ToolFunction{
			Name:        "fetch_url",
			Description: "Fetch and read the text content of a web page to get detailed information from a source.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"The URL to fetch"}},"required":["url"]}`),
		},
	}

	SubmitScriptTool = inference.Tool{
		Type: "function",
		Function: inference.ToolFunction{
			Name:        "submit_script",
			Description: "Submit the final video script once you have completed your research. Call this exactly once when ready.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"topic": {
						"type": "string",
						"description": "A short, catchy title for this video (under 60 chars). For series episodes, this is the specific topic you chose."
					},
					"script": {
						"type": "string",
						"description": "The complete video script. Plain ASCII text, 80-120 words, no markdown. One sentence per line (use \\n between sentences). Each line becomes a subtitle card."
					},
					"sources": {
						"type": "array",
						"description": "Sources used during research",
						"items": {
							"type": "object",
							"properties": {
								"title":   {"type": "string"},
								"url":     {"type": "string"},
								"snippet": {"type": "string"}
							}
						}
					}
				},
				"required": ["topic", "script", "sources"]
			}`),
		},
	}
)

var braveHTTPClient = &http.Client{Timeout: 10 * time.Second}

type braveResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

// BraveSearch queries the Brave Search API and returns a human-readable result string.
// If apiKey is empty, returns a message telling the LLM to use training data.
func BraveSearch(ctx context.Context, apiKey, query string) string {
	if apiKey == "" {
		return "Web search is not configured (no Brave API key). Use your training knowledge to write the script."
	}

	endpoint := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(query) + "&count=5&search_lang=en"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Sprintf("Search failed: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	resp, err := braveHTTPClient.Do(req)
	if err != nil {
		return fmt.Sprintf("Search failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Sprintf("Search API returned %d: %s", resp.StatusCode, string(body))
	}

	var result braveResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Sprintf("Failed to parse search results: %v", err)
	}

	if len(result.Web.Results) == 0 {
		return "No results found for: " + query
	}

	var sb strings.Builder
	for i, r := range result.Web.Results {
		fmt.Fprintf(&sb, "Result %d: %s\nURL: %s\nSnippet: %s\n\n", i+1, r.Title, r.URL, r.Description)
	}
	return strings.TrimSpace(sb.String())
}

var fetchHTTPClient = &http.Client{Timeout: 12 * time.Second}

// FetchURL fetches a URL and returns its plain text content.
// Never returns an error — failures are returned as descriptive strings so the agent continues.
func FetchURL(ctx context.Context, rawURL string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Sprintf("Error fetching URL: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; MoneyPrinter/1.0)")
	req.Header.Set("Accept", "text/html,text/plain")

	resp, err := fetchHTTPClient.Do(req)
	if err != nil {
		return fmt.Sprintf("Error fetching URL: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("URL returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return fmt.Sprintf("Error reading URL body: %v", err)
	}

	text := extractText(string(body))
	if len(text) > 4000 {
		text = text[:4000] + "… [truncated]"
	}
	return text
}

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reTags        = regexp.MustCompile(`<[^>]+>`)
	reWhitespace  = regexp.MustCompile(`\s+`)
)

func extractText(html string) string {
	html = reScriptStyle.ReplaceAllString(html, " ")
	html = reTags.ReplaceAllString(html, " ")
	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&nbsp;", " ")
	html = strings.ReplaceAll(html, "&#39;", "'")
	html = strings.ReplaceAll(html, "&quot;", `"`)
	html = reWhitespace.ReplaceAllString(html, " ")
	return strings.TrimSpace(html)
}
