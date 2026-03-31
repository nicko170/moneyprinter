# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

MoneyPrinter automates short-form video creation (YouTube Shorts, Instagram Reels, TikTok) from text topics. The repo contains two implementations:

- **`MoneyPrinter-Python/`** — Production Python version (Flask API, Postgres job queue, moviepy/ImageMagick video pipeline). Has its own `CLAUDE.md` and `AGENTS.md` with detailed conventions.
- **Root (`./`)** — In-progress Go rewrite targeting the same functionality without Python dependencies. Uses Taskfile, templ, and Tailwind CSS.

## Go Rewrite (root project)

### Tech Stack
- **Go 1.25+**, **templ** (HTML templating with hot reload), **Tailwind CSS** (via npx)
- **templui** —  UI component library; available and required
- **Taskfile v3** for task automation
- Config stored in **`state.json`** (copied from `sample.state.json` on setup)
- Entry point: `cmd/server`

### Commands

```bash
# Setup
task setup:env                 # copies sample.state.json -> state.json

# Development (starts templ hot-reload + Tailwind watcher)
task dev

# Individual watchers
task templ                     # templ generate with proxy on :8080
task tailwind                  # watch Tailwind CSS changes
task tailwind:build            # production CSS build (minified)

# Build/run manually
go run ./cmd/server            # start server on :8080
go build ./...                 # compile check
go test ./...                  # run all tests
go test ./pkg/foo -run TestBar # single test
```

## Python Version (`MoneyPrinter-Python/`)

See `MoneyPrinter-Python/CLAUDE.md` for full details. Quick reference:

```bash
cd MoneyPrinter-Python
uv sync                                              # install deps
uv run python Backend/main.py                        # API on :8080
uv run python Backend/worker.py                      # job worker
python3 -m http.server 3000 --directory Frontend     # frontend on :3000
uv run pytest                                        # run tests
uv run python -m compileall Backend                  # syntax check
docker compose up --build                            # full Docker stack
```

### Pipeline Flow
User input -> Flask API -> Postgres job queue -> Worker claims job -> Ollama script generation -> Pexels stock video -> TikTok TTS -> moviepy/ImageMagick video composition -> output.mp4

### Required env vars: `TIKTOK_SESSION_ID`, `PEXELS_API_KEY`

## Design Goals (from PRD)

- Go rewrite should avoid Python dependencies; prefer tools installable via brew
- Config stored in `state.json`; custom inference API endpoint replaces Ollama
- Target platforms: macOS and Linux first
- Output suitable for YouTube, Instagram, Twitter, and TikTok (controllable via UI/config)
