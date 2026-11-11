package ams

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPlayerPlaysASilentFile drives the real module with a silent WAV: the
// player is made, a load opens with its generation, the position reports as
// the app asks, and the track ends. The volume is zero, so nothing is heard.
func TestPlayerPlaysASilentFile(t *testing.T) {
	player, err := NewPlayer()
	if err != nil {
		t.Skipf("the module is not built for this machine: %v (run `go tool mygo native build -target host`)", err)
	}
	defer player.Close()
	_ = player.SetVolume(0)
	// The app asks for a position at a rate; nothing comes unasked.
	if err := player.SetPositionRate(50 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 16)
	sub := player.OnEvent(func(event Event) { events <- event })
	defer sub.Close()

	path := filepath.Join(t.TempDir(), "silent.wav")
	writeSilentWAV(t, path, time.Second)
	uri := "file:///" + filepath.ToSlash(path)
	if err := player.Play(Load{File: uri, Generation: 7, Start: true}); err != nil {
		t.Fatal(err)
	}
	opened := waitEvent(t, events, "opened")
	if opened.Generation != 7 {
		t.Fatalf("opened generation = %d; want 7", opened.Generation)
	}
	if opened.DurationMS < 900 {
		t.Fatalf("opened duration = %vms; want the file's second", opened.DurationMS)
	}

	// The position is the media stack's, reported while the track plays.
	if position := waitPosition(t, events, 100); position < 100 {
		t.Fatalf("position stuck at %vms; want it to advance", position)
	}
	waitEvent(t, events, "ended")
}

// waitPosition waits for a position event at or past min.
func waitPosition(t *testing.T, events <-chan Event, min float64) float64 {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Event == "failed" {
				t.Fatalf("player failed: code=%s ext=%d msg=%s", event.Code, event.Ext, event.Msg)
			}
			if event.Event == "position" && event.MS >= min {
				return event.MS
			}
		case <-deadline:
			t.Fatalf("no position of %vms within 10s", min)
		}
	}
}

// waitEvent waits for one event of the named kind, failing on playback
// failures.
func waitEvent(t *testing.T, events <-chan Event, name string) Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Event == "failed" {
				t.Fatalf("player failed: code=%s ext=%d msg=%s", event.Code, event.Ext, event.Msg)
			}
			if event.Event == name {
				return event
			}
		case <-deadline:
			t.Fatalf("no %s event within 10s", name)
		}
	}
}

// writeSilentWAV writes a mono 16-bit PCM file of silence.
func writeSilentWAV(t *testing.T, path string, length time.Duration) {
	t.Helper()
	const rate = 8000
	samples := int(length.Seconds() * rate)
	data := make([]byte, samples*2)
	header := make([]byte, 44)
	copy(header[0:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+len(data)))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1) // PCM
	binary.LittleEndian.PutUint16(header[22:], 1) // mono
	binary.LittleEndian.PutUint32(header[24:], rate)
	binary.LittleEndian.PutUint32(header[28:], rate*2)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(len(data)))
	if err := os.WriteFile(path, append(header, data...), 0o644); err != nil {
		t.Fatal(err)
	}
}
