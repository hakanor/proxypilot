// Package ca manages the ProxyPilot root certificate authority and the
// per-host leaf certificates used for HTTPS interception.
package ca

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	CertFile = "ca.pem"
	KeyFile  = "ca-key.pem"

	rootValidity = 10 * 365 * 24 * time.Hour
)

// RootCA is the signing authority. It is safe for concurrent use; Regenerate
// swaps the key pair atomically and bumps Generation so caches can drop leaves.
type RootCA struct {
	dir string

	mu   sync.RWMutex
	cert *x509.Certificate
	key  *rsa.PrivateKey
	gen  uint64
}

// LoadOrCreate loads the CA from dir, creating it on first run.
// dir is created with 0700; loose permissions are logged as warnings.
func LoadOrCreate(dir string) (*RootCA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	r := &RootCA{dir: dir}
	err := r.load()
	if errors.Is(err, fs.ErrNotExist) {
		if err := r.Regenerate(); err != nil {
			return nil, err
		}
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	r.warnPermissions()
	return r, nil
}

func (r *RootCA) Dir() string      { return r.dir }
func (r *RootCA) CertPath() string { return filepath.Join(r.dir, CertFile) }
func (r *RootCA) KeyPath() string  { return filepath.Join(r.dir, KeyFile) }

// Current returns the signing pair and its generation.
func (r *RootCA) Current() (*x509.Certificate, *rsa.PrivateKey, uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cert, r.key, r.gen
}

func (r *RootCA) Certificate() *x509.Certificate {
	c, _, _ := r.Current()
	return c
}

// Generation increases every time the CA is regenerated.
func (r *RootCA) Generation() uint64 {
	_, _, g := r.Current()
	return g
}

func (r *RootCA) load() error {
	certPEM, err := os.ReadFile(r.CertPath())
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(r.KeyPath())
	if err != nil {
		return err
	}
	cb, _ := pem.Decode(certPEM)
	if cb == nil || cb.Type != "CERTIFICATE" {
		return fmt.Errorf("%s: no CERTIFICATE block", r.CertPath())
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return fmt.Errorf("%s: %w", r.CertPath(), err)
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return fmt.Errorf("%s: no key block", r.KeyPath())
	}
	key, err := parseRSAKey(kb.Bytes)
	if err != nil {
		return fmt.Errorf("%s: %w", r.KeyPath(), err)
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return fmt.Errorf("%s does not match %s", r.KeyPath(), r.CertPath())
	}
	r.mu.Lock()
	r.cert, r.key = cert, key
	r.gen++
	r.mu.Unlock()
	return nil
}

func parseRSAKey(der []byte) (*rsa.PrivateKey, error) {
	if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("CA key is not RSA")
	}
	return rk, nil
}

// Regenerate creates a brand-new CA and overwrites the files on disk.
// Every device that trusted the old CA must install the new one.
func (r *RootCA) Regenerate() error {
	// RSA 2048 for compatibility with older Android versions.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	serial, err := randSerial()
	if err != nil {
		return err
	}
	ski, err := subjectKeyID(&key.PublicKey)
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName(), Organization: []string{"ProxyPilot"}},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(rootValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		SubjectKeyId:          ski,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(r.KeyPath(), keyPEM, 0o600); err != nil {
		return err
	}
	if err := writeFileAtomic(r.CertPath(), certPEM, 0o644); err != nil {
		return err
	}

	r.mu.Lock()
	r.cert, r.key = cert, key
	r.gen++
	r.mu.Unlock()
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil { // WriteFile honors umask
		return err
	}
	return os.Rename(tmp, path)
}

func (r *RootCA) warnPermissions() {
	check := func(path string, max os.FileMode) {
		st, err := os.Stat(path)
		if err != nil {
			return
		}
		if st.Mode().Perm()&^max != 0 {
			slog.Warn("permissions too open", "path", path, "mode", fmt.Sprintf("%#o", st.Mode().Perm()), "want", fmt.Sprintf("%#o", max))
		}
	}
	check(r.dir, 0o700)
	check(r.KeyPath(), 0o600)
}

// ExportPEM returns the CA certificate (never the key) in PEM form.
func (r *RootCA) ExportPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.Certificate().Raw})
}

// ExportDER returns the CA certificate in DER form (Android .crt).
func (r *RootCA) ExportDER() []byte {
	return append([]byte(nil), r.Certificate().Raw...)
}

// Fingerprint is the SHA-256 of the certificate as colon-separated hex.
func (r *RootCA) Fingerprint() string {
	sum := sha256.Sum256(r.Certificate().Raw)
	return colonHex(sum[:])
}

// FingerprintSHA1 is used by macOS `security delete-certificate -Z`.
func (r *RootCA) FingerprintSHA1() string {
	sum := sha1.Sum(r.Certificate().Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func colonHex(b []byte) string {
	s := strings.ToUpper(hex.EncodeToString(b))
	var sb strings.Builder
	for i := 0; i < len(s); i += 2 {
		if i > 0 {
			sb.WriteByte(':')
		}
		sb.WriteString(s[i : i+2])
	}
	return sb.String()
}

func commonName() string {
	name := "user"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	host = strings.TrimSuffix(host, ".local")
	return fmt.Sprintf("ProxyPilot CA (%s@%s)", name, host)
}

func randSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

func subjectKeyID(pub any) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum(der)
	return sum[:], nil
}

// SHA256 is the raw SHA-256 of the certificate DER (trust store key on iOS simulators).
func (r *RootCA) SHA256() []byte {
	s := sha256.Sum256(r.ExportDER())
	return s[:]
}
