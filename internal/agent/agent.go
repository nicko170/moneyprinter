package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/moneyprinter/internal/inference"
)

const maxTurns = 50

// EventFunc is called with progress updates during research.
type EventFunc func(message, level string)

// Config holds parameters for a research run.
type Config struct {
	LLM          *inference.Client
	BraveAPIKey  string
	VideoSubject string
	TonePreset   string
	HookStyle    string
	ParagraphNum int
	// Series context — set when this is an episode in a series.
	SeriesTheme  string
	EpisodeIndex int
	EpisodeTotal int
}

// Result is the output of a successful research run.
type Result struct {
	Script  string
	Sources []Source
}

var hookInstructions = map[string]string{
	"didyouknow":    `[HOOK] Open with a surprising "Did you know..." fact directly related to the subject. This MUST be the very first words.`,
	"controversial": `[HOOK] Open with a bold, slightly controversial statement that challenges what most people believe. Be provocative but not offensive.`,
	"question":      `[HOOK] Open with a thought-provoking rhetorical question that makes the viewer stop and think.`,
	"myth":          `[HOOK] Open with "Most people think... but actually..." to bust a common misconception about the subject.`,
	"story":         `[HOOK] Open with a brief 1-sentence scenario or anecdote that pulls the viewer in, like "Imagine..." or "Picture this...".`,
	"listicle":      `[HOOK] Open with a numbered hook like "3 things about [topic] you never knew" or "5 reasons why...". Then deliver each point.`,
	"challenge":     `[HOOK] Open with a direct challenge: "I bet you didn't know..." or "Try this and tell me I'm wrong...".`,
	"stopscrolling": `[HOOK] Open with an urgent attention-grabber: "Stop scrolling." or "Wait — you need to hear this." Make it feel unmissable.`,
}

var toneInstructions = map[string]string{
	"informative":  `[TONE] Calm, educational. Clear explanations, factual delivery. Like a friendly teacher.`,
	"hype":         `[TONE] HIGH energy and excitement. Power words, urgency, exclamation. Like an enthusiastic presenter.`,
	"sarcastic":    `[TONE] Dry humor and clever observations. Witty and sarcastic but not mean-spirited.`,
	"dramatic":     `[TONE] Epic documentary narration. Build tension and wonder.`,
	"casual":       `[TONE] Like explaining to a friend over coffee. Conversational, relaxed, use "you" and "we".`,
	"professional": `[TONE] Polished, authoritative. Confident assertions, precise language. Like a TED talk.`,
}

// Run executes the agentic research loop and returns a script with sources.
func Run(ctx context.Context, cfg Config, onEvent EventFunc) (Result, error) {
	emit := func(msg, level string) {
		if onEvent != nil {
			onEvent(msg, level)
		}
	}

	hook := hookInstructions[cfg.HookStyle]
	if cfg.HookStyle == "custom" {
		hook = "" // custom hooks are handled via VideoSubject context
	}
	tone := toneInstructions[cfg.TonePreset]

	wordTarget := "80 to 120"
	if cfg.ParagraphNum == 2 {
		wordTarget = "120 to 180"
	} else if cfg.ParagraphNum >= 3 {
		wordTarget = "180 to 240"
	}

	seriesContext := ""
	if cfg.SeriesTheme != "" {
		seriesContext = fmt.Sprintf("\n[SERIES CONTEXT] This is episode %d of %d in a series titled \"%s\". Keep the focus tight on this episode's specific topic. Do not repeat the series-level premise — other episodes cover that.",
			cfg.EpisodeIndex, cfg.EpisodeTotal, cfg.SeriesTheme)
	}

	now := time.Now()
	systemPrompt := fmt.Sprintf(`You are a research assistant that writes short-form viral video scripts.

Today's date is %s. Search for the most recent information available — prioritise sources from %d.

Your task: research "%s" using the provided tools, then write a polished script.

RESEARCH PROCESS:
1. Use web_search to find current facts, statistics, and interesting angles (do 2-4 searches)
2. Use fetch_url to read the most promising sources in depth
3. Synthesize what you have learned
4. Call submit_script with the final script and sources list

SCRIPT REQUIREMENTS:
- %s words total (strictly enforced)
- Plain ASCII text only — no markdown, no titles, no bullet points, no emoji
- Short punchy sentences, each under 15 words
- Do NOT say "in this video", "welcome", or reference yourself
- Hook the viewer immediately in the first sentence

%s
%s
%s

You MUST call submit_script to finish. Do not respond with plain text.`,
		now.Format("Monday, 2 January 2006"), now.Year(), cfg.VideoSubject, wordTarget, hook, tone, seriesContext)

	messages := []inference.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: fmt.Sprintf("Research and write a video script about: %s", cfg.VideoSubject)},
	}

	tools := []inference.Tool{
		WebSearchTool,
		FetchURLTool,
		SubmitScriptTool,
	}

	emit("Starting research...", "info")

	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return Result{}, fmt.Errorf("research cancelled")
		}

		result, err := cfg.LLM.Chat(ctx, messages, tools)
		if err != nil {
			return Result{}, fmt.Errorf("LLM error on turn %d: %w", turn+1, err)
		}

		// Append the assistant message to history.
		messages = append(messages, result.Message)

		if len(result.Message.ToolCalls) == 0 {
			// The model replied with text instead of calling a tool.
			// This shouldn't happen if the model follows instructions.
			return Result{}, fmt.Errorf("model did not call any tool (responded with text). Check that the model supports function calling")
		}

		var submitted bool
		var finalResult Result

		for _, tc := range result.Message.ToolCalls {
			var toolOutput string

			switch tc.Function.Name {
			case "web_search":
				var args struct {
					Query string `json:"query"`
				}
				json.Unmarshal([]byte(tc.Function.Arguments), &args)
				emit(fmt.Sprintf("Searching: %s", args.Query), "info")
				toolOutput = BraveSearch(ctx, cfg.BraveAPIKey, args.Query)
				emit(fmt.Sprintf("Got search results for: %s", args.Query), "info")

			case "fetch_url":
				var args struct {
					URL string `json:"url"`
				}
				json.Unmarshal([]byte(tc.Function.Arguments), &args)
				emit(fmt.Sprintf("Reading: %s", args.URL), "info")
				toolOutput = FetchURL(ctx, args.URL)

			case "submit_script":
				var args struct {
					Script  string   `json:"script"`
					Sources []Source `json:"sources"`
				}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					toolOutput = fmt.Sprintf("Error parsing submit_script arguments: %v. Please try again.", err)
				} else if args.Script == "" {
					toolOutput = "Script cannot be empty. Please provide the script text."
				} else {
					submitted = true
					finalResult = Result{Script: args.Script, Sources: args.Sources}
					toolOutput = "Script accepted."
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

		if submitted {
			emit("Script ready.", "success")
			return finalResult, nil
		}
	}

	return Result{}, fmt.Errorf("agent did not submit a script within %d turns", maxTurns)
}
