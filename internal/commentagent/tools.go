package commentagent

import (
	"encoding/json"

	"github.com/moneyprinter/internal/inference"
)

var (
	WebSearchTool = inference.Tool{
		Type: "function",
		Function: inference.ToolFunction{
			Name:        "web_search",
			Description: "Search the web to fact-check something or find info to improve your reply.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"The search query"}},"required":["query"]}`),
		},
	}

	FetchURLTool = inference.Tool{
		Type: "function",
		Function: inference.ToolFunction{
			Name:        "fetch_url",
			Description: "Read a web page for detailed information.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"The URL to fetch"}},"required":["url"]}`),
		},
	}

	SubmitReplyTool = inference.Tool{
		Type: "function",
		Function: inference.ToolFunction{
			Name:        "submit_reply",
			Description: "Submit your reply to the comment. Set skip=true to skip spam/hateful comments.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"reply": {
						"type": "string",
						"description": "Your reply text (1-3 sentences)"
					},
					"skip": {
						"type": "boolean",
						"description": "Set true to skip this comment without replying"
					}
				},
				"required": ["reply"]
			}`),
		},
	}
)
