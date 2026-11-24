package sessionstore

import (
	"bytes"
	"errors"
	"testing"

	"github.com/openfloorcontrol/ofc/floor"
)

// testBlobs exercises PutBlob / GetBlob against any SessionStore.
func testBlobs(t *testing.T, s floor.SessionStore) {
	t.Helper()
	data := []byte("\x89PNG not really")

	id, err := s.PutBlob("s", data)
	if err != nil {
		t.Fatalf("PutBlob: %v", err)
	}
	if id != floor.BlobID(data) {
		t.Errorf("id = %s, want the sha256 of the data", id)
	}
	if again, err := s.PutBlob("s", data); err != nil || again != id {
		t.Errorf("PutBlob again = %s, %v", again, err)
	}
	got, err := s.GetBlob("s", id)
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("GetBlob = %q, %v", got, err)
	}

	// Blobs belong to their session.
	if _, err := s.GetBlob("other", id); !errors.Is(err, floor.ErrBlobNotFound) {
		t.Errorf("other session: err = %v, want ErrBlobNotFound", err)
	}
	if _, err := s.GetBlob("s", floor.BlobID([]byte("x"))); !errors.Is(err, floor.ErrBlobNotFound) {
		t.Errorf("unknown id: err = %v, want ErrBlobNotFound", err)
	}

	// Delete removes them with the session.
	if err := s.Delete("s"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetBlob("s", id); !errors.Is(err, floor.ErrBlobNotFound) {
		t.Errorf("after Delete: err = %v, want ErrBlobNotFound", err)
	}
}

func TestMemoryStoreBlobs(t *testing.T) {
	testBlobs(t, floor.NewMemoryStore())
}

func TestJSONLStoreBlobs(t *testing.T) {
	s, _ := NewJSONL(t.TempDir())
	defer s.Close()
	testBlobs(t, s)
}

func TestPostgresBlobs(t *testing.T) {
	s, cleanup := postgresTestStore(t)
	defer cleanup()
	testBlobs(t, s)
}

func TestJSONLStoreBlobsSurviveReload(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewJSONL(dir)
	data := []byte("image bytes")
	id, err := s.PutBlob("s", data)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, _ := NewJSONL(dir)
	defer s2.Close()
	got, err := s2.GetBlob("s", id)
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("after reload: %q, %v", got, err)
	}
	// The blob directory is not taken for a session.
	infos, err := s2.List()
	if err != nil || len(infos) != 0 {
		t.Errorf("List = %+v, %v; want no sessions", infos, err)
	}
	if _, err := s2.GetBlob("s", "../../etc/passwd"); !errors.Is(err, floor.ErrBlobNotFound) {
		t.Errorf("path-like id: err = %v", err)
	}
}
