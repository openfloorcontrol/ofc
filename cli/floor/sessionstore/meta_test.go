package sessionstore

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/openfloorcontrol/ofc/floor"
)

func sampleMeta() floor.SessionMeta {
	return floor.SessionMeta{
		CWD:           "/home/me/project",
		BlueprintPath: "/home/me/project/bp.yaml",
		BlueprintName: "my-blueprint",
		BlueprintHash: "abc123",
		OfcVersion:    "0.1.0",
		CreatedAt:     time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC),
	}
}

func TestJSONLStoreMetaPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	s, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL: %v", err)
	}
	meta := sampleMeta()
	if err := s.SetMeta("default", meta); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	// Also write an event so the file has more than just meta.
	s.Append(floor.AppendOpts{
		SessionID: "default",
		RoomID:    "#main",
		Event:     newMemMsg("hi"),
		VisibleTo: []string{"@a"},
	})
	s.Close()

	// Reload and verify meta survives.
	s2, err := NewJSONL(path)
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
	events, _ := s2.Read("default", floor.EventFilter{})
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

	s, _ := NewJSONL(path)
	first := sampleMeta()
	first.BlueprintName = "first"
	s.SetMeta("default", first)

	second := sampleMeta()
	second.BlueprintName = "second"
	s.SetMeta("default", second)
	s.Close()

	s2, _ := NewJSONL(path)
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

	s, _ := NewJSONL(path)
	s.Append(floor.AppendOpts{
		SessionID: "default",
		Event:     newMemMsg("hello"),
		VisibleTo: []string{"@a"},
	})
	s.Close()

	s2, _ := NewJSONL(path)
	defer s2.Close()

	_, err := s2.GetMeta("default")
	if !errors.Is(err, floor.ErrNoSessionMeta) {
		t.Errorf("expected ErrNoSessionMeta for legacy file, got %v", err)
	}
	events, _ := s2.Read("default", floor.EventFilter{})
	if len(events) != 1 {
		t.Errorf("events should still load: got %d", len(events))
	}
}
