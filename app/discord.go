package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync/atomic"
	"time"
	"unsafe"
)

// The Discord Rich Presence: the track playing, shown on the user's Discord
// profile the way WSA_RPC_Bridge shows it. The app speaks Discord's local
// IPC protocol itself — the named pipe `\\.\pipe\discord-ipc-N` and 8-byte
// little-endian frames of JSON, the protocol WSA_RPC_Bridge's Rust client
// uses — with the same policies: a few connect attempts, a small queue, and
// a connection dropped on a failed send and made again by the next update.

// discordClientID is VocaBox's Discord application, which names and pictures
// the card. Discord asks for one on every connection.
const discordClientID = "1560608108722524272"

// discordActivityListening is the activity type the card shows, as
// WSA_RPC_Bridge shows it: the user listening to the track.
const discordActivityListening = 2

// discordConnectAttempts and discordConnectBackoff are how one connection
// takes up a Discord client that may still be starting up.
const (
	discordConnectAttempts = 3
	discordConnectBackoff  = time.Second
)

// discordQueueSize is how many updates wait for the worker; a full queue
// drops the update rather than slowing the UI thread down.
const discordQueueSize = 8

// discordResendTick is the step the resend loop takes, and the intervals are
// whole multiples of it, so one loop serves both. discordResendInterval is how
// often a playing presence is sent again unchanged: a Discord client that
// starts after the app hears of the track playing only on the next send.
// discordPausedResendInterval is how often a paused card is re-pinned: Discord
// counts its elapsed time up itself and cannot be told to freeze, so only a
// fresh start of now minus the position keeps the paused position on show.
const (
	discordResendTick           = time.Second
	discordResendInterval       = 5 * time.Second
	discordPausedResendInterval = 3 * time.Second
	discordResendEvery          = int(discordResendInterval / discordResendTick)
	discordPausedResendEvery    = int(discordPausedResendInterval / discordResendTick)
)

// discordPipes is how many `discord-ipc-N` pipes Discord may be listening
// on; discordMaxFrame is the length a frame read from it may not pass.
const (
	discordPipes    = 10
	discordMaxFrame = 1 << 20
)

// The frame opcodes of the Discord IPC protocol.
const (
	discordOpHandshake = 0
	discordOpFrame     = 1
	discordOpClose     = 2
)

// discordActivity is the card Discord shows, in the shape its SET_ACTIVITY
// command takes.
type discordActivity struct {
	// Name heads the card with the track's title, which Discord would
	// otherwise take from the application's own name.
	Name       string             `json:"name"`
	Type       int                `json:"type"`
	Details    string             `json:"details,omitempty"`
	State      string             `json:"state,omitempty"`
	Assets     *discordAssets     `json:"assets,omitempty"`
	Timestamps *discordTimestamps `json:"timestamps,omitempty"`
}

type discordAssets struct {
	LargeImage string `json:"large_image"`
	LargeText  string `json:"large_text,omitempty"`
}

type discordTimestamps struct {
	Start int64 `json:"start,omitempty"`
	End   int64 `json:"end,omitempty"`
}

// discordPresence is the card the track playing shows, the shape of
// WSA_RPC_Bridge's: the track names the card and reads under state, the app
// reads under details, the artwork and its uploader fill the large image.
// The timer runs from the position reached whether the track plays or is
// paused, so a paused card shows that position rather than the count Discord
// makes up from the activity's start.
func discordPresence(st playerState) discordActivity {
	activity := discordActivity{
		Name:    st.Title,
		Type:    discordActivityListening,
		Details: "VocaBox",
		State:   st.Title,
	}
	if st.ThumbURL != "" {
		activity.Assets = &discordAssets{LargeImage: st.ThumbURL, LargeText: st.Uploader}
	}
	start := time.Now().Unix() - int64(st.PositionMS/1000)
	activity.Timestamps = &discordTimestamps{Start: start}
	if st.DurationMS > 0 {
		activity.Timestamps.End = start + int64(st.DurationMS/1000)
	}
	return activity
}

// syncDiscord hands the track playing to Discord: at once when it changed,
// and on force from the periodic resend and the settings toggle.
func (p *playback) syncDiscord(force bool) {
	if !p.app.discordOn || p.app.discord == nil {
		return
	}
	st := p.app.player
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%v\x00%d\x00%d",
		st.Title, st.Uploader, st.ThumbURL, p.wantPlaying, int64(st.PositionMS/1000), int64(st.DurationMS/1000))
	if !force && key == p.discordKey {
		return
	}
	p.discordKey = key
	if st.Title == "" {
		// Nothing plays: the card comes off with the connection.
		p.app.discord.disconnect()
		return
	}
	p.app.discord.update(discordPresence(st))
}

// resendDiscord keeps Discord in step with the app: a client that starts or
// restarts while a track plays hears of it within discordResendInterval even
// though the app only sends on a change, and a paused card is re-pinned on
// its own interval, since Discord keeps counting its elapsed time up.
func (p *playback) resendDiscord() {
	for tick := 1; ; tick++ {
		time.Sleep(discordResendTick)
		n := tick
		p.post(func() {
			if p.app.discordOn && p.app.player.Title != "" && discordResendDue(n, p.wantPlaying) {
				p.syncDiscord(true)
			}
		})
	}
}

// discordResendDue reports whether the tick numbered n is due a resend: every
// discordPausedResendEvery ticks while paused, so the paused position is
// re-pinned, and every discordResendEvery ticks while playing, which needs
// only the keepalive.
func discordResendDue(n int, playing bool) bool {
	if playing {
		return n%discordResendEvery == 0
	}
	return n%discordPausedResendEvery == 0
}

// setDiscordRPC turns the presence on or off from the settings: on shows the
// track playing at once, off takes the card off the profile.
func (app *nativeApp) setDiscordRPC(enabled bool) {
	app.discordOn = enabled
	if !enabled {
		if app.discord != nil {
			app.discord.disconnect()
		}
		return
	}
	if app.playback != nil {
		app.playback.syncDiscord(true)
	}
}

// discordConn is the app's presence client, which newNativeApp wires and a
// test stands in for.
type discordConn interface {
	update(discordActivity)
	disconnect()
}

// discordCommand is one order to the worker: an activity to show, or the
// close that takes the card off.
type discordCommand struct {
	activity discordActivity
	stop     bool
}

// discordRPC keeps the Discord connection in its own goroutine, as
// WSA_RPC_Bridge's worker thread does: the UI thread only drops updates on
// the queue and never waits for the pipe.
type discordRPC struct {
	clientID string
	cmds     chan discordCommand
}

func newDiscordRPC(clientID string) *discordRPC {
	r := &discordRPC{clientID: clientID, cmds: make(chan discordCommand, discordQueueSize)}
	go r.run()
	return r
}

// update queues one activity; a full queue drops it, and the periodic resend
// catches up.
func (r *discordRPC) update(activity discordActivity) {
	select {
	case r.cmds <- discordCommand{activity: activity}:
	default:
	}
}

// disconnect closes the connection, which is what takes the activity off the
// user's profile.
func (r *discordRPC) disconnect() {
	select {
	case r.cmds <- discordCommand{stop: true}:
	default:
	}
}

// run is the worker: one connection at a time, made when an update needs it
// and dropped when a send fails.
func (r *discordRPC) run() {
	var conn *os.File
	for cmd := range r.cmds {
		if cmd.stop {
			discordClose(conn)
			conn = nil
			continue
		}
		if conn == nil {
			if conn = discordConnect(r.clientID); conn == nil {
				continue
			}
		}
		if err := discordSetActivity(conn, cmd.activity); err != nil {
			log.Printf("discord: send failed: %v", err)
			conn.Close()
			conn = nil
			continue
		}
		discordDrain(conn)
	}
	discordClose(conn)
}

// discordConnect reaches a running Discord client, trying the handshake a
// few times as WSA_RPC_Bridge does, so a client still starting up is not
// missed.
func discordConnect(clientID string) *os.File {
	var err error
	for attempt := 1; attempt <= discordConnectAttempts; attempt++ {
		var conn *os.File
		if conn, err = discordDial(clientID); err == nil {
			return conn
		}
		if attempt < discordConnectAttempts {
			time.Sleep(discordConnectBackoff)
		}
	}
	log.Printf("discord: %v", err)
	return nil
}

// discordDial opens the first Discord pipe that answers the handshake; a
// client that rejects it closes the pipe, so a failed read is a failed
// connect.
func discordDial(clientID string) (*os.File, error) {
	handshake, err := json.Marshal(map[string]any{"v": 1, "client_id": clientID})
	if err != nil {
		return nil, err
	}
	var openErr error
	for i := 0; i < discordPipes; i++ {
		conn, err := os.OpenFile(discordPipePath(i), os.O_RDWR, 0)
		if err != nil {
			openErr = err
			continue
		}
		if err = discordWriteFrame(conn, discordOpHandshake, handshake); err == nil {
			_, _, err = discordReadFrame(conn)
		}
		if err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}
	return nil, openErr
}

// discordPipePath is where Discord listens, its instances numbered from 0.
func discordPipePath(i int) string {
	return `\\.\pipe\discord-ipc-` + strconv.Itoa(i)
}

// discordDrain takes the replies Discord has already sent off the pipe. The
// peek is what keeps this from holding up the sends: a plain read on the same
// file locks the writes out until it returns, and a client that answers
// nothing would stall there for good.
func discordDrain(conn *os.File) {
	var avail uint32
	for {
		ok, _, _ := procPeekNamedPipe.Call(conn.Fd(), 0, 0, 0, uintptr(unsafe.Pointer(&avail)), 0)
		if ok == 0 || avail == 0 {
			return
		}
		buf := make([]byte, min(avail, 4096))
		if _, err := conn.Read(buf); err != nil {
			return
		}
	}
}

var procPeekNamedPipe = kernel32.NewProc("PeekNamedPipe")

// discordSetActivity sends one activity under the app's own process.
func discordSetActivity(conn *os.File, activity discordActivity) error {
	payload, err := json.Marshal(map[string]any{
		"cmd":   "SET_ACTIVITY",
		"args":  map[string]any{"pid": os.Getpid(), "activity": activity},
		"nonce": discordNextNonce(),
	})
	if err != nil {
		return err
	}
	return discordWriteFrame(conn, discordOpFrame, payload)
}

// discordNonceCount names the commands this run sends; the worker is the
// only sender, so a counter keeps them apart.
var discordNonceCount atomic.Uint64

func discordNextNonce() string {
	return strconv.FormatUint(discordNonceCount.Add(1), 10)
}

// discordFrame wraps one payload in the protocol's header: the opcode and
// the byte length, both little-endian 32-bit numbers.
func discordFrame(opcode uint32, payload []byte) []byte {
	frame := make([]byte, 8+len(payload))
	binary.LittleEndian.PutUint32(frame[0:4], opcode)
	binary.LittleEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	return frame
}

func discordWriteFrame(conn *os.File, opcode uint32, payload []byte) error {
	_, err := conn.Write(discordFrame(opcode, payload))
	return err
}

// discordReadFrame reads one frame, refusing a length no answer of
// Discord's can take.
func discordReadFrame(r io.Reader) (uint32, []byte, error) {
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	opcode := binary.LittleEndian.Uint32(header[0:4])
	length := binary.LittleEndian.Uint32(header[4:8])
	if length > discordMaxFrame {
		return 0, nil, errors.New("discord: oversized frame")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return opcode, payload, nil
}

// discordClose shuts a connection down the way the Rust client does: a close
// frame, then the pipe, whose closing clears the activity.
func discordClose(conn *os.File) {
	if conn == nil {
		return
	}
	_ = discordWriteFrame(conn, discordOpClose, []byte("{}"))
	conn.Close()
}
