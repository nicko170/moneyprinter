package job

import (
	"context"
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
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type Event struct {
	ID        int       `json:"id"`
	Type      string    `json:"type"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

type Job struct {
	ID              string          `json:"id"`
	SeriesID        string          `json:"seriesId,omitempty"`
	Status          Status          `json:"status"`
	CancelRequested bool            `json:"cancelRequested"`
	ResultPath      string          `json:"resultPath,omitempty"`
	ErrorMessage    string          `json:"errorMessage,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	StartedAt       time.Time       `json:"startedAt,omitempty"`
	CompletedAt     time.Time       `json:"completedAt,omitempty"`
	Subject         string          `json:"subject"`
	Payload         json.RawMessage `json:"payload"`
	Events          []Event         `json:"events"`
	NextEventID     int             `json:"nextEventId"`

	mu     sync.Mutex `json:"-"`
	cancel context.CancelFunc `json:"-"`
}

// Queue manages jobs with optional persistence to disk.
type Queue struct {
	mu       sync.RWMutex
	jobs     map[string]*Job
	filePath string // empty = no persistence
}

// NewQueue creates a queue. If filePath is non-empty, loads existing jobs
// from disk and persists on every mutation.
func NewQueue(filePath string) *Queue {
	q := &Queue{
		jobs:     make(map[string]*Job),
		filePath: filePath,
	}
	if filePath != "" {
		q.loadFromDisk()
	}
	return q
}

// Submit creates a new queued job and returns its ID.
func (q *Queue) Submit(payload json.RawMessage, subject string) string {
	return q.SubmitWithSeries(payload, subject, "")
}

// SubmitWithSeries creates a new queued job linked to an optional series.
func (q *Queue) SubmitWithSeries(payload json.RawMessage, subject, seriesID string) string {
	id := uuid.New().String()
	j := &Job{
		ID:        id,
		SeriesID:  seriesID,
		Status:    StatusQueued,
		CreatedAt: time.Now(),
		Subject:   subject,
		Payload:   payload,
		Events:    []Event{},
	}
	j.appendEvent("queued", "info", "Job queued")

	q.mu.Lock()
	q.jobs[id] = j
	q.mu.Unlock()
	q.persist()

	return id
}

// Get returns a job by ID.
func (q *Queue) Get(id string) *Job {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.jobs[id]
}

// List returns all jobs sorted by creation time (newest first).
func (q *Queue) List() []*Job {
	q.mu.RLock()
	defer q.mu.RUnlock()

	jobs := make([]*Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		jobs = append(jobs, j)
	}
	sort.Slice(jobs, func(i, k int) bool {
		return jobs[i].CreatedAt.After(jobs[k].CreatedAt)
	})
	return jobs
}

// ListBySeries returns jobs belonging to a series.
func (q *Queue) ListBySeries(seriesID string) []*Job {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var jobs []*Job
	for _, j := range q.jobs {
		if j.SeriesID == seriesID {
			jobs = append(jobs, j)
		}
	}
	sort.Slice(jobs, func(i, k int) bool {
		return jobs[i].CreatedAt.Before(jobs[k].CreatedAt)
	})
	return jobs
}

// Claim finds the oldest queued job, marks it running, and returns it with a context.
func (q *Queue) Claim() (*Job, context.Context) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var oldest *Job
	for _, j := range q.jobs {
		if j.Status != StatusQueued {
			continue
		}
		if oldest == nil || j.CreatedAt.Before(oldest.CreatedAt) {
			oldest = j
		}
	}
	if oldest == nil {
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	oldest.mu.Lock()
	oldest.Status = StatusRunning
	oldest.StartedAt = time.Now()
	oldest.cancel = cancel
	oldest.appendEventLocked("running", "info", "Job started")
	oldest.mu.Unlock()
	q.persistUnlocked()

	return oldest, ctx
}

// Cancel requests cancellation for a job.
func (q *Queue) Cancel(id string) bool {
	j := q.Get(id)
	if j == nil {
		return false
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if j.Status == StatusCompleted || j.Status == StatusFailed || j.Status == StatusCancelled {
		return false
	}

	j.CancelRequested = true
	j.appendEventLocked("cancel_requested", "warning", "Cancellation requested")

	if j.Status == StatusQueued {
		j.Status = StatusCancelled
		j.CompletedAt = time.Now()
		j.appendEventLocked("cancelled", "warning", "Job cancelled before start")
	}

	if j.cancel != nil {
		j.cancel()
	}

	q.persist()
	return true
}

// Complete marks a job as successfully completed.
func (j *Job) Complete(resultPath string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = StatusCompleted
	j.ResultPath = resultPath
	j.CompletedAt = time.Now()
	j.appendEventLocked("complete", "success", "Video generated")
}

// Fail marks a job as failed.
func (j *Job) Fail(errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = StatusFailed
	j.ErrorMessage = errMsg
	j.CompletedAt = time.Now()
	j.appendEventLocked("error", "error", errMsg)
}

// MarkCancelled marks a running job as cancelled.
func (j *Job) MarkCancelled(reason string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = StatusCancelled
	j.CompletedAt = time.Now()
	j.appendEventLocked("cancelled", "warning", reason)
}

// AppendLog adds a log event to the job.
func (j *Job) AppendLog(message, level string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.appendEventLocked("log", level, message)
}

// GetEvents returns events with ID > afterID.
func (j *Job) GetEvents(afterID int) []Event {
	j.mu.Lock()
	defer j.mu.Unlock()

	var result []Event
	for _, e := range j.Events {
		if e.ID > afterID {
			result = append(result, e)
		}
	}
	return result
}

func (j *Job) appendEvent(eventType, level, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.appendEventLocked(eventType, level, message)
}

func (j *Job) appendEventLocked(eventType, level, message string) {
	j.NextEventID++
	j.Events = append(j.Events, Event{
		ID:        j.NextEventID,
		Type:      eventType,
		Level:     level,
		Message:   message,
		Timestamp: time.Now(),
	})
}

// --- Persistence ---

// persist saves all jobs to disk (called with q.mu NOT held).
func (q *Queue) persist() {
	q.mu.RLock()
	defer q.mu.RUnlock()
	q.persistUnlocked()
}

// persistUnlocked saves all jobs to disk (called with q.mu already held).
func (q *Queue) persistUnlocked() {
	if q.filePath == "" {
		return
	}
	data, err := json.MarshalIndent(q.jobs, "", "  ")
	if err != nil {
		log.Printf("Warning: failed to persist jobs: %v", err)
		return
	}
	if err := os.WriteFile(q.filePath, data, 0644); err != nil {
		log.Printf("Warning: failed to write %s: %v", q.filePath, err)
	}
}

// PersistNow forces a save — call after Job.Complete/Fail/MarkCancelled
// which don't have access to the queue.
func (q *Queue) PersistNow() {
	q.persist()
}

func (q *Queue) loadFromDisk() {
	data, err := os.ReadFile(q.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Warning: failed to read %s: %v", q.filePath, err)
		}
		return
	}

	var jobs map[string]*Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		log.Printf("Warning: failed to parse %s: %v", q.filePath, err)
		return
	}

	// Restore jobs. Any that were "running" at shutdown are reset to "queued"
	// so they get re-claimed by workers.
	for _, j := range jobs {
		if j.Status == StatusRunning {
			j.Status = StatusQueued
			j.StartedAt = time.Time{}
			j.appendEvent("queued", "info", "Re-queued after server restart")
		}
	}

	q.jobs = jobs
	log.Printf("Loaded %d jobs from %s", len(jobs), q.filePath)
}
