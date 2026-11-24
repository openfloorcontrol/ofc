package agents_test

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strconv"
	"strings"
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/llm/llmtest"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// attachTurn posts "look" with a PNG and runs one turn of the LLM agent.
func attachTurn(t *testing.T, vision bool) (*harness, []byte) {
	t.Helper()
	h := newHarness(t, func(a *blueprint.Agent) { a.Vision = vision }, llmtest.Response{Content: "A dot."})
	data := testPNG(t)
	att, err := h.sess.AttachImage("dot.png", data)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.sess.MainRoom.PostUserInput(floor.UserInput{Text: "look", Attachments: []floor.Attachment{att}}); err != nil {
		t.Fatal(err)
	}
	h.turn(t, "and?")
	return h, data
}

// A vision model gets the image after the message text, as a data URL.
func TestLLMAgentSeesImage(t *testing.T) {
	h, data := attachTurn(t, true)
	users := byRole(h.mock.Requests()[0].Messages, "user")
	if len(users) != 2 {
		t.Fatalf("user messages = %+v", users)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	if users[0].Content != "@user: look" || len(users[0].Images) != 1 || users[0].Images[0] != want {
		t.Errorf("message with image = %q + %d images", users[0].Content, len(users[0].Images))
	}
	if len(users[1].Images) != 0 {
		t.Errorf("message without image got %d images", len(users[1].Images))
	}
}

// A model without vision gets a note in place of the image.
func TestLLMAgentWithoutVisionGetsNote(t *testing.T) {
	h, _ := attachTurn(t, false)
	first := byRole(h.mock.Requests()[0].Messages, "user")[0]
	if len(first.Images) != 0 {
		t.Errorf("got %d images, want none", len(first.Images))
	}
	if !strings.Contains(first.Content, "[image dot.png (image/png) attached — not shown") {
		t.Errorf("content = %q, want a note about the image", first.Content)
	}
}

// An ACP agent that takes images gets an image block after the text.
func TestACPAgentSeesImage(t *testing.T) {
	f := startACPFloor(t)
	sess := f.DefaultSession()
	events := sess.Subscribe()
	sess.Start()

	data := testPNG(t)
	att, err := sess.AttachImage("dot.png", data)
	if err != nil {
		t.Fatal(err)
	}
	reply := askInput(t, sess, events, floor.UserInput{Text: "look", Attachments: []floor.Attachment{att}})
	if want := "@user: look / <image image/png " + strconv.Itoa(len(data)) + " bytes>"; !strings.Contains(reply, want) {
		t.Errorf("reply = %q, want it to contain %q", reply, want)
	}
}

// An ACP agent that doesn't take images gets a note.
func TestACPAgentWithoutImagesGetsNote(t *testing.T) {
	f := newACPFloorEnv(t, floor.NewMemoryStore(), map[string]string{"FAKE_ACP_NO_IMAGES": "1"}, 0)
	t.Cleanup(f.Stop)
	sess := f.DefaultSession()
	events := sess.Subscribe()
	sess.Start()

	att, err := sess.AttachImage("dot.png", testPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	reply := askInput(t, sess, events, floor.UserInput{Text: "look", Attachments: []floor.Attachment{att}})
	if strings.Contains(reply, "<image") || !strings.Contains(reply, "[image dot.png (image/png) attached — not shown") {
		t.Errorf("reply = %q, want a note instead of the image", reply)
	}
}
