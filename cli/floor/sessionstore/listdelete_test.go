package sessionstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/openfloorcontrol/ofc/floor"
)

// testListDelete exercises List and Delete against any SessionStore.
func testListDelete(t *testing.T, s floor.SessionStore) {
	t.Helper()

	if infos, err := s.List(); err != nil || len(infos) != 0 {
		t.Fatalf("empty store: List = %v, %v", infos, err)
	}

	s.Append(floor.AppendOpts{SessionID: "older", Event: newMemMsg("1"), VisibleTo: []string{"@a"}})
	s.Append(floor.AppendOpts{SessionID: "newer", Event: newMemMsg("2")})
	s.Append(floor.AppendOpts{SessionID: "newer", Event: newMemMsg("3"), Private: true, VisibleTo: []string{"@a"}})
	meta := sampleMeta()
	if err := s.SetMeta("meta-only", meta); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	infos, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var ids []string
	for _, info := range infos {
		ids = append(ids, info.ID)
	}
	if len(ids) != 3 || ids[0] != "newer" || ids[1] != "older" || ids[2] != "meta-only" {
		t.Fatalf("List order: got %v, want [newer older meta-only]", ids)
	}
	if infos[0].EventCount != 2 {
		t.Errorf("newer: EventCount = %d, want 2 (private events count)", infos[0].EventCount)
	}
	if infos[0].LastActivity.IsZero() || infos[0].Meta != nil {
		t.Errorf("newer: LastActivity = %v, Meta = %v", infos[0].LastActivity, infos[0].Meta)
	}
	if !infos[2].LastActivity.IsZero() || infos[2].EventCount != 0 {
		t.Errorf("meta-only: LastActivity = %v, EventCount = %d", infos[2].LastActivity, infos[2].EventCount)
	}
	if infos[2].Meta == nil || infos[2].Meta.BlueprintName != meta.BlueprintName {
		t.Errorf("meta-only: Meta = %+v", infos[2].Meta)
	}

	if err := s.Delete("older"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if evs, _ := s.ReadForAgent("older", "@a", floor.EventFilter{}); len(evs) != 0 {
		t.Errorf("deleted session still has %d agent events", len(evs))
	}
	if err := s.Delete("meta-only"); err != nil {
		t.Fatalf("Delete meta-only: %v", err)
	}
	if _, err := s.GetMeta("meta-only"); !errors.Is(err, floor.ErrNoSessionMeta) {
		t.Errorf("deleted session still has meta: %v", err)
	}
	infos, _ = s.List()
	if len(infos) != 1 || infos[0].ID != "newer" {
		t.Errorf("after Delete: List = %v", infos)
	}

	if err := s.Delete("missing"); !errors.Is(err, floor.ErrSessionNotFound) {
		t.Errorf("Delete missing: got %v, want ErrSessionNotFound", err)
	}
}

func TestMemoryStoreListDelete(t *testing.T) {
	testListDelete(t, floor.NewMemoryStore())
}

func TestJSONLStoreListDelete(t *testing.T) {
	s, err := NewJSONL(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONL: %v", err)
	}
	defer s.Close()
	testListDelete(t, s)
}

func TestPostgresListDelete(t *testing.T) {
	s, cleanup := postgresTestStore(t)
	defer cleanup()
	testListDelete(t, s)
}

func TestJSONLStoreListUnloadedSessions(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewJSONL(dir)
	s.Append(floor.AppendOpts{SessionID: "a", Event: newMemMsg("1")})
	s.Append(floor.AppendOpts{SessionID: "a", Event: newMemMsg("2")})
	s.SetMeta("b", sampleMeta())
	s.Close()

	// A fresh store has loaded nothing; List reads the files.
	s2, _ := NewJSONL(dir)
	defer s2.Close()
	infos, err := s2.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 2 || infos[0].ID != "a" || infos[0].EventCount != 2 || infos[1].Meta == nil {
		t.Errorf("List after reopen: %+v", infos)
	}
}

func TestJSONLStoreFilenameIsSessionID(t *testing.T) {
	// Files written before session IDs were UUIDs carry
	// "session_id":"default" inside a <uuid>.jsonl file.
	dir := t.TempDir()
	line := `{"kind":"event","session_id":"default","seq":1,"time":"2026-06-01T10:00:00Z","payload_type":"message_posted","payload":{"Message":{"From":"@user","Content":"legacy"}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "abc.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	s, _ := NewJSONL(dir)
	defer s.Close()
	evs, err := s.Read("abc", floor.EventFilter{})
	if err != nil || len(evs) != 1 {
		t.Fatalf("Read legacy file: %d events, err %v", len(evs), err)
	}
	if evs, _ := s.Read("default", floor.EventFilter{}); len(evs) != 0 {
		t.Errorf("records leaked into session %q", "default")
	}
}

func TestJSONLStoreRejectsPathLikeIDs(t *testing.T) {
	s, _ := NewJSONL(t.TempDir())
	defer s.Close()
	for _, id := range []string{"", "../escape", "a/b", ".hidden"} {
		if _, err := s.Append(floor.AppendOpts{SessionID: id, Event: newMemMsg("x")}); err == nil {
			t.Errorf("Append accepted session ID %q", id)
		}
		if err := s.Delete(id); err == nil || errors.Is(err, floor.ErrSessionNotFound) {
			t.Errorf("Delete(%q): got %v, want invalid-ID error", id, err)
		}
	}
}
