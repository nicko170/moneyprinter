package modelagent

import (
	"encoding/json"

	"github.com/moneyprinter/internal/inference"
)

var (
	WebSearchTool = inference.Tool{
		Type: "function",
		Function: inference.ToolFunction{
			Name:        "web_search",
			Description: "Search the web for trending content ideas, current events, seasonal themes, or Instagram inspiration.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"The search query"}},"required":["query"]}`),
		},
	}

	FetchURLTool = inference.Tool{
		Type: "function",
		Function: inference.ToolFunction{
			Name:        "fetch_url",
			Description: "Read a web page for detailed content ideas or visual inspiration references.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"The URL to fetch"}},"required":["url"]}`),
		},
	}

	SubmitPostTool = inference.Tool{
		Type: "function",
		Function: inference.ToolFunction{
			Name:        "submit_post",
			Description: "Submit the Instagram post. Call this exactly once when you have decided on the scene, written the caption, and crafted the image generation prompt.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"scene": {
						"type": "string",
						"description": "Short description of the scene/setting (e.g. 'reading at a Parisian cafe on a rainy afternoon')"
					},
					"caption": {
						"type": "string",
						"description": "The Instagram caption text, written in the model's voice/personality"
					},
					"hashtags": {
						"type": "array",
						"items": {"type": "string"},
						"description": "Array of hashtags without the # symbol"
					},
					"imagePrompt": {
						"type": "string",
						"description": "Detailed image generation prompt. MUST include the full character appearance description and scene details."
					}
				},
				"required": ["scene", "caption", "hashtags", "imagePrompt"]
			}`),
		},
	}
)
