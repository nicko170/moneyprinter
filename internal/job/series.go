package job

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type SeriesStatus string

const (
	SeriesStatusPending   SeriesStatus = "pending"
	SeriesStatusRunning   SeriesStatus = "running"
	SeriesStatusPaused    SeriesStatus = "paused"
	SeriesStatusCompleted SeriesStatus = "completed"
	SeriesStatusFailed    SeriesStatus = "failed"
)

type EpisodeStatus string

const (
	EpisodeStatusPlanned     EpisodeStatus = "planned"
	EpisodeStatusResearching EpisodeStatus = "researching"
	EpisodeStatusGenerating  EpisodeStatus = "generating"
	EpisodeStatusCompleted   EpisodeStatus = "completed"
	EpisodeStatusFailed      EpisodeStatus = "failed"
)

// EpisodeSource is a web source found during episode research.
type EpisodeSource struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// SeriesEpisode tracks a single episode through its full lifecycle.
type SeriesEpisode struct {
	Index       int             `json:"index"` // 1-based
	Status      EpisodeStatus   `json:"status"`
	Subject     string          `json:"subject,omitempty"`     // determined by agent
	Script      string          `json:"script,omitempty"`      // written by agent
	Sources     []EpisodeSource `json:"sources,omitempty"`     // research sources
	JobID       string          `json:"jobId,omitempty"`       // linked video job
	Error       string          `json:"error,omitempty"`       // failure reason
	StartedAt   time.Time       `json:"startedAt,omitempty"`   // research start
	CompletedAt time.Time       `json:"completedAt,omitempty"` // video done or failed
}

type Series struct {
	ID           string          `json:"id"`
	Theme        string          `json:"theme"`
	EpisodeCount int             `json:"episodeCount"`
	Schedule     string          `json:"schedule"` // "now", "1h", "6h", "12h", "24h"
	Status       SeriesStatus    `json:"status"`
	Params       json.RawMessage `json:"params,omitempty"`    // shared video params
	Episodes     []SeriesEpisode `json:"episodes,omitempty"`  // full episode tracking
	NextRunAt    time.Time       `json:"nextRunAt,omitempty"` // when next episode should start
	Events       []Event         `json:"events"`
	NextEventID  int             `json:"nextEventId"`
	CreatedAt    time.Time       `json:"createdAt"`

	mu sync.Mutex `json:"-"`
}

// ScheduleInterval returns the duration between episodes, or 0 for "now" (immediate).
func (s *Series) ScheduleInterval() time.Duration {
	switch s.Schedule {
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "12h":
		return 12 * time.Hour
	case "24h":
		return 24 * time.Hour
	case "48h":
		return 48 * time.Hour
	default:
		return 0 // "now" — immediate
	}
}

// NextPlannedEpisode returns the next episode in "planned" state, or nil.
func (s *Series) NextPlannedEpisode() *SeriesEpisode {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Episodes {
		if s.Episodes[i].Status == EpisodeStatusPlanned {
			return &s.Episodes[i]
		}
	}
	return nil
}

// HasActiveEpisode returns true if any episode is currently researching or generating.
func (s *Series) HasActiveEpisode() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ep := range s.Episodes {
		if ep.Status == EpisodeStatusResearching || ep.Status == EpisodeStatusGenerating {
			return true
		}
	}
	return false
}

// CompletedEpisodes returns all episodes that have finished research (have a script).
func (s *Series) CompletedEpisodes() []SeriesEpisode {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []SeriesEpisode
	for _, ep := range s.Episodes {
		if ep.Script != "" {
			result = append(result, ep)
		}
	}
	return result
}

// MarkEpisodeResearching transitions an episode to researching state.
func (s *Series) MarkEpisodeResearching(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Episodes {
		if s.Episodes[i].Index == index {
			s.Episodes[i].Status = EpisodeStatusResearching
			s.Episodes[i].StartedAt = time.Now()
			return
		}
	}
}

// CompleteEpisodeResearch stores the script/sources and queues for generation.
func (s *Series) CompleteEpisodeResearch(index int, subject, script string, sources []EpisodeSource, jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Episodes {
		if s.Episodes[i].Index == index {
			s.Episodes[i].Subject = subject
			s.Episodes[i].Script = script
			s.Episodes[i].Sources = sources
			s.Episodes[i].JobID = jobID
			s.Episodes[i].Status = EpisodeStatusGenerating
			return
		}
	}
}

// FailEpisode marks an episode as failed.
func (s *Series) FailEpisode(index int, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Episodes {
		if s.Episodes[i].Index == index {
			s.Episodes[i].Status = EpisodeStatusFailed
			s.Episodes[i].Error = errMsg
			s.Episodes[i].CompletedAt = time.Now()
			return
		}
	}
}

// MarkEpisodeCompleted marks an episode's video as done.
func (s *Series) MarkEpisodeCompleted(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Episodes {
		if s.Episodes[i].Index == index {
			s.Episodes[i].Status = EpisodeStatusCompleted
			s.Episodes[i].CompletedAt = time.Now()
			return
		}
	}
}

// AdvanceSchedule sets NextRunAt for the next pending episode.
func (s *Series) AdvanceSchedule() {
	s.mu.Lock()
	defer s.mu.Unlock()
	interval := s.ScheduleInterval()
	if interval > 0 {
		s.NextRunAt = time.Now().Add(interval)
	} else {
		// "now" — next episode starts immediately.
		s.NextRunAt = time.Now()
	}
}

// CheckComplete checks if all episodes are done and updates series status.
func (s *Series) CheckComplete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	allDone := true
	anyFailed := false
	for _, ep := range s.Episodes {
		if ep.Status != EpisodeStatusCompleted && ep.Status != EpisodeStatusFailed {
			allDone = false
		}
		if ep.Status == EpisodeStatusFailed {
			anyFailed = true
		}
	}
	if !allDone {
		return false
	}
	if anyFailed {
		s.Status = SeriesStatusFailed
	} else {
		s.Status = SeriesStatusCompleted
	}
	return true
}

// AppendLog adds a log event.
func (s *Series) AppendLog(message, level string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.NextEventID++
	s.Events = append(s.Events, Event{
		ID:        s.NextEventID,
		Type:      "log",
		Level:     level,
		Message:   message,
		Timestamp: time.Now(),
	})
}

// GetEvents returns events with ID > afterID.
func (s *Series) GetEvents(afterID int) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []Event
	for _, e := range s.Events {
		if e.ID > afterID {
			result = append(result, e)
		}
	}
	return result
}

// IsDue returns true if this series has a pending episode that should start now.
func (s *Series) IsDue() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Status != SeriesStatusRunning {
		return false
	}
	return !s.NextRunAt.IsZero() && !time.Now().Before(s.NextRunAt)
}

// EpisodeScheduledAt returns the estimated time this episode will start.
// For completed/active episodes, returns the actual start time.
// For planned episodes, extrapolates from NextRunAt and the schedule interval.
func (s *Series) EpisodeScheduledAt(index int) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find how many planned episodes are ahead of this one.
	plannedBefore := 0
	for _, ep := range s.Episodes {
		if ep.Index == index {
			if !ep.StartedAt.IsZero() {
				return ep.StartedAt
			}
			break
		}
		if ep.Status == EpisodeStatusPlanned {
			plannedBefore++
		}
	}

	interval := s.ScheduleInterval()
	if interval == 0 || s.NextRunAt.IsZero() {
		return time.Time{} // "now" mode — no meaningful future time
	}
	return s.NextRunAt.Add(time.Duration(plannedBefore) * interval)
}

// TriggerEpisode resets a planned/failed episode to planned and sets NextRunAt to now
// so the scheduler picks it up immediately.
func (s *Series) TriggerEpisode(index int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Episodes {
		if s.Episodes[i].Index == index {
			st := s.Episodes[i].Status
			if st == EpisodeStatusPlanned || st == EpisodeStatusFailed {
				s.Episodes[i].Status = EpisodeStatusPlanned
				s.Episodes[i].Error = ""
				s.Episodes[i].StartedAt = time.Time{}
				s.Episodes[i].CompletedAt = time.Time{}
				s.NextRunAt = time.Now()
				if s.Status != SeriesStatusRunning {
					s.Status = SeriesStatusRunning
				}
				return true
			}
			return false
		}
	}
	return false
}

// --- Store ---

// SeriesStore manages series with optional persistence to disk.
type SeriesStore struct {
	mu       sync.RWMutex
	series   map[string]*Series
	filePath string
}

func NewSeriesStore(filePath string) *SeriesStore {
	s := &SeriesStore{
		series:   make(map[string]*Series),
		filePath: filePath,
	}
	if filePath != "" {
		s.loadFromDisk()
	}
	return s
}

// Create creates a new series with planned episodes.
func (s *SeriesStore) Create(theme string, episodeCount int, schedule string, params json.RawMessage) *Series {
	episodes := make([]SeriesEpisode, episodeCount)
	for i := range episodes {
		episodes[i] = SeriesEpisode{Index: i + 1, Status: EpisodeStatusPlanned}
	}

	ser := &Series{
		ID:           uuid.New().String(),
		Theme:        theme,
		EpisodeCount: episodeCount,
		Schedule:     schedule,
		Status:       SeriesStatusRunning,
		Params:       params,
		Episodes:     episodes,
		Events:       []Event{},
		CreatedAt:    time.Now(),
	}

	// First episode is always due immediately.
	ser.NextRunAt = time.Now()
	ser.AppendLog("Series created — "+scheduleLabel(schedule), "info")

	s.mu.Lock()
	s.series[ser.ID] = ser
	s.mu.Unlock()
	s.persist()
	return ser
}


func (s *SeriesStore) Get(id string) *Series {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.series[id]
}

func (s *SeriesStore) List() []*Series {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*Series, 0, len(s.series))
	for _, ser := range s.series {
		list = append(list, ser)
	}
	sort.Slice(list, func(i, k int) bool {
		return list[i].CreatedAt.After(list[k].CreatedAt)
	})
	return list
}

// AddJob is a no-op kept for backward compat — new series track jobs via episodes.
func (s *SeriesStore) AddJob(seriesID, jobID string) {
	// Episodes track their own JobID now.
}


func (s *SeriesStore) PersistNow() {
	s.persist()
}

func (s *SeriesStore) persist() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.persistUnlocked()
}

func (s *SeriesStore) persistUnlocked() {
	if s.filePath == "" {
		return
	}
	data, err := json.MarshalIndent(s.series, "", "  ")
	if err != nil {
		log.Printf("Warning: failed to persist series: %v", err)
		return
	}
	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		log.Printf("Warning: failed to write %s: %v", s.filePath, err)
	}
}

func (s *SeriesStore) loadFromDisk() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Warning: failed to read %s: %v", s.filePath, err)
		}
		return
	}

	var series map[string]*Series
	if err := json.Unmarshal(data, &series); err != nil {
		log.Printf("Warning: failed to parse %s: %v", s.filePath, err)
		return
	}

	// Recover series that were active at shutdown.
	for _, ser := range series {
		for i := range ser.Episodes {
			if ser.Episodes[i].Status == EpisodeStatusResearching {
				ser.Episodes[i].Status = EpisodeStatusPlanned
				ser.Episodes[i].StartedAt = time.Time{}
			}
		}
		// Ensure running series with pending episodes can be picked up.
		if ser.Status == SeriesStatusRunning && ser.NextRunAt.IsZero() {
			ser.NextRunAt = time.Now()
		}
	}

	s.series = series
	log.Printf("Loaded %d series from %s", len(series), s.filePath)
}

func scheduleLabel(schedule string) string {
	switch schedule {
	case "1h":
		return "one episode every hour"
	case "6h":
		return "one episode every 6 hours"
	case "12h":
		return "one episode every 12 hours"
	case "24h":
		return "one episode daily"
	case "48h":
		return "one episode every 2 days"
	default:
		return "all episodes now"
	}
}
