package imagegen

import "context"

// Request describes what to generate.
type Request struct {
	Prompt    string   // detailed image description
	RefImages []string // local file paths to reference images for character consistency
	Width     int      // target width (e.g. 1080)
	Height    int      // target height (e.g. 1350 for 4:5 Instagram portrait)
	Count     int      // number of images to generate (default 1)
}

// Result holds the generated image paths.
type Result struct {
	ImagePaths []string // local file paths where images were saved
}

// Provider generates images from text prompts with optional reference images.
type Provider interface {
	Generate(ctx context.Context, req Request, outputDir string) (Result, error)
	Name() string
}
