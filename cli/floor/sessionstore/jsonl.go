// Package sessionstore holds file- and database-backed implementations
// of the floor.SessionStore interface. The in-memory default
// (floor.MemoryStore) stays in floor/ to avoid an import cycle with
// floor.NewFloor.
package sessionstore

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openfloorcontrol/ofc/floor"
)

// JSONLStore persists sessions as JSON Lines files in a directory, one
// file per session: <dir>/<sessionID>.jsonl. The filename is the session
// ID; the session_id field inside records is written for readability but
// ignored on load.
//
// Each Append writes one event line plus one ref line per agent in
// VisibleTo (all flushed to disk before returning). Clear writes a
// clear-record so reload reapplies the deletion.
//
// Reads go to an in-memory mirror (a floor.MemoryStore). A session's
// file is replayed into the mirror the first time the session is
// touched; files are only created on the first write.
//
// File format: one JSON object per line. Five record kinds:
//
//	{"kind":"event","session_id":"...","seq":1,"time":"...","room_id":"...",
//	 "private":false,"payload_type":"message_posted","payload":{...}}
//	{"kind":"ref","session_id":"...","agent_id":"@hiro","event_seq":1}
//	{"kind":"clear","session_id":"...","filter":{"room_id":"#main"}}
//	{"kind":"meta","session_id":"...","meta":{...}}
//	{"kind":"agent_state","session_id":"...","agent_id":"@coder","state":{...}}
//
// For meta and agent_state, the last record wins.
//
// Blobs (attachment bytes) are files beside the log:
// <dir>/<sessionID>.blobs/<sha256>.
//
// <dir>/schema_version counts the format migrations applied to the
// directory (see jsonl_migrate.go).
type JSONLStore struct {
	mu     sync.Mutex
	dir    string
	mem    *floor.MemoryStore
	loaded map[string]bool     // sessions replayed into mem
	files  map[string]*os.File // append handles, opened on first write
}

// NewJSONL opens a store over dir, creating the directory if needed and
// migrating its files to the current format.
func NewJSONL(dir string) (*JSONLStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create sessions dir %s: %w", dir, err)
	}
	if err := migrateJSONL(dir); err != nil {
		return nil, err
	}
	return &JSONLStore{
		dir:    dir,
		mem:    floor.NewMemoryStore(),
		loaded: make(map[string]bool),
		files:  make(map[string]*os.File),
	}, nil
}

// Close closes all open session files. Safe to call multiple times.
func (s *JSONLStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for sid, f := range s.files {
		errs = append(errs, f.Close())
		delete(s.files, sid)
	}
	return errors.Join(errs...)
}

// --- SessionStore implementation ---

// Append writes the event and its visibility refs to disk, then mirrors
// them in memory. Returns the StoredEvent with Seq and Time populated.
func (s *JSONLStore) Append(opts floor.AppendOpts) (floor.StoredEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureLoaded(opts.SessionID); err != nil {
		return floor.StoredEvent{}, err
	}

	// Append to the in-memory mirror first to get Seq + Time assigned.
	stored, err := s.mem.Append(opts)
	if err != nil {
		return stored, err
	}

	// Encode payload to JSON so we can store it as a tagged record.
	payload, err := json.Marshal(opts.Event)
	if err != nil {
		return stored, fmt.Errorf("marshal payload: %w", err)
	}

	// Build the records to write.
	records := make([][]byte, 0, 1+len(opts.VisibleTo))

	er := eventRecord{
		Kind:        "event",
		SessionID:   opts.SessionID,
		Seq:         stored.Seq,
		Time:        stored.Time,
		RoomID:      stored.RoomID,
		Private:     stored.Private,
		PayloadType: opts.Event.Type(),
		Payload:     payload,
	}
	erLine, err := json.Marshal(er)
	if err != nil {
		return stored, fmt.Errorf("marshal event record: %w", err)
	}
	records = append(records, erLine)

	for _, aid := range opts.VisibleTo {
		rr := refRecord{
			Kind:      "ref",
			SessionID: opts.SessionID,
			AgentID:   aid,
			EventSeq:  stored.Seq,
		}
		rrLine, err := json.Marshal(rr)
		if err != nil {
			return stored, fmt.Errorf("marshal ref record: %w", err)
		}
		records = append(records, rrLine)
	}

	if err := s.writeAndSync(opts.SessionID, records); err != nil {
		return stored, err
	}
	return stored, nil
}

// Read delegates to the in-memory mirror.
func (s *JSONLStore) Read(sessionID string, filter floor.EventFilter) ([]floor.StoredEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(sessionID); err != nil {
		return nil, err
	}
	return s.mem.Read(sessionID, filter)
}

// ReadForAgent delegates to the in-memory mirror.
func (s *JSONLStore) ReadForAgent(sessionID, agentID string, filter floor.EventFilter) ([]floor.StoredEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(sessionID); err != nil {
		return nil, err
	}
	return s.mem.ReadForAgent(sessionID, agentID, filter)
}

// SetMeta writes a meta-record to the file and applies it to the mirror.
// Append-only: a later SetMeta overrides earlier ones at load time
// because replay applies records in order (the last one wins).
func (s *JSONLStore) SetMeta(sessionID string, meta floor.SessionMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureLoaded(sessionID); err != nil {
		return err
	}
	rec := metaRecord{
		Kind:      "meta",
		SessionID: sessionID,
		Meta:      meta,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal meta record: %w", err)
	}
	if err := s.writeAndSync(sessionID, [][]byte{line}); err != nil {
		return err
	}
	return s.mem.SetMeta(sessionID, meta)
}

// GetMeta delegates to the in-memory mirror.
func (s *JSONLStore) GetMeta(sessionID string) (floor.SessionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(sessionID); err != nil {
		return floor.SessionMeta{}, err
	}
	return s.mem.GetMeta(sessionID)
}

// Clear writes a clear-record to the file and applies it to the mirror.
func (s *JSONLStore) Clear(sessionID string, filter floor.EventFilter) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureLoaded(sessionID); err != nil {
		return err
	}
	cr := clearRecord{
		Kind:      "clear",
		SessionID: sessionID,
		Filter:    filter,
	}
	line, err := json.Marshal(cr)
	if err != nil {
		return fmt.Errorf("marshal clear record: %w", err)
	}
	if err := s.writeAndSync(sessionID, [][]byte{line}); err != nil {
		return err
	}
	return s.mem.Clear(sessionID, filter)
}

// SetAgentState writes an agent_state record and applies it to the mirror.
func (s *JSONLStore) SetAgentState(sessionID, agentID string, st floor.AgentState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureLoaded(sessionID); err != nil {
		return err
	}
	line, err := json.Marshal(agentStateRecord{
		Kind:      "agent_state",
		SessionID: sessionID,
		AgentID:   agentID,
		State:     st,
	})
	if err != nil {
		return fmt.Errorf("marshal agent_state record: %w", err)
	}
	if err := s.writeAndSync(sessionID, [][]byte{line}); err != nil {
		return err
	}
	return s.mem.SetAgentState(sessionID, agentID, st)
}

// GetAgentState delegates to the in-memory mirror.
func (s *JSONLStore) GetAgentState(sessionID, agentID string) (floor.AgentState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(sessionID); err != nil {
		return floor.AgentState{}, err
	}
	return s.mem.GetAgentState(sessionID, agentID)
}

// List summarizes every *.jsonl file in the directory. Sessions not yet
// loaded are replayed into a scratch store and not kept in memory.
func (s *JSONLStore) List() ([]floor.SessionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read sessions dir %s: %w", s.dir, err)
	}
	var infos []floor.SessionInfo
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		sid := strings.TrimSuffix(name, ".jsonl")
		src := s.mem
		if !s.loaded[sid] {
			src = floor.NewMemoryStore()
			if err := replay(filepath.Join(s.dir, name), sid, src); err != nil {
				return nil, fmt.Errorf("load session %s: %w", sid, err)
			}
		}
		info, ok := src.Info(sid)
		if !ok {
			info = floor.SessionInfo{ID: sid} // file without any records
		}
		infos = append(infos, info)
	}
	floor.SortSessionInfos(infos)
	return infos, nil
}

// PutBlob writes data to <dir>/<sessionID>.blobs/<id>. Blobs are not
// mirrored in memory; GetBlob reads the file.
func (s *JSONLStore) PutBlob(sessionID string, data []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir, err := s.blobDir(sessionID)
	if err != nil {
		return "", err
	}
	id := floor.BlobID(data)
	path := filepath.Join(dir, id)
	if _, err := os.Stat(path); err == nil {
		return id, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create blob dir: %w", err)
	}
	// Write under a temporary name and rename, so a crash never leaves a
	// partial blob under its ID.
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create blob: %w", err)
	}
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("write blob: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("store blob: %w", err)
	}
	return id, nil
}

// GetBlob reads <dir>/<sessionID>.blobs/<id>.
func (s *JSONLStore) GetBlob(sessionID, id string) ([]byte, error) {
	dir, err := s.blobDir(sessionID)
	if err != nil {
		return nil, err
	}
	if !floor.ValidBlobID(id) {
		return nil, floor.ErrBlobNotFound
	}
	data, err := os.ReadFile(filepath.Join(dir, id))
	if os.IsNotExist(err) {
		return nil, floor.ErrBlobNotFound
	}
	return data, err
}

// Delete closes and removes the session's file and blobs and drops it
// from the mirror.
func (s *JSONLStore) Delete(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.path(sessionID)
	if err != nil {
		return err
	}
	blobDir, err := s.blobDir(sessionID)
	if err != nil {
		return err
	}
	if f, ok := s.files[sessionID]; ok {
		f.Close()
		delete(s.files, sessionID)
	}
	if s.loaded[sessionID] {
		delete(s.loaded, sessionID)
		_ = s.mem.Delete(sessionID) // absent if the file held no records
	}
	_, statErr := os.Stat(blobDir)
	hadBlobs := statErr == nil
	if err := os.RemoveAll(blobDir); err != nil {
		return fmt.Errorf("delete blobs of session %s: %w", sessionID, err)
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			if hadBlobs {
				return nil
			}
			return floor.ErrSessionNotFound
		}
		return fmt.Errorf("delete session %s: %w", sessionID, err)
	}
	return nil
}

// --- File handling ---

// path returns the file for a session. Session IDs become filenames, so
// anything that could escape the directory is rejected.
func (s *JSONLStore) path(sessionID string) (string, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) || strings.HasPrefix(sessionID, ".") {
		return "", fmt.Errorf("invalid session ID %q", sessionID)
	}
	return filepath.Join(s.dir, sessionID+".jsonl"), nil
}

// blobDir returns the directory of a session's blobs.
func (s *JSONLStore) blobDir(sessionID string) (string, error) {
	path, err := s.path(sessionID)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, ".jsonl") + ".blobs", nil
}

// ensureLoaded replays the session's file into the mirror on first use.
// Must be called with s.mu held.
func (s *JSONLStore) ensureLoaded(sessionID string) error {
	if s.loaded[sessionID] {
		return nil
	}
	path, err := s.path(sessionID)
	if err != nil {
		return err
	}
	if err := replay(path, sessionID, s.mem); err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}
	s.loaded[sessionID] = true
	return nil
}

// writeAndSync appends each record followed by '\n' to the session's
// file, opening (and creating) it on first write, then fsyncs.
// Must be called with s.mu held.
func (s *JSONLStore) writeAndSync(sessionID string, records [][]byte) error {
	f, ok := s.files[sessionID]
	if !ok {
		path, err := s.path(sessionID)
		if err != nil {
			return err
		}
		f, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("open %s for append: %w", path, err)
		}
		s.files[sessionID] = f
	}
	for _, r := range records {
		if _, err := f.Write(r); err != nil {
			return fmt.Errorf("write record: %w", err)
		}
		if _, err := f.Write([]byte{'\n'}); err != nil {
			return fmt.Errorf("write newline: %w", err)
		}
	}
	return f.Sync()
}

// --- Disk format ---

// recordHead is the minimal envelope we read first to dispatch records
// by kind.
type recordHead struct {
	Kind string `json:"kind"`
}

type eventRecord struct {
	Kind        string          `json:"kind"`
	SessionID   string          `json:"session_id"`
	Seq         uint64          `json:"seq"`
	Time        time.Time       `json:"time"`
	RoomID      string          `json:"room_id,omitempty"`
	Private     bool            `json:"private,omitempty"`
	PayloadType string          `json:"payload_type"`
	Payload     json.RawMessage `json:"payload"`
}

type refRecord struct {
	Kind      string `json:"kind"`
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	EventSeq  uint64 `json:"event_seq"`
}

type clearRecord struct {
	Kind      string            `json:"kind"`
	SessionID string            `json:"session_id"`
	Filter    floor.EventFilter `json:"filter"`
}

type metaRecord struct {
	Kind      string            `json:"kind"`
	SessionID string            `json:"session_id"`
	Meta      floor.SessionMeta `json:"meta"`
}

type agentStateRecord struct {
	Kind      string           `json:"kind"`
	SessionID string           `json:"session_id"`
	AgentID   string           `json:"agent_id"`
	State     floor.AgentState `json:"state"`
}

// replay loads the file at path into mem under sessionID. A missing
// file loads nothing. Tolerates a truncated final line (treats it as if
// the crashed Append never happened).
func replay(path, sessionID string, mem *floor.MemoryStore) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Allow large lines (default is 64KB)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var head recordHead
		if err := json.Unmarshal(line, &head); err != nil {
			// Truncated/corrupt final line: stop, treat as crash recovery.
			break
		}
		switch head.Kind {
		case "event":
			var er eventRecord
			if err := json.Unmarshal(line, &er); err != nil {
				break
			}
			ev, err := unmarshalSessionEvent(er.PayloadType, er.Payload)
			if err != nil {
				return fmt.Errorf("decode event payload (seq %d): %w", er.Seq, err)
			}
			// Append directly into the mirror, preserving Seq + Time.
			mem.AppendRaw(sessionID, floor.StoredEvent{
				Seq:     er.Seq,
				Time:    er.Time,
				RoomID:  er.RoomID,
				Event:   ev,
				Private: er.Private,
			})
		case "ref":
			var rr refRecord
			if err := json.Unmarshal(line, &rr); err != nil {
				break
			}
			mem.AddRef(sessionID, rr.AgentID, rr.EventSeq)
		case "clear":
			var cr clearRecord
			if err := json.Unmarshal(line, &cr); err != nil {
				break
			}
			_ = mem.Clear(sessionID, cr.Filter)
		case "meta":
			var mr metaRecord
			if err := json.Unmarshal(line, &mr); err != nil {
				break
			}
			_ = mem.SetMeta(sessionID, mr.Meta)
		case "agent_state":
			var ar agentStateRecord
			if err := json.Unmarshal(line, &ar); err != nil {
				break
			}
			_ = mem.SetAgentState(sessionID, ar.AgentID, ar.State)
		default:
			// Unknown record kind — skip (forward compat)
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		// Partial-read errors on the last line are tolerated; other
		// errors are returned.
		return err
	}
	return nil
}

// unmarshalSessionEvent dispatches by PayloadType to the concrete type.
// Future event types add their own case here.
func unmarshalSessionEvent(payloadType string, payload json.RawMessage) (floor.SessionEvent, error) {
	switch payloadType {
	case "message_posted":
		var p floor.MessagePostedEvent
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unknown payload type %q", payloadType)
	}
}
