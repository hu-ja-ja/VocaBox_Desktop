// Package discord shows the track playing as Discord Rich Presence. The app
// speaks Discord's local IPC protocol itself — the named pipe
// `\\.\pipe\discord-ipc-N` and 8-byte little-endian frames of JSON, the
// protocol WSA_RPC_Bridge's Rust client uses — with the same policies: a few
// connect attempts, a small queue, and a connection dropped on a failed send
// and made again by the next update.
package discord

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// connectAttempts and connectBackoff are how one connection takes up a
// Discord client that may still be starting up.
const (
	connectAttempts = 3
	connectBackoff  = time.Second
)

// queueSize is how many updates wait for the worker; a full queue drops the
// update rather than slowing the UI thread down.
const queueSize = 8

// pipes is how many `discord-ipc-N` pipes Discord may be listening on;
// maxFrame is the length a frame read from it may not pass.
const (
	pipes    = 10
	maxFrame = 1 << 20
)

// The frame opcodes of the Discord IPC protocol.
const (
	opHandshake = 0
	opFrame     = 1
	opClose     = 2
)

// Activity is the card Discord shows, in the shape its SET_ACTIVITY command
// takes.
type Activity struct {
	// Name heads the card with the track's title, which Discord would
	// otherwise take from the application's own name.
	Name       string      `json:"name"`
	Type       int         `json:"type"`
	Details    string      `json:"details,omitempty"`
	State      string      `json:"state,omitempty"`
	Assets     *Assets     `json:"assets,omitempty"`
	Timestamps *Timestamps `json:"timestamps,omitempty"`
}

type Assets struct {
	LargeImage string `json:"large_image"`
	LargeText  string `json:"large_text,omitempty"`
}

type Timestamps struct {
	Start int64 `json:"start,omitempty"`
	End   int64 `json:"end,omitempty"`
}

// command is one order to the worker: an activity to show, or the close that
// takes the card off.
type command struct {
	activity Activity
	stop     bool
}

// RPC keeps the Discord connection in its own goroutine, as WSA_RPC_Bridge's
// worker thread does: the UI thread only drops updates on the queue and never
// waits for the pipe.
type RPC struct {
	clientID string
	cmds     chan command
}

// New returns the presence client for the Discord application clientID and
// starts its worker.
func New(clientID string) *RPC {
	r := &RPC{clientID: clientID, cmds: make(chan command, queueSize)}
	go r.run()
	return r
}

// Update queues one activity; a full queue drops it, and the periodic resend
// catches up.
func (r *RPC) Update(activity Activity) {
	select {
	case r.cmds <- command{activity: activity}:
	default:
	}
}

// Disconnect closes the connection, which is what takes the activity off the
// user's profile.
func (r *RPC) Disconnect() {
	select {
	case r.cmds <- command{stop: true}:
	default:
	}
}

// run is the worker: one connection at a time, made when an update needs it
// and dropped when a send fails.
func (r *RPC) run() {
	var conn *os.File
	for cmd := range r.cmds {
		if cmd.stop {
			closeConn(conn)
			conn = nil
			continue
		}
		if conn == nil {
			if conn = connect(r.clientID); conn == nil {
				continue
			}
		}
		if err := setActivity(conn, cmd.activity); err != nil {
			log.Printf("discord: send failed: %v", err)
			conn.Close()
			conn = nil
			continue
		}
		drain(conn)
	}
	closeConn(conn)
}

// connect reaches a running Discord client, trying the handshake a few times
// as WSA_RPC_Bridge does, so a client still starting up is not missed.
func connect(clientID string) *os.File {
	var err error
	for attempt := 1; attempt <= connectAttempts; attempt++ {
		var conn *os.File
		if conn, err = dial(clientID); err == nil {
			return conn
		}
		if attempt < connectAttempts {
			time.Sleep(connectBackoff)
		}
	}
	log.Printf("discord: %v", err)
	return nil
}

// dial opens the first Discord pipe that answers the handshake; a client that
// rejects it closes the pipe, so a failed read is a failed connect.
func dial(clientID string) (*os.File, error) {
	handshake, err := json.Marshal(map[string]any{"v": 1, "client_id": clientID})
	if err != nil {
		return nil, err
	}
	var openErr error
	for i := 0; i < pipes; i++ {
		conn, err := os.OpenFile(pipePath(i), os.O_RDWR, 0)
		if err != nil {
			openErr = err
			continue
		}
		if err = writeFrame(conn, opHandshake, handshake); err == nil {
			_, _, err = readFrame(conn)
		}
		if err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}
	return nil, openErr
}

// pipePath is where Discord listens, its instances numbered from 0.
func pipePath(i int) string {
	return `\\.\pipe\discord-ipc-` + strconv.Itoa(i)
}

// drain takes the replies Discord has already sent off the pipe. The peek is
// what keeps this from holding up the sends: a plain read on the same file
// locks the writes out until it returns, and a client that answers nothing
// would stall there for good.
func drain(conn *os.File) {
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

var procPeekNamedPipe = syscall.NewLazyDLL("kernel32.dll").NewProc("PeekNamedPipe")

// setActivity sends one activity under the app's own process.
func setActivity(conn *os.File, activity Activity) error {
	payload, err := json.Marshal(map[string]any{
		"cmd":   "SET_ACTIVITY",
		"args":  map[string]any{"pid": os.Getpid(), "activity": activity},
		"nonce": nextNonce(),
	})
	if err != nil {
		return err
	}
	return writeFrame(conn, opFrame, payload)
}

// nonceCount names the commands this run sends; the worker is the only
// sender, so a counter keeps them apart.
var nonceCount atomic.Uint64

func nextNonce() string {
	return strconv.FormatUint(nonceCount.Add(1), 10)
}

// frame wraps one payload in the protocol's header: the opcode and the byte
// length, both little-endian 32-bit numbers.
func frame(opcode uint32, payload []byte) []byte {
	frame := make([]byte, 8+len(payload))
	binary.LittleEndian.PutUint32(frame[0:4], opcode)
	binary.LittleEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	return frame
}

func writeFrame(conn *os.File, opcode uint32, payload []byte) error {
	_, err := conn.Write(frame(opcode, payload))
	return err
}

// readFrame reads one frame, refusing a length no answer of Discord's can
// take.
func readFrame(r io.Reader) (uint32, []byte, error) {
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	opcode := binary.LittleEndian.Uint32(header[0:4])
	length := binary.LittleEndian.Uint32(header[4:8])
	if length > maxFrame {
		return 0, nil, errors.New("discord: oversized frame")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return opcode, payload, nil
}

// closeConn shuts a connection down the way the Rust client does: a close
// frame, then the pipe, whose closing clears the activity.
func closeConn(conn *os.File) {
	if conn == nil {
		return
	}
	_ = writeFrame(conn, opClose, []byte("{}"))
	conn.Close()
}
