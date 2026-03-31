package video

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// ffmpeg runs an ffmpeg command with context, returning combined stderr on failure.
func ffmpeg(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg %s: %w\n%s", strings.Join(args[:min(len(args), 4)], " "), err, string(output))
	}
	return nil
}

// ffprobe runs ffprobe and returns stdout.
func ffprobe(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// GetDuration returns the duration of a media file in seconds.
func GetDuration(ctx context.Context, path string) (float64, error) {
	out, err := ffprobe(ctx,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	if err != nil {
		return 0, err
	}
	var dur float64
	if _, err := fmt.Sscanf(out, "%f", &dur); err != nil {
		return 0, fmt.Errorf("parsing duration %q: %w", out, err)
	}
	return dur, nil
}

// ConcatAndCrop takes multiple video files, concatenates them up to maxDuration,
// crops to 9:16, and outputs at 1080x1920. Returns the output path.
// ConcatAndCrop concatenates video clips, crops to 9:16, and applies effects.
// effects: slice of enabled effects ("slowmo", "kenburns"). One is randomly
// chosen per clip segment to keep looped footage visually varied.
func ConcatAndCrop(ctx context.Context, videoPaths []string, maxDuration float64, effects []string, tempDir string) (string, error) {
	if len(videoPaths) == 0 {
		return "", fmt.Errorf("no video paths provided")
	}

	outPath := filepath.Join(tempDir, uuid.New().String()+".mp4")

	// Build a clip list — repeat to fill duration.
	var totalSourceDur float64
	for _, p := range videoPaths {
		dur, err := GetDuration(ctx, p)
		if err == nil {
			totalSourceDur += dur
		}
	}
	loops := 1
	if totalSourceDur > 0 && totalSourceDur < maxDuration {
		loops = int(maxDuration/totalSourceDur) + 1
	}

	// Process each clip individually with a randomly assigned effect,
	// then concat the processed clips.
	var processedPaths []string
	clipIdx := 0
	for range loops {
		for _, p := range videoPaths {
			// Pick a random effect for this clip (or none).
			filter := "crop='min(iw,ih*9/16)':'min(ih,iw*16/9)',scale=1080:1920,setsar=1"
			if len(effects) > 0 {
				chosen := effects[clipIdx%len(effects)]
				switch chosen {
				case "slowmo":
					filter += ",setpts=1.3*PTS"
				case "kenburns":
					filter += ",zoompan=z='min(zoom+0.0003,1.15)':x='iw/2-(iw/zoom/2)':y='ih/2-(ih/zoom/2)':d=1:s=1080x1920:fps=30"
				}
			}

			processedPath := filepath.Join(tempDir, fmt.Sprintf("clip_%d.mp4", clipIdx))
			err := ffmpeg(ctx,
				"-y",
				"-i", p,
				"-vf", filter,
				"-r", "30",
				"-c:v", "libx264",
				"-preset", "fast",
				"-an",
				processedPath,
			)
			if err != nil {
				return "", fmt.Errorf("processing clip %d: %w", clipIdx, err)
			}
			processedPaths = append(processedPaths, processedPath)
			clipIdx++
		}
	}

	// Concat all processed clips.
	listPath := filepath.Join(tempDir, uuid.New().String()+".txt")
	var listContent strings.Builder
	for _, p := range processedPaths {
		abs, _ := filepath.Abs(p)
		listContent.WriteString(fmt.Sprintf("file '%s'\n", abs))
	}
	if err := os.WriteFile(listPath, []byte(listContent.String()), 0644); err != nil {
		return "", fmt.Errorf("writing concat list: %w", err)
	}
	defer os.Remove(listPath)

	err := ffmpeg(ctx,
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-t", fmt.Sprintf("%.2f", maxDuration),
		"-c", "copy",
		outPath,
	)
	if err != nil {
		return "", err
	}

	return outPath, nil
}

// BurnSubtitles hard-burns subtitles into the video using the libass subtitles filter.
// Requires ffmpeg built with libass (startup check enforces this).
func BurnSubtitles(ctx context.Context, videoPath, srtPath, fontPath, textColor, tempDir string) (string, error) {
	outPath := filepath.Join(tempDir, uuid.New().String()+".mp4")
	absSRT, _ := filepath.Abs(srtPath)

	textBGR := hexToBGR(textColor)

	// Calculate font size dynamically based on longest subtitle line.
	fontSize := calcFontSize(srtPath, 1080, 100)

	// BorderStyle=1: outline + drop shadow. Clean TikTok look.
	// Alignment is handled by \an tags in the SRT cues, not here.
	style := fmt.Sprintf(
		"FontName=TikTok Sans,Bold=1,FontSize=%d,PrimaryColour=&H00%s&,OutlineColour=&H00000000&,BorderStyle=1,Outline=3,Shadow=2,ShadowColour=&H80000000&,MarginV=80,MarginL=100,MarginR=100",
		fontSize, textBGR,
	)
	escapedSRT := escapeFilterPath(absSRT)
	subtitleFilter := fmt.Sprintf("subtitles=filename=%s:force_style='%s'", escapedSRT, style)
	if fontPath != "" {
		absFontsDir, _ := filepath.Abs(filepath.Dir(fontPath))
		subtitleFilter = fmt.Sprintf("subtitles=filename=%s:fontsdir=%s:force_style='%s'",
			escapedSRT, escapeFilterPath(absFontsDir), style)
	}

	err := ffmpeg(ctx,
		"-y",
		"-i", videoPath,
		"-vf", subtitleFilter,
		"-c:v", "libx264",
		"-preset", "medium",
		"-c:a", "copy",
		outPath,
	)
	if err != nil {
		return "", err
	}

	return outPath, nil
}

// MergeAudio combines a video (no audio or to be replaced) with an audio track.
func MergeAudio(ctx context.Context, videoPath, audioPath, outPath string) error {
	return ffmpeg(ctx,
		"-y",
		"-i", videoPath,
		"-i", audioPath,
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-c:v", "copy",
		"-c:a", "aac",
		"-b:a", "192k",
		"-shortest",
		outPath,
	)
}

// MixBackgroundMusic mixes background music at the given volume (0.0-1.0) with the video's audio.
func MixBackgroundMusic(ctx context.Context, videoPath, songPath, outPath string, musicVolume float64) error {
	filter := fmt.Sprintf(
		"[1:a]volume=%.2f,aloop=loop=-1:size=2e+09[bg];[0:a][bg]amix=inputs=2:duration=first:dropout_transition=2[out]",
		musicVolume,
	)
	return ffmpeg(ctx,
		"-y",
		"-i", videoPath,
		"-i", songPath,
		"-filter_complex", filter,
		"-map", "0:v:0",
		"-map", "[out]",
		"-c:v", "copy",
		"-c:a", "aac",
		"-b:a", "192k",
		"-shortest",
		outPath,
	)
}

// ConcatAudio concatenates audio files listed in a concat text file.
func ConcatAudio(ctx context.Context, listPath, outPath string) error {
	return ffmpeg(ctx,
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-c", "copy",
		outPath,
	)
}

// escapeFilterPath escapes special chars for ffmpeg filter path arguments.
func escapeFilterPath(path string) string {
	// ffmpeg filter syntax needs colons, backslashes, and single quotes escaped.
	r := strings.NewReplacer(
		`\`, `\\`,
		`:`, `\:`,
		`'`, `\'`,
	)
	return r.Replace(path)
}

// positionToASS converts "horizontal,vertical" to ASS Alignment code and MarginV.
// ASS alignment numpad: 1=bottom-left, 2=bottom-center, 3=bottom-right,
// 4=mid-left, 5=mid-center, 6=mid-right, 7=top-left, 8=top-center, 9=top-right.
// positionToASS is no longer used — alignment is handled by \an tags in SRT cues.
// See subtitles.go PositionToASSTag().

// calcFontSize reads an SRT file, finds the longest text line, and computes
// a font size that fits within the video width minus margins.
// Tuned for TikTok-style 2-4 word subtitle chunks with TikTokSans-Bold.
func calcFontSize(srtPath string, videoWidth, margin int) int {
	usable := float64(videoWidth - 2*margin)

	data, err := os.ReadFile(srtPath)
	if err != nil {
		return 36
	}

	// Find longest subtitle text line.
	maxChars := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "-->") {
			continue
		}
		// Skip index lines (pure digits).
		isIndex := true
		for _, c := range line {
			if c < '0' || c > '9' {
				isIndex = false
				break
			}
		}
		if isIndex {
			continue
		}
		if len(line) > maxChars {
			maxChars = len(line)
		}
	}

	if maxChars == 0 {
		return 36
	}

	// TikTokSans-Bold: average char width is ~0.52 × fontSize.
	// Solve: maxChars × 0.52 × fontSize = usable
	fontSize := int(usable / (float64(maxChars) * 0.52))

	// Clamp: min 16 (still readable), max 24 (fits 3-word stacked chunks at 1080p).
	if fontSize < 16 {
		fontSize = 16
	}
	if fontSize > 24 {
		fontSize = 24
	}

	return fontSize
}

// isColorDark returns true if a hex colour is perceptually dark (luminance < 128).
func isColorDark(hex string) bool {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return false
	}
	r, g, b := hexByte(hex[0:2]), hexByte(hex[2:4]), hexByte(hex[4:6])
	// Perceived luminance formula.
	lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	return lum < 128
}

func hexByte(s string) int {
	v := 0
	for _, c := range s {
		v *= 16
		switch {
		case c >= '0' && c <= '9':
			v += int(c - '0')
		case c >= 'a' && c <= 'f':
			v += int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v += int(c-'A') + 10
		}
	}
	return v
}

// hexToBGR converts "#RRGGBB" or "RRGGBB" to BGR format for ASS/SSA subtitle styling.
func hexToBGR(hex string) string {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return "00FFFF" // default yellow in BGR
	}
	// Swap RR and BB
	return hex[4:6] + hex[2:4] + hex[0:2]
}
