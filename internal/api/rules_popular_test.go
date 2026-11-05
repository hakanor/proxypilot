package api

import "testing"

func TestPatternHostPath(t *testing.T) {
	for in, want := range map[string][2]string{
		"https://api.x.com/users/*": {"api.x.com", "/users"},
		"api.x.com/cards":           {"api.x.com", "/cards"},
		"https://API.x.com:8443/*":  {"API.x.com", ""},
		"https://*.x.com/a":         {"", ""},
		"":                          {"", ""},
	} {
		if h, p := patternHostPath(in); h != want[0] || p != want[1] {
			t.Errorf("%q: got %q %q", in, h, p)
		}
	}
}
