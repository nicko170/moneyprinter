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
	SeriesStatusCompleted SeriesStatus = "completed"
	SeriesStatusFailed    SeriesStatus = "failed"
)

type Series struct {
	ID           string       `json:"id"`
	Theme        string       `json:"theme"`
	EpisodeCount int          `json:"episodeCount"`
	Status       SeriesStatus `json:"status"`
	CreatedAt    time.Time    `json:"createdAt"`
	JobIDs       []string     `json:"jobIds"`
}

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

func (s *SeriesStore) Create(theme string, episodeCount int) *Series {
	ser := &Series{
		ID:           uuid.New().String(),
		Theme:        theme,
		EpisodeCount: episodeCount,
		Status:       SeriesStatusPending,
		CreatedAt:    time.Now(),
	}
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

func (s *SeriesStore) AddJob(seriesID, jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ser, ok := s.series[seriesID]; ok {
		ser.JobIDs = append(ser.JobIDs, jobID)
	}
	s.persistUnlocked()
}

func (s *SeriesStore) UpdateStatus(seriesID string, queue *Queue) {
	ser := s.Get(seriesID)
	if ser == nil {
		return
	}

	jobs := queue.ListBySeries(seriesID)
	if len(jobs) == 0 {
		return
	}

	allDone := true
	anyFailed := false
	anyRunning := false
	for _, j := range jobs {
		switch j.Status {
		case StatusFailed:
			anyFailed = true
		case StatusRunning, StatusQueued:
			allDone = false
		}
		if j.Status == StatusRunning {
			anyRunning = true
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case allDone && anyFailed:
		ser.Status = SeriesStatusFailed
	case allDone:
		ser.Status = SeriesStatusCompleted
	case anyRunning:
		ser.Status = SeriesStatusRunning
	default:
		ser.Status = SeriesStatusPending
	}
	s.persistUnlocked()
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

	s.series = series
	log.Printf("Loaded %d series from %s", len(series), s.filePath)
}
