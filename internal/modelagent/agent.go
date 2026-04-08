package modelagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/moneyprinter/internal/agent"
	"github.com/moneyprinter/internal/inference"
)

const maxTurns = 25

// PreviousPost provides context about a completed post.
type PreviousPost struct {
	Index   int
	Scene   string
	Caption string // first ~100 chars
}

// Config holds parameters for an Instagram content generation run.
type Config struct {
	LLM         *inference.Client
	BraveAPIKey string

	// Model identity
	ModelName   string
	Description string // appearance for image gen
	Personality string // caption voice
	Style       string // photography style

	PreviousPosts []PreviousPost
}

// Result is the output of a successful content generation run.
type Result struct {
	Scene       string
	Caption     string
	Hashtags    []string
	ImagePrompt string
}

// Run executes the content planning agent and returns a post plan.
func Run(ctx context.Context, cfg Config, onEvent func(string, string)) (Result, error) {
	emit := func(msg, level string) {
		if onEvent != nil {
			onEvent(msg, level)
		}
	}

	now := time.Now()

	var previousContext string
	if len(cfg.PreviousPosts) > 0 {
		var sb strings.Builder
		sb.WriteString("\n\nPREVIOUS POSTS (do NOT repeat these scenes — always pick something fresh):")
		for _, p := range cfg.PreviousPosts {
			sb.WriteString(fmt.Sprintf("\n  Post %d: Scene: \"%s\" — %s", p.Index, p.Scene, p.Caption))
		}
		previousContext = sb.String()
	}

	systemPrompt := fmt.Sprintf(`You are the creative director for an Instagram AI model.

Today's date is %s.

CHARACTER PROFILE:
- Name: %s
- Appearance: %s
- Personality: %s
- Photography style: %s

Your job: plan a new Instagram post for this character. You need to:
1. Decide on a specific, vivid scene/setting for the photo
2. Write an Instagram caption in the character's voice
3. Choose relevant hashtags
4. Write a detailed image generation prompt
%s

PROCESS:
1. Optionally use web_search to find trending content ideas, current events, or seasonal themes for inspiration
2. Choose a specific scene that fits the character (vacation spot, cafe, gym, city street, home, event, etc.)
3. Write the caption as if the character is posting — match their personality and voice
4. Craft a detailed image prompt

IMAGE PROMPT RULES (critical for visual consistency):
- ALWAYS start the image prompt with the full character appearance: "%s"
- Then describe the scene, setting, lighting, camera angle, and mood
- Include style markers: "%s photography, Instagram photo, high quality, realistic, 4k"
- Be specific about clothing, pose, expression, and environment
- The prompt must be detailed enough to generate a consistent, high-quality image

CAPTION RULES:
- Write in first person as the character
- Match the personality: %s
- Keep it authentic and engaging — not generic influencer speak
- 1-3 sentences max, plus optional emoji
- Hashtags go in the hashtags array, NOT in the caption

Call submit_post when ready. Do not respond with plain text.`,
		now.Format("Monday, 2 January 2006"),
		cfg.ModelName,
		cfg.Description,
		cfg.Personality,
		cfg.Style,
		previousContext,
		cfg.Description,
		cfg.Style,
		cfg.Personality,
	)

	messages := []inference.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: fmt.Sprintf("Plan a new Instagram post for %s. Make it something fresh and interesting that fits the character.", cfg.ModelName)},
	}

	tools := []inference.Tool{
		WebSearchTool,
		FetchURLTool,
		SubmitPostTool,
	}

	emit("Planning new post...", "info")

	var finalResult Result
	submitted := false

	for turn := 0; turn < maxTurns && !submitted; turn++ {
		result, err := cfg.LLM.Chat(ctx, messages, tools)
		if err != nil {
			return Result{}, fmt.Errorf("LLM chat (turn %d): %w", turn+1, err)
		}

		messages = append(messages, result.Message)

		if len(result.Message.ToolCalls) == 0 {
			emit("Agent responded without tools, nudging...", "warning")
			messages = append(messages, inference.Message{
				Role:    "user",
				Content: "Please use the submit_post tool to submit the post.",
			})
			continue
		}

		for _, tc := range result.Message.ToolCalls {
			var toolOutput string

			switch tc.Function.Name {
			case "web_search":
				var args struct {
					Query string `json:"query"`
				}
				json.Unmarshal([]byte(tc.Function.Arguments), &args)
				emit(fmt.Sprintf("Searching: %s", args.Query), "info")
				toolOutput = agent.BraveSearch(ctx, cfg.BraveAPIKey, args.Query)

			case "fetch_url":
				var args struct {
					URL string `json:"url"`
				}
				json.Unmarshal([]byte(tc.Function.Arguments), &args)
				emit(fmt.Sprintf("Reading: %s", args.URL), "info")
				toolOutput = agent.FetchURL(ctx, args.URL)

			case "submit_post":
				var args struct {
					Scene       string   `json:"scene"`
					Caption     string   `json:"caption"`
					Hashtags    []string `json:"hashtags"`
					ImagePrompt string   `json:"imagePrompt"`
				}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					toolOutput = fmt.Sprintf("Error parsing arguments: %v. Please try again.", err)
				} else if args.ImagePrompt == "" || args.Caption == "" {
					toolOutput = "imagePrompt and caption cannot be empty. Please provide both."
				} else {
					submitted = true
					finalResult = Result{
						Scene:       args.Scene,
						Caption:     args.Caption,
						Hashtags:    args.Hashtags,
						ImagePrompt: args.ImagePrompt,
					}
					toolOutput = "Post accepted."
				}

			default:
				toolOutput = fmt.Sprintf("Unknown tool: %s", tc.Function.Name)
			}

			messages = append(messages, inference.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    toolOutput,
			})
		}
	}

	if !submitted {
		return Result{}, fmt.Errorf("agent did not submit a post after %d turns", maxTurns)
	}

	emit(fmt.Sprintf("Post planned: %s", finalResult.Scene), "success")
	return finalResult, nil
}
