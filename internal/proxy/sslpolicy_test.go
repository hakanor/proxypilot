package proxy

import "testing"

func TestSSLPolicy(t *testing.T) {
	deny := []string{"*.apple.com", "bank.example.com:8443"}
	cases := []struct {
		mode  SSLMode
		allow []string
		host  string
		want  bool
	}{
		{ModeAll, nil, "api.example.com:443", true},
		{ModeAll, nil, "gs.apple.com:443", false},
		{ModeAll, nil, "apple.com:443", true}, // *.apple.com is subdomains only
		{ModeAll, nil, "bank.example.com:8443", false},
		{ModeAll, nil, "bank.example.com:443", true},
		{ModeOff, nil, "api.example.com:443", false},
		{ModeAllowlist, []string{"*.invio.com"}, "api.invio.com:443", true},
		{ModeAllowlist, []string{"*.invio.com"}, "api.example.com:443", false},
		{ModeAllowlist, []string{"*"}, "x.apple.com:443", false}, // deny wins
		{ModeAll, nil, "API.Example.COM.:443", true},
	}
	for _, c := range cases {
		p := NewSSLPolicy(c.mode, c.allow, deny)
		if got := p.ShouldIntercept(c.host); got != c.want {
			t.Errorf("mode=%s allow=%v host=%s: got %v want %v", c.mode, c.allow, c.host, got, c.want)
		}
	}
}

func TestSSLPolicyLearnedAndDeny(t *testing.T) {
	p := NewSSLPolicy(ModeAll, nil, nil)
	p.LearnTunnel("mtls.example.com:443", "client certificate required")
	if p.ShouldIntercept("mtls.example.com:443") {
		t.Fatal("learned tunnel host still intercepted")
	}
	p.AddDeny("pinning.bank.com")
	p.AddDeny("pinning.bank.com")
	if p.ShouldIntercept("pinning.bank.com:443") {
		t.Fatal("added deny not applied")
	}
	if n := len(p.Snapshot().Denylist); n != 1 {
		t.Fatalf("duplicate deny entries: %d", n)
	}
}
