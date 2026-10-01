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
		strings.Replace(callback.URL, "http://", "https://", 1) + "/callback?code=def&state=xyz", // browser upgrade
	}, "\n")
	var out strings.Builder
	forwardPastedCallbacks(strings.NewReader(in), &out)

	want := []string{"code=abc&scope=read+write&state=xyz", "code=def&state=xyz"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("delivered = %v, want %v (query intact, https sent as http)", got, want)
	}
	if !strings.Contains(out.String(), "not a callback address") || !strings.Contains(out.String(), "received") {
		t.Errorf("output = %q", out.String())
	}
}
