package appicon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIconFileIOSPicksLargest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Starlight.app")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "AppIcon60x60@2x.png"), make([]byte, 10), 0o644)
	os.WriteFile(filepath.Join(dir, "AppIcon60x60@3x.png"), make([]byte, 30), 0o644)
	if got := IconFile(dir); filepath.Base(got) != "AppIcon60x60@3x.png" {
		t.Fatalf("got %q", got)
	}
}

func TestPNGUnknownApp(t *testing.T) {
	if _, err := New(t.TempDir()).PNG("nope"); err != ErrNoIcon {
		t.Fatalf("err = %v", err)
	}
}
