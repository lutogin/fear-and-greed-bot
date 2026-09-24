package dedup

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMemoryStore(t *testing.T) {
	s := NewMemoryStore()
	if _, ok := s.Get("fear"); ok {
		t.Fatal("new store must be empty")
	}
	rec := Record{Level: 25, Value: 20, SentAt: t0}
	if err := s.Put("fear", rec); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Get("fear"); !ok || got != rec {
		t.Fatalf("Get = %+v, %v; want %+v", got, ok, rec)
	}
}

func TestFileStorePersistsAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "state.json")
	s, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("missing file must mean empty state: %v", err)
	}
	fear := Record{Level: 25, Value: 20, SentAt: t0}
	greed := Record{Level: 75, Value: 80.5, SentAt: t0.Add(time.Hour)}
	if err := s.Put("fear", fear); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("greed", greed); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]Record{"fear": fear, "greed": greed} {
		got, ok := reopened.Get(key)
		if !ok || !got.SentAt.Equal(want.SentAt) || got.Level != want.Level || got.Value != want.Value {
			t.Errorf("after reopen %s = %+v (found %v), want %+v", key, got, ok, want)
		}
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("state file permissions = %o, want 600", perm)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestOpenFileStoreEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("fear"); ok {
		t.Error("empty file must mean empty state")
	}
}

func TestOpenFileStoreCorruptedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileStore(path); err == nil {
		t.Fatal("expected an error for a corrupted state file")
	}
}

func TestFileStoreKeepsRecordInMemoryWhenWriteFails(t *testing.T) {
	// A directory in place of the state file makes the final rename fail.
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := Record{Level: 25, Value: 20, SentAt: t0}
	if err := s.Put("fear", rec); err == nil {
		t.Fatal("expected a write error")
	}
	if got, ok := s.Get("fear"); !ok || got != rec {
		t.Errorf("record must stay in memory after a failed write, got %+v, %v", got, ok)
	}
}
