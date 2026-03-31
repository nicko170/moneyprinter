package tts

import "context"

// Provider generates speech audio from text.
type Provider interface {
	Synthesize(ctx context.Context, text, voice, outPath string) error
}
