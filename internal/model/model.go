package model

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/moneyprinter/internal/job"
)

type ModelStatus string

const (
	ModelStatusActive ModelStatus = "active"
	ModelStatusPaused ModelStatus = "paused"
)

type PostStatus string

const (
	PostStatusPlanned    PostStatus = "planned"
	PostStatusCaptioning PostStatus = "captioning" // agent writing caption + image prompt
	PostStatusGenerating PostStatus = "generating"  // image gen API in progress
	PostStatusCompleted  PostStatus = "completed"
	PostStatusFailed     PostStatus = "failed"
)

// Post is a single Instagram post in a model's feed.
type Post struct {
	Index       int        `json:"index"` // 1-based
	Status      PostStatus `json:"status"`
	Scene       string     `json:"scene,omitempty"`       // scene description chosen by agent
	Caption     string     `json:"caption,omitempty"`     // Instagram caption
	Hashtags    []string   `json:"hashtags,omitempty"`
	ImagePrompt string     `json:"imagePrompt,omitempty"` // prompt sent to image gen
	ImagePaths  []string   `json:"imagePaths,omitempty"`  // generated image files
	Error       string     `json:"error,omitempty"`
	StartedAt   time.Time  `json:"startedAt,omitempty"`
	CompletedAt time.Time  `json:"completedAt,omitempty"`
}

// Model is a virtual Instagram personality.
type Model struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`        // display name
	Handle      string      `json:"handle"`      // @handle
	Bio         string      `json:"bio"`         // Instagram bio
	Description string      `json:"description"` // detailed appearance for image gen
	Personality string      `json:"personality"` // personality traits for caption voice
	Style       string      `json:"style"`       // photography style (candid, editorial, etc.)
	RefImages   []string    `json:"refImages"`   // paths to reference photos
	Schedule    string      `json:"schedule"`    // "6h", "12h", "24h", "48h"
	Status      ModelStatus `json:"status"`
	Posts       []Post      `json:"posts,omitempty"`
	NextRunAt   time.Time   `json:"nextRunAt,omitempty"`
	Events      []job.Event `json:"events"`
	NextEventID int         `json:"nextEventId"`
	CreatedAt   time.Time   `json:"createdAt"`

	mu sync.Mutex `json:"-"`
}

// ScheduleInterval returns the duration between posts.
func (m *Model) ScheduleInterval() time.Duration {
	switch m.Schedule {
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
		return 24 * time.Hour
	}
}

// NextPlannedPost returns the next post in "planned" state, or nil.
func (m *Model) NextPlannedPost() *Post {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Posts {
		if m.Posts[i].Status == PostStatusPlanned {
			return &m.Posts[i]
		}
	}
	return nil
}

// HasActivePost returns true if any post is currently being processed.
func (m *Model) HasActivePost() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.Posts {
		if p.Status == PostStatusCaptioning || p.Status == PostStatusGenerating {
			return true
		}
	}
	return false
}

// CompletedPosts returns all posts that have completed (have captions).
func (m *Model) CompletedPosts() []Post {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []Post
	for _, p := range m.Posts {
		if p.Caption != "" {
			result = append(result, p)
		}
	}
	return result
}

// AddPlannedPosts appends n new planned posts.
func (m *Model) AddPlannedPosts(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	nextIndex := len(m.Posts) + 1
	for i := range n {
		m.Posts = append(m.Posts, Post{
			Index:  nextIndex + i,
			Status: PostStatusPlanned,
		})
	}
}

// EnsurePlannedPost adds a planned post only if none exist. Returns the next planned post.
func (m *Model) EnsurePlannedPost() *Post {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Posts {
		if m.Posts[i].Status == PostStatusPlanned {
			return &m.Posts[i]
		}
	}
	// None found — add one.
	idx := len(m.Posts) + 1
	m.Posts = append(m.Posts, Post{Index: idx, Status: PostStatusPlanned})
	return &m.Posts[len(m.Posts)-1]
}

// MarkPostCaptioning transitions a post to captioning state.
func (m *Model) MarkPostCaptioning(index int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Posts {
		if m.Posts[i].Index == index {
			m.Posts[i].Status = PostStatusCaptioning
			m.Posts[i].StartedAt = time.Now()
			return
		}
	}
}

// CompletePostCaption stores the agent result and transitions to generating.
func (m *Model) CompletePostCaption(index int, scene, caption, imagePrompt string, hashtags []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Posts {
		if m.Posts[i].Index == index {
			m.Posts[i].Scene = scene
			m.Posts[i].Caption = caption
			m.Posts[i].Hashtags = hashtags
			m.Posts[i].ImagePrompt = imagePrompt
			m.Posts[i].Status = PostStatusGenerating
			return
		}
	}
}

// CompletePostGeneration stores image paths and marks the post completed.
func (m *Model) CompletePostGeneration(index int, imagePaths []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Posts {
		if m.Posts[i].Index == index {
			m.Posts[i].ImagePaths = imagePaths
			m.Posts[i].Status = PostStatusCompleted
			m.Posts[i].CompletedAt = time.Now()
			return
		}
	}
}

// FailPost marks a post as failed.
func (m *Model) FailPost(index int, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Posts {
		if m.Posts[i].Index == index {
			m.Posts[i].Status = PostStatusFailed
			m.Posts[i].Error = errMsg
			m.Posts[i].CompletedAt = time.Now()
			return
		}
	}
}

// AdvanceSchedule sets NextRunAt based on schedule interval.
func (m *Model) AdvanceSchedule() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.NextRunAt = time.Now().Add(m.ScheduleInterval())
}

// TriggerPost resets a planned/failed post and sets NextRunAt to now.
func (m *Model) TriggerPost(index int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Posts {
		if m.Posts[i].Index == index {
			st := m.Posts[i].Status
			if st == PostStatusPlanned || st == PostStatusFailed {
				m.Posts[i].Status = PostStatusPlanned
				m.Posts[i].Error = ""
				m.Posts[i].StartedAt = time.Time{}
				m.Posts[i].CompletedAt = time.Time{}
				m.NextRunAt = time.Now()
				return true
			}
			return false
		}
	}
	return false
}

// IsDue returns true if this model has a pending post that should start now.
func (m *Model) IsDue() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Status != ModelStatusActive {
		return false
	}
	return !m.NextRunAt.IsZero() && !time.Now().Before(m.NextRunAt)
}

// PostCount returns total and completed post counts.
func (m *Model) PostCount() (total, completed int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	total = len(m.Posts)
	for _, p := range m.Posts {
		if p.Status == PostStatusCompleted {
			completed++
		}
	}
	return
}

// AppendLog adds a log event.
func (m *Model) AppendLog(message, level string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.NextEventID++
	m.Events = append(m.Events, job.Event{
		ID:        m.NextEventID,
		Type:      "log",
		Level:     level,
		Message:   message,
		Timestamp: time.Now(),
	})
}

// GetEvents returns events with ID > afterID.
func (m *Model) GetEvents(afterID int) []job.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []job.Event
	for _, e := range m.Events {
		if e.ID > afterID {
			result = append(result, e)
		}
	}
	return result
}

// --- Store ---

type Store struct {
	mu       sync.RWMutex
	models   map[string]*Model
	filePath string
}

func NewStore(filePath string) *Store {
	s := &Store{
		models:   make(map[string]*Model),
		filePath: filePath,
	}
	if filePath != "" {
		s.loadFromDisk()
	}
	return s
}

func (s *Store) Create(name, handle, bio, description, personality, style, schedule string) *Model {
	m := &Model{
		ID:          uuid.New().String(),
		Name:        name,
		Handle:      handle,
		Bio:         bio,
		Description: description,
		Personality: personality,
		Style:       style,
		Schedule:    schedule,
		Status:      ModelStatusPaused, // starts paused until ref images are generated
		Events:      []job.Event{},
		CreatedAt:   time.Now(),
	}
	m.AppendLog("Model created", "info")

	s.mu.Lock()
	s.models[m.ID] = m
	s.mu.Unlock()
	s.persist()
	return m
}

func (s *Store) Get(id string) *Model {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.models[id]
}

func (s *Store) List() []*Model {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*Model, 0, len(s.models))
	for _, m := range s.models {
		list = append(list, m)
	}
	sort.Slice(list, func(i, k int) bool {
		return list[i].CreatedAt.After(list[k].CreatedAt)
	})
	return list
}

func (s *Store) PersistNow() {
	s.persist()
}

func (s *Store) persist() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.filePath == "" {
		return
	}
	data, err := json.MarshalIndent(s.models, "", "  ")
	if err != nil {
		log.Printf("Warning: failed to persist models: %v", err)
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

	var models map[string]*Model
	if err := json.Unmarshal(data, &models); err != nil {
		log.Printf("Warning: failed to parse %s: %v", s.filePath, err)
		return
	}

	// Reset stuck posts on restart.
	for _, m := range models {
		for i := range m.Posts {
			if m.Posts[i].Status == PostStatusCaptioning || m.Posts[i].Status == PostStatusGenerating {
				m.Posts[i].Status = PostStatusPlanned
				m.Posts[i].StartedAt = time.Time{}
			}
		}
		if m.Status == ModelStatusActive && m.NextRunAt.IsZero() {
			m.NextRunAt = time.Now()
		}
	}

	s.models = models
	log.Printf("Loaded %d models from %s", len(models), s.filePath)
}
