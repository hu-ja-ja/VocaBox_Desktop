package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hu-ja-ja/VocaBox_Desktop/modules/ams"
)

// TestPlaybackE2E plays a real niconico track through the real module: the
// acquisition, the manifest, playback, seek, pause/resume, and the position.
// It is skipped unless VOCABOX_E2E=1, as it needs the network, the built
// module, and sound comes out of the speakers for a few seconds.
func TestPlaybackE2E(t *testing.T) {
	if os.Getenv("VOCABOX_E2E") == "" {
		t.Skip("set VOCABOX_E2E=1 to play a real track (sm1097445 for a few seconds)")
	}
	sources, err := NewNico().Prepare("sm1097445")
	if err != nil {
		t.Fatalf("acquisition: %v", err)
	}
	t.Logf("sources: %d bands, cookie=%d chars", len(sources.URIs), len(sources.Cookie))
	player, events := e2ePlayer(t)

	// The app starts a track by playing a load; without Start it installs the
	// track paused.
	if err := player.Play(ams.Load{
		URL: sources.URIs[0], Cookie: sources.Cookie, UserAgent: sources.UserAgent,
		Generation: 1, Start: true,
	}); err != nil {
		t.Fatal(err)
	}
	opened := waitEvent(t, events, "opened", 30*time.Second)
	if opened.DurationMS < 60_000 {
		t.Fatalf("duration = %vms; want the track's", opened.DurationMS)
	}

	// The position advances on its own.
	position := waitPosition(t, events, 1000, 15*time.Second)
	// A seek lands where it was asked to.
	if err := player.Seek(55_000); err != nil {
		t.Fatal(err)
	}
	position = waitPosition(t, events, 54_000, 15*time.Second)
	if position > 60_000 {
		t.Fatalf("seek overshot: %vms", position)
	}

	// Pause holds the position; resume moves it again.
	if err := player.Pause(); err != nil {
		t.Fatal(err)
	}
	// The player keeps reporting at the rate it was given, and the pause
	// lands through the queue: let it, then compare two samples from after it.
	time.Sleep(300 * time.Millisecond)
	paused := latestPosition(t, events)
	time.Sleep(500 * time.Millisecond)
	if moved := latestPosition(t, events); moved-paused > 300 {
		t.Fatalf("the position moved while paused: %v -> %v", paused, moved)
	}
	if err := player.Resume(); err != nil {
		t.Fatal(err)
	}
	waitPosition(t, events, paused+500, 10*time.Second)

	// Switching tracks replaces the source playing: the replaced track's
	// media objects are disposed, and the one replacing it opens in their
	// place (the leak a switch used to leave).
	if err := player.Play(ams.Load{
		URL: sources.URIs[0], Cookie: sources.Cookie, UserAgent: sources.UserAgent,
		Generation: 2, Start: true,
	}); err != nil {
		t.Fatal(err)
	}
	if switched := waitEvent(t, events, "opened", 30*time.Second); switched.Generation != 2 {
		t.Fatalf("opened generation = %d; want 2", switched.Generation)
	}
}

// e2ePlayer makes the module's player and its event channel, skipping when
// the module has not been built for this machine.
func e2ePlayer(t *testing.T) (ams.Player, <-chan ams.Event) {
	t.Helper()
	player, err := ams.NewPlayer()
	if err != nil {
		t.Skipf("the ams module is not built: %v (run `go tool mygo native build -target host ./modules/ams`)", err)
	}
	t.Cleanup(func() { _ = player.Close() })
	// The tests run on a developer's machine too: silence them.
	if err := player.SetVolume(0); err != nil {
		t.Fatal(err)
	}
	// The app asks for a position at a rate; nothing comes unasked.
	if err := player.SetPositionRate(positionRateLyrics); err != nil {
		t.Fatal(err)
	}
	events := make(chan ams.Event, 64)
	sub := player.OnEvent(func(event ams.Event) { events <- event })
	t.Cleanup(func() { _ = sub.Close() })
	return player, events
}

// waitEvent waits for one event of the named kind, failing on playback
// failures.
func waitEvent(t *testing.T, events <-chan ams.Event, name string, timeout time.Duration) ams.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-events:
			if event.Event == "failed" {
				t.Fatalf("player failed: code=%s ext=0x%08X msg=%s", event.Code, event.Ext, event.Msg)
			}
			if event.Event == name {
				return event
			}
		case <-deadline:
			t.Fatalf("no %s event within %s", name, timeout)
		}
	}
}

// waitPosition waits for a position report at or past min.
func waitPosition(t *testing.T, events <-chan ams.Event, min float64, timeout time.Duration) float64 {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-events:
			if event.Event == "failed" {
				t.Fatalf("player failed: code=%s ext=0x%08X msg=%s", event.Code, event.Ext, event.Msg)
			}
			if event.Event == "position" && event.MS >= min {
				return event.MS
			}
		case <-deadline:
			t.Fatalf("no position of %vms within %s", min, timeout)
		}
	}
}

// latestPosition drains the reports queued for 200ms and returns the newest
// position among them, failing when there is none.
func latestPosition(t *testing.T, events <-chan ams.Event) float64 {
	t.Helper()
	deadline := time.After(200 * time.Millisecond)
	var latest float64
	found := false
	for {
		select {
		case event := <-events:
			if event.Event == "failed" {
				t.Fatalf("player failed: code=%s ext=0x%08X msg=%s", event.Code, event.Ext, event.Msg)
			}
			if event.Event == "position" {
				latest, found = event.MS, true
			}
		case <-deadline:
			if !found {
				t.Fatal("no position report within 200ms")
			}
			return latest
		}
	}
}

// TestOfflineSaveE2E saves a real track and plays the saved file through the
// real module: the container the app writes must open without a network and
// seek like a streamed track does. It is skipped unless VOCABOX_E2E=1, as it
// needs the network, the built module, and sound comes out of the speakers.
func TestOfflineSaveE2E(t *testing.T) {
	if os.Getenv("VOCABOX_E2E") == "" {
		t.Skip("set VOCABOX_E2E=1 to save and play a real track (sm1097445 for a few seconds)")
	}
	dest := filepath.Join(t.TempDir(), "sm1097445.m4a")
	if err := NewNico().DownloadAudio("sm1097445", 192, dest); err != nil {
		t.Fatalf("saving the track: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil || info.Size() == 0 {
		t.Fatalf("the saved file: %v, %d bytes", err, info.Size())
	}
	t.Logf("saved %d bytes", info.Size())
	player, events := e2ePlayer(t)

	if err := player.Play(ams.Load{File: savedAudioURI(dest), Generation: 1, Start: true}); err != nil {
		t.Fatal(err)
	}
	opened := waitEvent(t, events, "opened", 30*time.Second)
	// The saved file carries its own length, so the player reports the track's.
	if opened.DurationMS < 60_000 {
		t.Fatalf("duration = %vms; want the track's", opened.DurationMS)
	}
	waitPosition(t, events, 1000, 15*time.Second)
	if err := player.Seek(55_000); err != nil {
		t.Fatal(err)
	}
	if position := waitPosition(t, events, 54_000, 15*time.Second); position > 60_000 {
		t.Fatalf("seek overshot: %vms", position)
	}
}

// TestOfflineSaveThroughTheAppE2E drives the app's own path with a real
// track: a mylist whose offline saving is on queues its audio and the
// background worker saves it. It is skipped unless VOCABOX_E2E=1, as it needs
// the network.
func TestOfflineSaveThroughTheAppE2E(t *testing.T) {
	if os.Getenv("VOCABOX_E2E") == "" {
		t.Skip("set VOCABOX_E2E=1 to save a real track through the app (sm1097445)")
	}
	app := newNativeApp(&Mylist{configDir: t.TempDir()})
	items := []MylistItem{{WatchID: "sm1097445", VideoID: "sm1097445", Title: "曲", Available: true, Duration: 99}}
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "オフライン", Download: true}, items)
	app.setDownload("111", true)
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if app.savedAudioPath("sm1097445") != "" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// The settings change fetches the saved audio again, at the band chosen:
	// the same track, written once more and smaller.
	saved := app.savedAudioPath("sm1097445")
	if saved == "" {
		t.Fatal("the worker did not save the track")
	}
	done, err := os.Stat(saved)
	if err != nil {
		t.Fatal(err)
	}
	app.setAudioQuality(64)
	app.startAudioDownloads()
	deadline = time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		at, err := os.Stat(saved)
		if err == nil && at.Size() != done.Size() {
			if at.Size() >= done.Size() {
				t.Fatalf("the re-fetch did not shrink the file: %d -> %d", done.Size(), at.Size())
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("the quality change did not re-fetch the track")
}
