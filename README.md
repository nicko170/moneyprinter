# MoneyPrinter

Automated short-form video generation for YouTube Shorts, TikTok, and Instagram Reels. Give it a topic, and it writes a script, finds stock footage, generates narration, burns subtitles, and outputs a ready-to-upload vertical video.

Built in Go with a web UI. No Python dependencies.

## How It Works

```
Topic or theme
    |
    v
LLM writes a punchy 30-60s script
    |
    v
Pexels API finds matching stock video clips
    |
    v
TikTok TTS narrates the script sentence-by-sentence
    |
    v
FFmpeg composes 9:16 video, burns TikTok-style subtitles, merges audio
    |
    v
Optional: end card with logo/CTA, background music
    |
    v
Final MP4 ready for upload
```

Videos can be generated individually or as a series - provide a theme and episode count, and the LLM generates distinct topics for each episode automatically.

## Features

- Script generation via any OpenAI-compatible LLM endpoint
- Configurable hook styles (question, myth-buster, stop-scrolling, etc.) and tone presets
- TikTok-style animated subtitles with customizable color, position, and font
- Stock video sourcing from Pexels with automatic 9:16 cropping
- Text-to-speech via TikTok TTS (18+ voices) or Chatterbox voice cloning
- Voice preview - audition voices before generating
- Series mode - batch-generate themed episode sets
- End cards with logo, CTA text, and configurable background
- Background music mixing (auto-ducked under narration)
- Social media metadata generation (title, description, hashtags)
- Resume mode - interrupted jobs restart from where they left off
- Reburn subtitles without re-downloading videos or regenerating audio
- Real-time job progress via live event stream
- Click-to-copy social media copy on the job detail page

## Prerequisites

- **Go 1.25+**
- **FFmpeg** built with **libass** (for subtitle burning)
- **FFprobe** (usually included with FFmpeg)
- **ImageMagick** (for end card generation)
- **Node.js** (for Tailwind CSS CLI)
- **Task** (task runner - https://taskfile.dev)
- **templ** (Go HTML templating - https://templ.guide)

### macOS

```bash
brew install go task imagemagick node
go install github.com/a-h/templ/cmd/templ@latest
```

**FFmpeg with libass** - the default Homebrew FFmpeg does not include libass, which is required for subtitle burning. You need the full build from the `homebrew-ffmpeg` tap:

```bash
# If you already have the default FFmpeg installed, remove it first
brew uninstall ffmpeg

# Install the full FFmpeg with libass support
brew install homebrew-ffmpeg/ffmpeg/ffmpeg
```

Verify subtitle support is working:

```bash
ffmpeg -filters 2>&1 | grep subtitles
```

### Linux

```bash
# Debian/Ubuntu
sudo apt install ffmpeg imagemagick nodejs npm
go install github.com/a-h/templ/cmd/templ@latest
# Install Task: https://taskfile.dev/installation/
```

## Quick Start

```bash
git clone https://github.com/your-org/moneyprinter.git
cd moneyprinter

# Install npm dependencies (Tailwind CSS)
npm install

# Create config from sample
task setup:env

# Edit state.json with your API keys
# (at minimum: inference_url, inference_api_key, inference_model, pexels_api_key)

# Start development server with hot reload
task dev
```

The UI will be available at http://localhost:8080.

## Configuration

All configuration lives in `state.json` (gitignored). Copy `sample.state.json` to get started:

```json
{
  "inference_url": "",
  "inference_api_key": "",
  "inference_model": "",
  "pexels_api_key": "",
  "tts_provider": "tiktok",
  "tts_tiktok_session_id": "",
  "tts_chatterbox_url": "http://localhost:7860",
  "tts_chatterbox_voice_ref": "",
  "assembly_ai_api_key": "",
  "imagemagick_binary": "",
  "output_dir": "./output",
  "temp_dir": "./temp",
  "fonts_dir": "./fonts",
  "songs_dir": "./songs"
}
```

### Required

| Key | Description |
|-----|-------------|
| `inference_url` | OpenAI-compatible API endpoint (e.g. `https://api.openai.com/v1`) |
| `inference_api_key` | API key for your LLM provider |
| `inference_model` | Model name (e.g. `gpt-4o`, `claude-sonnet-4-20250514`) |
| `pexels_api_key` | Free API key from https://www.pexels.com/api/ |

### Optional

| Key | Description |
|-----|-------------|
| `tts_provider` | `tiktok` (default, free) or `chatterbox` (local voice cloning) |
| `tts_chatterbox_url` | Chatterbox Gradio server URL |
| `tts_chatterbox_voice_ref` | Path to reference WAV for voice cloning |
| `imagemagick_binary` | Custom path to ImageMagick if not on PATH |
| `songs_dir` | Directory of MP3/M4A files for background music |

## Usage

### Create a Single Video

1. Go to **Create Job** in the nav
2. Enter a topic (e.g. "Why sourdough bread is better than store-bought")
3. Optionally paste background material (articles, research, product specs) for the AI to reference
4. Choose a hook style and tone
5. Expand **Advanced Options** to pick a voice (with preview), subtitle style, and script length
6. Click Generate

### Create a Series

1. Go to **Create Series** in the nav
2. Enter a theme and episode count (e.g. "Distillation techniques for home brewers", 7 episodes)
3. The LLM generates distinct topics for each episode
4. All episodes are queued and processed by the worker pool

### Reburn Subtitles

If you change subtitle settings and want to re-render without regenerating scripts, audio, or video:

1. Go to the series detail page
2. Click **Reburn Subtitles**
3. Only the SRT generation, subtitle burn, and audio merge steps re-run - everything else is cached

## Project Structure

```
cmd/server/          Entry point - HTTP server and worker pool
internal/
  inference/         LLM client (OpenAI-compatible)
  pipeline/          Video generation pipeline orchestrator
  pexels/            Stock video search and download
  tts/               Text-to-speech providers (TikTok, Chatterbox)
  video/             FFmpeg/ImageMagick operations (compose, subtitles, end cards)
  job/               Job queue and series management (JSON persistence)
  state/             Configuration loader
templates/           Templ HTML templates (dashboard, create, detail pages)
components/          templui UI component library
static/
  css/               Tailwind CSS output
  fonts/             TikTok Sans font family
  js/                Page-specific JavaScript
output/              Generated videos (gitignored)
temp/                Per-job working directories (gitignored)
songs/               Background music library
```

## Commands

```bash
task dev              # Start dev server with hot reload (templ + Tailwind watchers)
task templ            # Run templ watcher only (proxy on :8080)
task tailwind         # Run Tailwind watcher only
task tailwind:build   # Production CSS build (minified)
task setup:env        # Create state.json from sample

go run ./cmd/server   # Run server directly
go build ./...        # Compile check
go test ./...         # Run tests
```

## Pipeline Details

Each job runs through these steps. Completed steps are cached in `temp/{jobID}/` and skipped on re-run:

| Step | Output | Description |
|------|--------|-------------|
| 1. Script | `script.txt` | LLM generates 80-120 word script with hook and tone |
| 1b. Metadata | `metadata.json` | LLM generates title, description, hashtags for social posting |
| 2. Search terms | `terms.json` | LLM extracts 5 stock video search queries |
| 3. Videos | `videos.json` + clips | Pexels API downloads matching stock footage |
| 4. TTS | `tts.mp3` + `timings.json` | Sentences synthesized and concatenated with timing data |
| 5. Subtitles | `subtitles.srt` | TikTok-style 2-4 word cues, character-weighted timing |
| 6. Compose | `combined.mp4` | Stock clips concatenated and cropped to 9:16 at 1080x1920 |
| 6a. Burn | `subtitled.mp4` | Subtitles hard-burned via FFmpeg libass |
| 6b. Merge | `merged.mp4` | TTS audio merged into video |
| 7. End card | `with_endcard.mp4` | Optional branded end card appended |
| 8. Music | Final output | Optional background music mixed at low volume |

## API

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/generate` | Submit a single video job |
| `POST` | `/api/series` | Submit a series (auto-generates topics) |
| `GET` | `/api/jobs/{id}` | Get job status and details |
| `GET` | `/api/jobs/{id}/events?after={n}` | Poll for new job events |
| `GET` | `/api/jobs/{id}/metadata` | Get generated social media copy |
| `POST` | `/api/jobs/{id}/cancel` | Cancel a running job |
| `POST` | `/api/jobs/{id}/reburn` | Clear subtitle cache and re-queue |
| `POST` | `/api/series/{id}/reburn` | Reburn all episodes in a series |
| `POST` | `/api/tts/preview` | Preview a TTS voice (cached) |
| `POST` | `/api/upload-logo` | Upload end card logo |
| `POST` | `/api/upload-songs` | Upload background music files |

## Contributing

Contributions are gladly received. Please open an issue first to discuss what you would like to change, then submit a pull request.

## License

This project is licensed under the [GNU Affero General Public License v3.0](https://www.gnu.org/licenses/agpl-3.0.html) (AGPL-3.0).
