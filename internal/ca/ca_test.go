package ca

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	r1, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := r1.Certificate()
	if !c.IsCA || c.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatalf("not a signing CA: IsCA=%v usage=%v", c.IsCA, c.KeyUsage)
	}
	if c.Subject.Organization[0] != "ProxyPilot" {
		t.Fatalf("unexpected O: %v", c.Subject.Organization)
	}

	st, err := os.Stat(r1.KeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %#o, want 0600", st.Mode().Perm())
	}

	r2, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Fingerprint() != r2.Fingerprint() {
		t.Fatal("second load returned a different CA")
	}
}

func TestRegenerateChangesFingerprint(t *testing.T) {
	r, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before, gen := r.Fingerprint(), r.Generation()
	if err := r.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if r.Fingerprint() == before {
		t.Fatal("fingerprint unchanged after regenerate")
	}
	if r.Generation() <= gen {
		t.Fatal("generation not bumped")
	}
	reloaded, err := LoadOrCreate(r.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Fingerprint() != r.Fingerprint() {
		t.Fatal("regenerated CA not persisted")
	}
}

func TestExports(t *testing.T) {
	r, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x509.ParseCertificate(r.ExportDER()); err != nil {
		t.Fatalf("DER export invalid: %v", err)
	}
	if len(r.ExportPEM()) == 0 {
		t.Fatal("empty PEM")
	}
}
