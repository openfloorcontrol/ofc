package floor_test

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDescribeImage(t *testing.T) {
	data := testPNG(t)
	att, err := floor.DescribeImage("dot.png", data)
	if err != nil {
		t.Fatal(err)
	}
	if att.MIME != "image/png" || att.Size != len(data) || att.ID != floor.BlobID(data) || att.Name != "dot.png" {
		t.Errorf("got %+v", att)
	}

	// The type comes from the bytes: a text file named .png is refused.
	if _, err := floor.DescribeImage("fake.png", []byte("hello")); err == nil || !strings.Contains(err.Error(), "not a supported image type") {
		t.Errorf("text as png: err = %v", err)
	}
	if _, err := floor.DescribeImage("empty.png", nil); err == nil {
		t.Error("empty file accepted")
	}
	big := append(append([]byte{}, data...), make([]byte, floor.MaxAttachmentSize)...)
	if _, err := floor.DescribeImage("big.png", big); err == nil {
		t.Error("oversized file accepted")
	}
}

// An image attached to user input is stored once and travels with the
// message; a command takes none.
func TestUserInputWithAttachment(t *testing.T) {
	f := floor.NewFloor(&blueprint.Blueprint{Name: "t"})
	sess := f.DefaultSession()
	go func() {
		for range sess.MainRoom.Events() {
		}
	}()

	data := testPNG(t)
	att, err := sess.AttachImage("dot.png", data)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.MainRoom.PostUserInput(floor.UserInput{Text: "look", Attachments: []floor.Attachment{att}}); err != nil {
		t.Fatal(err)
	}
	hist := sess.MainRoom.History()
	if len(hist) != 1 || len(hist[0].Attachments) != 1 || hist[0].Attachments[0] != att {
		t.Fatalf("history = %+v", hist)
	}
	got, err := sess.AttachmentData(att.ID)
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("AttachmentData = %d bytes, %v", len(got), err)
	}
	if _, err := sess.AttachmentData(floor.BlobID([]byte("other"))); !errors.Is(err, floor.ErrBlobNotFound) {
		t.Errorf("unknown blob: err = %v", err)
	}

	if err := sess.MainRoom.PostUserInput(floor.UserInput{Text: "/clear", Attachments: []floor.Attachment{att}}); err == nil {
		t.Error("command with attachment accepted")
	}
	if err := sess.MainRoom.PostUserInput(floor.UserInput{}); err == nil {
		t.Error("empty input accepted")
	}
	// Images alone are a message.
	if err := sess.MainRoom.PostUserInput(floor.UserInput{Attachments: []floor.Attachment{att}}); err != nil {
		t.Errorf("image-only input: %v", err)
	}
}
