package simctl

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hakanor/proxypilot/internal/devices/execx"
)

type fakeRun struct {
	out  string
	err  error
	last []string
}

func (f *fakeRun) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.last = append([]string{name}, args...)
	return []byte(f.out), f.err
}

func TestListInstallRemove(t *testing.T) {
	dir := t.TempDir()
	booted := filepath.Join(dir, "A")
	store := trustStore(booted)
	os.MkdirAll(filepath.Dir(store), 0o755)
	db, _ := sql.Open("sqlite", "file:"+store)
	db.Exec(`CREATE TABLE tsettings(sha256 BLOB NOT NULL DEFAULT '',subj BLOB NOT NULL DEFAULT '',tset BLOB,data BLOB,uuid BLOB NOT NULL DEFAULT '',UNIQUE(sha256,uuid))`)
	ours := []byte{1, 2, 3}
	db.Exec(`INSERT INTO tsettings(sha256) VALUES (?), (?)`, ours, []byte{9, 9})
	db.Close()

	f := &fakeRun{out: `{"devices":{
	  "com.apple.CoreSimulator.SimRuntime.iOS-26-3":[
	    {"udid":"A","name":"iPhone 17 Pro","state":"Booted","isAvailable":true,"dataPath":"` + booted + `"},
	    {"udid":"B","name":"iPhone 16e","state":"Shutdown","isAvailable":true,"dataPath":"` + filepath.Join(dir, "B") + `"},
	    {"udid":"C","name":"Broken","state":"Shutdown","isAvailable":false}]}}`}
	s := &Simctl{Run: f, CASHA256: ours}
	list, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].UDID != "A" || list[0].Runtime != "iOS 26.3" || list[0].CA != CAInstalled || list[1].CA != CAUnknown {
		t.Fatalf("list: %+v", list)
	}

	if err := s.InstallCA(context.Background(), "A", "/ca.pem"); err != nil || strings.Join(f.last, " ") != "xcrun simctl keychain A add-root-cert /ca.pem" {
		t.Fatalf("install: %v %v", err, f.last)
	}

	removed, err := s.RemoveCA(context.Background(), list[0])
	if err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if st := s.caState(list[0]); st != CAMissing {
		t.Fatalf("after remove: %s", st)
	}
	db, _ = sql.Open("sqlite", "file:"+store)
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM tsettings`).Scan(&n)
	db.Close()
	if n != 1 {
		t.Fatalf("other certificates must stay, have %d rows", n)
	}
}

func TestNoXcode(t *testing.T) {
	s := &Simctl{Run: &fakeRun{err: &execx.Error{Cmd: "xcrun simctl", Output: "xcrun: error: unable to find utility \"simctl\""}}}
	if _, err := s.List(context.Background()); !errors.Is(err, ErrNoXcode) {
		t.Fatalf("got %v", err)
	}
}
