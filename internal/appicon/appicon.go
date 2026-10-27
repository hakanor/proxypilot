// Package appicon turns the .app bundles seen behind proxied connections into
// small PNG icons for the sidebar (macOS only: uses plutil and sips).
package appicon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Size is the edge of the PNGs we render (shown at 16–20px, so 2× is plenty).
const Size = 64

var ErrNoIcon = errors.New("no icon for this app")

// Store remembers which bundle each app name came from and caches rendered icons
// under dir, so icons survive restarts even before the app is seen again.
type Store struct {
	dir     string
	mu      sync.Mutex
	bundles map[string]string // app name → .app path
	failed  map[string]bool
}

func New(dir string) *Store {
	return &Store{dir: dir, bundles: map[string]string{}, failed: map[string]bool{}}
}

// Remember records the bundle an app runs from ("" bundles are ignored).
func (s *Store) Remember(app, bundle string) {
	if app == "" || bundle == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bundles[app] != bundle {
		s.bundles[app] = bundle
		delete(s.failed, app)
	}
}

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (s *Store) cachePath(app string) string {
	return filepath.Join(s.dir, unsafe.ReplaceAllString(app, "_")+".png")
}

// PNG returns the app's icon, rendering and caching it on first use.
func (s *Store) PNG(app string) ([]byte, error) {
	if b, err := os.ReadFile(s.cachePath(app)); err == nil {
		return b, nil
	}
	s.mu.Lock()
	bundle, failed := s.bundles[app], s.failed[app]
	s.mu.Unlock()
	if bundle == "" || failed || runtime.GOOS != "darwin" {
		return nil, ErrNoIcon
	}
	src := IconFile(bundle)
	if src == "" {
		s.markFailed(app)
		return nil, ErrNoIcon
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	dst := s.cachePath(app)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "sips", "-s", "format", "png", "-Z", "64", src, "--out", dst).Run(); err != nil {
		s.markFailed(app)
		return nil, ErrNoIcon
	}
	return os.ReadFile(dst)
}

func (s *Store) markFailed(app string) {
	s.mu.Lock()
	s.failed[app] = true
	s.mu.Unlock()
}

// IconFile finds the best icon image inside a bundle: the .icns named in a Mac
// app's Info.plist, or the largest AppIcon*.png of an iOS (simulator) app.
func IconFile(bundle string) string {
	if res := filepath.Join(bundle, "Contents", "Resources"); isDir(res) {
		name := plistString(filepath.Join(bundle, "Contents", "Info.plist"), "CFBundleIconFile")
		if name != "" {
			if filepath.Ext(name) == "" {
				name += ".icns"
			}
			if p := filepath.Join(res, name); isFile(p) {
				return p
			}
		}
		if m, _ := filepath.Glob(filepath.Join(res, "*.icns")); len(m) > 0 {
			return m[0]
		}
		return ""
	}
	// iOS bundle: icons sit at the root, e.g. AppIcon60x60@2x.png.
	m, _ := filepath.Glob(filepath.Join(bundle, "AppIcon*.png"))
	if len(m) == 0 {
		return ""
	}
	sort.Slice(m, func(i, j int) bool { return size(m[i]) > size(m[j]) })
	return m[0]
}

func plistString(plist, key string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "plutil", "-extract", key, "raw", "-o", "-", plist).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func isDir(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }
func isFile(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }
func size(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}
