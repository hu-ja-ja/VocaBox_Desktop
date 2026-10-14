package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionStoreRoundTrip(t *testing.T) {
	mylist := NewMylist()
	mylist.configDir = t.TempDir()
	store := sessionStore{mylist: mylist}

	if got, err := store.Load(); err != nil || got != "" {
		t.Fatalf("empty store = %q, %v", got, err)
	}
	header := "user_session=user_session_1_abc; user_session_secure=def"
	if err := store.Save(header); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(mylist.configDir, "session.dat"))
	if err != nil {
		t.Fatalf("the session file: %v", err)
	}
	if bytes.Contains(raw, []byte("user_session_1_abc")) {
		t.Fatal("the session file holds the cookies in the clear")
	}
	if got, err := store.Load(); err != nil || got != header {
		t.Fatalf("Load = %q, %v", got, err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got, err := store.Load(); err != nil || got != "" {
		t.Fatalf("after Clear = %q, %v", got, err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear of a missing file: %v", err)
	}
}

func TestSessionStoreRejectsGarbage(t *testing.T) {
	mylist := NewMylist()
	mylist.configDir = t.TempDir()
	store := sessionStore{mylist: mylist}

	path := filepath.Join(mylist.configDir, "session.dat")
	if err := os.WriteFile(path, []byte("not a dpapi blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Load(); err == nil || got != "" {
		t.Fatalf("a corrupted store = %q, %v", got, err)
	}
	if err := store.Save(""); err == nil {
		t.Fatal("saving no session was accepted")
	}
}
