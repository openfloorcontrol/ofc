// Package building runs many floors from one directory, served by one API
// server and persisted to one session store. It implements api.Building.
//
// A building directory holds:
//
//	templates/<name>/blueprint.yaml   blueprints floors are made from, with the files they use
//	floors/<dir>/floor.yaml           a floor's manifest (see Manifest)
//	floors/<dir>/blueprint.yaml       the floor's own copy of its template, and the rest of it
//
// Floors start when a request names them and stop after IdleTimeout
// without requests, followers or running turns.
package building

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openfloorcontrol/ofc/api"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"gopkg.in/yaml.v3"
)

// Manifest is a floor's floor.yaml: who it is and where it came from.
type Manifest struct {
	// ID identifies the floor in URLs and the session store; it never
	// changes. A new floor's directory is named after it.
	ID string `yaml:"id"`
	// Name is what people call the floor.
	Name string `yaml:"name"`
	// Template is the template the floor was made from.
	Template string    `yaml:"template"`
	Created  time.Time `yaml:"created"`
}

const (
	manifestFile  = "floor.yaml"
	blueprintFile = "blueprint.yaml"
)

// validID matches floor IDs and template names, which appear unescaped
// in URLs and paths.
var validID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Building manages the floors of a directory.
type Building struct {
	dir    string
	server floor.APIServer
	store  floor.SessionStore

	// Setup configures a floor before it starts, e.g. its AgentFactory.
	// The building has already set its APIServer and Store.
	Setup func(*floor.Floor) error
	// IdleTimeout stops a floor that has been idle this long. Zero keeps
	// floors running.
	IdleTimeout time.Duration
	// Info reports what floors do: starting, stopping.
	Info func(string)

	mu       sync.Mutex
	running  map[string]*entry // by floor ID; entries are never removed
	stopIdle chan struct{}
}

// entry is one floor's runtime state. mu is held while the floor starts
// or stops, so a slow start holds up only requests to this floor.
type entry struct {
	mu       sync.Mutex
	f        *floor.Floor // nil while stopped
	lastUsed time.Time
	// up mirrors f != nil for listings, which must not wait for a start.
	up atomic.Bool
}

// New returns the building at dir, whose floors are served by server and
// persisted to store. Both stay the caller's to stop and close.
func New(dir string, server floor.APIServer, store floor.SessionStore) *Building {
	return &Building{
		dir:     dir,
		server:  server,
		store:   store,
		Setup:   func(*floor.Floor) error { return nil },
		Info:    func(string) {},
		running: map[string]*entry{},
	}
}

var _ api.Building = (*Building)(nil)

// --- Listing ---

// floorDir is one directory of floors/ with its manifest.
type floorDir struct {
	path     string
	manifest Manifest
}

// scan reads the manifests of floors/. Directories without floor.yaml
// are not floors and are skipped; two floors with one ID are an error.
func (b *Building) scan() ([]floorDir, error) {
	entries, err := os.ReadDir(filepath.Join(b.dir, "floors"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []floorDir
	seen := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(b.dir, "floors", e.Name())
		data, err := os.ReadFile(filepath.Join(path, manifestFile))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var m Manifest
		if err := yaml.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(path, manifestFile), err)
		}
		if !validID.MatchString(m.ID) {
			return nil, fmt.Errorf("%s: id %q must use only letters, digits, '.', '_' or '-'", filepath.Join(path, manifestFile), m.ID)
		}
		if other, ok := seen[m.ID]; ok {
			return nil, fmt.Errorf("floors %s and %s both have id %q", other, path, m.ID)
		}
		seen[m.ID] = path
		out = append(out, floorDir{path: path, manifest: m})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].manifest.ID < out[j].manifest.ID })
	return out, nil
}

func (b *Building) find(id string) (floorDir, error) {
	floors, err := b.scan()
	if err != nil {
		return floorDir{}, err
	}
	for _, fd := range floors {
		if fd.manifest.ID == id {
			return fd, nil
		}
	}
	return floorDir{}, api.ErrFloorNotFound
}

// Floors lists every floor, running or not.
func (b *Building) Floors() ([]api.FloorInfo, error) {
	floors, err := b.scan()
	if err != nil {
		return nil, err
	}
	out := make([]api.FloorInfo, len(floors))
	for i, fd := range floors {
		out[i] = b.info(fd)
	}
	return out, nil
}

func (b *Building) info(fd floorDir) api.FloorInfo {
	m := fd.manifest
	b.mu.Lock()
	e := b.running[m.ID]
	b.mu.Unlock()
	running := e != nil && e.up.Load()
	return api.FloorInfo{
		ID:          m.ID,
		Name:        m.Name,
		Description: readHeader(filepath.Join(fd.path, blueprintFile)).Description,
		Template:    m.Template,
		Running:     running,
	}
}

// header is the part of a blueprint shown in listings. Reading only it
// keeps listing cheap: blueprint.Load resolves models and environment.
type header struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// readHeader reads a blueprint's header; an unreadable blueprint has an
// empty one.
func readHeader(path string) header {
	var h header
	if data, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(data, &h)
	}
	return h
}

// Templates lists templates/: each directory with a blueprint.yaml.
func (b *Building) Templates() ([]api.TemplateInfo, error) {
	entries, err := os.ReadDir(filepath.Join(b.dir, "templates"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []api.TemplateInfo
	for _, e := range entries {
		path := filepath.Join(b.dir, "templates", e.Name(), blueprintFile)
		if !e.IsDir() || !validID.MatchString(e.Name()) {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		out = append(out, api.TemplateInfo{Name: e.Name(), Description: readHeader(path).Description})
	}
	return out, nil
}

// --- Lifecycle ---

func (b *Building) entry(id string) *entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.running[id]
	if !ok {
		e = &entry{}
		b.running[id] = e
	}
	return e
}

// Open returns the floor with the given ID, starting it if it is stopped,
// and marks it as in use.
func (b *Building) Open(id string) (*floor.Floor, error) {
	e := b.entry(id)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.f == nil {
		fd, err := b.find(id)
		if err != nil {
			return nil, err
		}
		f, err := b.start(fd)
		if err != nil {
			return nil, fmt.Errorf("start floor %s: %w", id, err)
		}
		e.f = f
		e.up.Store(true)
	}
	e.lastUsed = time.Now()
	return e.f, nil
}

// start loads a floor's blueprint and starts the floor.
func (b *Building) start(fd floorDir) (*floor.Floor, error) {
	bp, err := blueprint.Load(filepath.Join(fd.path, blueprintFile))
	if err != nil {
		return nil, err
	}
	f := floor.NewFloorWithID(bp, fd.manifest.ID)
	f.APIServer = b.server
	f.Store = b.store
	if err := b.Setup(f); err != nil {
		return nil, err
	}
	b.Info(fmt.Sprintf("Starting floor %s", fd.manifest.ID))
	if err := f.Start(b.Info); err != nil {
		f.Stop()
		return nil, err
	}
	return f, nil
}

// Stop stops a running floor.
func (b *Building) Stop(id string) error {
	if _, err := b.find(id); err != nil {
		return err
	}
	e := b.entry(id)
	e.mu.Lock()
	defer e.mu.Unlock()
	b.stop(id, e)
	return nil
}

// stop stops e's floor, if running. Must be called with e.mu held.
func (b *Building) stop(id string, e *entry) {
	if e.f == nil {
		return
	}
	b.Info(fmt.Sprintf("Stopping floor %s", id))
	e.f.Stop()
	e.f = nil
	e.up.Store(false)
}

// Delete stops a floor and removes its directory. Its sessions stay in
// the store.
func (b *Building) Delete(id string) error {
	fd, err := b.find(id)
	if err != nil {
		return err
	}
	e := b.entry(id)
	e.mu.Lock()
	defer e.mu.Unlock()
	b.stop(id, e)
	b.Info(fmt.Sprintf("Deleting floor %s (%s)", id, fd.path))
	return os.RemoveAll(fd.path)
}

// Create makes a floor from a template: a copy of the template's
// directory in floors/<id>, with a manifest. The ID is <template>-<n>,
// with the smallest n not yet taken.
func (b *Building) Create(template string) (api.FloorInfo, error) {
	src := filepath.Join(b.dir, "templates", template)
	if !validID.MatchString(template) {
		return api.FloorInfo{}, api.ErrTemplateNotFound
	}
	if _, err := os.Stat(filepath.Join(src, blueprintFile)); err != nil {
		return api.FloorInfo{}, api.ErrTemplateNotFound
	}

	// Creating is serialized so two requests don't pick the same ID.
	b.mu.Lock()
	defer b.mu.Unlock()
	floors, err := b.scan()
	if err != nil {
		return api.FloorInfo{}, err
	}
	taken := map[string]bool{}
	for _, fd := range floors {
		taken[fd.manifest.ID] = true
	}
	var id, dst string
	for n := 1; ; n++ {
		id = template + "-" + strconv.Itoa(n)
		dst = filepath.Join(b.dir, "floors", id)
		if _, err := os.Stat(dst); !taken[id] && errors.Is(err, os.ErrNotExist) {
			break
		}
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return api.FloorInfo{}, err
	}
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		return api.FloorInfo{}, fmt.Errorf("copy template %s: %w", template, err)
	}
	m := Manifest{ID: id, Name: id, Template: template, Created: time.Now().UTC().Truncate(time.Second)}
	if err := writeManifest(dst, m); err != nil {
		return api.FloorInfo{}, err
	}
	b.Info(fmt.Sprintf("Created floor %s from template %s", id, template))
	return api.FloorInfo{
		ID:          id,
		Name:        m.Name,
		Description: readHeader(filepath.Join(dst, blueprintFile)).Description,
		Template:    template,
	}, nil
}

// Rename gives a floor a new name in its manifest; its ID stays.
func (b *Building) Rename(id, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	fd, err := b.find(id)
	if err != nil {
		return err
	}
	fd.manifest.Name = name
	return writeManifest(fd.path, fd.manifest)
}

// Blueprint returns the text of a floor's blueprint.yaml.
func (b *Building) Blueprint(id string) (string, error) {
	fd, err := b.find(id)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(fd.path, blueprintFile))
	return string(data), err
}

// SaveBlueprint replaces a floor's blueprint.yaml with text and stops the
// floor, so its next request starts it with the new blueprint. text is
// checked first, as a file beside blueprint.yaml so that paths in it
// resolve the same; if it doesn't load, nothing changes and the reason is
// returned as an *api.BlueprintError.
func (b *Building) SaveBlueprint(id, text string) error {
	fd, err := b.find(id)
	if err != nil {
		return err
	}
	e := b.entry(id)
	e.mu.Lock()
	defer e.mu.Unlock()

	check, err := os.CreateTemp(fd.path, ".blueprint-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(check.Name())
	_, werr := check.WriteString(text)
	cerr := check.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return err
	}
	if _, err := blueprint.Load(check.Name()); err != nil {
		return &api.BlueprintError{Err: err}
	}
	path := filepath.Join(fd.path, blueprintFile)
	if info, err := os.Stat(path); err == nil {
		if err := os.Chmod(check.Name(), info.Mode().Perm()); err != nil {
			return err
		}
	}
	if err := os.Rename(check.Name(), path); err != nil {
		return err
	}
	b.Info(fmt.Sprintf("Blueprint of floor %s changed", id))
	b.stop(id, e)
	return nil
}

// writeManifest writes m as the floor.yaml of the floor directory dir.
func writeManifest(dir string, m Manifest) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, manifestFile), data, 0o644)
}

// StopIdleFloors stops, every interval, the floors that have been idle
// for IdleTimeout: no request opened them in that time, nobody follows
// their sessions and no agent is taking a turn. Runs until Close.
func (b *Building) StopIdleFloors(interval time.Duration) {
	if b.IdleTimeout <= 0 {
		return
	}
	b.stopIdle = make(chan struct{})
	go func(done <-chan struct{}) {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				b.stopIdleOnce()
			}
		}
	}(b.stopIdle)
}

func (b *Building) stopIdleOnce() {
	b.mu.Lock()
	ids := make([]string, 0, len(b.running))
	for id := range b.running {
		ids = append(ids, id)
	}
	b.mu.Unlock()
	for _, id := range ids {
		e := b.entry(id)
		e.mu.Lock()
		if e.f != nil && time.Since(e.lastUsed) >= b.IdleTimeout && !e.f.Busy() {
			b.stop(id, e)
		}
		e.mu.Unlock()
	}
}

// Close stops every running floor and the idle check.
func (b *Building) Close() {
	if b.stopIdle != nil {
		close(b.stopIdle)
	}
	b.mu.Lock()
	ids := make([]string, 0, len(b.running))
	for id := range b.running {
		ids = append(ids, id)
	}
	b.mu.Unlock()
	for _, id := range ids {
		e := b.entry(id)
		e.mu.Lock()
		b.stop(id, e)
		e.mu.Unlock()
	}
}
