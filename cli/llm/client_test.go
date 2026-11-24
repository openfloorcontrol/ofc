package llm

import (
	"encoding/json"
	"testing"
)

// A message without images keeps content a string; with images, content
// becomes a list of parts: the text, then each image as a data URL.
func TestMessageContentParts(t *testing.T) {
	plain, err := json.Marshal(Message{Role: "user", Content: ""})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(plain), `{"role":"user","content":""}`; got != want {
		t.Errorf("plain = %s, want %s", got, want)
	}

	withImage, err := json.Marshal(Message{Role: "user", Content: "look", Name: "user",
		Images: []Image{{MIME: "image/png", Data: []byte("abc")}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"role":"user","name":"user","content":[` +
		`{"type":"text","text":"look"},` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,YWJj"}}]}`
	if string(withImage) != want {
		t.Errorf("with image =\n%s\nwant\n%s", withImage, want)
	}
}
