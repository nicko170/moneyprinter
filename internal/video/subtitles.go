package video

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const wordsPerChunk = 3 // TikTok-style: 2-4 words per cue

// SentenceTiming holds a sentence and its audio duration in seconds.
type SentenceTiming struct {
	Text     string
	Duration float64
}

// em/en dashes and non-breaking hyphens → space (sentence-level breaks).
// Regular hyphens (-) are kept as word joiners (e.g. "hyper-stimulated").
var dashRe = regexp.MustCompile(`[—–‑\x{2011}\x{2013}\x{2014}]`)

// punctuation to strip — periods and % handled separately to preserve decimals/percentages
var punctuationRe = regexp.MustCompile(`[,;:"'"'""()\[\]{}…\x{2018}\x{2019}\x{201C}\x{201D}\x{2026}]+`)

// nonLatinRe strips everything outside basic printable ASCII + common accented
// Latin characters. This catches emoji, zero-width joiners, invisible formatters,
// variation selectors, and other non-renderable Unicode that fonts can't display.
var nonLatinRe = regexp.MustCompile(`[^\x{0020}-\x{007E}\x{00A0}-\x{024F}]`)

// PositionToASSTag converts "horizontal,vertical" to an ASS override tag.
// \an2 = bottom-center, \an5 = mid-center, \an8 = top-center
func PositionToASSTag(position string) string {
	parts := strings.SplitN(position, ",", 2)
	vert := "bottom"
	if len(parts) >= 2 {
		vert = strings.TrimSpace(parts[1])
	}
	switch vert {
	case "top":
		return `{\an8}`
	case "center":
		return `{\an5}`
	default:
		return `{\an2}`
	}
}

// GenerateSRTContent builds TikTok-style SRT content: short 2-4 word phrases
// that punch on/off screen in sync with narration.
// position is "horizontal,vertical" e.g. "center,bottom".
func GenerateSRTContent(timings []SentenceTiming, position string) (string, error) {
	assTag := PositionToASSTag(position)
	var sb strings.Builder
	cueIndex := 1
	globalStart := 0.0

	for _, t := range timings {
		words := strings.Fields(t.Text)
		if len(words) == 0 {
			globalStart += t.Duration
			continue
		}

		// Split sentence into short chunks.
		chunks := chunkWords(words, wordsPerChunk)

		// Char-weighted distribution — longer chunks get proportionally more time.
		// This matches TTS speaking rate better than even distribution.
		totalChars := 0
		for _, c := range chunks {
			totalChars += len(c)
		}
		chunkStart := globalStart
		for _, chunk := range chunks {
			chunkDur := t.Duration * (float64(len(chunk)) / float64(totalChars))
			chunkEnd := chunkStart + chunkDur

			// Clean for display: strip non-renderable chars, punctuation, title case.
			display := cleanForDisplay(chunk)

			sb.WriteString(fmt.Sprintf("%d\n", cueIndex))
			sb.WriteString(fmt.Sprintf("%s --> %s\n", formatSRTTime(chunkStart), formatSRTTime(chunkEnd)))
			sb.WriteString(assTag)
			sb.WriteString(display)
			sb.WriteString("\n\n")

			cueIndex++
			chunkStart = chunkEnd
		}

		globalStart += t.Duration
	}

	return sb.String(), nil
}

// stripSentenceDots removes periods that aren't decimal points (between digits).
func stripSentenceDots(s string) string {
	runes := []rune(s)
	var out []rune
	for i, r := range runes {
		if r == '.' {
			prevDigit := i > 0 && runes[i-1] >= '0' && runes[i-1] <= '9'
			nextDigit := i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9'
			if prevDigit && nextDigit {
				out = append(out, r) // keep decimal point
				continue
			}
			continue // strip sentence-ending dot
		}
		out = append(out, r)
	}
	return string(out)
}

// cleanForDisplay strips non-renderable characters, replaces hyphens,
// removes punctuation, and converts to Title Case (preserving all-caps words).
func cleanForDisplay(text string) string {
	// Replace em/en dashes with spaces first (before non-Latin strip removes them).
	cleaned := dashRe.ReplaceAllString(text, " ")
	// Strip emoji, zero-width chars, and anything outside Latin range.
	cleaned = nonLatinRe.ReplaceAllString(cleaned, "")
	// Strip punctuation (but not dots or %).
	cleaned = punctuationRe.ReplaceAllString(cleaned, "")
	// Strip dots only when not decimal points between digits.
	cleaned = stripSentenceDots(cleaned)
	// Collapse multiple spaces.
	cleaned = strings.Join(strings.Fields(cleaned), " ")

	// Title Case each word — preserve words already ALL CAPS (acronyms like AI, GPU, USA).
	words := strings.Fields(cleaned)
	for i, w := range words {
		if w == strings.ToUpper(w) && len(w) >= 2 {
			continue
		}
		words[i] = titleCase(w)
	}

	return strings.Join(words, " ")
}

// titleCase capitalises the first letter of a word, lowercases the rest.
func titleCase(s string) string {
	if len(s) == 0 {
		return s
	}
	runes := []rune(s)
	for i, r := range runes {
		if i == 0 {
			runes[i] = []rune(strings.ToUpper(string(r)))[0]
		} else {
			runes[i] = []rune(strings.ToLower(string(r)))[0]
		}
	}
	return string(runes)
}

// GenerateSRT creates an SRT subtitle file and returns its path.
func GenerateSRT(timings []SentenceTiming, outDir string) (string, error) {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("creating subtitles dir: %w", err)
	}

	outPath := filepath.Join(outDir, uuid.New().String()+".srt")
	content, err := GenerateSRTContent(timings, "center,bottom")
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(outPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("writing SRT file: %w", err)
	}

	return outPath, nil
}

// chunkWords splits a word list into groups of n words.
func chunkWords(words []string, n int) []string {
	var chunks []string
	for i := 0; i < len(words); i += n {
		end := i + n
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[i:end], " "))
	}
	return chunks
}

func formatSRTTime(seconds float64) string {
	totalMS := int(seconds * 1000)
	h := totalMS / 3_600_000
	totalMS %= 3_600_000
	m := totalMS / 60_000
	totalMS %= 60_000
	s := totalMS / 1000
	ms := totalMS % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}
