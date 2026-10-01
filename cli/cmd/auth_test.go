package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForwardPastedCallbacks(t *testing.T) {
	var got []string
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.RawQuery)
		fmt.Fprintln(w, "received")
	}))
	defer callback.Close()

	in := strings.Join([]string{
		"not a url at all",
		callback.URL + "/callback?code=abc&scope=read+write&state=xyz",
	}, "\n")
	var out strings.Builder
	forwardPastedCallbacks(strings.NewReader(in), &out)

	if len(got) != 1 || got[0] != "code=abc&scope=read+write&state=xyz" {
		t.Errorf("delivered = %v, want the pasted callback once, query intact", got)
	}
	if !strings.Contains(out.String(), "not a callback address") || !strings.Contains(out.String(), "received") {
		t.Errorf("output = %q", out.String())
	}
}
