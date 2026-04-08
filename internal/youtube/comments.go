package youtube

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"sync"
	"time"
)

// CommentRecord tracks a comment and our reply to it.
type CommentRecord struct {
	CommentID   string    `json:"commentId"`
	VideoID     string    `json:"videoId"`
	JobID       string    `json:"jobId"`
	Author      string    `json:"author"`
	Text        string    `json:"text"`
	PublishedAt time.Time `json:"publishedAt"`
	ReplyText   string    `json:"replyText,omitempty"`
	ReplyID     string    `json:"replyId,omitempty"`
	RepliedAt   time.Time `json:"repliedAt,omitempty"`
	Skipped     bool      `json:"skipped,omitempty"`
	Error       string    `json:"error,omitempty"`
	ProcessedAt time.Time `json:"processedAt"`
}

// CommentStore manages processed comments with JSON persistence.
type CommentStore struct {
	mu       sync.RWMutex
	records  map[string]*CommentRecord // keyed by comment ID
	filePath string
}

func NewCommentStore(filePath string) *CommentStore {
	s := &CommentStore{
		records:  make(map[string]*CommentRecord),
		filePath: filePath,
	}
	if filePath != "" {
		s.loadFromDisk()
	}
	return s
}

// IsProcessed returns true if we've already handled this comment.
func (s *CommentStore) IsProcessed(commentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.records[commentID]
	return ok
}

// RecordReply stores a successful reply.
func (s *CommentStore) RecordReply(rec CommentRecord) {
	s.mu.Lock()
	rec.ProcessedAt = time.Now()
	s.records[rec.CommentID] = &rec
	s.mu.Unlock()
	s.persist()
}

// RecordSkip stores a comment we chose not to reply to.
func (s *CommentStore) RecordSkip(commentID, videoID, jobID, author, text string) {
	s.mu.Lock()
	s.records[commentID] = &CommentRecord{
		CommentID:   commentID,
		VideoID:     videoID,
		JobID:       jobID,
		Author:      author,
		Text:        text,
		Skipped:     true,
		ProcessedAt: time.Now(),
	}
	s.mu.Unlock()
	s.persist()
}

// RecentActivity returns the most recent comment records, newest first.
func (s *CommentStore) RecentActivity(limit int) []*CommentRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*CommentRecord, 0, len(s.records))
	for _, r := range s.records {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ProcessedAt.After(list[j].ProcessedAt)
	})
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list
}

// Stats returns total processed, replied, and skipped counts.
func (s *CommentStore) Stats() (total, replied, skipped int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.records {
		total++
		if r.Skipped {
			skipped++
		} else if r.ReplyID != "" {
			replied++
		}
	}
	return
}

func (s *CommentStore) PersistNow() {
	s.persist()
}

func (s *CommentStore) persist() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.filePath == "" {
		return
	}
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		log.Printf("Warning: failed to persist comments: %v", err)
		return
	}
	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		log.Printf("Warning: failed to write %s: %v", s.filePath, err)
	}
}

func (s *CommentStore) loadFromDisk() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Warning: failed to read %s: %v", s.filePath, err)
		}
		return
	}
	var records map[string]*CommentRecord
	if err := json.Unmarshal(data, &records); err != nil {
		log.Printf("Warning: failed to parse %s: %v", s.filePath, err)
		return
	}
	s.records = records
	log.Printf("Loaded %d comment records from %s", len(records), s.filePath)
}
