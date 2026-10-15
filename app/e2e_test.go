package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPlaybackE2E plays a real niconico track through the real helper: the
// acquisition, the manifest, playback, seek, pause/resume, and the position
// pull. It is skipped unless VOCABOX_E2E=1, as it needs the network, the
// built helper, and sound comes out of the speakers for a few seconds.
func TestPlaybackE2E(t *testing.T) {
	if os.Getenv("VOCABOX_E2E") == "" {
		t.Skip("set VOCABOX_E2E=1 to play a real track (sm1097445 for a few seconds)")
	}
	path, err := filepath.Abs(filepath.Join("..", "resources", "AmsHelper.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("AmsHelper.exe is not built; run `bun run helper`")
	}
	sources, err := NewNico().Prepare("sm1097445")
	if err != nil {
		t.Fatalf("acquisition: %v", err)
	}
	t.Logf("sources: %d bands, cookie=%d chars", len(sources.URIs), len(sources.Cookie))

	events := make(chan helperEvent, 64)
	exited := make(chan error, 1)
	helper, err := startHelper(path, "..", func(event helperEvent) { events <- event }, func(err error) { exited <- err })
	if err != nil {
		t.Fatal(err)
	}
	defer helper.shutdown()

	// The app starts a track by sending play with play true; without it the
	// helper installs the track paused.
	if err := helper.send(helperCommand{Cmd: "play", URL: sources.URIs[0], Cookie: sources.Cookie, UA: sources.UserAgent, Play: true}); err != nil {
		t.Fatal(err)
	}
	opened := waitHelperEvent(t, events, "opened", 30*time.Second)
	if opened.DurationMS < 60_000 {
		t.Fatalf("duration = %vms; want the track's", opened.DurationMS)
	}

	// The position advances on its own.
	position := waitHelperPosition(t, helper, events, 1000, 15*time.Second)
	// A seek lands where it was asked to.
	if err := helper.send(helperCommand{Cmd: "seek", MS: 55_000}); err != nil {
		t.Fatal(err)
	}
	position = waitHelperPosition(t, helper, events, 54_000, 15*time.Second)
	if position > 60_000 {
		t.Fatalf("seek overshot: %vms", position)
	}

	// Pause holds the position; resume moves it again.
	if err := helper.send(helperCommand{Cmd: "pause"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	paused := pullHelperPosition(t, helper, events)
	if paused-position > 300 {
		t.Fatalf("the position moved while paused: %v -> %v", position, paused)
	}
	if err := helper.send(helperCommand{Cmd: "resume"}); err != nil {
		t.Fatal(err)
	}
	waitHelperPosition(t, helper, events, paused+500, 10*time.Second)

	// Switching tracks replaces the source playing: the replaced track's
	// media objects are disposed, and the one replacing it opens in their
	// place (the leak a switch used to leave).
	if err := helper.send(helperCommand{Cmd: "play", URL: sources.URIs[0], Cookie: sources.Cookie, UA: sources.UserAgent, Gen: 2, Play: true}); err != nil {
		t.Fatal(err)
	}
	if switched := waitHelperEvent(t, events, "opened", 30*time.Second); switched.Gen != 2 {
		t.Fatalf("opened gen = %d; want 2", switched.Gen)
	}

	if err := helper.send(helperCommand{Cmd: "quit"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("quit did not end the helper cleanly: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the helper did not exit on quit")
	}
}

// waitHelperEvent waits for one event of the named kind, failing on playback
// failures.
func waitHelperEvent(t *testing.T, events <-chan helperEvent, name string, timeout time.Duration) helperEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-events:
			if event.Event == "failed" {
				t.Fatalf("helper failed: code=%s ext=0x%08X msg=%s", event.Code, event.Ext, event.Msg)
			}
			if event.Event == name {
				return event
			}
		case <-deadline:
			t.Fatalf("no %s event within %s", name, timeout)
		}
	}
}

// pullHelperPosition asks the helper where it is and waits for the answer.
func pullHelperPosition(t *testing.T, helper helperConn, events <-chan helperEvent) float64 {
	t.Helper()
	if err := helper.send(helperCommand{Cmd: "position"}); err != nil {
		t.Fatal(err)
	}
	return waitHelperEvent(t, events, "position", 5*time.Second).MS
}

// waitHelperPosition pulls the position until it reaches at least min.
func waitHelperPosition(t *testing.T, helper helperConn, events <-chan helperEvent, min float64, timeout time.Duration) float64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var position float64
	for time.Now().Before(deadline) {
		position = pullHelperPosition(t, helper, events)
		if position >= min {
			return position
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("position stuck at %vms; want at least %vms", position, min)
	return position
}
