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

type Status string

const (
	StatusResearching Status = "researching"
	StatusDone        Status = "done"
	StatusFailed      Status = "failed"
)

// Source is a web source found during research.
type Source struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Event mirrors job.Event.
type Event struct {
	ID        int       `json:"id"`
	Type      string    `json:"type"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// Draft represents a research + script draft awaiting approval.
type Draft struct {
	ID           string          `json:"id"`
	Status       Status          `json:"status"`
	Subject      string          `json:"subject"`
	Script       string          `json:"script,omitempty"`
	Sources      []Source        `json:"sources,omitempty"`
	ErrorMessage string          `json:"errorMessage,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	CompletedAt  time.Time       `json:"completedAt,omitempty"`
	Params       json.RawMessage `json:"params"`
	Events       []Event         `json:"events"`
	NextEventID  int             `json:"nextEventId"`

	mu sync.Mutex `json:"-"`
}

// Store manages drafts with optional persistence.
type Store struct {
	mu       sync.RWMutex
	drafts   map[string]*Draft
	filePath string
}

// NewStore creates a draft store, loading existing drafts from disk if filePath is set.
func NewStore(filePath string) *Store {
	s := &Store{
		drafts:   make(map[string]*Draft),
		filePath: filePath,
	}
	if filePath != "" {
		s.loadFromDisk()
	}
	return s
}

// Create creates a new draft in researching state.
func (s *Store) Create(subject string, params json.RawMessage) *Draft {
	d := &Draft{
		ID:        uuid.New().String(),
		Status:    StatusResearching,
		Subject:   subject,
		Params:    params,
		CreatedAt: time.Now(),
		Events:    []Event{},
	}
	d.appendEventLocked("created", "info", "Research started")

	s.mu.Lock()
	s.drafts[d.ID] = d
	s.mu.Unlock()
	s.persist()

	return d
}

// Get returns a draft by ID.
func (s *Store) Get(id string) *Draft {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.drafts[id]
}

// List returns all drafts sorted newest-first.
func (s *Store) List() []*Draft {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*Draft, 0, len(s.drafts))
	for _, d := range s.drafts {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})
	return list
}

// AppendLog adds a log event to the draft.
func (d *Draft) AppendLog(message, level string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.appendEventLocked("log", level, message)
}

// GetEvents returns events with ID > afterID.
func (d *Draft) GetEvents(afterID int) []Event {
	d.mu.Lock()
	defer d.mu.Unlock()

	var result []Event
	for _, e := range d.Events {
		if e.ID > afterID {
			result = append(result, e)
		}
	}
	return result
}

// Complete marks the draft as done with a script and sources.
func (d *Draft) Complete(script string, sources []Source) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Status = StatusDone
	d.Script = script
	d.Sources = sources
	d.CompletedAt = time.Now()
	d.appendEventLocked("done", "success", "Research complete — script ready for approval")
}

// Fail marks the draft as failed.
func (d *Draft) Fail(errMsg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Status = StatusFailed
	d.ErrorMessage = errMsg
	d.CompletedAt = time.Now()
	d.appendEventLocked("error", "error", errMsg)
}

func (d *Draft) appendEventLocked(eventType, level, message string) {
	d.NextEventID++
	d.Events = append(d.Events, Event{
		ID:        d.NextEventID,
		Type:      eventType,
		Level:     level,
		Message:   message,
		Timestamp: time.Now(),
	})
}

// PersistNow forces a save to disk.
func (s *Store) PersistNow() {
	s.persist()
}

func (s *Store) persist() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.persistUnlocked()
}

func (s *Store) persistUnlocked() {
	if s.filePath == "" {
		return
	}
	data, err := json.MarshalIndent(s.drafts, "", "  ")
	if err != nil {
		log.Printf("Warning: failed to marshal drafts: %v", err)
		return
	}
	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		log.Printf("Warning: failed to write %s: %v", s.filePath, err)
	}
}

func (s *Store) loadFromDisk() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Warning: failed to read %s: %v", s.filePath, err)
		}
		return
	}

	var drafts map[string]*Draft
	if err := json.Unmarshal(data, &drafts); err != nil {
		log.Printf("Warning: failed to parse %s: %v", s.filePath, err)
		return
	}

	// Any draft that was researching at shutdown can never resume — mark failed.
	for _, d := range drafts {
		if d.Status == StatusResearching {
			d.Status = StatusFailed
			d.ErrorMessage = "Server restarted during research — please start a new draft"
			d.CompletedAt = time.Now()
		}
	}

	s.drafts = drafts
	log.Printf("Loaded %d drafts from %s", len(drafts), s.filePath)
}
