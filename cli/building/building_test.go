package building_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openfloorcontrol/ofc/api"
	"github.com/openfloorcontrol/ofc/building"
	"github.com/openfloorcontrol/ofc/floor"
)

const template = `name: notes
description: Takes notes
furniture:
  - name: tasks
    type: taskboard
`

// newBuilding makes a building directory with template "notes" (a
// blueprint and a file beside it) and serves it.
func newBuilding(t *testing.T) (*building.Building, string, string) {
	t.Helper()
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "templates", "notes")
	if err := os.MkdirAll(tmpl, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpl, "blueprint.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpl, "persona.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := api.New()
	b := building.New(dir, srv, floor.NewMemoryStore())
	b.Setup = func(f *floor.Floor) error {
		f.SessionMetaTemplate = &floor.SessionMeta{}
		return nil
	}
	srv.SetBuilding(b)
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b.Close()
		// Shutdown waits up to 5s for connections the client opened but
		// never used; closing them first keeps the test fast.
		http.DefaultClient.CloseIdleConnections()
		srv.Stop()
	})
	return b, dir, srv.BaseURL()
}

func do(t *testing.T, method, url string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(`{"template":"notes"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

type floorList struct {
	Manageable bool
	Floors     []api.FloorInfo
}

func TestCreateFloorsFromTemplate(t *testing.T) {
	_, dir, base := newBuilding(t)

	var templates struct{ Templates []api.TemplateInfo }
	do(t, "GET", base+"/api/v1/templates", &templates)
	if len(templates.Templates) != 1 || templates.Templates[0].Name != "notes" || templates.Templates[0].Description != "Takes notes" {
		t.Errorf("templates = %+v", templates.Templates)
	}

	for _, want := range []string{"notes-1", "notes-2"} {
		var created api.FloorInfo
		if status := do(t, "POST", base+"/api/v1/floors", &created); status != http.StatusCreated {
			t.Fatalf("create: status %d", status)
		}
		if created.ID != want || created.Name != want || created.Template != "notes" {
			t.Errorf("created %+v, want %s", created, want)
		}
		if _, err := os.Stat(filepath.Join(dir, "floors", want, "persona.md")); err != nil {
			t.Errorf("floor %s lacks the template's files: %v", want, err)
		}
	}

	var list floorList
	do(t, "GET", base+"/api/v1/floors", &list)
	if !list.Manageable || len(list.Floors) != 2 || list.Floors[0].Running {
		t.Errorf("floors = %+v, want two stopped, manageable", list)
	}
}

func TestCreateFromUnknownTemplate(t *testing.T) {
	b, _, _ := newBuilding(t)
	for _, name := range []string{"nope", "../templates/notes", ""} {
		if _, err := b.Create(name); !errors.Is(err, api.ErrTemplateNotFound) {
			t.Errorf("Create(%q) = %v, want ErrTemplateNotFound", name, err)
		}
	}
}

// A request to a stopped floor starts it; Stop and the idle check stop
// it; its sessions survive.
func TestFloorStartsOnRequestAndStops(t *testing.T) {
	b, _, base := newBuilding(t)
	do(t, "POST", base+"/api/v1/floors", nil)

	var created struct{ ID string }
	if status := do(t, "POST", base+"/api/v1/floors/notes-1/sessions", &created); status != http.StatusCreated {
		t.Fatalf("create session: status %d", status)
	}
	var list floorList
	do(t, "GET", base+"/api/v1/floors", &list)
	if !list.Floors[0].Running {
		t.Errorf("floor not running after a request: %+v", list.Floors[0])
	}

	if status := do(t, "POST", base+"/api/v1/floors/notes-1/stop", nil); status != http.StatusOK {
		t.Fatalf("stop: status %d", status)
	}
	do(t, "GET", base+"/api/v1/floors", &list)
	if list.Floors[0].Running {
		t.Error("floor still running after stop")
	}

	var sessions struct{ Sessions []struct{ ID string } }
	do(t, "GET", base+"/api/v1/floors/notes-1/sessions", &sessions)
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != created.ID {
		t.Errorf("sessions after restart = %+v, want %s", sessions.Sessions, created.ID)
	}

	b.IdleTimeout = 50 * time.Millisecond
	b.StopIdleFloors(10 * time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for {
		do(t, "GET", base+"/api/v1/floors", &list)
		if !list.Floors[0].Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle floor was not stopped")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A floor with someone following a session is not idle.
func TestFollowedFloorIsNotIdle(t *testing.T) {
	b, _, base := newBuilding(t)
	do(t, "POST", base+"/api/v1/floors", nil)
	f, err := b.Open("notes-1")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := f.CreateSession("")
	if err != nil {
		t.Fatal(err)
	}
	events := sess.Subscribe()
	go func() {
		for range events {
		}
	}()

	b.IdleTimeout = 10 * time.Millisecond
	b.StopIdleFloors(5 * time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	var list floorList
	do(t, "GET", base+"/api/v1/floors", &list)
	if !list.Floors[0].Running {
		t.Error("a followed floor was stopped as idle")
	}
	sess.Unsubscribe(events)
}

func TestDeleteFloor(t *testing.T) {
	_, dir, base := newBuilding(t)
	do(t, "POST", base+"/api/v1/floors", nil)
	do(t, "GET", base+"/api/v1/floors/notes-1/agents", nil) // start it

	if status := do(t, "DELETE", base+"/api/v1/floors/notes-1", nil); status != http.StatusOK {
		t.Fatalf("delete: status %d", status)
	}
	if _, err := os.Stat(filepath.Join(dir, "floors", "notes-1")); !os.IsNotExist(err) {
		t.Errorf("floor directory still there: %v", err)
	}
	if status := do(t, "GET", base+"/api/v1/floors/notes-1/agents", nil); status != http.StatusNotFound {
		t.Errorf("deleted floor: status %d, want 404", status)
	}
	if status := do(t, "DELETE", base+"/api/v1/floors/notes-1", nil); status != http.StatusNotFound {
		t.Errorf("deleting again: status %d, want 404", status)
	}
}

func putBlueprint(t *testing.T, base, text string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"blueprint": text})
	req, _ := http.NewRequest("PUT", base+"/api/v1/floors/notes-1/blueprint", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct{ Error string }
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out.Error
}

// A saved blueprint replaces the floor's and restarts it; an invalid one
// is rejected with the reason and changes nothing.
func TestSaveBlueprint(t *testing.T) {
	_, dir, base := newBuilding(t)
	do(t, "POST", base+"/api/v1/floors", nil)
	path := filepath.Join(dir, "floors", "notes-1", "blueprint.yaml")

	var got struct{ Blueprint string }
	do(t, "GET", base+"/api/v1/floors/notes-1/blueprint", &got)
	if got.Blueprint != template {
		t.Errorf("GET blueprint = %q, want the template's", got.Blueprint)
	}

	status, msg := putBlueprint(t, base, "name: notes\nagents:\n  - id: \"@a\"\n    promt: hi\n")
	if status != http.StatusBadRequest || !strings.Contains(msg, `did you mean "prompt"`) {
		t.Errorf("invalid blueprint: status %d, error %q", status, msg)
	}
	if data, _ := os.ReadFile(path); string(data) != template {
		t.Errorf("rejected blueprint was written:\n%s", data)
	}
	status, msg = putBlueprint(t, base, "name: notes\nagents:\n  - id: \"@a\"\n    prompt_file: persona.md\n    furniture: [nope]\n")
	if status != http.StatusBadRequest || !strings.Contains(msg, `furniture "nope"`) {
		t.Errorf("blueprint with unknown furniture: status %d, error %q", status, msg)
	}

	do(t, "GET", base+"/api/v1/floors/notes-1/agents", nil) // start the floor
	edited := strings.Replace(template, "description: Takes notes", "description: Edited", 1)
	if status, msg := putBlueprint(t, base, edited); status != http.StatusOK {
		t.Fatalf("valid blueprint: status %d, error %q", status, msg)
	}
	if data, _ := os.ReadFile(path); string(data) != edited {
		t.Errorf("blueprint.yaml = %q, want the edited one", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Errorf("blueprint.yaml mode %v, want 0644", info.Mode().Perm())
	}
	var list floorList
	do(t, "GET", base+"/api/v1/floors", &list)
	if list.Floors[0].Running || list.Floors[0].Description != "Edited" {
		t.Errorf("after save: %+v, want stopped with the new description", list.Floors[0])
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".blueprint-") {
			t.Errorf("check file %s left behind", e.Name())
		}
	}
}

// Renaming changes a floor's name, not its ID.
func TestRenameFloor(t *testing.T) {
	_, dir, base := newBuilding(t)
	do(t, "POST", base+"/api/v1/floors", nil)

	req, _ := http.NewRequest("PATCH", base+"/api/v1/floors/notes-1", strings.NewReader(`{"name":"  Meeting notes "}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename: status %d", resp.StatusCode)
	}

	var list floorList
	do(t, "GET", base+"/api/v1/floors", &list)
	if len(list.Floors) != 1 || list.Floors[0].ID != "notes-1" || list.Floors[0].Name != "Meeting notes" {
		t.Errorf("floors after rename = %+v", list.Floors)
	}
	manifest, _ := os.ReadFile(filepath.Join(dir, "floors", "notes-1", "floor.yaml"))
	if !strings.Contains(string(manifest), "name: Meeting notes") || !strings.Contains(string(manifest), "template: notes") {
		t.Errorf("floor.yaml after rename:\n%s", manifest)
	}

	for body, want := range map[string]int{`{"name":""}`: http.StatusBadRequest, `{"name":"x"}`: http.StatusNotFound} {
		path := "/api/v1/floors/notes-1"
		if want == http.StatusNotFound {
			path = "/api/v1/floors/nope"
		}
		req, _ := http.NewRequest("PATCH", base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("PATCH %s %s: status %d, want %d", path, body, resp.StatusCode, want)
		}
	}
}

// The manifest, not the directory name, gives a floor its ID; two floors
// with one ID are an error.
func TestManifestGivesTheID(t *testing.T) {
	b, dir, _ := newBuilding(t)
	if _, err := b.Create("notes"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "floors", "notes-1"), filepath.Join(dir, "floors", "renamed")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open("notes-1"); err != nil {
		t.Errorf("open after renaming the directory: %v", err)
	}
	if err := os.CopyFS(filepath.Join(dir, "floors", "copy"), os.DirFS(filepath.Join(dir, "floors", "renamed"))); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Floors(); err == nil {
		t.Error("two floors with id notes-1 were listed")
	}
}
