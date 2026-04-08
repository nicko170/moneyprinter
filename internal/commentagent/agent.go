package commentagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moneyprinter/internal/agent"
	"github.com/moneyprinter/internal/inference"
)

const maxTurns = 15

// Config holds parameters for a comment reply run.
type Config struct {
	LLM         *inference.Client
	BraveAPIKey string

	// Video context
	VideoSubject string
	Script       string
	SeriesTheme  string
	TonePreset   string

	// Comment to reply to
	CommentAuthor string
	CommentText   string
}

// Result is the output of a reply generation.
type Result struct {
	ReplyText string
	Skipped   bool
}

// Run generates a reply to a YouTube comment using the video's context.
func Run(ctx context.Context, cfg Config, onEvent func(string, string)) (Result, error) {
	emit := func(msg, level string) {
		if onEvent != nil {
			onEvent(msg, level)
		}
	}

	toneInstruction := ""
	if cfg.TonePreset != "" {
		toneInstruction = fmt.Sprintf("\nMatch the tone of the video: %s.", cfg.TonePreset)
	}

	seriesContext := ""
	if cfg.SeriesTheme != "" {
		seriesContext = fmt.Sprintf("\nThis video is part of a series about: %s", cfg.SeriesTheme)
	}

	systemPrompt := fmt.Sprintf(`You are the creator of a YouTube Shorts channel. You made a video about "%s".
%s%s
Here's the script you wrote for the video:
---
%s
---

A viewer left a comment on your video. Your job is to write a genuine, engaging reply.

REPLY RULES:
- Keep it short: 1-3 sentences max
- Be conversational and human — not corporate or robotic
- Actually respond to what they said, don't be generic
- You can be funny, ask follow-up questions, share extra info, or agree/disagree
- If they ask a question, answer it using your knowledge (search if needed)
- If they share a personal experience, acknowledge it genuinely
- If the comment is pure spam, hateful, or nonsensical — call submit_reply with skip=true%s

You can use web_search if you need to fact-check something or find additional info to make your reply better. Then call submit_reply with your response.`,
		cfg.VideoSubject,
		seriesContext,
		toneInstruction,
		cfg.Script,
		toneInstruction,
	)

	messages := []inference.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: fmt.Sprintf("Comment by @%s:\n%s", cfg.CommentAuthor, cfg.CommentText)},
	}

	tools := []inference.Tool{
		WebSearchTool,
		FetchURLTool,
		SubmitReplyTool,
	}

	emit(fmt.Sprintf("Generating reply to @%s...", cfg.CommentAuthor), "info")

	var finalResult Result
	submitted := false

	for turn := 0; turn < maxTurns && !submitted; turn++ {
		result, err := cfg.LLM.Chat(ctx, messages, tools)
		if err != nil {
			return Result{}, fmt.Errorf("LLM chat (turn %d): %w", turn+1, err)
		}

		messages = append(messages, result.Message)

		if len(result.Message.ToolCalls) == 0 {
			messages = append(messages, inference.Message{
				Role:    "user",
				Content: "Please use the submit_reply tool to submit your reply.",
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

			case "submit_reply":
				var args struct {
					Reply string `json:"reply"`
					Skip  bool   `json:"skip"`
				}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					toolOutput = fmt.Sprintf("Error parsing: %v. Try again.", err)
				} else {
					submitted = true
					if args.Skip {
						finalResult = Result{Skipped: true}
						toolOutput = "Comment skipped."
					} else if strings.TrimSpace(args.Reply) == "" {
						submitted = false
						toolOutput = "Reply cannot be empty. Provide text or set skip=true."
					} else {
						finalResult = Result{ReplyText: strings.TrimSpace(args.Reply)}
						toolOutput = "Reply accepted."
					}
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
		return Result{}, fmt.Errorf("agent did not submit a reply after %d turns", maxTurns)
	}

	if finalResult.Skipped {
		emit("Skipped (spam/irrelevant)", "warning")
	} else {
		emit(fmt.Sprintf("Reply: %s", finalResult.ReplyText), "success")
	}

	return finalResult, nil
}
