package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to any OpenAI-compatible /v1/chat/completions endpoint.
type Client struct {
	BaseURL string
	Model   string
	APIKey  string
	Timeout time.Duration
	HTTP    *http.Client
}

func NewClient(baseURL, model, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		APIKey:  apiKey,
		Timeout: 180 * time.Second,
		HTTP:    &http.Client{},
	}
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Generate sends a prompt and returns the assistant's text response.
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	body := chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshalling request: %w", err)
	}

	base := c.BaseURL
	if strings.HasSuffix(base, "/v1") {
		base = base + "/chat/completions"
	} else {
		base = base + "/v1/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending request to %s: %w", base, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("inference API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result chatResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("inference error: %s", result.Error.Message)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("inference returned no choices")
	}

	content := strings.TrimSpace(result.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("inference returned empty response")
	}

	return content, nil
}
