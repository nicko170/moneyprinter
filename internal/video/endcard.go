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

// EndCardOpts holds options for generating a branded end card.
type EndCardOpts struct {
	BgColor  string // hex color e.g. "#00A651"
	LogoPath string // path to logo image (optional)
	CTAText  string // call-to-action text
	Duration int    // seconds (default 4)
}

// GenerateEndCard creates a branded end card video clip using ImageMagick + ffmpeg.
// Returns the path to the generated MP4 clip.
func GenerateEndCard(ctx context.Context, opts EndCardOpts, tempDir string) (string, error) {
	if opts.BgColor == "" {
		opts.BgColor = "#000000"
	}
	if opts.Duration <= 0 {
		opts.Duration = 4
	}

	imagePath := filepath.Join(tempDir, uuid.New().String()+"_endcard.png")
	videoPath := filepath.Join(tempDir, uuid.New().String()+"_endcard.mp4")

	// Pick text color for contrast against background.
	textColor := "white"
	if !isColorDark(opts.BgColor) {
		textColor = "black"
	}

	// Step 1: Create the end card image with ImageMagick.
	magickArgs := []string{
		"-size", "1080x1920",
		"xc:" + opts.BgColor,
	}

	// Add logo if provided.
	if opts.LogoPath != "" {
		// Resize logo to max 500px wide, center in upper third.
		magickArgs = append(magickArgs,
			"(", opts.LogoPath, "-resize", "500x500>", ")",
			"-gravity", "North",
			"-geometry", "+0+500",
			"-composite",
		)
	}

	// Add CTA text if provided — word-wrapped at 80% width, centered.
	if opts.CTAText != "" {
		ctaText := strings.ReplaceAll(opts.CTAText, "\\n", "\n")
		magickArgs = append(magickArgs,
			"(",
			"-size", "864x",  // 80% of 1080 = 864
			"-background", "none",
			"-fill", textColor,
			"-font", "Arial-Bold",
			"-pointsize", "42",
			"-gravity", "Center",
			"caption:"+ctaText,
			")",
			"-gravity", "Center",
			"-geometry", "+0+100",
			"-composite",
		)
	}

	magickArgs = append(magickArgs, imagePath)

	magickCmd := exec.CommandContext(ctx, "magick", magickArgs...)
	if output, err := magickCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ImageMagick end card: %w\n%s", err, string(output))
	}

	// Step 2: Convert static image to video clip.
	err := ffmpeg(ctx,
		"-y",
		"-loop", "1",
		"-i", imagePath,
		"-t", fmt.Sprintf("%d", opts.Duration),
		"-vf", "format=yuv420p",
		"-r", "30",
		"-c:v", "libx264",
		"-preset", "medium",
		videoPath,
	)
	if err != nil {
		return "", fmt.Errorf("end card video: %w", err)
	}

	return videoPath, nil
}

// AppendEndCard concatenates the main video with an end card clip.
func AppendEndCard(ctx context.Context, mainVideo, endCardVideo, outPath, tempDir string) error {
	absMain, _ := filepath.Abs(mainVideo)
	absCard, _ := filepath.Abs(endCardVideo)

	listPath := filepath.Join(tempDir, uuid.New().String()+"_endcard_list.txt")
	content := fmt.Sprintf("file '%s'\nfile '%s'\n", absMain, absCard)
	if err := writeFile(listPath, []byte(content)); err != nil {
		return err
	}

	return ffmpeg(ctx,
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-c", "copy",
		outPath,
	)
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}
