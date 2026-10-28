package rules

import "testing"

func TestJSONGet(t *testing.T) {
	doc := map[string]any{"items": []any{map[string]any{"p": 1.0}, map[string]any{"p": 2.0}}}
	for path, want := range map[string]any{"$.items[0].p": 1.0, "$.items[-1].p": 2.0} {
		if got, err := JSONGet(doc, path); err != nil || got != want {
			t.Fatalf("%s = %v, %v", path, got, err)
		}
	}
	for _, bad := range []string{"$.nope", "$.items[5]", "$.items.p", "x"} {
		if _, err := JSONGet(doc, bad); err == nil {
			t.Fatalf("%s: no error", bad)
		}
	}
}
