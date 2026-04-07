package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moneyprinter/internal/agent"
	"github.com/moneyprinter/internal/draft"
	"github.com/moneyprinter/internal/inference"
	"github.com/moneyprinter/internal/job"
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

	// Start worker pool.
	for i := range workerCount {
		go worker(i, queue, series, cfg)
	}

	mux := http.NewServeMux()

	// --- Static files ---
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.Handle("GET /output/", http.StripPrefix("/output/", http.FileServer(http.Dir(cfg.OutputDir))))

	// --- Page routes ---
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/jobs", http.StatusFound)
	})

	mux.HandleFunc("GET /jobs", func(w http.ResponseWriter, r *http.Request) {
		templates.Dashboard(queue.List(), series.List()).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /jobs/create", func(w http.ResponseWriter, r *http.Request) {
		current := loadState()
		if current == nil {
			current = cfg
		}
		templates.CreateJob(templates.CreateJobProps{
			InferenceURL:   current.InferenceURL,
			InferenceModel: current.InferenceModel,
			TTSProvider:    current.TTSProvider,
		}).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		j := queue.Get(r.PathValue("id"))
		if j == nil {
			http.NotFound(w, r)
			return
		}
		templates.JobDetail(j).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /drafts", func(w http.ResponseWriter, r *http.Request) {
		templates.DraftsPage(templates.DraftsPageProps{
			Drafts:       drafts.List(),
			SeriesDrafts: seriesDrafts.List(),
		}).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		d := drafts.Get(r.PathValue("id"))
		if d == nil {
			http.NotFound(w, r)
			return
		}
		templates.DraftDetail(d).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /series-drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		sd := seriesDrafts.Get(r.PathValue("id"))
		if sd == nil {
			http.NotFound(w, r)
			return
		}
		templates.SeriesDraftDetail(sd).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /series/create", func(w http.ResponseWriter, r *http.Request) {
		templates.SeriesCreate().Render(r.Context(), w)
	})

	mux.HandleFunc("GET /series/{id}", func(w http.ResponseWriter, r *http.Request) {
		ser := series.Get(r.PathValue("id"))
		if ser == nil {
			http.NotFound(w, r)
			return
		}
		jobs := queue.ListBySeries(ser.ID)
		templates.SeriesDetail(ser, jobs).Render(r.Context(), w)
	})

	// --- API routes ---
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, cfg.Redacted())
	})

	// --- Draft API ---

	mux.HandleFunc("POST /api/drafts", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("GET /api/drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		d := drafts.Get(r.PathValue("id"))
		if d == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Draft not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "draft": d})
	})

	mux.HandleFunc("GET /api/drafts/{id}/events", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("POST /api/drafts/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("POST /api/series-drafts", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("GET /api/series-drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		sd := seriesDrafts.Get(r.PathValue("id"))
		if sd == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Series draft not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "draft": sd})
	})

	mux.HandleFunc("GET /api/series-drafts/{id}/events", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("POST /api/series-drafts/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
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

		ser := series.Create(sd.Theme, len(sd.Episodes))
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

	mux.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("POST /api/generate", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("GET /api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		j := queue.Get(r.PathValue("id"))
		if j == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Job not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "job": j})
	})

	mux.HandleFunc("GET /api/jobs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("GET /api/jobs/{id}/metadata", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("GET /api/jobs/{id}/script", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		scriptPath := filepath.Join(cfg.TempDir, id, "script.txt")
		data, err := os.ReadFile(scriptPath)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "No script available"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"script": strings.TrimSpace(string(data))})
	})

	mux.HandleFunc("POST /api/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !queue.Cancel(r.PathValue("id")) {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Job not found or already finished"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "Cancellation requested"})
	})

	// Reburn a single job: clear subtitle cache and re-queue.
	mux.HandleFunc("POST /api/jobs/{id}/reburn", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("POST /api/series/{id}/reburn", func(w http.ResponseWriter, r *http.Request) {
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
		series.UpdateStatus(seriesID, queue)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":   "success",
			"message":  fmt.Sprintf("Re-queued %d jobs for subtitle reburn", requeued),
			"requeued": requeued,
		})
	})

	mux.HandleFunc("POST /api/series", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Theme        string `json:"theme"`
			EpisodeCount int    `json:"episodeCount"`
			Voice        string `json:"voice"`
			Context      string `json:"context"`
			HookStyle    string `json:"hookStyle"`
			CustomHook   string `json:"customHook"`
			TonePreset   string `json:"tonePreset"`
			SubtitlePos     string `json:"subtitlesPosition"`
			Color           string `json:"color"`
			ParagraphNum    int    `json:"paragraphNumber"`
			UseMusic        bool   `json:"useMusic"`
			VideoEffects    []string `json:"videoEffects"`
			EndCardBgColor  string `json:"endCardBgColor"`
			EndCardCTAText  string `json:"endCardCTAText"`
			EndCardLogoPath string `json:"endCardLogoPath"`
			EndCardDuration int    `json:"endCardDuration"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Theme == "" || req.EpisodeCount <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "theme and episodeCount required"})
			return
		}

		// Generate topics FIRST — only create series if this succeeds.
		llm := inference.NewClient(cfg.InferenceURL, cfg.InferenceModel, cfg.InferenceAPIKey)
		topics, err := generateSeriesTopics(r.Context(), llm, req.Theme, req.Context, req.EpisodeCount)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": fmt.Sprintf("Failed to generate topics: %v", err)})
			return
		}

		ser := series.Create(req.Theme, req.EpisodeCount)

		// Submit a job for each topic.
		var jobIDs []string
		for i, topic := range topics {
			paragraphs := req.ParagraphNum
			if paragraphs <= 0 {
				paragraphs = 1
			}
			params := pipeline.Params{
				VideoSubject:    topic,
				Voice:           req.Voice,
				ParagraphNum:    paragraphs,
				Context:         req.Context,
				HookStyle:       req.HookStyle,
				CustomHook:      req.CustomHook,
				TonePreset:      req.TonePreset,
				SubtitlePos:     req.SubtitlePos,
				SubtitleColor:   req.Color,
				UseMusic:        req.UseMusic,
				VideoEffects:    req.VideoEffects,
				EndCardBgColor:  req.EndCardBgColor,
				EndCardCTAText:  req.EndCardCTAText,
				EndCardLogoPath: req.EndCardLogoPath,
				EndCardDuration: req.EndCardDuration,
				SeriesTheme:     req.Theme,
				EpisodeIndex:    i + 1,
			}
			payload, _ := json.Marshal(params)
			jobID := queue.SubmitWithSeries(payload, topic, ser.ID)
			series.AddJob(ser.ID, jobID)
			jobIDs = append(jobIDs, jobID)
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":   "success",
			"seriesId": ser.ID,
			"topics":   topics,
			"jobIds":   jobIDs,
		})
	})

	mux.HandleFunc("GET /api/series", func(w http.ResponseWriter, r *http.Request) {
		list := series.List()
		// Update statuses.
		for _, s := range list {
			series.UpdateStatus(s.ID, queue)
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "series": list})
	})

	mux.HandleFunc("GET /api/series/{id}", func(w http.ResponseWriter, r *http.Request) {
		ser := series.Get(r.PathValue("id"))
		if ser == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "Series not found"})
			return
		}
		series.UpdateStatus(ser.ID, queue)
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
		}
		queue.PersistNow()

		// Update series status if this job belongs to one.
		if j.SeriesID != "" {
			series.UpdateStatus(j.SeriesID, queue)
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
