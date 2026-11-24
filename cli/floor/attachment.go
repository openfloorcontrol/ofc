package floor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
)

// Attachment is an image a message carries. The bytes live in the
// session's blob store under ID, the sha256 of the bytes; the message
// holds only this description.
type Attachment struct {
	ID   string `json:"id"`
	MIME string `json:"mime"`
	Name string `json:"name,omitempty"` // the uploaded file's name, for display
	Size int    `json:"size"`
}

// MaxAttachmentSize is the largest image a session accepts.
const MaxAttachmentSize = 10 << 20

// ErrBlobNotFound is returned by GetBlob for an ID the session doesn't hold.
var ErrBlobNotFound = errors.New("blob not found")

// attachableMIME lists the image types a session accepts: the ones vision
// models take, as http.DetectContentType names them.
var attachableMIME = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// BlobID is the ID of data in a blob store: its sha256, hex encoded.
func BlobID(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ValidBlobID reports whether id has the form of a BlobID. Stores use it
// before turning an ID into a file name or query.
func ValidBlobID(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// DescribeImage checks that data is an image a session accepts and
// describes it. The type comes from the bytes, never from the file name.
func DescribeImage(name string, data []byte) (Attachment, error) {
	if len(data) == 0 {
		return Attachment{}, fmt.Errorf("%s: empty file", name)
	}
	if len(data) > MaxAttachmentSize {
		return Attachment{}, fmt.Errorf("%s: %d bytes, the limit is %d", name, len(data), MaxAttachmentSize)
	}
	mime := http.DetectContentType(data)
	if !attachableMIME[mime] {
		return Attachment{}, fmt.Errorf("%s: %s is not a supported image type (png, jpeg, gif, webp)", name, mime)
	}
	return Attachment{ID: BlobID(data), MIME: mime, Name: name, Size: len(data)}, nil
}

// AttachImage stores an image in the session and returns its Attachment,
// ready to go into a UserInput.
func (s *Session) AttachImage(name string, data []byte) (Attachment, error) {
	att, err := DescribeImage(name, data)
	if err != nil {
		return Attachment{}, err
	}
	if _, err := s.Store().PutBlob(s.ID(), data); err != nil {
		return Attachment{}, fmt.Errorf("store %s: %w", name, err)
	}
	return att, nil
}

// LookupAttachment describes the image the session holds under id, with
// name as its display name. Used where a client refers to an image it
// uploaded before.
func (s *Session) LookupAttachment(id, name string) (Attachment, error) {
	data, err := s.Store().GetBlob(s.ID(), id)
	if err != nil {
		return Attachment{}, fmt.Errorf("attachment %s: %w", id, err)
	}
	return DescribeImage(name, data)
}

// AttachmentData returns the bytes of an attachment in the session.
func (s *Session) AttachmentData(id string) ([]byte, error) {
	return s.Store().GetBlob(s.ID(), id)
}
