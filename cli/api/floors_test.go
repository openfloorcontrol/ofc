package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openfloorcontrol/ofc/api"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/furniture"
)

// startTwoFloors serves floors "a" and "b", sharing one session store,
// from one server.
func startTwoFloors(t *testing.T) (a, b *floor.Floor, base string) {
	t.Helper()
	store := floor.NewMemoryStore()
	newFloor := func(name string) *floor.Floor {
		f := newTestFloor(&blueprint.Blueprint{Name: name, Description: "floor " + name, Agents: []blueprint.Agent{
			{ID: "@agent", Furniture: []string{"tasks"}},
		}}, nil)
		f.Store = store
		f.SessionMetaTemplate = &floor.SessionMeta{}
		return f
	}
	a, b = newFloor("a"), newFloor("b")
	srv := api.New()
	for _, f := range []*floor.Floor{a, b} {
		f.APIServer = srv
		if err := srv.AddFloor(f); err != nil {
			t.Fatal(err)
		}
		srv.RegisterFurniture(f.ID(), "tasks", furniture.WrapAsMCP(furniture.NewTaskBoard()))
	}
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopServer(srv) })
	return a, b, srv.BaseURL()
}

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func TestListFloors(t *testing.T) {
	_, _, base := startTwoFloors(t)
	var list struct {
		Floors []struct{ ID, Name, Description string }
	}
	if status := getJSON(t, base+"/api/v1/floors", &list); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if len(list.Floors) != 2 || list.Floors[0].ID != "a" || list.Floors[1].ID != "b" || list.Floors[1].Description != "floor b" {
		t.Errorf("floors = %+v, want a and b", list.Floors)
	}
}

// Each floor lists and opens only its own sessions.
func TestFloorsOnOneServerKeepTheirSessions(t *testing.T) {
	a, _, base := startTwoFloors(t)
	sess, err := a.CreateSession("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	var list struct{ Sessions []struct{ ID string } }
	getJSON(t, base+"/api/v1/floors/a/sessions", &list)
	if len(list.Sessions) != 1 || list.Sessions[0].ID != sess.ID() {
		t.Errorf("floor a lists %+v, want %s", list.Sessions, sess.ID())
	}
	list.Sessions = nil
	getJSON(t, base+"/api/v1/floors/b/sessions", &list)
	if len(list.Sessions) != 0 {
		t.Errorf("floor b lists %+v, want none", list.Sessions)
	}
	if status := getJSON(t, base+"/api/v1/floors/b/sessions/"+sess.ID()+"/messages", nil); status == http.StatusOK {
		t.Error("floor b opened floor a's session")
	}
}

func TestUnknownFloorIs404(t *testing.T) {
	_, _, base := startTwoFloors(t)
	for _, path := range []string{"/agents", "/sessions", "/furniture"} {
		if status := getJSON(t, base+"/api/v1/floors/nope"+path, nil); status != http.StatusNotFound {
			t.Errorf("%s of an unknown floor: status %d, want 404", path, status)
		}
	}
}

func TestAddFloorRejectsDuplicateID(t *testing.T) {
	srv := api.New()
	if err := srv.AddFloor(floor.NewFloor(&blueprint.Blueprint{Name: "a"})); err != nil {
		t.Fatal(err)
	}
	if err := srv.AddFloor(floor.NewFloor(&blueprint.Blueprint{Name: "a"})); err == nil {
		t.Error("a second floor a was added")
	}
}

// A removed floor is gone from the API, its MCP endpoints included; a
// floor with the same ID can be added again.
func TestRemoveFloor(t *testing.T) {
	a, _, base := startTwoFloors(t)
	listTools := func() int {
		req, _ := http.NewRequest("POST", base+"/api/v1/floors/a/mcp/tasks/",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+a.AgentToken("@agent"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if status := listTools(); status != http.StatusOK {
		t.Fatalf("MCP of floor a: status %d, want 200", status)
	}

	srv := a.APIServer
	srv.RemoveFloor("a")

	var list struct{ Floors []struct{ ID string } }
	getJSON(t, base+"/api/v1/floors", &list)
	if len(list.Floors) != 1 || list.Floors[0].ID != "b" {
		t.Errorf("floors after removing a: %+v, want b", list.Floors)
	}
	if status := getJSON(t, base+"/api/v1/floors/a/agents", nil); status != http.StatusNotFound {
		t.Errorf("agents of removed floor: status %d, want 404", status)
	}
	if status := listTools(); status == http.StatusOK {
		t.Error("MCP of removed floor still answers")
	}

	if err := srv.AddFloor(floor.NewFloor(&blueprint.Blueprint{Name: "a"})); err != nil {
		t.Errorf("adding floor a again: %v", err)
	}
}

// An agent's token opens its furniture on its own floor only.
func TestAgentTokenOpensOnlyItsFloorsFurniture(t *testing.T) {
	a, _, base := startTwoFloors(t)
	connects := func(floorID string) bool {
		client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
		session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
			Endpoint:   base + "/api/v1/floors/" + floorID + "/mcp/tasks/",
			HTTPClient: bearerClient(a.AgentToken("@agent")),
		}, nil)
		if err != nil {
			return false
		}
		session.Close()
		return true
	}
	if !connects("a") {
		t.Error("agent of floor a cannot open floor a's tasks")
	}
	if connects("b") {
		t.Error("agent of floor a opened floor b's tasks")
	}
}
