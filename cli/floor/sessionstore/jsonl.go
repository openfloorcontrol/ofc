// Package sessionstore holds file- and database-backed implementations
// of the floor.SessionStore interface. The in-memory default
// (floor.MemoryStore) stays in floor/ to avoid an import cycle with
// floor.NewFloor.
package sessionstore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/openfloorcontrol/ofc/floor"
)

// JSONLStore persists session events to a JSON Lines file. Each Append
// writes one event line plus one ref line per agent in VisibleTo (all
// flushed to disk before returning). Clear writes a clear-record so
// reload reapplies the deletion.
//
// Reads go to an in-memory mirror (a floor.MemoryStore) so they're fast;
// the file is write-only at runtime. On startup, the file is replayed
// into the mirror.
//
// File format: one JSON object per line. Four record kinds:
//
//	{"kind":"event","session_id":"...","seq":1,"time":"...","room_id":"...",
//	 "private":false,"payload_type":"message_posted","payload":{...}}
//	{"kind":"ref","session_id":"...","agent_id":"@hiro","event_seq":1}
//	{"kind":"clear","session_id":"...","filter":{"room_id":"#main"}}
//	{"kind":"meta","session_id":"...","meta":{...}}
type JSONLStore struct {
	mu   sync.Mutex
	path string
	file *os.File
	mem  *floor.MemoryStore
}

// NewJSONL opens (or creates) the given path, replays existing records
// into an in-memory mirror, then opens the file for appending.
func NewJSONL(path string) (*JSONLStore, error) {
	s := &JSONLStore{
		path: path,
		mem:  floor.NewMemoryStore(),
	}
	if err := s.load(); err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s for append: %w", path, err)
	}
	s.file = f
	return s, nil
}

// Close closes the underlying file. Safe to call multiple times.
func (s *JSONLStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// --- SessionStore implementation ---

// Append writes the event and its visibility refs to disk, then mirrors
// them in memory. Returns the StoredEvent with Seq and Time populated.
func (s *JSONLStore) Append(opts floor.AppendOpts) (floor.StoredEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	if err := s.writeAndSync(records); err != nil {
		return stored, err
	}
	return stored, nil
}

// Read delegates to the in-memory mirror.
func (s *JSONLStore) Read(sessionID string, filter floor.EventFilter) ([]floor.StoredEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mem.Read(sessionID, filter)
}

// ReadForAgent delegates to the in-memory mirror.
func (s *JSONLStore) ReadForAgent(sessionID, agentID string, filter floor.EventFilter) ([]floor.StoredEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mem.ReadForAgent(sessionID, agentID, filter)
}

// SetMeta writes a meta-record to the file and applies it to the mirror.
// Append-only: a later SetMeta overrides earlier ones at load time
// because replay applies records in order (the last one wins).
func (s *JSONLStore) SetMeta(sessionID string, meta floor.SessionMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := metaRecord{
		Kind:      "meta",
		SessionID: sessionID,
		Meta:      meta,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal meta record: %w", err)
	}
	if err := s.writeAndSync([][]byte{line}); err != nil {
		return err
	}
	return s.mem.SetMeta(sessionID, meta)
}

// GetMeta delegates to the in-memory mirror.
func (s *JSONLStore) GetMeta(sessionID string) (floor.SessionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mem.GetMeta(sessionID)
}

// Clear writes a clear-record to the file and applies it to the mirror.
func (s *JSONLStore) Clear(sessionID string, filter floor.EventFilter) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cr := clearRecord{
		Kind:      "clear",
		SessionID: sessionID,
		Filter:    filter,
	}
	line, err := json.Marshal(cr)
	if err != nil {
		return fmt.Errorf("marshal clear record: %w", err)
	}
	if err := s.writeAndSync([][]byte{line}); err != nil {
		return err
	}
	return s.mem.Clear(sessionID, filter)
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

// writeAndSync writes each record followed by '\n', then fsyncs.
// Must be called with s.mu held.
func (s *JSONLStore) writeAndSync(records [][]byte) error {
	for _, r := range records {
		if _, err := s.file.Write(r); err != nil {
			return fmt.Errorf("write record: %w", err)
		}
		if _, err := s.file.Write([]byte{'\n'}); err != nil {
			return fmt.Errorf("write newline: %w", err)
		}
	}
	return s.file.Sync()
}

// load replays the file into the in-memory mirror. Tolerates a
// truncated final line (treats it as if the crashed Append never
// happened).
func (s *JSONLStore) load() error {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no file yet, mirror stays empty
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
			s.mem.AppendRaw(er.SessionID, floor.StoredEvent{
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
			s.mem.AddRef(rr.SessionID, rr.AgentID, rr.EventSeq)
		case "clear":
			var cr clearRecord
			if err := json.Unmarshal(line, &cr); err != nil {
				break
			}
			_ = s.mem.Clear(cr.SessionID, cr.Filter)
		case "meta":
			var mr metaRecord
			if err := json.Unmarshal(line, &mr); err != nil {
				break
			}
			_ = s.mem.SetMeta(mr.SessionID, mr.Meta)
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
