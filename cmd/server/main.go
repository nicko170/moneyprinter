package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moneyprinter/internal/agent"
	ytpkg "github.com/moneyprinter/internal/youtube"
	"github.com/moneyprinter/internal/draft"
	"github.com/moneyprinter/internal/imagegen"
	"github.com/moneyprinter/internal/inference"
	"github.com/moneyprinter/internal/job"
	"github.com/moneyprinter/internal/model"
	"github.com/moneyprinter/internal/modelagent"
	"github.com/moneyprinter/internal/pipeline"
	"github.com/moneyprinter/internal/state"
	"github.com/moneyprinter/templates"
)

const (
	statePath   = "state.json"
	workerCount = 2
)

func loadState() *state.State {
	cfg, err := state.Load(statePath)
	if err != nil {
		log.Printf("Warning: failed to reload %s: %v", statePath, err)
		return nil
	}
	return cfg
}

func main() {
	cfg := loadState()
	if cfg == nil {
		fmt.Fprintf(os.Stderr, "Failed to load %s\n", statePath)
		fmt.Fprintf(os.Stderr, "Run 'task setup:env' to create it from sample.state.json\n")
		os.Exit(1)
	}
	log.Printf("State loaded from %s", statePath)

	// Verify required tools are available.
	checkDependencies()

	queue := job.NewQueue("jobs.json")
	series := job.NewSeriesStore("series.json")
	drafts := draft.NewStore("drafts.json")
	seriesDrafts := draft.NewSeriesDraftStore("series_drafts.json")
	models := model.NewStore("models.json")

	// Start worker pool.
	for i := range workerCount {
		go worker(i, queue, series, cfg)
	}

	// Start schedulers.
	go seriesScheduler(queue, series, cfg)
	go modelScheduler(models, cfg)

	mux := http.NewServeMux()

	// --- Static files ---
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.Handle("GET /output/", http.StripPrefix("/output/", http.FileServer(http.Dir(cfg.OutputDir))))
	mux.Handle("GET /model-images/", http.StripPrefix("/model-images/", http.FileServer(http.Dir("data/models"))))

	// --- Page routes ---
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/shorts", http.StatusFound)
	})

	mux.HandleFunc("GET /shorts", func(w http.ResponseWriter, r *http.Request) {
		templates.Dashboard(templates.DashboardProps{
			Jobs:         queue.List(),
			SeriesList:   series.List(),
			Drafts:       drafts.List(),
			SeriesDrafts: seriesDrafts.List(),
		}).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /shorts/create", func(w http.ResponseWriter, r *http.Request) {
		current := loadState()
		if current == nil {
			current = cfg
		}
		templates.CreateJob(templates.CreateJobProps{
			InferenceURL:     current.InferenceURL,
			InferenceModel:   current.InferenceModel,
			TTSProvider:      current.TTSProvider,
			YouTubeConnected: current.YouTubeRefreshToken != "",
		}).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /shorts/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		j := queue.Get(r.PathValue("id"))
		if j == nil {
			http.NotFound(w, r)
			return
		}
		templates.JobDetail(j).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /shorts/drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		d := drafts.Get(r.PathValue("id"))
		if d == nil {
			http.NotFound(w, r)
			return
		}
		templates.DraftDetail(d).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /shorts/series-drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		sd := seriesDrafts.Get(r.PathValue("id"))
		if sd == nil {
			http.NotFound(w, r)
			return
		}
		templates.SeriesDraftDetail(sd).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /shorts/series/{id}", func(w http.ResponseWriter, r *http.Request) {
		ser := series.Get(r.PathValue("id"))
		if ser == nil {
			http.NotFound(w, r)
			return
		}
		jobs := queue.ListBySeries(ser.ID)
		templates.SeriesDetail(ser, jobs).Render(r.Context(), w)
	})


	// --- Model page routes ---
	mux.HandleFunc("GET /models", func(w http.ResponseWriter, r *http.Request) {
		templates.ModelsDashboard(models.List()).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /models/create", func(w http.ResponseWriter, r *http.Request) {
		templates.ModelCreate().Render(r.Context(), w)
	})

	mux.HandleFunc("GET /models/{id}", func(w http.ResponseWriter, r *http.Request) {
		m := models.Get(r.PathValue("id"))
		if m == nil {
			http.NotFound(w, r)
			return
		}
		templates.ModelDetail(m).Render(r.Context(), w)
	})

	// --- Model API routes ---
	mux.HandleFunc("POST /api/models", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name        string `json:"name"`
			Handle      string `json:"handle"`
			Bio         string `json:"bio"`
			Description string `json:"description"`
			Personality string `json:"personality"`
			Style       string `json:"style"`
			Schedule    string `json:"schedule"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Description == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "name and description required"})
			return
		}
		if req.Schedule == "" {
			req.Schedule = "24h"
		}
		m := models.Create(req.Name, req.Handle, req.Bio, req.Description, req.Personality, req.Style, req.Schedule)
		log.Printf("Created model %s: %q", m.ID[:8], m.Name)
		writeJSON(w, http.StatusOK, map[string]string{"status": "success", "modelId": m.ID})
	})

	mux.HandleFunc("GET /api/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "success", "models": models.List()})
	})

	mux.HandleFunc("GET /api/models/{id}", func(w http.ResponseWriter, r *http.Request) {
		m := models.Get(r.PathValue("id"))
		if m == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Model not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success", "model": m})
	})

	mux.HandleFunc("GET /api/models/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		m := models.Get(r.PathValue("id"))
		if m == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Model not found"})
			return
		}
		afterID := 0
		if v := r.URL.Query().Get("after"); v != "" {
			afterID, _ = strconv.Atoi(v)
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success", "events": m.GetEvents(afterID)})
	})

	mux.HandleFunc("POST /api/models/{id}/pause", func(w http.ResponseWriter, r *http.Request) {
		m := models.Get(r.PathValue("id"))
		if m == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Model not found"})
			return
		}
		m.Status = model.ModelStatusPaused
		m.AppendLog("Model paused", "warning")
		models.PersistNow()
		writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
	})

	mux.HandleFunc("POST /api/models/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		m := models.Get(r.PathValue("id"))
		if m == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Model not found"})
			return
		}
		m.Status = model.ModelStatusActive
		m.NextRunAt = time.Now()
		if m.NextPlannedPost() == nil {
			m.AddPlannedPosts(1)
		}
		m.AppendLog("Model resumed", "info")
		models.PersistNow()
		writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
	})

	mux.HandleFunc("POST /api/models/{id}/trigger", func(w http.ResponseWriter, r *http.Request) {
		m := models.Get(r.PathValue("id"))
		if m == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Model not found"})
			return
		}
		if m.NextPlannedPost() == nil {
			m.AddPlannedPosts(1)
		}
		m.NextRunAt = time.Now()
		m.Status = model.ModelStatusActive
		m.AppendLog("Post manually triggered", "info")
		models.PersistNow()
		writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
	})

	mux.HandleFunc("POST /api/models/{id}/generate-refs", func(w http.ResponseWriter, r *http.Request) {
		m := models.Get(r.PathValue("id"))
		if m == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Model not found"})
			return
		}

		current := loadState()
		if current == nil {
			current = cfg
		}
		provider := selectImageGenProvider(current)

		go func() {
			m.AppendLog("Generating reference images...", "info")
			models.PersistNow()

			refDir := filepath.Join("data", "models", m.ID, "refs")
			os.MkdirAll(refDir, 0755)

			prompt := fmt.Sprintf("Professional portrait photo of %s. %s photography, studio lighting, high quality, 4k, detailed face, looking at camera.",
				m.Description, m.Style)

			result, err := provider.Generate(context.Background(), imagegen.Request{
				Prompt: prompt,
				Width:  1080,
				Height: 1350,
				Count:  3,
			}, refDir)
			if err != nil {
				m.AppendLog(fmt.Sprintf("Reference generation failed: %v", err), "error")
				models.PersistNow()
				return
			}

			m.RefImages = result.ImagePaths
			m.Status = model.ModelStatusActive
			m.NextRunAt = time.Now()
			m.AddPlannedPosts(1)
			m.AppendLog(fmt.Sprintf("Generated %d reference images — model activated", len(result.ImagePaths)), "success")
			models.PersistNow()

			log.Printf("[model:%s] Reference images generated, model active", m.ID[:8])
		}()

		writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "Generating reference images..."})
	})

	mux.HandleFunc("POST /api/models/randomise", func(w http.ResponseWriter, r *http.Request) {
		current := loadState()
		if current == nil {
			current = cfg
		}
		llm := inference.NewClient(current.InferenceURL, current.InferenceModel, current.InferenceAPIKey)

		// Build context of existing models so the AI avoids duplicates.
		var existingDesc string
		for _, m := range models.List() {
			existingDesc += fmt.Sprintf("- %s (@%s): %s\n", m.Name, m.Handle, m.Description)
		}

		// Random seed elements to force diversity.
		regions := []string{"East Asian", "South Asian", "Southeast Asian", "Middle Eastern", "North African", "West African", "East African", "Caribbean", "Latin American", "Southern European", "Northern European", "Eastern European", "Scandinavian", "Pacific Islander", "Indigenous Australian", "Korean", "Japanese", "Filipino", "Brazilian", "Nigerian", "Ethiopian", "Moroccan", "Turkish", "Persian", "Colombian", "Mexican", "Argentinian", "Irish", "Italian", "Greek", "Polish", "Russian", "Swedish", "French"}
		ages := []string{"early 20s", "mid 20s", "late 20s", "early 30s", "mid 30s", "late 30s", "early 40s"}
		niches := []string{"rock climbing and outdoor adventure", "vintage fashion and thrift finds", "street food and hidden restaurants", "digital art and creative coding", "yoga and mindfulness", "tattoo culture and body art", "motorcycle touring", "sustainable living and zero waste", "urban photography", "competitive gaming", "plant parenthood and indoor gardens", "vinyl records and music production", "surfing and beach culture", "book reviews and cozy aesthetics", "boxing and martial arts", "pottery and ceramics", "van life and road trips", "architecture and brutalist buildings", "craft cocktails and mixology", "trail running and ultramarathons", "vintage cars and restoration", "baking sourdough and pastry art", "birdwatching and nature", "standup comedy and open mics", "figure skating", "graffiti and street art", "chess and board games", "scuba diving and marine life", "film photography on 35mm", "woodworking and furniture making"}
		vibes := []string{"chaotic and unfiltered", "moody and introspective", "warm and wholesome", "sharp and sarcastic", "bubbly and energetic", "mysterious and minimalist", "goofy and self-deprecating", "poetic and dreamy", "bold and confrontational", "chill and laid-back", "nerdy and enthusiastic", "glamorous and unapologetic", "dry humor and deadpan", "earnest and vulnerable"}
		genders := []string{"woman", "man", "non-binary person"}

		region := regions[rand.Intn(len(regions))]
		age := ages[rand.Intn(len(ages))]
		niche := niches[rand.Intn(len(niches))]
		vibe := vibes[rand.Intn(len(vibes))]
		gender := genders[rand.Intn(len(genders))]

		prompt := fmt.Sprintf(`Create an Instagram model profile for a %s %s %s in their %s who is into %s. Their vibe is %s.

Return ONLY valid JSON:
{
  "name": "full display name (culturally appropriate for their background)",
  "handle": "instagram.handle (creative, not just their name)",
  "bio": "Instagram bio under 150 chars — in their voice, reflects their niche",
  "description": "detailed physical appearance for image generation: specific hair color/style/length, eye color/shape, skin tone, body type, facial features, any distinctive features like piercings/tattoos/freckles/dimples. Be extremely specific — this drives visual consistency.",
  "personality": "how they write captions: specific tone, slang they use, emoji habits, topics they rant about, their weird opinions. Make it feel like a real person, not a brand.",
  "style": "one of: candid, editorial, lifestyle, fashion, fitness, street, portrait"
}

This must feel like a REAL person with quirks and specificity. Not a generic model. Their appearance, personality, and niche should all be coherent.`, gender, region, gender, age, niche, vibe)

		if existingDesc != "" {
			prompt += "\n\nDo NOT create anyone similar to these existing models:\n" + existingDesc
		}

		response, err := llm.Generate(r.Context(), prompt)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": fmt.Sprintf("LLM error: %v", err)})
			return
		}

		// Extract JSON from response.
		var result map[string]string
		if err := json.Unmarshal([]byte(response), &result); err != nil {
			start := strings.Index(response, "{")
			end := strings.LastIndex(response, "}")
			if start >= 0 && end > start {
				json.Unmarshal([]byte(response[start:end+1]), &result)
			}
		}
		if result == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": "Failed to parse model profile"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"status": "success", "model": result})
	})

	// --- YouTube OAuth ---
	mux.HandleFunc("GET /api/youtube/auth", func(w http.ResponseWriter, r *http.Request) {
		current := loadState()
		if current == nil {
			current = cfg
		}
		if current.YouTubeClientID == "" || current.YouTubeClientSecret == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "Set youtube_client_id and youtube_client_secret in state.json first"})
			return
		}
		redirectURL := "http://" + r.Host + "/api/youtube/callback"
		url := ytpkg.AuthURL(current.YouTubeClientID, current.YouTubeClientSecret, redirectURL)
		http.Redirect(w, r, url, http.StatusFound)
	})

	mux.HandleFunc("GET /api/youtube/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "No authorization code"})
			return
		}
		current := loadState()
		if current == nil {
			current = cfg
		}
		redirectURL := "http://" + r.Host + "/api/youtube/callback"
		refreshToken, err := ytpkg.ExchangeCode(r.Context(), current.YouTubeClientID, current.YouTubeClientSecret, redirectURL, code)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": fmt.Sprintf("Token exchange failed: %v", err)})
			return
		}
		// Save refresh token to state.
		current.YouTubeRefreshToken = refreshToken
		if err := current.Save(statePath); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": fmt.Sprintf("Failed to save token: %v", err)})
			return
		}
		cfg.YouTubeRefreshToken = refreshToken
		log.Printf("YouTube OAuth complete — refresh token saved")
		// Redirect back to shorts page with success.
		http.Redirect(w, r, "/shorts?youtube=connected", http.StatusFound)
	})

	mux.HandleFunc("GET /api/youtube/channels", func(w http.ResponseWriter, r *http.Request) {
		current := loadState()
		if current == nil {
			current = cfg
		}
		if current.YouTubeRefreshToken == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "YouTube not connected"})
			return
		}
		ytClient, err := ytpkg.NewClient(r.Context(), ytpkg.OAuthConfig{
			ClientID:     current.YouTubeClientID,
			ClientSecret: current.YouTubeClientSecret,
			RefreshToken: current.YouTubeRefreshToken,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": fmt.Sprintf("YouTube auth: %v", err)})
			return
		}
		channels, err := ytClient.ListChannels(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": fmt.Sprintf("Listing channels: %v", err)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":            "success",
			"channels":          channels,
			"selectedChannelId": current.YouTubeChannelID,
		})
	})

	mux.HandleFunc("POST /api/youtube/channel", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChannelID string `json:"channelId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChannelID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "channelId required"})
			return
		}
		current := loadState()
		if current == nil {
			current = cfg
		}
		current.YouTubeChannelID = req.ChannelID
		if err := current.Save(statePath); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": fmt.Sprintf("Failed to save: %v", err)})
			return
		}
		cfg.YouTubeChannelID = req.ChannelID
		log.Printf("YouTube channel set to %s", req.ChannelID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
	})

	// --- Shorts API routes ---
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, cfg.Redacted())
	})

	// --- Draft API ---

	mux.HandleFunc("POST /api/shorts/drafts", func(w http.ResponseWriter, r *http.Request) {
		var params pipeline.Params
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil || params.VideoSubject == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "videoSubject is required"})
			return
		}

		payload, _ := json.Marshal(params)
		d := drafts.Create(params.VideoSubject, payload)

		current := loadState()
		if current == nil {
			current = cfg
		}
		llm := inference.NewClient(current.InferenceURL, current.InferenceModel, current.InferenceAPIKey)

		go func() {
			agentCfg := agent.Config{
				LLM:          llm,
				BraveAPIKey:  current.BraveSearchAPIKey,
				VideoSubject: params.VideoSubject,
				TonePreset:   params.TonePreset,
				HookStyle:    params.HookStyle,
				ParagraphNum: params.ParagraphNum,
			}
			onEvent := func(message, level string) {
				d.AppendLog(message, level)
				log.Printf("[draft:%s] %s", d.ID[:8], message)
			}
			result, err := agent.Run(context.Background(), agentCfg, onEvent)
			if err != nil {
				d.Fail(err.Error())
			} else {
				sources := make([]draft.Source, len(result.Sources))
				for i, s := range result.Sources {
					sources[i] = draft.Source{Title: s.Title, URL: s.URL, Snippet: s.Snippet}
				}
				d.Complete(result.Script, sources)
			}
			drafts.PersistNow()
		}()

		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "success",
			"draftId": d.ID,
		})
	})

	mux.HandleFunc("GET /api/shorts/drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		d := drafts.Get(r.PathValue("id"))
		if d == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Draft not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "draft": d})
	})

	mux.HandleFunc("GET /api/shorts/drafts/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		d := drafts.Get(r.PathValue("id"))
		if d == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Draft not found"})
			return
		}
		afterID := 0
		if v := r.URL.Query().Get("after"); v != "" {
			afterID, _ = strconv.Atoi(v)
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "events": d.GetEvents(afterID)})
	})

	mux.HandleFunc("POST /api/shorts/drafts/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		d := drafts.Get(r.PathValue("id"))
		if d == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Draft not found"})
			return
		}
		if d.Status != draft.StatusDone {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "Draft is not ready for approval"})
			return
		}
		var params pipeline.Params
		if err := json.Unmarshal(d.Params, &params); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": "Invalid draft params"})
			return
		}
		params.ScriptOverride = d.Script
		payload, _ := json.Marshal(params)
		jobID := queue.Submit(payload, d.Subject)
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "success",
			"jobId":  jobID,
		})
	})

	// --- Series Draft API ---

	mux.HandleFunc("POST /api/shorts/series-drafts", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Theme        string          `json:"theme"`
			EpisodeCount int             `json:"episodeCount"`
			SharedParams json.RawMessage `json:"sharedParams"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Theme == "" || req.EpisodeCount <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "theme and episodeCount required"})
			return
		}

		sd := seriesDrafts.Create(req.Theme, req.EpisodeCount, req.SharedParams)

		current := loadState()
		if current == nil {
			current = cfg
		}
		llm := inference.NewClient(current.InferenceURL, current.InferenceModel, current.InferenceAPIKey)

		// Decode shared params once for tone/hook fields.
		var sharedParams pipeline.Params
		json.Unmarshal(req.SharedParams, &sharedParams)

		go func() {
			emit := func(msg, level string) {
				sd.AppendLog(msg, level)
				log.Printf("[series-draft:%s] %s", sd.ID[:8], msg)
			}

			// Phase 1: generate episode topics.
			emit("Planning episode topics...", "info")
			topics, err := generateSeriesTopics(context.Background(), llm, req.Theme, "", req.EpisodeCount)
			if err != nil {
				sd.Fail(fmt.Sprintf("Failed to plan topics: %v", err))
				seriesDrafts.PersistNow()
				return
			}
			emit(fmt.Sprintf("Planned %d episode topics", len(topics)), "success")
			sd.SetTopics(topics)
			seriesDrafts.PersistNow()

			// Phase 2: research all episodes in parallel.
			var wg sync.WaitGroup
			for i, topic := range topics {
				wg.Add(1)
				go func(idx int, subject string) {
					defer wg.Done()
					epIdx := idx + 1
					sd.MarkEpisodeResearching(epIdx)
					emit(fmt.Sprintf("[Ep %d] Researching: %s", epIdx, subject), "info")

					agentCfg := agent.Config{
						LLM:          llm,
						BraveAPIKey:  current.BraveSearchAPIKey,
						VideoSubject: subject,
						TonePreset:   sharedParams.TonePreset,
						HookStyle:    sharedParams.HookStyle,
						ParagraphNum: sharedParams.ParagraphNum,
						SeriesTheme:  req.Theme,
						EpisodeIndex: epIdx,
						EpisodeTotal: len(topics),
					}
					onEvent := func(msg, level string) {
						sd.AppendLog(fmt.Sprintf("[Ep %d] %s", epIdx, msg), level)
					}

					result, err := agent.Run(context.Background(), agentCfg, onEvent)
					if err != nil {
						emit(fmt.Sprintf("[Ep %d] Failed: %v", epIdx, err), "error")
						sd.UpdateEpisode(epIdx, draft.EpisodeStatusFailed, "", nil, err.Error())
					} else {
						sources := make([]draft.Source, len(result.Sources))
						for i, s := range result.Sources {
							sources[i] = draft.Source{Title: s.Title, URL: s.URL, Snippet: s.Snippet}
						}
						sd.UpdateEpisode(epIdx, draft.EpisodeStatusDone, result.Script, sources, "")
						emit(fmt.Sprintf("[Ep %d] Done: %s", epIdx, subject), "success")
					}
					seriesDrafts.PersistNow()
				}(i, topic)
			}
			wg.Wait()

			if sd.CheckComplete() {
				emit(fmt.Sprintf("All %d episodes ready for approval", len(topics)), "success")
			}
			seriesDrafts.PersistNow()
		}()

		writeJSON(w, http.StatusOK, map[string]string{
			"status":        "success",
			"seriesDraftId": sd.ID,
		})
	})

	mux.HandleFunc("GET /api/shorts/series-drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		sd := seriesDrafts.Get(r.PathValue("id"))
		if sd == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Series draft not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "draft": sd})
	})

	mux.HandleFunc("GET /api/shorts/series-drafts/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		sd := seriesDrafts.Get(r.PathValue("id"))
		if sd == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Series draft not found"})
			return
		}
		afterID := 0
		if v := r.URL.Query().Get("after"); v != "" {
			afterID, _ = strconv.Atoi(v)
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "events": sd.GetEvents(afterID)})
	})

	mux.HandleFunc("POST /api/shorts/series-drafts/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		sd := seriesDrafts.Get(r.PathValue("id"))
		if sd == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Series draft not found"})
			return
		}
		if sd.Status != draft.SeriesDraftStatusReady {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "Series draft is not ready for approval"})
			return
		}

		var sharedParams pipeline.Params
		if err := json.Unmarshal(sd.SharedParams, &sharedParams); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": "Invalid series params"})
			return
		}

		ser := series.Create(sd.Theme, len(sd.Episodes), "now", sd.SharedParams)
		for _, ep := range sd.Episodes {
			if ep.Status != draft.EpisodeStatusDone {
				continue // skip failed episodes
			}
			params := sharedParams
			params.VideoSubject = ep.Subject
			params.ScriptOverride = ep.Script
			params.SeriesTheme = sd.Theme
			params.EpisodeIndex = ep.Index
			payload, _ := json.Marshal(params)
			jobID := queue.SubmitWithSeries(payload, ep.Subject, ser.ID)
			series.AddJob(ser.ID, jobID)
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"status":   "success",
			"seriesId": ser.ID,
		})
	})

	mux.HandleFunc("GET /api/shorts/jobs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "success",
			"jobs":   queue.List(),
		})
	})

	// Voice preview — synthesize a short sample and stream audio back.
	previewCacheDir := filepath.Join(cfg.TempDir, "preview_cache")
	os.MkdirAll(previewCacheDir, 0755)

	mux.HandleFunc("POST /api/tts/preview", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Voice string `json:"voice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Voice == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "voice is required"})
			return
		}

		// Check cache first.
		cached := filepath.Join(previewCacheDir, req.Voice+".mp3")
		if data, err := os.ReadFile(cached); err == nil && len(data) > 0 {
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Write(data)
			return
		}

		// Synthesize with a 10s timeout.
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		tmpFile := filepath.Join(previewCacheDir, req.Voice+"_tmp.mp3")
		defer os.Remove(tmpFile)

		provider := pipeline.SelectTTSProvider(cfg)
		sampleText := "Here's what this voice sounds like for your video."
		if err := provider.Synthesize(ctx, sampleText, req.Voice, tmpFile); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": fmt.Sprintf("TTS failed: %v", err)})
			return
		}

		data, err := os.ReadFile(tmpFile)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": "Failed to read audio"})
			return
		}

		// Cache for next time.
		os.WriteFile(cached, data, 0644)

		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write(data)
	})

	mux.HandleFunc("POST /api/shorts/generate", func(w http.ResponseWriter, r *http.Request) {
		var params pipeline.Params
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"status":  "error",
				"message": "Invalid request body",
			})
			return
		}
		if params.VideoSubject == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"status":  "error",
				"message": "videoSubject is required",
			})
			return
		}

		payload, _ := json.Marshal(params)
		seriesID := r.URL.Query().Get("seriesId")
		var jobID string
		if seriesID != "" {
			jobID = queue.SubmitWithSeries(payload, params.VideoSubject, seriesID)
			series.AddJob(seriesID, jobID)
		} else {
			jobID = queue.Submit(payload, params.VideoSubject)
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "success",
			"message": "Job queued",
			"jobId":   jobID,
		})
	})

	mux.HandleFunc("GET /api/shorts/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		j := queue.Get(r.PathValue("id"))
		if j == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Job not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "job": j})
	})

	mux.HandleFunc("GET /api/shorts/jobs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		j := queue.Get(r.PathValue("id"))
		if j == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Job not found"})
			return
		}
		afterID := 0
		if v := r.URL.Query().Get("after"); v != "" {
			afterID, _ = strconv.Atoi(v)
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "events": j.GetEvents(afterID)})
	})

	mux.HandleFunc("GET /api/shorts/jobs/{id}/metadata", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		metaPath := filepath.Join(cfg.TempDir, id, "metadata.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "No metadata available"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})

	mux.HandleFunc("GET /api/shorts/jobs/{id}/script", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		scriptPath := filepath.Join(cfg.TempDir, id, "script.txt")
		data, err := os.ReadFile(scriptPath)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "No script available"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"script": strings.TrimSpace(string(data))})
	})

	mux.HandleFunc("POST /api/shorts/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !queue.Cancel(r.PathValue("id")) {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Job not found or already finished"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "Cancellation requested"})
	})

	// Reburn a single job: clear subtitle cache and re-queue.
	mux.HandleFunc("POST /api/shorts/jobs/{id}/reburn", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		j := queue.Get(id)
		if j == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Job not found"})
			return
		}
		// Delete cached subtitle artifacts so the pipeline regenerates them.
		tempDir := filepath.Join(cfg.TempDir, id)
		for _, f := range []string{"subtitles.srt", "subtitled.mp4", "merged.mp4", "with_endcard.mp4"} {
			os.Remove(filepath.Join(tempDir, f))
		}
		if !queue.Requeue(id) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "Job cannot be re-queued"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "Job re-queued for subtitle reburn"})
	})

	// Reburn all jobs in a series.
	mux.HandleFunc("POST /api/shorts/series/{id}/reburn", func(w http.ResponseWriter, r *http.Request) {
		seriesID := r.PathValue("id")
		jobs := queue.ListBySeries(seriesID)
		if len(jobs) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Series not found or has no jobs"})
			return
		}
		requeued := 0
		for _, j := range jobs {
			tempDir := filepath.Join(cfg.TempDir, j.ID)
			for _, f := range []string{"subtitles.srt", "subtitled.mp4", "merged.mp4", "with_endcard.mp4"} {
				os.Remove(filepath.Join(tempDir, f))
			}
			if queue.Requeue(j.ID) {
				requeued++
			}
		}
		series.PersistNow()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":   "success",
			"message":  fmt.Sprintf("Re-queued %d jobs for subtitle reburn", requeued),
			"requeued": requeued,
		})
	})

	// Series creation — schedule-aware, agent picks topics per episode.
	mux.HandleFunc("POST /api/shorts/series", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Theme        string          `json:"theme"`
			EpisodeCount int             `json:"episodeCount"`
			Schedule     string          `json:"schedule"`
			Params       json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Theme == "" || req.EpisodeCount <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "theme and episodeCount required"})
			return
		}
		if req.Schedule == "" {
			req.Schedule = "now"
		}

		ser := series.Create(req.Theme, req.EpisodeCount, req.Schedule, req.Params)
		log.Printf("Created series %s: %q (%d episodes, schedule=%s)", ser.ID[:8], ser.Theme, ser.EpisodeCount, ser.Schedule)

		writeJSON(w, http.StatusOK, map[string]string{
			"status":   "success",
			"seriesId": ser.ID,
		})
	})

	mux.HandleFunc("POST /api/shorts/series/{id}/episodes/{ep}/run", func(w http.ResponseWriter, r *http.Request) {
		ser := series.Get(r.PathValue("id"))
		if ser == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Series not found"})
			return
		}
		epIndex, _ := strconv.Atoi(r.PathValue("ep"))
		if !ser.TriggerEpisode(epIndex) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "Episode cannot be triggered (already running or completed)"})
			return
		}
		ser.AppendLog(fmt.Sprintf("[Ep %d] Manually triggered", epIndex), "info")
		series.PersistNow()
		writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
	})

	mux.HandleFunc("GET /api/shorts/series/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		ser := series.Get(r.PathValue("id"))
		if ser == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Series not found"})
			return
		}
		afterID := 0
		if v := r.URL.Query().Get("after"); v != "" {
			afterID, _ = strconv.Atoi(v)
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "events": ser.GetEvents(afterID)})
	})

	mux.HandleFunc("GET /api/shorts/series", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "series": series.List()})
	})

	mux.HandleFunc("GET /api/shorts/series/{id}", func(w http.ResponseWriter, r *http.Request) {
		ser := series.Get(r.PathValue("id"))
		if ser == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Series not found"})
			return
		}
		jobs := queue.ListBySeries(ser.ID)
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "series": ser, "jobs": jobs})
	})

	// API: upload logo
	mux.HandleFunc("POST /api/upload-logo", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil { // 10MB max
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "Invalid upload"})
			return
		}
		file, header, err := r.FormFile("logo")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "No logo file"})
			return
		}
		defer file.Close()

		logosDir := "logos"
		os.MkdirAll(logosDir, 0755)
		outPath := filepath.Join(logosDir, filepath.Base(header.Filename))
		dst, err := os.Create(outPath)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": "Failed to save logo"})
			return
		}
		defer dst.Close()
		io.Copy(dst, file)

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "success",
			"path":   outPath,
		})
	})

	mux.HandleFunc("POST /api/upload-songs", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "Invalid upload"})
			return
		}
		files := r.MultipartForm.File["songs"]
		if len(files) == 0 {
			files = r.MultipartForm.File["songs[]"]
		}
		os.MkdirAll(cfg.SongsDir, 0755)
		uploaded := 0
		for _, fh := range files {
			if !strings.HasSuffix(strings.ToLower(fh.Filename), ".mp3") {
				continue
			}
			src, err := fh.Open()
			if err != nil {
				continue
			}
			dst, err := os.Create(filepath.Join(cfg.SongsDir, filepath.Base(fh.Filename)))
			if err != nil {
				src.Close()
				continue
			}
			io.Copy(dst, src)
			src.Close()
			dst.Close()
			uploaded++
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "message": fmt.Sprintf("Uploaded %d song(s)", uploaded)})
	})

	addr := ":8080"
	log.Printf("Server starting on %s with %d workers", addr, workerCount)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func worker(id int, queue *job.Queue, series *job.SeriesStore, cfg *state.State) {
	for {
		j, ctx := queue.Claim()
		if j == nil {
			time.Sleep(1 * time.Second)
			continue
		}

		log.Printf("[worker-%d] Processing job %s", id, j.ID)

		var params pipeline.Params
		if err := json.Unmarshal(j.Payload, &params); err != nil {
			j.Fail(fmt.Sprintf("invalid job payload: %v", err))
			queue.PersistNow()
			continue
		}

		onLog := func(message, level string) {
			j.AppendLog(message, level)
			log.Printf("[%s] %s: %s", j.ID[:8], level, message)
		}

		result, err := pipeline.Run(ctx, j.ID, params, cfg, onLog)
		if err != nil {
			if ctx.Err() != nil {
				j.MarkCancelled("Video generation was cancelled")
			} else {
				j.Fail(err.Error())
			}
		} else {
			j.Complete(result)
			log.Printf("[worker-%d] Job %s completed: %s", id, j.ID, result)

			// Auto-publish to YouTube if enabled.
			if params.AutoPublishYT || cfg.YouTubeAutoPublish {
				publishToYouTube(j, params, cfg, onLog)
				queue.PersistNow()
			}
		}
		queue.PersistNow()

		// Update series episode status if this job belongs to one.
		if j.SeriesID != "" {
			ser := series.Get(j.SeriesID)
			if ser != nil {
				for _, ep := range ser.Episodes {
					if ep.JobID == j.ID {
						if j.Status == job.StatusCompleted {
							ser.MarkEpisodeCompleted(ep.Index)
						} else if j.Status == job.StatusFailed {
							ser.FailEpisode(ep.Index, j.ErrorMessage)
						}
						break
					}
				}
				ser.CheckComplete()
				series.PersistNow()
			}
		}
	}
}

func generateSeriesTopics(ctx context.Context, llm *inference.Client, theme, context string, count int) ([]string, error) {
	prompt := fmt.Sprintf(`Generate exactly %d distinct video topics for a content series about: %s

Each topic should be a specific, unique angle suitable as a standalone short video.
Return ONLY a JSON array of strings, nothing else.

Example: ["Topic 1", "Topic 2", "Topic 3"]`, count, theme)

	if context != "" {
		prompt += fmt.Sprintf("\n\n[BACKGROUND MATERIAL — use this to inform the topics, ensure episodes cover key points from this material]\n%s", context)
	}

	response, err := llm.Generate(ctx, prompt)
	if err != nil {
		return nil, err
	}

	var topics []string
	if err := json.Unmarshal([]byte(response), &topics); err != nil {
		// Try to extract JSON array.
		start := strings.Index(response, "[")
		end := strings.LastIndex(response, "]")
		if start >= 0 && end > start {
			json.Unmarshal([]byte(response[start:end+1]), &topics)
		}
	}

	if len(topics) == 0 {
		return nil, fmt.Errorf("could not parse topics from LLM response")
	}

	return topics, nil
}

func seriesScheduler(queue *job.Queue, seriesStore *job.SeriesStore, cfg *state.State) {
	for {
		time.Sleep(5 * time.Second)

		for _, ser := range seriesStore.List() {
			if ser.Status != job.SeriesStatusRunning {
				continue
			}
			if ser.HasActiveEpisode() {
				continue // one at a time
			}
			ep := ser.NextPlannedEpisode()
			if ep == nil {
				continue // all episodes started
			}
			if !ser.IsDue() {
				continue // not time yet
			}

			go processEpisode(ser, ep.Index, queue, seriesStore, cfg)
		}
	}
}

func processEpisode(ser *job.Series, epIndex int, queue *job.Queue, seriesStore *job.SeriesStore, cfg *state.State) {
	// Reload state for fresh API keys.
	current := loadState()
	if current == nil {
		current = cfg
	}

	ser.MarkEpisodeResearching(epIndex)
	ser.AppendLog(fmt.Sprintf("[Ep %d/%d] Starting research...", epIndex, ser.EpisodeCount), "info")
	seriesStore.PersistNow()

	llm := inference.NewClient(current.InferenceURL, current.InferenceModel, current.InferenceAPIKey)

	// Build context from previous completed episodes.
	var prevEpisodes []agent.PreviousEpisode
	for _, ep := range ser.CompletedEpisodes() {
		summary := ep.Script
		if len(summary) > 150 {
			summary = summary[:150] + "..."
		}
		prevEpisodes = append(prevEpisodes, agent.PreviousEpisode{
			Index:   ep.Index,
			Subject: ep.Subject,
			Summary: summary,
		})
	}

	// Decode shared params for tone/hook.
	var params pipeline.Params
	if ser.Params != nil {
		json.Unmarshal(ser.Params, &params)
	}

	agentCfg := agent.Config{
		LLM:              llm,
		BraveAPIKey:      current.BraveSearchAPIKey,
		VideoSubject:     "", // agent picks its own topic
		TonePreset:       params.TonePreset,
		HookStyle:        params.HookStyle,
		ParagraphNum:     params.ParagraphNum,
		SeriesTheme:      ser.Theme,
		EpisodeIndex:     epIndex,
		EpisodeTotal:     ser.EpisodeCount,
		PreviousEpisodes: prevEpisodes,
	}

	onEvent := func(msg, level string) {
		ser.AppendLog(fmt.Sprintf("[Ep %d] %s", epIndex, msg), level)
	}

	result, err := agent.Run(context.Background(), agentCfg, onEvent)
	if err != nil {
		ser.AppendLog(fmt.Sprintf("[Ep %d] Research failed: %v", epIndex, err), "error")
		ser.FailEpisode(epIndex, err.Error())
		ser.CheckComplete()
		seriesStore.PersistNow()
		return
	}

	// Build pipeline params and queue the video job.
	params.VideoSubject = result.Subject
	params.ScriptOverride = result.Script
	params.SeriesTheme = ser.Theme
	params.EpisodeIndex = epIndex

	payload, _ := json.Marshal(params)
	jobID := queue.SubmitWithSeries(payload, result.Subject, ser.ID)

	sources := make([]job.EpisodeSource, len(result.Sources))
	for i, s := range result.Sources {
		sources[i] = job.EpisodeSource{Title: s.Title, URL: s.URL, Snippet: s.Snippet}
	}

	ser.CompleteEpisodeResearch(epIndex, result.Subject, result.Script, sources, jobID)
	ser.AdvanceSchedule()
	ser.AppendLog(fmt.Sprintf("[Ep %d] \"%s\" — queued for video generation", epIndex, result.Subject), "success")
	seriesStore.PersistNow()

	log.Printf("[series:%s] Episode %d researched: %q → job %s", ser.ID[:8], epIndex, result.Subject, jobID[:8])
}

func publishToYouTube(j *job.Job, params pipeline.Params, cfg *state.State, onLog pipeline.LogFunc) {
	current := loadState()
	if current == nil {
		current = cfg
	}
	if current.YouTubeRefreshToken == "" {
		onLog("YouTube publish skipped — not authorized", "warning")
		return
	}

	onLog("Publishing to YouTube...", "info")

	ytClient, err := ytpkg.NewClient(context.Background(), ytpkg.OAuthConfig{
		ClientID:     current.YouTubeClientID,
		ClientSecret: current.YouTubeClientSecret,
		RefreshToken: current.YouTubeRefreshToken,
	})
	if err != nil {
		onLog(fmt.Sprintf("YouTube auth failed: %v", err), "error")
		return
	}

	// Load metadata.
	meta := ytpkg.Metadata{
		Title:   j.Subject,
		Privacy: "public",
	}
	metaPath := filepath.Join(current.TempDir, j.ID, "metadata.json")
	if data, err := os.ReadFile(metaPath); err == nil {
		var vm pipeline.VideoMetadata
		if json.Unmarshal(data, &vm) == nil {
			if vm.Title != "" {
				meta.Title = vm.Title
			}
			meta.Description = vm.Description
			meta.Tags = vm.Hashtags
		}
	}
	// Per-job channel override, fall back to global config.
	if params.YouTubeChannelID != "" {
		meta.ChannelID = params.YouTubeChannelID
	} else {
		meta.ChannelID = current.YouTubeChannelID
	}

	videoID, err := ytClient.Upload(context.Background(), j.ResultPath, meta)
	if err != nil {
		onLog(fmt.Sprintf("YouTube upload failed: %v", err), "error")
		return
	}

	j.YouTubeVideoID = videoID
	j.YouTubeURL = ytpkg.VideoURL(videoID)
	onLog(fmt.Sprintf("Published to YouTube: %s", j.YouTubeURL), "success")
	log.Printf("[%s] Published to YouTube: %s", j.ID[:8], j.YouTubeURL)
}

func selectImageGenProvider(cfg *state.State) imagegen.Provider {
	switch cfg.ImageGenProvider {
	case "replicate":
		return &imagegen.Replicate{
			APIToken: cfg.ImageGenAPIKey,
			Model:    cfg.ImageGenModel,
		}
	default: // "vllm"
		return &imagegen.VLLM{
			BaseURL: cfg.ImageGenURL,
			APIKey:  cfg.ImageGenAPIKey,
			Model:   cfg.ImageGenModel,
		}
	}
}

func modelScheduler(modelStore *model.Store, cfg *state.State) {
	for {
		time.Sleep(5 * time.Second)

		for _, m := range modelStore.List() {
			if m.Status != model.ModelStatusActive {
				continue
			}
			if m.HasActivePost() {
				continue
			}
			if !m.IsDue() {
				continue
			}
			post := m.EnsurePlannedPost()
			modelStore.PersistNow()
			if post == nil {
				continue
			}
			go processPost(m, post.Index, modelStore, cfg)
		}
	}
}

func processPost(m *model.Model, postIndex int, modelStore *model.Store, cfg *state.State) {
	current := loadState()
	if current == nil {
		current = cfg
	}

	m.MarkPostCaptioning(postIndex)
	m.AppendLog(fmt.Sprintf("[Post %d] Planning content...", postIndex), "info")
	modelStore.PersistNow()

	llm := inference.NewClient(current.InferenceURL, current.InferenceModel, current.InferenceAPIKey)

	// Build context from previous posts.
	var prevPosts []modelagent.PreviousPost
	for _, p := range m.CompletedPosts() {
		caption := p.Caption
		if len(caption) > 100 {
			caption = caption[:100] + "..."
		}
		prevPosts = append(prevPosts, modelagent.PreviousPost{
			Index:   p.Index,
			Scene:   p.Scene,
			Caption: caption,
		})
	}

	agentCfg := modelagent.Config{
		LLM:           llm,
		BraveAPIKey:   current.BraveSearchAPIKey,
		ModelName:     m.Name,
		Description:   m.Description,
		Personality:   m.Personality,
		Style:         m.Style,
		PreviousPosts: prevPosts,
	}

	onEvent := func(msg, level string) {
		m.AppendLog(fmt.Sprintf("[Post %d] %s", postIndex, msg), level)
	}

	result, err := modelagent.Run(context.Background(), agentCfg, onEvent)
	if err != nil {
		m.AppendLog(fmt.Sprintf("[Post %d] Content planning failed: %v", postIndex, err), "error")
		m.FailPost(postIndex, err.Error())
		modelStore.PersistNow()
		return
	}

	m.CompletePostCaption(postIndex, result.Scene, result.Caption, result.ImagePrompt, result.Hashtags)
	m.AppendLog(fmt.Sprintf("[Post %d] Caption ready, generating image: %s", postIndex, result.Scene), "info")
	modelStore.PersistNow()

	// Generate image.
	provider := selectImageGenProvider(current)
	postDir := filepath.Join("data", "models", m.ID, "posts", fmt.Sprintf("%03d", postIndex))
	os.MkdirAll(postDir, 0755)

	imgResult, err := provider.Generate(context.Background(), imagegen.Request{
		Prompt:    result.ImagePrompt,
		RefImages: m.RefImages,
		Width:     1080,
		Height:    1350,
		Count:     1,
	}, postDir)
	if err != nil {
		m.AppendLog(fmt.Sprintf("[Post %d] Image generation failed: %v", postIndex, err), "error")
		m.FailPost(postIndex, err.Error())
		modelStore.PersistNow()
		return
	}

	m.CompletePostGeneration(postIndex, imgResult.ImagePaths)
	m.AdvanceSchedule()
	m.AppendLog(fmt.Sprintf("[Post %d] Complete: %s", postIndex, result.Scene), "success")
	modelStore.PersistNow()

	log.Printf("[model:%s] Post %d complete: %q", m.ID[:8], postIndex, result.Scene)
}

func checkDependencies() {
	// ffmpeg must exist
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: ffmpeg not found in PATH")
		fmt.Fprintln(os.Stderr, "Install with: brew install homebrew-ffmpeg/ffmpeg/ffmpeg")
		os.Exit(1)
	}
	log.Printf("Found ffmpeg: %s", ffmpegPath)

	// ffmpeg must have the subtitles filter (requires libass)
	out, err := exec.Command("ffmpeg", "-filters").CombinedOutput()
	if err == nil && !strings.Contains(string(out), "subtitles") {
		fmt.Fprintln(os.Stderr, "ERROR: ffmpeg is missing the 'subtitles' filter (needs libass)")
		fmt.Fprintln(os.Stderr, "Your ffmpeg was likely installed without libass support.")
		fmt.Fprintln(os.Stderr, "Fix with:")
		fmt.Fprintln(os.Stderr, "  brew uninstall ffmpeg")
		fmt.Fprintln(os.Stderr, "  brew tap homebrew-ffmpeg/ffmpeg")
		fmt.Fprintln(os.Stderr, "  brew install homebrew-ffmpeg/ffmpeg/ffmpeg")
		os.Exit(1)
	}

	// ffprobe must exist
	if _, err := exec.LookPath("ffprobe"); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: ffprobe not found in PATH (usually installed with ffmpeg)")
		os.Exit(1)
	}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
