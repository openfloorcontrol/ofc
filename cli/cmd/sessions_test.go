package cmd

import (
	"strings"
	"testing"
)

func TestMatchSessionID(t *testing.T) {
	ids := []string{
		"b9f012c2-f410-4d35-9061-a2696d655566",
		"b9f0aaaa-0000-4000-8000-000000000000",
		"68346b26-7f01-4aa9-82c7-682bea7388b5",
	}
	for _, c := range []struct {
		prefix, want, err string
	}{
		{"68346b26", ids[2], ""},
		{ids[0], ids[0], ""},
		{"b9f012", ids[0], ""},
		{"b9f0", "", "ambiguous"},
		{"ffff", "", "not found"},
		{"", "", "empty"},
	} {
		got, err := matchSessionID(ids, c.prefix)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err %v, want one containing %q", c.prefix, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: got %q, %v; want %q", c.prefix, got, err, c.want)
		}
	}
}
