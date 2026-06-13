package sessionstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openfloorcontrol/ofc/floor"
)

// newMemMsg builds a message_posted event for tests. Duplicated from
// floor.store_test (same one-liner) so this package's tests don't have
// to import unexported helpers from floor.
func newMemMsg(content string) floor.MessagePostedEvent {
	return floor.MessagePostedEvent{Message: floor.ChatMessage{From: "@user", Content: content}}
}

func extractMessages(events []floor.StoredEvent) []floor.ChatMessage {
	out := make([]floor.ChatMessage, 0, len(events))
	for _, ev := range events {
		if mp, ok := ev.Event.(floor.MessagePostedEvent); ok {
			out = append(out, mp.Message)
		}
	}
	return out
}

func jsonlTempPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "session.jsonl")
}

func TestJSONLStoreAppendAndReload(t *testing.T) {
	path := jsonlTempPath(t)

	// Write some events with one store instance, close it.
	s, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL: %v", err)
	}
	s.Append(floor.AppendOpts{
		SessionID: "sid",
		RoomID:    "#main",
		Event:     newMemMsg("hi"),
		VisibleTo: []string{"@a", "@b"},
	})
	s.Append(floor.AppendOpts{
		SessionID: "sid",
		RoomID:    "#main",
		Event:     newMemMsg("psst"),
		VisibleTo: []string{"@a"},
		Private:   true,
	})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen — events and refs should be preserved.
	s2, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL reload: %v", err)
	}
	defer s2.Close()

	// Read (display) should see only the public event.
	pub, _ := s2.Read("sid", floor.EventFilter{})
	if len(pub) != 1 {
		t.Errorf("expected 1 public event after reload, got %d", len(pub))
	}
	if extractMessages(pub)[0].Content != "hi" {
		t.Errorf("unexpected content: %q", extractMessages(pub)[0].Content)
	}

	// ReadForAgent @a should see both events.
	aEvents, _ := s2.ReadForAgent("sid", "@a", floor.EventFilter{})
	if len(aEvents) != 2 {
		t.Errorf("@a: expected 2 events after reload, got %d", len(aEvents))
	}
	// @b should see only the public one (psst was VisibleTo=[@a])
	bEvents, _ := s2.ReadForAgent("sid", "@b", floor.EventFilter{})
	if len(bEvents) != 1 {
		t.Errorf("@b: expected 1 event after reload, got %d", len(bEvents))
	}
}

func TestJSONLStoreClearReapplied(t *testing.T) {
	path := jsonlTempPath(t)

	s, _ := NewJSONL(path)
	s.Append(floor.AppendOpts{SessionID: "sid", RoomID: "#main", Event: newMemMsg("a"), VisibleTo: []string{"@x"}})
	s.Append(floor.AppendOpts{SessionID: "sid", RoomID: "#sub", Event: newMemMsg("b"), VisibleTo: []string{"@x"}})
	s.Clear("sid", floor.EventFilter{RoomID: "#main"})
	s.Close()

	s2, _ := NewJSONL(path)
	defer s2.Close()

	all, _ := s2.Read("sid", floor.EventFilter{})
	if len(all) != 1 {
		t.Fatalf("expected 1 event after Clear + reload, got %d", len(all))
	}
	if all[0].RoomID != "#sub" {
		t.Errorf("expected #sub event preserved, got %s", all[0].RoomID)
	}

	// Agent refs to cleared events should be gone too.
	x, _ := s2.ReadForAgent("sid", "@x", floor.EventFilter{})
	if len(x) != 1 {
		t.Errorf("@x: expected 1 event, got %d", len(x))
	}
}

func TestJSONLStoreMultipleSessions(t *testing.T) {
	path := jsonlTempPath(t)

	s, _ := NewJSONL(path)
	s.Append(floor.AppendOpts{SessionID: "s1", Event: newMemMsg("from s1"), VisibleTo: []string{"@x"}})
	s.Append(floor.AppendOpts{SessionID: "s2", Event: newMemMsg("from s2"), VisibleTo: []string{"@x"}})
	s.Close()

	s2, _ := NewJSONL(path)
	defer s2.Close()

	e1, _ := s2.Read("s1", floor.EventFilter{})
	e2, _ := s2.Read("s2", floor.EventFilter{})
	if len(e1) != 1 || len(e2) != 1 {
		t.Fatalf("session isolation broken: s1=%d s2=%d", len(e1), len(e2))
	}
	if extractMessages(e1)[0].Content != "from s1" || extractMessages(e2)[0].Content != "from s2" {
		t.Error("session events leaked across sessions")
	}
}

func TestJSONLStoreTruncatedLineRecovery(t *testing.T) {
	path := jsonlTempPath(t)

	// Write a valid event, then corrupt the file by appending a
	// truncated line (simulating a crash mid-write).
	s, _ := NewJSONL(path)
	s.Append(floor.AppendOpts{SessionID: "sid", Event: newMemMsg("good"), VisibleTo: []string{"@a"}})
	s.Close()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	// Append a partial line (no closing brace, no newline).
	f.WriteString(`{"kind":"event","session_id":"sid"`)
	f.Close()

	// Reload should tolerate the truncated line and recover the good event.
	s2, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("reload after truncation: %v", err)
	}
	defer s2.Close()

	events, _ := s2.Read("sid", floor.EventFilter{})
	if len(events) != 1 {
		t.Errorf("expected 1 recovered event, got %d", len(events))
	}
}

func TestJSONLStoreFileFormatIsLineDelimitedJSON(t *testing.T) {
	// Sanity check: opening the file in any JSONL parser should work.
	// Verify each line is a valid JSON object with a "kind" field.
	path := jsonlTempPath(t)
	s, _ := NewJSONL(path)
	s.Append(floor.AppendOpts{
		SessionID: "sid",
		RoomID:    "#main",
		Event:     newMemMsg("hello"),
		VisibleTo: []string{"@a"},
	})
	s.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 { // 1 event + 1 ref
		t.Fatalf("expected 2 lines (event + ref), got %d", len(lines))
	}
	if !strings.Contains(lines[0], `"kind":"event"`) {
		t.Errorf("line 1 not an event record: %q", lines[0])
	}
	if !strings.Contains(lines[1], `"kind":"ref"`) {
		t.Errorf("line 2 not a ref record: %q", lines[1])
	}
}

func TestJSONLStoreSeqMonotonicAcrossReload(t *testing.T) {
	path := jsonlTempPath(t)

	s, _ := NewJSONL(path)
	a, _ := s.Append(floor.AppendOpts{SessionID: "sid", Event: newMemMsg("1")})
	b, _ := s.Append(floor.AppendOpts{SessionID: "sid", Event: newMemMsg("2")})
	s.Close()
	if a.Seq != 1 || b.Seq != 2 {
		t.Fatalf("initial seqs unexpected: %d, %d", a.Seq, b.Seq)
	}

	// Reload; next Append should get Seq 3, not 1.
	s2, _ := NewJSONL(path)
	defer s2.Close()
	c, _ := s2.Append(floor.AppendOpts{SessionID: "sid", Event: newMemMsg("3")})
	if c.Seq != 3 {
		t.Errorf("expected Seq 3 after reload, got %d", c.Seq)
	}
}
