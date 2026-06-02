package floor

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func sampleMeta() SessionMeta {
	return SessionMeta{
		CWD:           "/home/me/project",
		BlueprintPath: "/home/me/project/bp.yaml",
		BlueprintName: "my-blueprint",
		BlueprintHash: "abc123",
		OfcVersion:    "0.1.0",
		CreatedAt:     time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC),
	}
}

func TestMemoryStoreMetaRoundTrip(t *testing.T) {
	s := NewMemoryStore()
	meta := sampleMeta()

	if err := s.SetMeta("sid", meta); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	got, err := s.GetMeta("sid")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got != meta {
		t.Errorf("meta round-trip mismatch: got %+v want %+v", got, meta)
	}
}

func TestMemoryStoreNoMetaReturnsError(t *testing.T) {
	s := NewMemoryStore()
	_, err := s.GetMeta("does-not-exist")
	if !errors.Is(err, ErrNoSessionMeta) {
		t.Errorf("expected ErrNoSessionMeta, got %v", err)
	}
}

func TestMemoryStoreSessionsIsolatedByMeta(t *testing.T) {
	s := NewMemoryStore()
	a := sampleMeta()
	a.BlueprintName = "a"
	b := sampleMeta()
	b.BlueprintName = "b"

	s.SetMeta("session-a", a)
	s.SetMeta("session-b", b)

	gotA, _ := s.GetMeta("session-a")
	gotB, _ := s.GetMeta("session-b")
	if gotA.BlueprintName != "a" || gotB.BlueprintName != "b" {
		t.Errorf("meta leaked between sessions: a=%s b=%s", gotA.BlueprintName, gotB.BlueprintName)
	}
}

func TestJSONLStoreMetaPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	s, err := NewJSONLStore(path)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	meta := sampleMeta()
	if err := s.SetMeta("default", meta); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	// Also write an event so the file has more than just meta.
	s.Append(AppendOpts{
		SessionID: "default",
		RoomID:    "#main",
		Event:     newMemMsg("hi"),
		VisibleTo: []string{"@a"},
	})
	s.Close()

	// Reload and verify meta survives.
	s2, err := NewJSONLStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	defer s2.Close()

	got, err := s2.GetMeta("default")
	if err != nil {
		t.Fatalf("GetMeta after reload: %v", err)
	}
	if got.BlueprintName != meta.BlueprintName {
		t.Errorf("blueprint_name lost: got %q want %q", got.BlueprintName, meta.BlueprintName)
	}
	if got.BlueprintHash != meta.BlueprintHash {
		t.Errorf("blueprint_hash lost: got %q want %q", got.BlueprintHash, meta.BlueprintHash)
	}
	if !got.CreatedAt.Equal(meta.CreatedAt) {
		t.Errorf("created_at lost: got %v want %v", got.CreatedAt, meta.CreatedAt)
	}

	// Events still work too.
	events, _ := s2.Read("default", EventFilter{})
	if len(events) != 1 {
		t.Errorf("expected 1 event after reload, got %d", len(events))
	}
}

func TestJSONLStoreSetMetaTwice(t *testing.T) {
	// SetMeta should replace, not accumulate. Append-only file means we
	// have two meta records on disk, but reload should resolve to the
	// last one.
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	s, _ := NewJSONLStore(path)
	first := sampleMeta()
	first.BlueprintName = "first"
	s.SetMeta("default", first)

	second := sampleMeta()
	second.BlueprintName = "second"
	s.SetMeta("default", second)
	s.Close()

	s2, _ := NewJSONLStore(path)
	defer s2.Close()
	got, err := s2.GetMeta("default")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.BlueprintName != "second" {
		t.Errorf("expected last SetMeta to win, got %q", got.BlueprintName)
	}
}

func TestJSONLStoreOldFileWithoutMeta(t *testing.T) {
	// Forward-compat: a file with only event/ref records (no meta) should
	// still load, and GetMeta returns ErrNoSessionMeta.
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	s, _ := NewJSONLStore(path)
	s.Append(AppendOpts{
		SessionID: "default",
		Event:     newMemMsg("hello"),
		VisibleTo: []string{"@a"},
	})
	s.Close()

	s2, _ := NewJSONLStore(path)
	defer s2.Close()

	_, err := s2.GetMeta("default")
	if !errors.Is(err, ErrNoSessionMeta) {
		t.Errorf("expected ErrNoSessionMeta for legacy file, got %v", err)
	}
	events, _ := s2.Read("default", EventFilter{})
	if len(events) != 1 {
		t.Errorf("events should still load: got %d", len(events))
	}
}
