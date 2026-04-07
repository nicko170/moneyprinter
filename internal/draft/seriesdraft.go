package draft

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type SeriesDraftStatus string

const (
	SeriesDraftStatusPlanning    SeriesDraftStatus = "planning"
	SeriesDraftStatusResearching SeriesDraftStatus = "researching"
	SeriesDraftStatusReady       SeriesDraftStatus = "ready"
	SeriesDraftStatusFailed      SeriesDraftStatus = "failed"
)

type EpisodeDraftStatus string

const (
	EpisodeStatusQueued      EpisodeDraftStatus = "queued"
	EpisodeStatusResearching EpisodeDraftStatus = "researching"
	EpisodeStatusDone        EpisodeDraftStatus = "done"
	EpisodeStatusFailed      EpisodeDraftStatus = "failed"
)

// EpisodeDraft holds the research result for one episode in a series.
type EpisodeDraft struct {
	Index   int                `json:"index"` // 1-based
	Subject string             `json:"subject"`
	Status  EpisodeDraftStatus `json:"status"`
	Script  string             `json:"script,omitempty"`
	Sources []Source           `json:"sources,omitempty"`
	Error   string             `json:"error,omitempty"`
}

// SeriesDraft is the research plan for a full content series.
type SeriesDraft struct {
	ID           string            `json:"id"`
	Theme        string            `json:"theme"`
	EpisodeCount int               `json:"episodeCount"`
	Status       SeriesDraftStatus `json:"status"`
	Episodes     []EpisodeDraft    `json:"episodes"`
	SharedParams json.RawMessage   `json:"sharedParams"` // video settings (no subject/script)
	Events       []Event           `json:"events"`
	NextEventID  int               `json:"nextEventId"`
	CreatedAt    time.Time         `json:"createdAt"`
	CompletedAt  time.Time         `json:"completedAt,omitempty"`
	ErrorMessage string            `json:"errorMessage,omitempty"`

	mu sync.Mutex `json:"-"`
}

// SetTopics populates episode subjects after planning and transitions to researching.
func (sd *SeriesDraft) SetTopics(topics []string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	sd.Status = SeriesDraftStatusResearching
	sd.Episodes = make([]EpisodeDraft, len(topics))
	for i, t := range topics {
		sd.Episodes[i] = EpisodeDraft{Index: i + 1, Subject: t, Status: EpisodeStatusQueued}
	}
	sd.appendEventLocked("topics", "info", "Episode topics planned — starting research")
}

// UpdateEpisode updates a single episode's research result.
func (sd *SeriesDraft) UpdateEpisode(index int, status EpisodeDraftStatus, script string, sources []Source, errMsg string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	for i := range sd.Episodes {
		if sd.Episodes[i].Index == index {
			sd.Episodes[i].Status = status
			sd.Episodes[i].Script = script
			sd.Episodes[i].Sources = sources
			sd.Episodes[i].Error = errMsg
			return
		}
	}
}

// MarkEpisodeResearching marks an episode as actively being researched.
func (sd *SeriesDraft) MarkEpisodeResearching(index int) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	for i := range sd.Episodes {
		if sd.Episodes[i].Index == index {
			sd.Episodes[i].Status = EpisodeStatusResearching
			return
		}
	}
}

// CheckComplete checks if all episodes are done and marks the series ready if so.
// Returns true if the series is now ready.
func (sd *SeriesDraft) CheckComplete() bool {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	for _, ep := range sd.Episodes {
		if ep.Status != EpisodeStatusDone && ep.Status != EpisodeStatusFailed {
			return false
		}
	}
	sd.Status = SeriesDraftStatusReady
	sd.CompletedAt = time.Now()
	sd.appendEventLocked("ready", "success", "All episodes researched — ready for approval")
	return true
}

// Fail marks the series draft as failed (e.g. planning step failed).
func (sd *SeriesDraft) Fail(errMsg string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	sd.Status = SeriesDraftStatusFailed
	sd.ErrorMessage = errMsg
	sd.CompletedAt = time.Now()
	sd.appendEventLocked("error", "error", errMsg)
}

// AppendLog adds a log event.
func (sd *SeriesDraft) AppendLog(message, level string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	sd.appendEventLocked("log", level, message)
}

// GetEvents returns events with ID > afterID.
func (sd *SeriesDraft) GetEvents(afterID int) []Event {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	var result []Event
	for _, e := range sd.Events {
		if e.ID > afterID {
			result = append(result, e)
		}
	}
	return result
}

func (sd *SeriesDraft) appendEventLocked(eventType, level, message string) {
	sd.NextEventID++
	sd.Events = append(sd.Events, Event{
		ID:        sd.NextEventID,
		Type:      eventType,
		Level:     level,
		Message:   message,
		Timestamp: time.Now(),
	})
}

// SeriesDraftStore manages series drafts with optional persistence.
type SeriesDraftStore struct {
	mu       sync.RWMutex
	drafts   map[string]*SeriesDraft
	filePath string
}

// NewSeriesDraftStore creates a store, loading from disk if filePath is set.
func NewSeriesDraftStore(filePath string) *SeriesDraftStore {
	s := &SeriesDraftStore{
		drafts:   make(map[string]*SeriesDraft),
		filePath: filePath,
	}
	if filePath != "" {
		s.loadFromDisk()
	}
	return s
}

// Create creates a new series draft in planning state.
func (s *SeriesDraftStore) Create(theme string, episodeCount int, sharedParams json.RawMessage) *SeriesDraft {
	sd := &SeriesDraft{
		ID:           uuid.New().String(),
		Theme:        theme,
		EpisodeCount: episodeCount,
		Status:       SeriesDraftStatusPlanning,
		SharedParams: sharedParams,
		CreatedAt:    time.Now(),
		Events:       []Event{},
	}
	sd.appendEventLocked("created", "info", "Series research started")

	s.mu.Lock()
	s.drafts[sd.ID] = sd
	s.mu.Unlock()
	s.persist()

	return sd
}

// Get returns a series draft by ID.
func (s *SeriesDraftStore) Get(id string) *SeriesDraft {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.drafts[id]
}

// List returns all series drafts sorted newest-first.
func (s *SeriesDraftStore) List() []*SeriesDraft {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*SeriesDraft, 0, len(s.drafts))
	for _, sd := range s.drafts {
		list = append(list, sd)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})
	return list
}

// PersistNow forces a save to disk.
func (s *SeriesDraftStore) PersistNow() {
	s.persist()
}

func (s *SeriesDraftStore) persist() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.persistUnlocked()
}

func (s *SeriesDraftStore) persistUnlocked() {
	if s.filePath == "" {
		return
	}
	data, err := json.MarshalIndent(s.drafts, "", "  ")
	if err != nil {
		log.Printf("Warning: failed to marshal series drafts: %v", err)
		return
	}
	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		log.Printf("Warning: failed to write %s: %v", s.filePath, err)
	}
}

func (s *SeriesDraftStore) loadFromDisk() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Warning: failed to read %s: %v", s.filePath, err)
		}
		return
	}

	var drafts map[string]*SeriesDraft
	if err := json.Unmarshal(data, &drafts); err != nil {
		log.Printf("Warning: failed to parse %s: %v", s.filePath, err)
		return
	}

	// Any in-progress drafts at shutdown can't resume — mark failed.
	for _, sd := range drafts {
		if sd.Status == SeriesDraftStatusPlanning || sd.Status == SeriesDraftStatusResearching {
			sd.Status = SeriesDraftStatusFailed
			sd.ErrorMessage = "Server restarted during research — please start a new draft"
			sd.CompletedAt = time.Now()
		}
	}

	s.drafts = drafts
	log.Printf("Loaded %d series drafts from %s", len(drafts), s.filePath)
}
