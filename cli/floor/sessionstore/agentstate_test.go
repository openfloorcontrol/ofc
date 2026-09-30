package sessionstore

import (
	"errors"
	"testing"

	"github.com/openfloorcontrol/ofc/floor"
)

// testAgentState exercises SetAgentState / GetAgentState against any
// SessionStore.
func testAgentState(t *testing.T, s floor.SessionStore) {
	t.Helper()

	s.Append(floor.AppendOpts{SessionID: "s", Event: newMemMsg("hi"), VisibleTo: []string{"@coder"}})

	if _, err := s.GetAgentState("s", "@coder"); !errors.Is(err, floor.ErrNoAgentState) {
		t.Fatalf("unset: got %v, want ErrNoAgentState", err)
	}

	if err := s.SetAgentState("s", "@coder", floor.AgentState{ACPSessionID: "acp-1", SentSeq: 1}); err != nil {
		t.Fatalf("SetAgentState: %v", err)
	}
	if err := s.SetAgentState("s", "@coder", floor.AgentState{ACPSessionID: "acp-1", SentSeq: 7}); err != nil {
		t.Fatalf("SetAgentState again: %v", err)
	}
	got, err := s.GetAgentState("s", "@coder")
	if err != nil || got != (floor.AgentState{ACPSessionID: "acp-1", SentSeq: 7}) {
		t.Fatalf("GetAgentState = %+v, %v; want the last state", got, err)
	}
	if _, err := s.GetAgentState("s", "@other"); !errors.Is(err, floor.ErrNoAgentState) {
		t.Errorf("other agent: got %v, want ErrNoAgentState", err)
	}

	// Agent state is not part of the conversation.
	if evs, _ := s.Read("s", floor.EventFilter{}); len(evs) != 1 {
		t.Errorf("Read returned %d events, want 1", len(evs))
	}

	if err := s.Delete("s"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetAgentState("s", "@coder"); !errors.Is(err, floor.ErrNoAgentState) {
		t.Errorf("after Delete: got %v, want ErrNoAgentState", err)
	}
}

func TestMemoryStoreAgentState(t *testing.T) {
	testAgentState(t, floor.NewMemoryStore())
}

func TestJSONLStoreAgentState(t *testing.T) {
	s, _ := NewJSONL(t.TempDir())
	defer s.Close()
	testAgentState(t, s)
}

func TestPostgresAgentState(t *testing.T) {
	s, cleanup := postgresTestStore(t)
	defer cleanup()
	testAgentState(t, s)
}

func TestJSONLStoreAgentStateSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewJSONL(dir)
	s.Append(floor.AppendOpts{SessionID: "s", Event: newMemMsg("hi")})
	s.SetAgentState("s", "@coder", floor.AgentState{ACPSessionID: "acp-1", SentSeq: 1})
	s.SetAgentState("s", "@coder", floor.AgentState{ACPSessionID: "acp-1", SentSeq: 5})
	s.Close()

	s2, _ := NewJSONL(dir)
	defer s2.Close()
	got, err := s2.GetAgentState("s", "@coder")
	if err != nil || got.SentSeq != 5 || got.ACPSessionID != "acp-1" {
		t.Errorf("after reload: %+v, %v", got, err)
	}
}
