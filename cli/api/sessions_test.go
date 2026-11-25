package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/openfloorcontrol/ofc/api"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
)

func TestCreateAndListSessions(t *testing.T) {
	f := newTestFloor(&blueprint.Blueprint{Name: "test"}, nil)
	f.SessionMetaTemplate = &floor.SessionMeta{BlueprintName: "test"}
	base := startTestAPI(t, f)

	resp, err := http.Post(base+"/api/v1/floors/test/sessions", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /sessions: %v", err)
	}
	var created struct{ ID string }
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || created.ID == "" {
		t.Fatalf("create: status %d, id %q", resp.StatusCode, created.ID)
	}

	resp, err = http.Post(base+"/api/v1/floors/test/sessions/"+created.ID+"/messages", "application/json",
		strings.NewReader(`{"from": "@test", "content": "hi"}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("post to new session: %v, %v", err, resp.Status)
	}
	resp.Body.Close()

	resp, err = http.Get(base + "/api/v1/floors/test/sessions")
	if err != nil {
		t.Fatalf("GET /sessions: %v", err)
	}
	var list struct {
		Sessions []struct {
			ID         string `json:"id"`
			EventCount int    `json:"event_count"`
		} `json:"sessions"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Sessions) != 1 || list.Sessions[0].ID != created.ID || list.Sessions[0].EventCount != 1 {
		t.Errorf("list = %+v, want only %s with 1 event", list.Sessions, created.ID)
	}
}

// Sessions created with X-Volume-User belong to that user: others neither
// list nor open them. Sessions without an owner are open to everyone, and
// a caller without the header sees everything.
func TestSessionsAreSeparatedByUser(t *testing.T) {
	f := newTestFloor(&blueprint.Blueprint{Name: "test"}, nil)
	f.SessionMetaTemplate = &floor.SessionMeta{BlueprintName: "test"}
	base := startTestAPI(t, f)

	do := func(method, path, user, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if user != "" {
			req.Header.Set(api.UserHeader, user)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}
	create := func(user string) string {
		t.Helper()
		resp := do("POST", "/api/v1/floors/test/sessions", user, "")
		defer resp.Body.Close()
		var created struct{ ID string }
		json.NewDecoder(resp.Body).Decode(&created)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create as %q: status %d", user, resp.StatusCode)
		}
		return created.ID
	}
	list := func(user string) (string, map[string]string) {
		t.Helper()
		resp := do("GET", "/api/v1/floors/test/sessions", user, "")
		defer resp.Body.Close()
		var l struct {
			User     string `json:"user"`
			Sessions []struct {
				ID   string `json:"id"`
				User string `json:"user"`
			} `json:"sessions"`
		}
		json.NewDecoder(resp.Body).Decode(&l)
		owners := map[string]string{}
		for _, s := range l.Sessions {
			owners[s.ID] = s.User
		}
		return l.User, owners
	}

	alice, bob, nobody := create("alice"), create("bob"), create("")

	me, got := list("alice")
	want := map[string]string{alice: "alice", nobody: ""}
	if me != "alice" || len(got) != len(want) || got[alice] != "alice" || got[nobody] != "" || got[bob] != "" {
		t.Errorf("alice lists user %q, sessions %v; want alice, %v", me, got, want)
	}
	if _, got := list(""); len(got) != 3 || got[bob] != "bob" {
		t.Errorf("without a user: %v, want all three", got)
	}

	for _, c := range []struct {
		user, id string
		status   int
	}{
		{"alice", bob, http.StatusNotFound},
		{"bob", bob, http.StatusOK},
		{"alice", nobody, http.StatusOK},
		{"", bob, http.StatusOK},
	} {
		resp := do("GET", "/api/v1/floors/test/sessions/"+c.id+"/messages", c.user, "")
		resp.Body.Close()
		if resp.StatusCode != c.status {
			t.Errorf("%q reads %s: status %d, want %d", c.user, c.id[:8], resp.StatusCode, c.status)
		}
	}
	resp := do("POST", "/api/v1/floors/test/sessions/"+bob+"/messages", "alice", `{"content": "hi"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("alice posts to bob's session: status %d, want 404", resp.StatusCode)
	}
}

// A client reconnecting with Last-Event-ID gets the stored messages after
// it, then live messages, each once.
func TestSSEReplaysAfterLastEventID(t *testing.T) {
	f := newTestFloor(&blueprint.Blueprint{Name: "test"}, nil)
	base := startTestAPI(t, f)
	sess := f.DefaultSession()
	sess.Start()
	for _, text := range []string{"one", "two", "three"} {
		sess.MainRoom.Post(floor.ChatMessage{From: "@user", Content: text})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", sessionURL(base, f)+"/events", nil)
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer resp.Body.Close()

	lines := bufio.NewScanner(resp.Body)
	var ids []string
	readIDs := func(n int) {
		for len(ids) < n && lines.Scan() {
			if id, ok := strings.CutPrefix(lines.Text(), "id: "); ok {
				ids = append(ids, id)
			}
		}
	}
	readIDs(2)
	sess.MainRoom.Post(floor.ChatMessage{From: "@user", Content: "four"})
	readIDs(3)

	if strings.Join(ids, ",") != "2,3,4" {
		t.Errorf("ids = %v, want [2 3 4]", ids)
	}
}

func TestUnknownSessionIs404(t *testing.T) {
	f := newTestFloor(&blueprint.Blueprint{Name: "test"}, nil)
	base := startTestAPI(t, f)

	resp, err := http.Get(base + "/api/v1/floors/test/sessions/no-such-session/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestSessionOfOtherFloorRejected(t *testing.T) {
	f := newTestFloor(&blueprint.Blueprint{Name: "test"}, nil)
	f.Store.SetMeta("theirs", floor.SessionMeta{Floor: "other", BlueprintName: "test"})
	base := startTestAPI(t, f)

	resp, err := http.Get(base + "/api/v1/floors/test/sessions/theirs/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
