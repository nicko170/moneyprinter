package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/moneyprinter/internal/inference"
	"github.com/moneyprinter/internal/pexels"
	"github.com/moneyprinter/internal/state"
	"github.com/moneyprinter/internal/tts"
	"github.com/moneyprinter/internal/video"
)

// Params holds the user-submitted generation parameters.
type Params struct {
	VideoSubject  string `json:"videoSubject"`
	Voice         string `json:"voice"`
	ParagraphNum  int    `json:"paragraphNumber"`
	CustomPrompt  string `json:"customPrompt"`
	Context       string `json:"context"`
	SubtitlePos   string `json:"subtitlesPosition"`
	SubtitleColor string `json:"color"`
	HookStyle     string   `json:"hookStyle"`
	CustomHook    string   `json:"customHook"`
	TonePreset    string   `json:"tonePreset"`
	VideoEffects  []string `json:"videoEffects"`
	UseMusic      bool     `json:"useMusic"`
	Force         bool     `json:"force"`

	// Naming context (set by worker for series episodes).
	SeriesTheme  string `json:"seriesTheme,omitempty"`
	EpisodeIndex int    `json:"episodeIndex,omitempty"` // 1-based

	// End card
	EndCardBgColor  string `json:"endCardBgColor"`
	EndCardCTAText  string `json:"endCardCTAText"`
	EndCardLogoPath string `json:"endCardLogoPath"`
	EndCardDuration int    `json:"endCardDuration"`
}

// LogFunc is called with progress messages during generation.
type LogFunc func(message, level string)

// buildOutputName creates a descriptive filename for the final video.
// Series: series_theme_ep01_jobid.mp4  |  Single: subject_jobid.mp4
func buildOutputName(params Params, jobID string) string {
	short := jobID
	if len(jobID) > 8 {
		short = jobID[:8]
	}
	if params.SeriesTheme != "" && params.EpisodeIndex > 0 {
		return fmt.Sprintf("%s_ep%02d_%s.mp4", slugify(params.SeriesTheme), params.EpisodeIndex, short)
	}
	return fmt.Sprintf("%s_%s.mp4", slugify(params.VideoSubject), short)
}

// slugify converts a string to a filesystem-safe lowercase slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if len(s) > 60 {
		s = s[:60]
		s = strings.TrimRight(s, "_")
	}
	return s
}

// fileExists returns true if path exists and has content.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// Run executes the full video generation pipeline.
// jobID isolates temp files and output per job. If Force is false, completed steps are skipped (resume mode).
func Run(ctx context.Context, jobID string, params Params, cfg *state.State, onLog LogFunc) (string, error) {
	emit := func(msg, level string) {
		if onLog != nil {
			onLog(msg, level)
		}
	}

	// Apply defaults.
	if params.Voice == "" {
		params.Voice = "en_us_001"
		emit("No voice selected, defaulting to en_us_001", "warning")
	}
	if params.ParagraphNum <= 0 {
		params.ParagraphNum = 1
	}
	if params.SubtitlePos == "" {
		params.SubtitlePos = "center,bottom"
	}
	if params.SubtitleColor == "" {
		params.SubtitleColor = "#FFFF00"
	}

	// Per-job isolation: each job gets its own temp and output path.
	tempDir := filepath.Join(cfg.TempDir, jobID)
	if params.Force {
		emit("Force mode: cleaning job temp directory", "info")
		os.RemoveAll(tempDir)
	}
	os.MkdirAll(tempDir, 0755)
	os.MkdirAll(cfg.OutputDir, 0755)

	// Deterministic paths for each step's output within the job dir.
	scriptPath := filepath.Join(tempDir, "script.txt")
	termsPath := filepath.Join(tempDir, "terms.json")
	videosManifest := filepath.Join(tempDir, "videos.json")
	ttsPath := filepath.Join(tempDir, "tts.mp3")
	srtPath := filepath.Join(tempDir, "subtitles.srt")
	combinedPath := filepath.Join(tempDir, "combined.mp4")
	subtitledPath := filepath.Join(tempDir, "subtitled.mp4")
	mergedPath := filepath.Join(tempDir, "merged.mp4")
	finalPath := filepath.Join(cfg.OutputDir, buildOutputName(params, jobID))

	emit(fmt.Sprintf("Generating video for: %s", params.VideoSubject), "info")

	llm := inference.NewClient(cfg.InferenceURL, cfg.InferenceModel, cfg.InferenceAPIKey)

	// --- Step 1: Generate script ---
	var script string
	if fileExists(scriptPath) {
		data, _ := os.ReadFile(scriptPath)
		script = string(data)
		emit("Script loaded from cache", "info")
	} else {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		emit("Generating script...", "info")
		var err error
		script, err = generateScript(ctx, llm, params)
		if err != nil {
			return "", fmt.Errorf("script generation: %w", err)
		}
		os.WriteFile(scriptPath, []byte(script), 0644)
		emit("Script generated", "success")
	}

	// --- Step 1b: Generate social media metadata ---
	metadataPath := filepath.Join(tempDir, "metadata.json")
	if !fileExists(metadataPath) {
		emit("Generating social metadata...", "info")
		meta, err := generateMetadata(ctx, llm, params.VideoSubject, script)
		if err != nil {
			emit(fmt.Sprintf("Metadata generation failed, continuing: %v", err), "warning")
		} else {
			metaJSON, _ := json.MarshalIndent(meta, "", "  ")
			os.WriteFile(metadataPath, metaJSON, 0644)
			emit("Social metadata generated", "success")
		}
	} else {
		emit("Social metadata loaded from cache", "info")
	}

	// --- Step 2: Generate search terms ---
	var searchTerms []string
	if fileExists(termsPath) {
		data, _ := os.ReadFile(termsPath)
		json.Unmarshal(data, &searchTerms)
		emit(fmt.Sprintf("Search terms loaded from cache: %s", strings.Join(searchTerms, ", ")), "info")
	} else {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		emit("Generating search terms...", "info")
		var err error
		// Scale search terms with content length — more terms = more variety.
		searchTerms, err = getSearchTerms(ctx, llm, params.VideoSubject, script, 5)
		if err != nil {
			return "", fmt.Errorf("search terms: %w", err)
		}
		data, _ := json.Marshal(searchTerms)
		os.WriteFile(termsPath, data, 0644)
		emit(fmt.Sprintf("Search terms: %s", strings.Join(searchTerms, ", ")), "info")
	}

	// --- Step 3: Search and download stock videos ---
	var videoPaths []string
	if fileExists(videosManifest) {
		data, _ := os.ReadFile(videosManifest)
		json.Unmarshal(data, &videoPaths)
		// Verify files still exist.
		var valid []string
		for _, p := range videoPaths {
			if fileExists(p) {
				valid = append(valid, p)
			}
		}
		videoPaths = valid
		if len(videoPaths) > 0 {
			emit(fmt.Sprintf("Videos loaded from cache (%d files)", len(videoPaths)), "info")
		}
	}
	if len(videoPaths) == 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		emit("Searching for stock videos...", "info")
		var videoURLs []string
		seen := make(map[string]bool)
		for _, term := range searchTerms {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			urls, err := pexels.SearchVideos(ctx, term, cfg.PexelsAPIKey, 15, 10)
			if err != nil {
				emit(fmt.Sprintf("Search failed for %q: %v", term, err), "warning")
				continue
			}
			for _, u := range urls {
				if !seen[u] {
					seen[u] = true
					videoURLs = append(videoURLs, u)
					break // one video per search term
				}
			}
		}
		if len(videoURLs) == 0 {
			return "", fmt.Errorf("no stock videos found")
		}

		emit(fmt.Sprintf("Downloading %d videos...", len(videoURLs)), "info")
		for _, url := range videoURLs {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			path, err := video.DownloadVideo(ctx, url, tempDir)
			if err != nil {
				emit(fmt.Sprintf("Download failed: %v", err), "warning")
				continue
			}
			videoPaths = append(videoPaths, path)
		}
		if len(videoPaths) == 0 {
			return "", fmt.Errorf("no videos downloaded successfully")
		}
		data, _ := json.Marshal(videoPaths)
		os.WriteFile(videosManifest, data, 0644)
		emit("Videos downloaded", "success")
	}

	// --- Step 4: Text-to-speech ---
	if !fileExists(ttsPath) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		emit("Generating speech...", "info")
		ttsProvider := selectTTSProvider(cfg)
		sentences := splitSentences(script)
		var timings []video.SentenceTiming
		var audioPaths []string

		for i, sentence := range sentences {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			audioPath := filepath.Join(tempDir, fmt.Sprintf("chunk_%03d.mp3", i))
			if err := ttsProvider.Synthesize(ctx, sentence, params.Voice, audioPath); err != nil {
				return "", fmt.Errorf("TTS for sentence: %w", err)
			}
			dur, err := video.GetDuration(ctx, audioPath)
			if err != nil {
				return "", fmt.Errorf("getting audio duration: %w", err)
			}
			timings = append(timings, video.SentenceTiming{Text: sentence, Duration: dur})
			audioPaths = append(audioPaths, audioPath)
		}

		if err := concatAudio(ctx, audioPaths, ttsPath); err != nil {
			return "", fmt.Errorf("concatenating audio: %w", err)
		}

		// Save timings for subtitle generation.
		timingsData, _ := json.Marshal(timings)
		os.WriteFile(filepath.Join(tempDir, "timings.json"), timingsData, 0644)
		emit("Speech generated", "success")
	} else {
		emit("TTS loaded from cache", "info")
	}

	// --- Step 5: Generate subtitles ---
	if !fileExists(srtPath) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		emit("Generating subtitles...", "info")

		// Load timings.
		var timings []video.SentenceTiming
		timingsData, err := os.ReadFile(filepath.Join(tempDir, "timings.json"))
		if err != nil {
			// Regenerate timings from script + TTS if missing.
			return "", fmt.Errorf("subtitle generation: timings file missing, use --force to regenerate")
		}
		json.Unmarshal(timingsData, &timings)

		srtContent, err := video.GenerateSRTContent(timings, params.SubtitlePos)
		if err != nil {
			return "", fmt.Errorf("subtitle generation: %w", err)
		}
		os.MkdirAll(filepath.Dir(srtPath), 0755)
		if err := os.WriteFile(srtPath, []byte(srtContent), 0644); err != nil {
			return "", fmt.Errorf("writing SRT: %w", err)
		}
		emit("Subtitles generated", "success")
	} else {
		emit("Subtitles loaded from cache", "info")
	}

	// --- Step 6: Compose video ---
	if !fileExists(combinedPath) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		totalDuration, err := video.GetDuration(ctx, ttsPath)
		if err != nil {
			return "", fmt.Errorf("getting total audio duration: %w", err)
		}
		emit("Composing video...", "info")
		result, err := video.ConcatAndCrop(ctx, videoPaths, totalDuration, params.VideoEffects, tempDir)
		if err != nil {
			return "", fmt.Errorf("video composition: %w", err)
		}
		os.Rename(result, combinedPath)
	} else {
		emit("Combined video loaded from cache", "info")
	}

	// Burn subtitles.
	if !fileExists(subtitledPath) {
		// Try TikTokSans-Bold first, then fall back to bold_font.ttf, then system Arial.
		fontPath := ""
		for _, candidate := range []string{
			filepath.Join("static", "fonts", "TikTokSans-Bold.ttf"),
			filepath.Join(cfg.FontsDir, "bold_font.ttf"),
		} {
			if _, err := os.Stat(candidate); err == nil {
				fontPath = candidate
				break
			}
		}
		if fontPath == "" {
			emit("No custom font found, using system Arial", "warning")
		}
		result, err := video.BurnSubtitles(ctx, combinedPath, srtPath, fontPath, params.SubtitleColor, tempDir)
		if err != nil {
			return "", fmt.Errorf("burning subtitles: %w", err)
		}
		os.Rename(result, subtitledPath)
	} else {
		emit("Subtitled video loaded from cache", "info")
	}

	// Merge TTS audio.
	if !fileExists(mergedPath) {
		if err := video.MergeAudio(ctx, subtitledPath, ttsPath, mergedPath); err != nil {
			return "", fmt.Errorf("merging audio: %w", err)
		}
		emit("Video composed", "success")
	} else {
		emit("Merged video loaded from cache", "info")
	}

	// --- Step 7: Optional end card ---
	endCardMergedPath := mergedPath
	if params.EndCardCTAText != "" || params.EndCardLogoPath != "" {
		endCardPath := filepath.Join(tempDir, "endcard.mp4")
		withEndCardPath := filepath.Join(tempDir, "with_endcard.mp4")

		if !fileExists(endCardPath) {
			emit("Generating end card...", "info")
			opts := video.EndCardOpts{
				BgColor:  params.EndCardBgColor,
				LogoPath: params.EndCardLogoPath,
				CTAText:  params.EndCardCTAText,
				Duration: params.EndCardDuration,
			}
			result, err := video.GenerateEndCard(ctx, opts, tempDir)
			if err != nil {
				emit(fmt.Sprintf("End card generation failed, continuing without: %v", err), "warning")
			} else {
				os.Rename(result, endCardPath)
			}
		}

		if fileExists(endCardPath) && !fileExists(withEndCardPath) {
			if err := video.AppendEndCard(ctx, mergedPath, endCardPath, withEndCardPath, tempDir); err != nil {
				emit(fmt.Sprintf("End card append failed, continuing without: %v", err), "warning")
			} else {
				endCardMergedPath = withEndCardPath
				emit("End card appended", "success")
			}
		} else if fileExists(withEndCardPath) {
			endCardMergedPath = withEndCardPath
		}
	}

	// --- Step 8: Optional background music ---
	if params.UseMusic {
		songPath := chooseRandomSong(cfg.SongsDir)
		if songPath != "" {
			emit("Mixing background music...", "info")
			if err := video.MixBackgroundMusic(ctx, endCardMergedPath, songPath, finalPath, 0.1); err != nil {
				emit(fmt.Sprintf("Music mixing failed, continuing without: %v", err), "warning")
				copyFile(endCardMergedPath, finalPath)
			} else {
				emit("Background music mixed", "success")
			}
		} else {
			emit("No songs found in songs directory, skipping music", "warning")
			copyFile(endCardMergedPath, finalPath)
		}
	} else {
		copyFile(endCardMergedPath, finalPath)
	}

	emit(fmt.Sprintf("Video generated: %s", finalPath), "success")
	return finalPath, nil
}

func selectTTSProvider(cfg *state.State) tts.Provider {
	switch cfg.TTSProvider {
	case "chatterbox":
		return &tts.Chatterbox{
			BaseURL:      cfg.TTSChatterboxURL,
			VoiceRefPath: cfg.TTSChatterboxVoice,
		}
	default:
		return &tts.TikTok{}
	}
}

// Hook style presets — prompt instructions for opening hooks.
var hookPresets = map[string]string{
	"didyouknow":   `Open with a surprising "Did you know..." fact directly related to the subject. This MUST be the very first words.`,
	"controversial": `Open with a bold, slightly controversial statement that challenges what most people believe. Be provocative but not offensive.`,
	"question":      `Open with a thought-provoking rhetorical question that makes the viewer stop and think.`,
	"myth":          `Open with "Most people think... but actually..." to bust a common misconception about the subject.`,
	"story":         `Open with a brief 1-sentence scenario or anecdote that pulls the viewer in, like "Imagine..." or "Picture this...".`,
	"listicle":      `Open with a numbered hook like "3 things about [topic] you never knew" or "5 reasons why...". Then deliver each point.`,
	"challenge":     `Open with a direct challenge: "I bet you didn't know..." or "Try this and tell me I'm wrong...".`,
	"stopscrolling": `Open with an urgent attention-grabber: "Stop scrolling." or "Wait — you need to hear this." Make it feel unmissable.`,
}

// Tone presets — prompt instructions for overall script style.
var tonePresets = map[string]string{
	"informative":  `Write in a calm, educational tone. Clear explanations, factual delivery. Like a friendly teacher.`,
	"hype":         `Write with HIGH energy and excitement. Use power words, urgency, exclamation. Like an enthusiastic presenter who can't contain themselves.`,
	"sarcastic":    `Write with dry humor and clever observations. Be witty and sarcastic, but not mean-spirited. Think stand-up comedian doing a bit.`,
	"dramatic":     `Write as if narrating an epic documentary. Build tension and wonder. Make the viewer feel like they're discovering something incredible for the first time.`,
	"casual":       `Write like you're explaining this to a friend over coffee. Conversational, relaxed, use "you" and "we". No formal language.`,
	"professional": `Write in a polished, authoritative business tone. Confident assertions, precise language. Like a TED talk.`,
}

func generateScript(ctx context.Context, llm *inference.Client, params Params) (string, error) {
	prompt := params.CustomPrompt
	if prompt == "" {
		prompt = `Generate a script for a short-form video (30-60 seconds when spoken aloud).
The script MUST be between 80 and 120 words. This is critical — short-form content must be concise.
Use short, punchy sentences. Each sentence should be under 15 words.
Do not under any circumstance reference this prompt in your response.
Get straight to the point, don't start with unnecessary things like, "welcome to this video".
The script should be related to the subject of the video.
YOU MUST NOT INCLUDE ANY TYPE OF MARKDOWN OR FORMATTING IN THE SCRIPT, NEVER USE A TITLE.
DO NOT USE EMOJI OR SPECIAL UNICODE CHARACTERS. Plain ASCII text only.
ONLY RETURN THE RAW CONTENT OF THE SCRIPT. DO NOT INCLUDE "VOICEOVER", "NARRATOR" OR SIMILAR INDICATORS.
YOU MUST NOT MENTION THE PROMPT, OR ANYTHING ABOUT THE SCRIPT ITSELF. JUST WRITE THE SCRIPT.`
	}

	// Inject hook style instruction.
	if params.HookStyle == "custom" && params.CustomHook != "" {
		prompt += fmt.Sprintf("\n\n[HOOK — OPENING STYLE]\n%s\nThe very first sentence of the script MUST use this hook style to grab attention.", params.CustomHook)
	} else if instruction, ok := hookPresets[params.HookStyle]; ok {
		prompt += fmt.Sprintf("\n\n[HOOK — OPENING STYLE]\n%s", instruction)
	}

	// Inject tone preset.
	if instruction, ok := tonePresets[params.TonePreset]; ok {
		prompt += fmt.Sprintf("\n\n[TONE & STYLE]\n%s\nMaintain this tone throughout the entire script.", instruction)
	}

	if params.Context != "" {
		prompt += fmt.Sprintf("\n\n[BACKGROUND MATERIAL — use this as a factual source, weave key points into the script naturally]\n%s", params.Context)
	}

	prompt += fmt.Sprintf("\n\nSubject: %s\nNumber of paragraphs: %d\nLanguage: %s",
		params.VideoSubject, params.ParagraphNum, params.Voice)

	response, err := llm.Generate(ctx, prompt)
	if err != nil {
		return "", err
	}

	// Clean markdown artifacts.
	response = strings.ReplaceAll(response, "*", "")
	response = strings.ReplaceAll(response, "#", "")
	response = regexp.MustCompile(`\[.*?\]`).ReplaceAllString(response, "")
	response = regexp.MustCompile(`\(.*?\)`).ReplaceAllString(response, "")

	// Select requested paragraph count.
	paragraphs := strings.Split(response, "\n\n")
	if len(paragraphs) > params.ParagraphNum {
		paragraphs = paragraphs[:params.ParagraphNum]
	}

	return strings.Join(paragraphs, "\n\n"), nil
}

func getSearchTerms(ctx context.Context, llm *inference.Client, subject, script string, count int) ([]string, error) {
	prompt := fmt.Sprintf(`Generate %d search terms for stock videos, depending on the subject of a video.
Subject: %s

The search terms are to be returned as a JSON-Array of strings.
Each search term should consist of 1-3 words, always add the main subject of the video.
YOU MUST ONLY RETURN THE JSON-ARRAY OF STRINGS. YOU MUST NOT RETURN ANYTHING ELSE.

For context, here is the full text:
%s`, count, subject, script)

	response, err := llm.Generate(ctx, prompt)
	if err != nil {
		return nil, err
	}

	var terms []string
	if err := json.Unmarshal([]byte(response), &terms); err != nil {
		re := regexp.MustCompile(`\[[\s\S]*?\]`)
		match := re.FindString(response)
		if match != "" {
			if err := json.Unmarshal([]byte(match), &terms); err == nil {
				return terms, nil
			}
		}
		re = regexp.MustCompile(`"([^"]+)"`)
		matches := re.FindAllStringSubmatch(response, -1)
		for _, m := range matches {
			if len(m) > 1 {
				terms = append(terms, m[1])
			}
		}
	}

	return terms, nil
}

// VideoMetadata holds platform-ready social media copy for a generated video.
type VideoMetadata struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Hashtags    []string `json:"hashtags"`
}

func generateMetadata(ctx context.Context, llm *inference.Client, subject, script string) (*VideoMetadata, error) {
	prompt := fmt.Sprintf(`You are a social media expert. Given a short-form video script, generate metadata for posting to YouTube Shorts, TikTok, and Instagram Reels.

Return a JSON object with these fields:
- "title": a catchy, SEO-friendly title under 70 characters. No emoji.
- "description": a 1-2 sentence hook that makes people want to watch. Under 150 characters. No emoji.
- "hashtags": an array of 5-8 relevant hashtags (without the # symbol).

YOU MUST ONLY RETURN VALID JSON. No markdown, no explanation.

Subject: %s

Script:
%s`, subject, script)

	response, err := llm.Generate(ctx, prompt)
	if err != nil {
		return nil, err
	}

	// Extract JSON from response.
	var meta VideoMetadata
	if err := json.Unmarshal([]byte(response), &meta); err != nil {
		re := regexp.MustCompile(`\{[\s\S]*\}`)
		match := re.FindString(response)
		if match != "" {
			if err := json.Unmarshal([]byte(match), &meta); err != nil {
				return nil, fmt.Errorf("parsing metadata JSON: %w", err)
			}
		} else {
			return nil, fmt.Errorf("no JSON found in metadata response")
		}
	}
	return &meta, nil
}

func splitSentences(script string) []string {
	// Protect common abbreviations from being split (Dr. Mr. Ms. etc.)
	abbrevs := regexp.MustCompile(`\b(Dr|Mr|Mrs|Ms|Prof|St|Jr|Sr|vs|etc|approx|inc|corp|ltd)\.\s`)
	protected := abbrevs.ReplaceAllStringFunc(script, func(m string) string {
		return strings.Replace(m, ". ", "ABBREVDOT ", 1)
	})

	// Split on all sentence-ending punctuation, semicolons, newlines, em-dashes.
	delimiters := regexp.MustCompile(`([.!?]+\s+|;\s+|\n+|—)`)
	tokens := delimiters.Split(protected, -1)
	separators := delimiters.FindAllString(protected, -1)

	// Re-attach punctuation to preceding chunk for natural TTS prosody.
	var sentences []string
	for i, tok := range tokens {
		// Restore protected abbreviations.
		tok = strings.ReplaceAll(tok, "ABBREVDOT", ".")
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		// Grab the first punctuation char from the separator that followed this token.
		if i < len(separators) {
			for _, r := range separators[i] {
				if r == '.' || r == '!' || r == '?' || r == ';' {
					tok += string(r)
					break
				}
			}
		}
		// If still very long (>150 chars), break on commas.
		if len(tok) > 150 {
			parts := strings.Split(tok, ", ")
			var buf strings.Builder
			for _, p := range parts {
				if buf.Len() > 0 && buf.Len()+len(p) > 100 {
					sentences = append(sentences, strings.TrimSpace(buf.String()))
					buf.Reset()
				}
				if buf.Len() > 0 {
					buf.WriteString(", ")
				}
				buf.WriteString(p)
			}
			if buf.Len() > 0 {
				sentences = append(sentences, strings.TrimSpace(buf.String()))
			}
		} else {
			sentences = append(sentences, tok)
		}
	}
	return sentences
}

func concatAudio(ctx context.Context, paths []string, outPath string) error {
	if len(paths) == 1 {
		return copyFile(paths[0], outPath)
	}

	absOut, _ := filepath.Abs(outPath)
	listPath := absOut + ".txt"
	var content strings.Builder
	for _, p := range paths {
		abs, _ := filepath.Abs(p)
		content.WriteString(fmt.Sprintf("file '%s'\n", abs))
	}
	if err := os.WriteFile(listPath, []byte(content.String()), 0644); err != nil {
		return err
	}
	defer os.Remove(listPath)

	return video.ConcatAudio(ctx, listPath, absOut)
}

func chooseRandomSong(songsDir string) string {
	entries, err := os.ReadDir(songsDir)
	if err != nil {
		return ""
	}
	var songs []string
	for _, e := range entries {
		if !e.IsDir() && (strings.HasSuffix(e.Name(), ".mp3") || strings.HasSuffix(e.Name(), ".m4a")) {
			songs = append(songs, filepath.Join(songsDir, e.Name()))
		}
	}
	if len(songs) == 0 {
		return ""
	}
	return songs[rand.Intn(len(songs))]
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Dir(dst), 0755)
	return os.WriteFile(dst, data, 0644)
}

func randomName() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
