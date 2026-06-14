package sessionstore

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/openfloorcontrol/ofc/floor"
)

// postgresTestStore opens a Postgres store against OFC_TEST_DATABASE_URL,
// inside a freshly-created schema so tests don't collide with each other
// or with prior runs. Returns the store and a cleanup that drops the
// schema. Skips the test when no DSN is configured.
//
// Local dev: docker run --rm -p 5432:5432 -e POSTGRES_PASSWORD=ofc -e POSTGRES_DB=ofc postgres:16
//            OFC_TEST_DATABASE_URL=postgres://postgres:ofc@localhost:5432/ofc?sslmode=disable go test ./floor/sessionstore/ -run Postgres
func postgresTestStore(t *testing.T) (*PostgresStore, func()) {
	t.Helper()
	dsn := os.Getenv("OFC_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("OFC_TEST_DATABASE_URL not set — skipping Postgres tests")
	}

	ctx := context.Background()

	// Connect to a tmp schema isolated per test. We do this by opening a
	// store, creating + setting the schema search_path, and re-running
	// migrations into it.
	schema := fmt.Sprintf("ofctest_%s", randSchemaSuffix())

	bare, err := OpenPostgres(ctx, dsn)
	if err != nil {
		t.Fatalf("OpenPostgres (bootstrap): %v", err)
	}
	if _, err := bare.db.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		bare.Close()
		t.Fatalf("create schema: %v", err)
	}
	bare.Close()

	// Reopen with search_path pinned to the new schema, then migrate
	// into that schema. The simplest way to pin search_path through
	// pgx's database/sql adapter is to append `&search_path=<schema>` to
	// the DSN — pgx parses the search_path query param at connect time.
	sep := "?"
	if hasQuery(dsn) {
		sep = "&"
	}
	scopedDSN := dsn + sep + "search_path=" + schema

	store, err := OpenPostgres(ctx, scopedDSN)
	if err != nil {
		// Clean up the schema we just created.
		cleanup, _ := OpenPostgres(ctx, dsn)
		if cleanup != nil {
			cleanup.db.ExecContext(ctx, fmt.Sprintf(`DROP SCHEMA %s CASCADE`, schema))
			cleanup.Close()
		}
		t.Fatalf("OpenPostgres (scoped): %v", err)
	}

	cleanupFn := func() {
		store.Close()
		drop, err := OpenPostgres(ctx, dsn)
		if err != nil {
			t.Logf("postgres cleanup: %v", err)
			return
		}
		drop.db.ExecContext(ctx, fmt.Sprintf(`DROP SCHEMA %s CASCADE`, schema))
		drop.Close()
	}
	return store, cleanupFn
}

func hasQuery(dsn string) bool {
	for i := 0; i < len(dsn); i++ {
		if dsn[i] == '?' {
			return true
		}
	}
	return false
}

// randSchemaSuffix returns 8 lowercase hex chars from crypto/rand-ish
// entropy via os.Getpid + monotonic counter. We can't use math/rand
// without a seed and don't want to import crypto/rand for one suffix;
// pid + a package-level counter is unique enough across one test run.
var schemaCounter int

func randSchemaSuffix() string {
	schemaCounter++
	return fmt.Sprintf("%d_%d", os.Getpid(), schemaCounter)
}

func TestPostgresAppendAndRead(t *testing.T) {
	s, cleanup := postgresTestStore(t)
	defer cleanup()

	_, err := s.Append(floor.AppendOpts{
		SessionID: "sid",
		RoomID:    "#main",
		Event:     newMemMsg("hi"),
		VisibleTo: []string{"@a", "@b"},
	})
	if err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	_, err = s.Append(floor.AppendOpts{
		SessionID: "sid",
		RoomID:    "#main",
		Event:     newMemMsg("psst"),
		VisibleTo: []string{"@a"},
		Private:   true,
	})
	if err != nil {
		t.Fatalf("Append 2: %v", err)
	}

	pub, err := s.Read("sid", floor.EventFilter{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(pub) != 1 || extractMessages(pub)[0].Content != "hi" {
		t.Errorf("Read public: want [hi], got %+v", extractMessages(pub))
	}

	aEvents, err := s.ReadForAgent("sid", "@a", floor.EventFilter{})
	if err != nil {
		t.Fatalf("ReadForAgent @a: %v", err)
	}
	if len(aEvents) != 2 {
		t.Errorf("@a: expected 2 events, got %d", len(aEvents))
	}

	bEvents, _ := s.ReadForAgent("sid", "@b", floor.EventFilter{})
	if len(bEvents) != 1 {
		t.Errorf("@b: expected 1 event (psst was VisibleTo=[@a]), got %d", len(bEvents))
	}
}

func TestPostgresSeqMonotonic(t *testing.T) {
	s, cleanup := postgresTestStore(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		stored, err := s.Append(floor.AppendOpts{
			SessionID: "sid",
			Event:     newMemMsg(fmt.Sprintf("msg %d", i)),
		})
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		want := uint64(i + 1)
		if stored.Seq != want {
			t.Errorf("Append %d: seq = %d, want %d", i, stored.Seq, want)
		}
	}
}

func TestPostgresMultipleSessions(t *testing.T) {
	s, cleanup := postgresTestStore(t)
	defer cleanup()

	s.Append(floor.AppendOpts{SessionID: "sid1", Event: newMemMsg("one")})
	s.Append(floor.AppendOpts{SessionID: "sid2", Event: newMemMsg("two")})
	s.Append(floor.AppendOpts{SessionID: "sid1", Event: newMemMsg("three")})

	r1, _ := s.Read("sid1", floor.EventFilter{})
	r2, _ := s.Read("sid2", floor.EventFilter{})
	if len(r1) != 2 {
		t.Errorf("sid1: expected 2 events, got %d", len(r1))
	}
	if len(r2) != 1 {
		t.Errorf("sid2: expected 1 event, got %d", len(r2))
	}
}

func TestPostgresClear(t *testing.T) {
	s, cleanup := postgresTestStore(t)
	defer cleanup()

	s.Append(floor.AppendOpts{
		SessionID: "sid", RoomID: "#main",
		Event: newMemMsg("a"), VisibleTo: []string{"@x"},
	})
	s.Append(floor.AppendOpts{
		SessionID: "sid", RoomID: "#other",
		Event: newMemMsg("b"), VisibleTo: []string{"@x"},
	})

	if err := s.Clear("sid", floor.EventFilter{RoomID: "#main"}); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	all, _ := s.Read("sid", floor.EventFilter{})
	if len(all) != 1 || extractMessages(all)[0].Content != "b" {
		t.Errorf("after clear: want [b], got %+v", extractMessages(all))
	}
	// Refs to the cleared event should be gone (cascade).
	xEvents, _ := s.ReadForAgent("sid", "@x", floor.EventFilter{})
	if len(xEvents) != 1 {
		t.Errorf("after clear: @x should see 1 event, got %d", len(xEvents))
	}
}

func TestPostgresMetaRoundTrip(t *testing.T) {
	s, cleanup := postgresTestStore(t)
	defer cleanup()

	if _, err := s.GetMeta("sid"); err != floor.ErrNoSessionMeta {
		t.Errorf("expected ErrNoSessionMeta, got %v", err)
	}

	meta := floor.SessionMeta{
		CWD:           "/tmp",
		BlueprintPath: "/tmp/bp.yaml",
		BlueprintName: "example",
		BlueprintHash: "deadbeef",
		OfcVersion:    "dev",
	}
	if err := s.SetMeta("sid", meta); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	got, err := s.GetMeta("sid")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.CWD != meta.CWD || got.BlueprintName != meta.BlueprintName {
		t.Errorf("meta round-trip mismatch: got %+v, want %+v", got, meta)
	}
}

func TestPostgresFilterByType(t *testing.T) {
	s, cleanup := postgresTestStore(t)
	defer cleanup()

	s.Append(floor.AppendOpts{SessionID: "sid", Event: newMemMsg("a")})
	s.Append(floor.AppendOpts{SessionID: "sid", Event: newMemMsg("b")})

	hits, _ := s.Read("sid", floor.EventFilter{Type: "message_posted"})
	if len(hits) != 2 {
		t.Errorf("filter by type: want 2, got %d", len(hits))
	}
	misses, _ := s.Read("sid", floor.EventFilter{Type: "no_such_type"})
	if len(misses) != 0 {
		t.Errorf("filter by bogus type: want 0, got %d", len(misses))
	}
}
