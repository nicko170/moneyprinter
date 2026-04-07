package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Tool represents an OpenAI-compatible function tool definition.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction describes a callable function.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema object
}

// Message is a chat message supporting text, tool calls, and tool results.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // for role:"tool"
	Name       string     `json:"name,omitempty"`         // for role:"tool"
}

// ToolCall is an LLM-requested function invocation.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the name and JSON-encoded arguments for a tool call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ChatResult is the response from a single Chat call.
type ChatResult struct {
	Message    Message
	StopReason string // "stop", "tool_calls", "length", etc.
}

type chatWithToolsRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Stream   bool      `json:"stream"`
}

// streamChunk is the shape of each SSE data payload.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Chat sends a multi-turn conversation with optional tools, using streaming.
// Streaming is required for tool calls on many hosted models.
// The caller is responsible for the tool-use loop.
func (c *Client) Chat(ctx context.Context, messages []Message, tools []Tool) (ChatResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	body := chatWithToolsRequest{
		Model:    c.Model,
		Messages: messages,
		Stream:   true, // streaming required for tool calls
	}
	if len(tools) > 0 {
		body.Tools = tools
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return ChatResult{}, fmt.Errorf("marshalling request: %w", err)
	}

	base := c.BaseURL
	if strings.HasSuffix(base, "/v1") {
		base = base + "/chat/completions"
	} else {
		base = base + "/v1/chat/completions"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewReader(payload))
	if err != nil {
		return ChatResult{}, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ChatResult{}, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ChatResult{}, fmt.Errorf("inference API returned %d: %s", resp.StatusCode, string(body))
	}

	return parseStream(resp.Body)
}

// parseStream reads an SSE stream and accumulates the full message.
func parseStream(r io.Reader) (ChatResult, error) {
	var (
		contentBuf strings.Builder
		// toolCalls accumulates partial tool call chunks by index.
		toolCalls  = map[int]*ToolCall{}
		finishReason string
	)

	scanner := bufio.NewScanner(r)
	// Increase buffer size for large chunks (long tool arguments).
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if line == "data: [DONE]" {
			break
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		raw := strings.TrimPrefix(line, "data: ")
		var chunk streamChunk
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			continue // skip malformed chunks
		}

		if chunk.Error != nil {
			return ChatResult{}, fmt.Errorf("stream error: %s", chunk.Error.Message)
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]

		if choice.FinishReason != nil && *choice.FinishReason != "" {
			finishReason = *choice.FinishReason
		}

		delta := choice.Delta

		// Accumulate text content.
		if delta.Content != "" {
			contentBuf.WriteString(delta.Content)
		}

		// Accumulate tool calls by index.
		for _, tc := range delta.ToolCalls {
			existing, ok := toolCalls[tc.Index]
			if !ok {
				existing = &ToolCall{Type: "function"}
				toolCalls[tc.Index] = existing
			}
			if tc.ID != "" {
				existing.ID = tc.ID
			}
			if tc.Type != "" {
				existing.Type = tc.Type
			}
			if tc.Function.Name != "" {
				existing.Function.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				existing.Function.Arguments += tc.Function.Arguments
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return ChatResult{}, fmt.Errorf("reading stream: %w", err)
	}

	// Build the final message.
	msg := Message{
		Role:    "assistant",
		Content: strings.TrimSpace(contentBuf.String()),
	}

	// Convert tool call map to ordered slice.
	if len(toolCalls) > 0 {
		calls := make([]ToolCall, len(toolCalls))
		for idx, tc := range toolCalls {
			if idx < len(calls) {
				calls[idx] = *tc
			}
		}
		msg.ToolCalls = calls
	}

	return ChatResult{
		Message:    msg,
		StopReason: finishReason,
	}, nil
}
