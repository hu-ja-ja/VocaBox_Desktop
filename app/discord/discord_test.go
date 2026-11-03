package discord

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
	"time"
	"unsafe"
)

// TestDiscordFrames checks the pipe frames: the little-endian header with
// the payload behind it, and the bound on what is read.
func TestDiscordFrames(t *testing.T) {
	payload := []byte(`{"v":1,"client_id":"1"}`)
	frameBytes := frame(opHandshake, payload)
	if len(frameBytes) != 8+len(payload) {
		t.Fatalf("frame length = %d", len(frameBytes))
	}
	if opcode := binary.LittleEndian.Uint32(frameBytes[0:4]); opcode != opHandshake {
		t.Fatalf("opcode = %d", opcode)
	}
	if length := binary.LittleEndian.Uint32(frameBytes[4:8]); length != uint32(len(payload)) {
		t.Fatalf("length = %d", length)
	}
	opcode, got, err := readFrame(bytes.NewReader(frameBytes))
	if err != nil || opcode != opHandshake || !bytes.Equal(got, payload) {
		t.Fatalf("read = %d %q, %v", opcode, got, err)
	}

	// A frame longer than any answer of Discord's is refused.
	oversized := frame(opFrame, nil)
	binary.LittleEndian.PutUint32(oversized[4:8], maxFrame+1)
	if _, _, err := readFrame(bytes.NewReader(oversized)); err == nil {
		t.Fatal("an oversized frame was read")
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
		drain(conn)
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
	drain(conn)

	var avail uint32
	if ok, _, _ := procPeekNamedPipe.Call(conn.Fd(), 0, 0, 0, uintptr(unsafe.Pointer(&avail)), 0); ok == 0 || avail != 0 {
		t.Fatalf("the drain left %d bytes", avail)
	}
}
