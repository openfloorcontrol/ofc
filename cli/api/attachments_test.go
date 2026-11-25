package api_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
)

// An image is uploaded, sent with a message by id, comes back in the
// history, and is served from the session.
func TestAttachmentUploadAndMessage(t *testing.T) {
	f := newTestFloor(&blueprint.Blueprint{Name: "test"}, nil)
	base := startTestAPI(t, f)

	resp, err := http.Post(base+"/api/v1/floors/test/sessions", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var created struct{ ID string }
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	sessURL := base + "/api/v1/floors/test/sessions/" + created.ID

	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	data := buf.Bytes()

	resp, err = http.Post(sessURL+"/attachments?name=dot.png", "image/png", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var att floor.Attachment
	json.NewDecoder(resp.Body).Decode(&att)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || att.ID != floor.BlobID(data) || att.MIME != "image/png" || att.Name != "dot.png" {
		t.Fatalf("upload: %s, %+v", resp.Status, att)
	}

	resp, err = http.Post(sessURL+"/attachments?name=notes.png", "image/png", strings.NewReader("just text"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("upload of text: %s, want 400", resp.Status)
	}

	post := func(body string) *http.Response {
		t.Helper()
		resp, err := http.Post(sessURL+"/messages", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if resp := post(`{"content": "look", "attachments": [{"id": "` + att.ID + `", "name": "dot.png"}]}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("post message: %s", resp.Status)
	}
	if resp := post(`{"content": "look", "attachments": [{"id": "` + floor.BlobID([]byte("x")) + `"}]}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown attachment: %s, want 400", resp.Status)
	}
	if resp := post(`{"from": "@bot", "content": "hi", "attachments": [{"id": "` + att.ID + `"}]}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("attachment from an agent: %s, want 400", resp.Status)
	}

	resp, err = http.Get(sessURL + "/messages")
	if err != nil {
		t.Fatal(err)
	}
	var hist struct {
		Messages []struct {
			Content     string             `json:"content"`
			Attachments []floor.Attachment `json:"attachments"`
		} `json:"messages"`
	}
	json.NewDecoder(resp.Body).Decode(&hist)
	resp.Body.Close()
	if len(hist.Messages) != 1 || len(hist.Messages[0].Attachments) != 1 || hist.Messages[0].Attachments[0] != att {
		t.Fatalf("history = %+v", hist.Messages)
	}

	resp, err = http.Get(sessURL + "/attachments/" + att.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || !bytes.Equal(got, data) {
		t.Errorf("GET attachment: %s, %s, %d bytes", resp.Status, resp.Header.Get("Content-Type"), len(got))
	}

	resp, err = http.Get(sessURL + "/attachments/" + floor.BlobID([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET unknown attachment: %s, want 404", resp.Status)
	}
}
