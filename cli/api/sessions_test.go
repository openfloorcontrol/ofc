package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
)

func TestCreateAndListSessions(t *testing.T) {
	f := newTestFloor(&blueprint.Blueprint{Name: "test"}, nil)
	f.SessionMetaTemplate = &floor.SessionMeta{BlueprintName: "test"}
	base := startTestAPI(t, f)

	resp, err := http.Post(base+"/api/v1/sessions", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /sessions: %v", err)
	}
	var created struct{ ID string }
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || created.ID == "" {
		t.Fatalf("create: status %d, id %q", resp.StatusCode, created.ID)
	}

	resp, err = http.Post(base+"/api/v1/sessions/"+created.ID+"/messages", "application/json",
		strings.NewReader(`{"from": "@test", "content": "hi"}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("post to new session: %v, %v", err, resp.Status)
	}
	resp.Body.Close()

	resp, err = http.Get(base + "/api/v1/sessions")
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

	resp, err := http.Get(base + "/api/v1/sessions/no-such-session/messages")
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
	f.Store.SetMeta("theirs", floor.SessionMeta{BlueprintName: "other"})
	base := startTestAPI(t, f)

	resp, err := http.Get(base + "/api/v1/sessions/theirs/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
