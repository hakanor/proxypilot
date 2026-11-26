package proxy

import "testing"

func TestSSLPolicy(t *testing.T) {
	deny := []string{"*.apple.com", "bank.example.com:8443"}
	cases := []struct {
		allow []string
		host  string
		want  bool
	}{
		{nil, "api.example.com:443", false}, // nothing is decrypted until it is allowed
		{[]string{"*.invio.com"}, "api.invio.com:443", true},
		{[]string{"*.invio.com"}, "invio.com:443", false}, // *. is subdomains only
		{[]string{"*.invio.com"}, "api.example.com:443", false},
		{[]string{"api.example.com"}, "API.Example.COM.:443", true},
		{[]string{"bank.example.com"}, "bank.example.com:443", true},
		{[]string{"bank.example.com"}, "bank.example.com:8443", false}, // deny wins
		{[]string{"*"}, "x.apple.com:443", false},                      // deny wins
	}
	for _, c := range cases {
		p := NewSSLPolicy(c.allow, deny)
		if got := p.ShouldIntercept(c.host); got != c.want {
			t.Errorf("allow=%v host=%s: got %v want %v", c.allow, c.host, got, c.want)
		}
	}
}

func TestSSLPolicyLearnedAndDeny(t *testing.T) {
	p := NewSSLPolicy([]string{"*"}, nil)
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

func TestSSLPolicyFavorites(t *testing.T) {
	p := NewSSLPolicy([]string{"*.listed.com"}, []string{"denied.com"})
	p.SetFavoriteHosts([]string{"Popular.com", "denied.com"})
	if p.ShouldIntercept("popular.com:443") {
		t.Fatal("favorites are off: popular.com must tunnel")
	}
	p.SetFavorites(true)
	for host, want := range map[string]bool{"popular.com:443": true, "a.listed.com:443": true, "denied.com:443": false, "other.com:443": false} {
		if got := p.ShouldIntercept(host); got != want {
			t.Errorf("%s: got %v, want %v", host, got, want)
		}
	}
}

func TestSSLPolicyTunnelReason(t *testing.T) {
	p := NewSSLPolicy([]string{"*.example.com"}, []string{"pinned.example.com"})
	p.LearnTunnel("mtls.example.com:443", "client certificate required")
	for host, want := range map[string]string{
		"api.example.com:443":    "",
		"other.com:443":          TunnelNotAllowed,
		"pinned.example.com:443": TunnelDenylist,
		"mtls.example.com:443":   TunnelLearned,
	} {
		if got := p.TunnelReason(host); got != want {
			t.Errorf("%s: got %q, want %q", host, got, want)
		}
	}
}
