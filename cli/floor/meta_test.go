package floor

import (
	"errors"
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
