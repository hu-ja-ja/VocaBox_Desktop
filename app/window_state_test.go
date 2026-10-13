package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
)

func TestWindowStateStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := windowStateStore{mylist: &Mylist{configDir: dir}}
	if states, err := store.LoadWindowStates(); err != nil || states != nil {
		t.Fatalf("missing file should load no states: states=%v err=%v", states, err)
	}
	want := map[string]mygo.WindowState{
		"main": {X: 120, Y: 80, Width: 1120, Height: 760, Maximized: true},
	}
	if err := store.SaveWindowStates(want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "window-state.json")); err != nil {
		t.Fatalf("state file was not written beside settings: %v", err)
	}
	got, err := windowStateStore{mylist: &Mylist{configDir: dir}}.LoadWindowStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["main"] != want["main"] {
		t.Fatalf("states did not round-trip: got=%v want=%v", got, want)
	}
}

func TestWindowStateStoreRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "window-state.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (windowStateStore{mylist: &Mylist{configDir: dir}}).LoadWindowStates(); err == nil {
		t.Fatal("a corrupt state file should report an error")
	}
}
