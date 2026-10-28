package mcpserver

import "testing"

func TestShortIDs(t *testing.T) {
	var x ids
	a, b := "01JC8Z3K4M5N6P7Q8R9S0T1U2V", "01JC8Z3K4MZZZZZZZZ9S0T1U2V" // same last 8
	if s := x.short(a); s != "9S0T1U2V" {
		t.Fatalf("short %q", s)
	}
	if s := x.short(b); s != b {
		t.Fatalf("collision should keep the full id, got %q", s)
	}
	for in, want := range map[string]string{"9S0T1U2V": a, "9s0t1u2v": a, b: b} {
		if got, err := x.resolve(in); err != nil || got != want {
			t.Fatalf("resolve %q = %q, %v", in, got, err)
		}
	}
	if _, err := x.resolve("AAAAAAAA"); err == nil {
		t.Fatal("unknown short id accepted")
	}
}
