package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os/exec"
	"sync"
	"syscall"
)

// AmsHelper is VocaBox's playback helper: a small Rust process that
// plays one niconico DOMAND audio playlist through Windows' own media stack
// (see helper/README.md for the protocol). The app runs it as a child, so a
// media-stack crash cannot take the UI down.

// helperEvent is one event line of the helper's protocol: opened, ended,
// failed, or the position reply with ms set.
type helperEvent struct {
	Event      string  `json:"ev"`
	DurationMS float64 `json:"durationMs"`
	MS         float64 `json:"ms"`
	Code       string  `json:"code"`
	// Ext is the failing HResult, for logging: the UI shows Code and Msg.
	Ext int    `json:"ext"`
	Msg string `json:"msg"`
	// Gen echoes the play command's generation: an event naming another
	// load belongs to a replaced track, and the engine drops it.
	Gen int `json:"gen,omitempty"`
}

// helperCommand is one command line of the protocol.
type helperCommand struct {
	Cmd string `json:"cmd"`
	// URL, Cookie and UA belong to play: the signed audio media playlist,
	// the app's cookies for .nicovideo.jp, and the User-Agent. File names a
	// saved track instead, a file:// URI the helper opens from disk.
	URL    string  `json:"url,omitempty"`
	File   string  `json:"file,omitempty"`
	Cookie string  `json:"cookie,omitempty"`
	UA     string  `json:"ua,omitempty"`
	MS     float64 `json:"ms,omitempty"`
	Value  float64 `json:"value,omitempty"`
	// Gen names the track load a play starts, and Play asks it to start
	// playing; false installs the track paused.
	Gen  int  `json:"gen,omitempty"`
	Play bool `json:"play,omitempty"`
}

// helperProcess is one running helper. Events arrive on its own goroutine,
// in the order the helper printed them; send is safe from any goroutine.
type helperProcess struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	mu    sync.Mutex
	// closed says a shutdown or a death ended this process; a send after
	// that reports an error rather than writing to a pipe nobody reads.
	closed bool
}

// startHelper launches the helper at path. onEvent hears every event line,
// onExit the exit once the process is gone (its error is nil for a clean
// quit).
func startHelper(path, dir string, onEvent func(helperEvent), onExit func(error)) (*helperProcess, error) {
	cmd := exec.Command(path)
	cmd.Dir = dir
	// The helper is a console program; without this a console window would
	// flash over the app at every start and restart.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	h := &helperProcess{
		cmd:   cmd,
		stdin: stdin,
	}
	go func() {
		readHelperEvents(stdout, onEvent)
		err := cmd.Wait()
		h.mu.Lock()
		closed := h.closed
		h.closed = true
		h.mu.Unlock()
		if !closed && err != nil {
			// An unexpected death is worth a word on the app's log; the
			// restart is the playback engine's business.
			log.Printf("AmsHelper exited: %v", err)
		}
		onExit(err)
	}()
	go drainHelperStderr(stderr)
	return h, nil
}

// send writes one command line. A send to a dead helper reports an error;
// the exit callback has already fired or will.
func (h *helperProcess) send(command helperCommand) error {
	line, err := json.Marshal(command)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errors.New("AmsHelper is not running")
	}
	_, err = h.stdin.Write(line)
	return err
}

// shutdown ends the helper for good: the app is quitting, so the audio stops
// with it. The kill is deliberate and racing callbacks must ignore it.
func (h *helperProcess) shutdown() {
	h.mu.Lock()
	h.closed = true
	h.stdin.Close()
	h.mu.Unlock()
	_ = h.cmd.Process.Kill()
}

// readHelperEvents dispatches the helper's event lines; a malformed line is
// dropped rather than stopping the stream.
func readHelperEvents(r io.Reader, onEvent func(helperEvent)) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := bytes.TrimSpace(s.Bytes())
		if len(line) == 0 {
			continue
		}
		var event helperEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		onEvent(event)
	}
}

// drainHelperStderr logs what the helper writes to stderr: Rust panics
// and Windows' media-stack complaints arrive there.
func drainHelperStderr(r io.Reader) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 4<<10), 64<<10)
	for s.Scan() {
		log.Printf("AmsHelper: %s", s.Text())
	}
}
