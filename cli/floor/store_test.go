package floor

import (
	"testing"
)

func newMemMsg(content string) MessagePostedEvent {
	return MessagePostedEvent{Message: ChatMessage{From: "@user", Content: content}}
}

func TestMemoryStoreAppendAssignsSequence(t *testing.T) {
	s := NewMemoryStore()

	a, err := s.Append(AppendOpts{SessionID: "sid", Event: newMemMsg("first")})
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if a.Seq != 1 {
		t.Errorf("expected Seq 1, got %d", a.Seq)
	}

	b, _ := s.Append(AppendOpts{SessionID: "sid", Event: newMemMsg("second")})
	if b.Seq != 2 {
		t.Errorf("expected Seq 2, got %d", b.Seq)
	}
	if a.Time.After(b.Time) {
		t.Errorf("timestamps out of order")
	}
}

func TestMemoryStoreReadFiltersByRoom(t *testing.T) {
	s := NewMemoryStore()
	s.Append(AppendOpts{SessionID: "sid", RoomID: "#main", Event: newMemMsg("main 1")})
	s.Append(AppendOpts{SessionID: "sid", RoomID: "#analysis", Event: newMemMsg("sub 1")})
	s.Append(AppendOpts{SessionID: "sid", RoomID: "#main", Event: newMemMsg("main 2")})

	mainEvents, _ := s.Read("sid", EventFilter{RoomID: "#main"})
	if len(mainEvents) != 2 {
		t.Errorf("expected 2 #main events, got %d", len(mainEvents))
	}

	subEvents, _ := s.Read("sid", EventFilter{RoomID: "#analysis"})
	if len(subEvents) != 1 {
		t.Errorf("expected 1 #analysis event, got %d", len(subEvents))
	}

	all, _ := s.Read("sid", EventFilter{})
	if len(all) != 3 {
		t.Errorf("expected 3 events total, got %d", len(all))
	}
}

func TestMemoryStoreReadForAgent(t *testing.T) {
	s := NewMemoryStore()
	// e1 visible to A and B
	s.Append(AppendOpts{SessionID: "sid", RoomID: "#main", Event: newMemMsg("hi"), VisibleTo: []string{"@a", "@b"}})
	// e2 visible to A only
	s.Append(AppendOpts{SessionID: "sid", RoomID: "#main", Event: newMemMsg("psst"), VisibleTo: []string{"@a"}})
	// e3 visible to B only
	s.Append(AppendOpts{SessionID: "sid", RoomID: "#main", Event: newMemMsg("over here"), VisibleTo: []string{"@b"}})

	aEvents, _ := s.ReadForAgent("sid", "@a", EventFilter{})
	if len(aEvents) != 2 {
		t.Errorf("@a: expected 2 events, got %d", len(aEvents))
	}
	bEvents, _ := s.ReadForAgent("sid", "@b", EventFilter{})
	if len(bEvents) != 2 {
		t.Errorf("@b: expected 2 events, got %d", len(bEvents))
	}
	cEvents, _ := s.ReadForAgent("sid", "@c", EventFilter{})
	if len(cEvents) != 0 {
		t.Errorf("@c: expected 0 events, got %d", len(cEvents))
	}
}

func TestMemoryStoreReadSkipsPrivate(t *testing.T) {
	s := NewMemoryStore()
	s.Append(AppendOpts{SessionID: "sid", RoomID: "#main", Event: newMemMsg("public"), VisibleTo: []string{"@a"}})
	s.Append(AppendOpts{SessionID: "sid", RoomID: "#main", Event: newMemMsg("private"), VisibleTo: []string{"@a"}, Private: true})

	// Read (display) returns only public events.
	pub, _ := s.Read("sid", EventFilter{})
	if len(pub) != 1 {
		t.Errorf("expected 1 public event in Read, got %d", len(pub))
	}
	if extractMessages(pub)[0].Content != "public" {
		t.Errorf("expected public event, got %q", extractMessages(pub)[0].Content)
	}

	// ReadForAgent returns both.
	all, _ := s.ReadForAgent("sid", "@a", EventFilter{})
	if len(all) != 2 {
		t.Errorf("expected 2 events for @a in ReadForAgent, got %d", len(all))
	}
}

func TestMemoryStoreFromSeqFilter(t *testing.T) {
	s := NewMemoryStore()
	s.Append(AppendOpts{SessionID: "sid", Event: newMemMsg("1"), VisibleTo: []string{"@a"}})
	s.Append(AppendOpts{SessionID: "sid", Event: newMemMsg("2"), VisibleTo: []string{"@a"}})
	s.Append(AppendOpts{SessionID: "sid", Event: newMemMsg("3"), VisibleTo: []string{"@a"}})

	rest, _ := s.ReadForAgent("sid", "@a", EventFilter{FromSeq: 1})
	if len(rest) != 2 {
		t.Errorf("expected 2 events after Seq 1, got %d", len(rest))
	}
	if rest[0].Seq != 2 || rest[1].Seq != 3 {
		t.Errorf("unexpected seqs: %d, %d", rest[0].Seq, rest[1].Seq)
	}
}

func TestMemoryStoreClear(t *testing.T) {
	s := NewMemoryStore()
	s.Append(AppendOpts{SessionID: "sid", RoomID: "#main", Event: newMemMsg("m1"), VisibleTo: []string{"@a"}})
	s.Append(AppendOpts{SessionID: "sid", RoomID: "#sub", Event: newMemMsg("s1"), VisibleTo: []string{"@a"}})
	s.Append(AppendOpts{SessionID: "sid", RoomID: "#main", Event: newMemMsg("m2"), VisibleTo: []string{"@a"}})

	// Clear #main only.
	s.Clear("sid", EventFilter{RoomID: "#main"})

	main, _ := s.Read("sid", EventFilter{RoomID: "#main"})
	if len(main) != 0 {
		t.Errorf("expected 0 #main events after Clear, got %d", len(main))
	}
	sub, _ := s.Read("sid", EventFilter{RoomID: "#sub"})
	if len(sub) != 1 {
		t.Errorf("expected #sub event preserved, got %d", len(sub))
	}

	// Agent refs to cleared events should also be gone.
	aEvents, _ := s.ReadForAgent("sid", "@a", EventFilter{})
	if len(aEvents) != 1 {
		t.Errorf("@a: expected 1 event (#sub only), got %d", len(aEvents))
	}
}

func TestMemoryStoreSessionIsolation(t *testing.T) {
	s := NewMemoryStore()
	s.Append(AppendOpts{SessionID: "s1", Event: newMemMsg("a"), VisibleTo: []string{"@x"}})
	s.Append(AppendOpts{SessionID: "s2", Event: newMemMsg("b"), VisibleTo: []string{"@x"}})

	s1Events, _ := s.Read("s1", EventFilter{})
	s2Events, _ := s.Read("s2", EventFilter{})
	if len(s1Events) != 1 || len(s2Events) != 1 {
		t.Fatalf("session events not isolated: %d, %d", len(s1Events), len(s2Events))
	}
	if extractMessages(s1Events)[0].Content != "a" || extractMessages(s2Events)[0].Content != "b" {
		t.Error("session events leaked across sessions")
	}

	// Same agent ID in different sessions should be separate
	s1x, _ := s.ReadForAgent("s1", "@x", EventFilter{})
	s2x, _ := s.ReadForAgent("s2", "@x", EventFilter{})
	if len(s1x) != 1 || len(s2x) != 1 {
		t.Errorf("per-agent refs leaked: s1=%d s2=%d", len(s1x), len(s2x))
	}
}
