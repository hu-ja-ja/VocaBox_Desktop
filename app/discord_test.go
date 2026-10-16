package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
	"time"
	"unsafe"
)

// TestDiscordPresenceCard checks the card the track playing shows: the shape
// WSA_RPC_Bridge sends, with the timer from the position reached, played or
// paused.
func TestDiscordPresenceCard(t *testing.T) {
	st := playerState{
		Title:      "一曲目",
		Uploader:   "投稿者",
		ThumbURL:   "https://thumb/1",
		PositionMS: 42000,
		DurationMS: 120000,
	}
	activity := discordPresence(st)
	if activity.Name != "一曲目" || activity.State != "一曲目" || activity.Details != "VocaBox" {
		t.Fatalf("activity = %#v", activity)
	}
	if activity.Type != discordActivityListening {
		t.Fatalf("type = %d", activity.Type)
	}
	if activity.Assets == nil || activity.Assets.LargeImage != "https://thumb/1" || activity.Assets.LargeText != "投稿者" {
		t.Fatalf("assets = %#v", activity.Assets)
	}
	if activity.Timestamps == nil || activity.Timestamps.End-activity.Timestamps.Start != 120 {
		t.Fatalf("timestamps = %#v", activity.Timestamps)
	}
	// The timer starts where the track is, 42 seconds in.
	if ago := time.Now().Unix() - activity.Timestamps.Start; ago < 41 || ago > 43 {
		t.Fatalf("the timer starts %d seconds in the past", ago)
	}

	// The timer is there the same way for a track without artwork, which
	// shows no image.
	if bare := discordPresence(playerState{Title: "x"}); bare.Assets != nil || bare.Timestamps == nil {
		t.Fatalf("bare = %#v", bare)
	}
}

// TestDiscordResendDue checks the resend timing: a paused card is re-pinned
// on its own interval, where the position must be kept on show, and a playing
// one only now and then, which needs just the keepalive.
func TestDiscordResendDue(t *testing.T) {
	for n := 1; n < discordPausedResendEvery; n++ {
		if discordResendDue(n, false) {
			t.Fatalf("paused tick %d was due early", n)
		}
	}
	if !discordResendDue(discordPausedResendEvery, false) {
		t.Fatal("the paused re-pin tick was not due")
	}
	for n := 1; n < discordResendEvery; n++ {
		if discordResendDue(n, true) {
			t.Fatalf("playing tick %d was due early", n)
		}
	}
	if !discordResendDue(discordResendEvery, true) {
		t.Fatal("the playing keepalive tick was not due")
	}
}

// TestDiscordFrames checks the pipe frames: the little-endian header with
// the payload behind it, and the bound on what is read.
func TestDiscordFrames(t *testing.T) {
	payload := []byte(`{"v":1,"client_id":"1"}`)
	frame := discordFrame(discordOpHandshake, payload)
	if len(frame) != 8+len(payload) {
		t.Fatalf("frame length = %d", len(frame))
	}
	if opcode := binary.LittleEndian.Uint32(frame[0:4]); opcode != discordOpHandshake {
		t.Fatalf("opcode = %d", opcode)
	}
	if length := binary.LittleEndian.Uint32(frame[4:8]); length != uint32(len(payload)) {
		t.Fatalf("length = %d", length)
	}
	opcode, got, err := discordReadFrame(bytes.NewReader(frame))
	if err != nil || opcode != discordOpHandshake || !bytes.Equal(got, payload) {
		t.Fatalf("read = %d %q, %v", opcode, got, err)
	}

	// A frame longer than any answer of Discord's is refused.
	oversized := discordFrame(discordOpFrame, nil)
	binary.LittleEndian.PutUint32(oversized[4:8], discordMaxFrame+1)
	if _, _, err := discordReadFrame(bytes.NewReader(oversized)); err == nil {
		t.Fatal("an oversized frame was read")
	}
}

// fakeDiscord records what the player told Discord.
type fakeDiscord struct {
	activities []discordActivity
	stops      int
}

func (f *fakeDiscord) update(activity discordActivity) { f.activities = append(f.activities, activity) }
func (f *fakeDiscord) disconnect()                     { f.stops++ }

// TestPlaybackSendsDiscordPresence checks what the player hands Discord: the
// track at once, a timer only while it plays, nothing on a change that does
// not show, and the card taken off when the track goes or the setting turns
// off.
func TestPlaybackSendsDiscordPresence(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	discord := &fakeDiscord{}
	p.app.discord = discord
	p.app.discordOn = true
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm1")

	if len(discord.activities) != 1 {
		t.Fatalf("activities = %#v", discord.activities)
	}
	if activity := discord.activities[0]; activity.Name != "一曲目" || activity.State != "一曲目" || activity.Timestamps == nil {
		t.Fatalf("activity = %#v", activity)
	}

	// The track opening reports the same card: it is not sent again.
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000, Gen: play.Gen})
	if len(discord.activities) != 1 {
		t.Fatalf("the same card was sent again: %#v", discord.activities)
	}

	// Pausing keeps the timer, from the position reached, and a volume change
	// shows nothing.
	p.pause()
	if len(discord.activities) != 2 || discord.activities[1].Timestamps == nil {
		t.Fatalf("paused = %#v", discord.activities)
	}
	p.setVolume(0.5)
	if len(discord.activities) != 2 {
		t.Fatalf("a volume change was sent: %#v", discord.activities)
	}

	// Turning the setting off takes the card off, turning it on shows the
	// track again at once.
	stops := discord.stops
	p.app.setDiscordRPC(false)
	if discord.stops != stops+1 {
		t.Fatalf("stops after turning off = %d", discord.stops)
	}
	p.app.setDiscordRPC(true)
	if len(discord.activities) != 3 {
		t.Fatalf("activities after turning on = %#v", discord.activities)
	}

	// Removing the track clears the card.
	stops = discord.stops
	p.remove("sm1")
	if discord.stops != stops+1 {
		t.Fatalf("stops after removing = %d", discord.stops)
	}
}

// TestDiscordDrainEmptiesThePipe checks the reply drain: it takes off what a
// client has sent, and it leaves an empty pipe alone. Waiting on that pipe
// instead would hold up every send behind it — the read lock and the write
// lock of one file are the same lock.
func TestDiscordDrainEmptiesThePipe(t *testing.T) {
	conn, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer conn.Close()
	defer writer.Close()

	idle := make(chan struct{})
	go func() {
		discordDrain(conn)
		close(idle)
	}()
	select {
	case <-idle:
	case <-time.After(5 * time.Second):
		t.Fatal("the drain waited on an empty pipe")
	}

	if _, err := writer.Write([]byte(`{"cmd":"SET_ACTIVITY","nonce":"1"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	discordDrain(conn)

	var avail uint32
	if ok, _, _ := procPeekNamedPipe.Call(conn.Fd(), 0, 0, 0, uintptr(unsafe.Pointer(&avail)), 0); ok == 0 || avail != 0 {
		t.Fatalf("the drain left %d bytes", avail)
	}
}
