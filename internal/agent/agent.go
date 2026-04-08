package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/moneyprinter/internal/inference"
)

const maxTurns = 50

// EventFunc is called with progress updates during research.
type EventFunc func(message, level string)

// PreviousEpisode provides context about a completed episode in the series.
type PreviousEpisode struct {
	Index   int
	Subject string
	Summary string // first ~150 chars of script
}

// Config holds parameters for a research run.
type Config struct {
	LLM          *inference.Client
	BraveAPIKey  string
	VideoSubject string // empty for series episodes — agent picks its own topic
	TonePreset   string
	HookStyle    string
	ParagraphNum int
	// Series context — set when this is an episode in a series.
	SeriesTheme      string
	EpisodeIndex     int
	EpisodeTotal     int
	PreviousEpisodes []PreviousEpisode // completed episodes for context
}

// Result is the output of a successful research run.
type Result struct {
	Subject string   // topic chosen by agent (for series episodes)
	Script  string
	Sources []Source
}

var hookInstructions = map[string]string{
	"didyouknow":    `[HOOK] Open with a surprising "Did you know..." fact directly related to the subject. This MUST be the very first words.`,
	"controversial": `[HOOK] Open with the most unhinged hot take you can defend. Make people furious enough to comment. Be bold, be wrong on purpose if it's funnier.`,
	"question":      `[HOOK] Open with a question so specific and weird that people HAVE to hear the answer. Not "Did you know?" — more like "Why do pigeons walk like they own the sidewalk?"`,
	"myth":          `[HOOK] Open with "Everyone thinks... and everyone is WRONG." Bust the myth hard, make them feel dumb for believing it.`,
	"story":         `[HOOK] Open mid-story, like the viewer just walked into the room at the best part. "So there I was..." or "Picture this:" energy.`,
	"listicle":      `[HOOK] Open with a numbered hook like "3 things about [topic] you never knew" or "5 reasons why...". Then deliver each point.`,
	"challenge":     `[HOOK] Open with a direct challenge that feels personal: "I bet you can't watch this without Googling it after." Dare the viewer.`,
	"stopscrolling": `[HOOK] Open with something so bizarre or urgent they physically cannot scroll past. "Stop. You need to hear what NASA just admitted."`,
}

var toneInstructions = map[string]string{
	"informative":  `[TONE] Smart and clear, but never boring. You're the friend who makes everyone at the party go "wait, really?!" Teach, but make it addictive.`,
	"hype":         `[TONE] UNHINGED ENERGY. You just found out the most insane thing and you're speed-talking to your friend before the cops show up. Every sentence hits like a headline.`,
	"sarcastic":    `[TONE] Peak internet sarcasm. Deadpan delivery, unexpected comparisons, the kind of commentary that makes people screenshot and send to friends. Think stand-up meets Reddit.`,
	"dramatic":     `[TONE] You're narrating the trailer for a documentary that doesn't exist yet. Every sentence should make the viewer feel like they're uncovering a conspiracy. Build tension like a thriller.`,
	"casual":       `[TONE] Talking to your best friend at 2am about the wildest thing you just learned. Conversational, zero filter, genuine reactions like "bro" and "honestly."`,
	"professional": `[TONE] TED talk energy but actually interesting. Polished, confident, authoritative — but with enough edge to keep gen Z watching.`,
	"unhinged":     `[TONE] Full internet brain rot. Write like a sleep-deprived genius who sees connections nobody else does. Chaotic, absurd, but somehow makes a point. Meme energy. Fever dream logic that still lands.`,
	"storyteller":  `[TONE] Master storyteller mode. Build a narrative arc even in 60 seconds — setup, escalation, twist. Every line should make them NEED to hear the next one. Cliffhanger energy throughout.`,
}

// researchSubject returns the topic the agent should research.
func researchSubject(cfg Config) string {
	if cfg.VideoSubject != "" {
		return cfg.VideoSubject
	}
	return cfg.SeriesTheme
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
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("\n[SERIES CONTEXT] This is episode %d of %d in a series themed \"%s\".",
			cfg.EpisodeIndex, cfg.EpisodeTotal, cfg.SeriesTheme))

		if len(cfg.PreviousEpisodes) > 0 {
			sb.WriteString("\n\nPrevious episodes already covered:")
			for _, ep := range cfg.PreviousEpisodes {
				sb.WriteString(fmt.Sprintf("\n  Episode %d: \"%s\" — %s", ep.Index, ep.Subject, ep.Summary))
			}
			sb.WriteString("\n\nDO NOT repeat any topic above. Find a fresh, different angle within the series theme.")
		}

		if cfg.VideoSubject == "" {
			sb.WriteString("\n\nYou MUST choose your own specific topic for this episode. Pick something surprising, interesting, and different from previous episodes. Your research should guide the topic — find what's trending or fascinating right now within the theme.")
		}

		seriesContext = sb.String()
	}

	now := time.Now()
	systemPrompt := fmt.Sprintf(`You write viral short-form video scripts. Your scripts get millions of views because they sound like a real person losing their mind over something fascinating — not a robot reading Wikipedia.

Today's date is %s. Search for the most recent, interesting, weird, or surprising angles on the topic — prioritise %d sources.

Your task: research "%s" using the provided tools, then write a script that makes people stop scrolling.

RESEARCH PROCESS:
1. Use web_search to find surprising angles, weird facts, memes, cultural context, recent events (do 2-4 searches)
2. Use fetch_url to read the most interesting sources — look for the detail nobody else mentions
3. Find the ANGLE. Not just "here are facts about X" but "here's why X is absolutely insane and nobody talks about it"
4. Call submit_script with the final script and sources list

WRITING STYLE:
- Write like the best storyteller at a party, not like a news anchor
- Every single line should make the viewer NEED to hear the next one
- Use unexpected comparisons, vivid images, and specific details (not generic statements)
- Vary sentence rhythm — short punches mixed with slightly longer builds
- Land the ending. The last line should hit hard — a callback, a twist, a punchline, or a mic drop
- NO filler. If a line doesn't make someone react, cut it
- BAD: "This is really interesting." GOOD: "Scientists still can't explain it."
- BAD: "Many people don't know this." GOOD: "Your dentist knows this and has been lying to your face."

FORMATTING:
- %s words total (strictly enforced)
- Plain ASCII text only — no markdown, no titles, no bullet points, no emoji
- Put EACH SENTENCE on its own line. One sentence per line, no blank lines.
  Each line becomes a separate subtitle card and TTS chunk in the final video.
- Do NOT say "in this video", "welcome", or reference yourself

%s
%s
%s

You MUST call submit_script to finish. Do not respond with plain text.`,
		now.Format("Monday, 2 January 2006"), now.Year(), researchSubject(cfg), wordTarget, hook, tone, seriesContext)

	userMsg := fmt.Sprintf("Research and write a video script about: %s", researchSubject(cfg))
	if cfg.VideoSubject == "" && cfg.SeriesTheme != "" {
		userMsg = fmt.Sprintf("Pick a specific topic within the series theme \"%s\" and write a video script about it. The topic should be fresh and different from any previous episodes.", cfg.SeriesTheme)
	}

	messages := []inference.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMsg},
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
					Topic   string   `json:"topic"`
					Script  string   `json:"script"`
					Sources []Source `json:"sources"`
				}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					toolOutput = fmt.Sprintf("Error parsing submit_script arguments: %v. Please try again.", err)
				} else if args.Script == "" {
					toolOutput = "Script cannot be empty. Please provide the script text."
				} else {
					submitted = true
					subject := args.Topic
					if subject == "" {
						subject = cfg.VideoSubject // fallback to config
					}
					finalResult = Result{Subject: subject, Script: args.Script, Sources: args.Sources}
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
