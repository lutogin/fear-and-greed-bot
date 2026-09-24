package dedup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Record describes the last notification sent for an alert side.
type Record struct {
	Level  float64   `json:"level"`
	Value  float64   `json:"value"`
	SentAt time.Time `json:"sent_at"`
}

// Store keeps the last Record per key.
type Store interface {
	Get(key string) (Record, bool)
	Put(key string, r Record) error
}

// MemoryStore keeps records in memory only; they are lost on restart.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]Record
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: map[string]Record{}}
}

func (s *MemoryStore) Get(key string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[key]
	return r, ok
}

func (s *MemoryStore) Put(key string, r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[key] = r
	return nil
}

// FileStore keeps records in memory and persists them to a JSON file, so the
// cooldown survives restarts.
type FileStore struct {
	path    string
	mu      sync.Mutex
	records map[string]Record
}

// OpenFileStore loads the records from path. A missing file means no records.
func OpenFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, records: map[string]Record{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state file: %w", err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &s.records); err != nil {
			return nil, fmt.Errorf("parse state file %s: %w", path, err)
		}
	}
	return s, nil
}

func (s *FileStore) Get(key string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[key]
	return r, ok
}

// Put stores the record and writes the whole state to disk. The in-memory copy
// is updated even if writing fails, so deduplication keeps working until restart.
func (s *FileStore) Put(key string, r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[key] = r
	return s.save()
}

// save writes the state atomically: a crash never leaves a half-written file.
func (s *FileStore) save() error {
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}
