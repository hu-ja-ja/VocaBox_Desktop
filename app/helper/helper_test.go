package helper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadHelperEventsParsesLines(t *testing.T) {
	input := strings.NewReader(`{"ev":"opened","durationMs":99150.5}` + "\n" +
		`{"ev":"position","ms":4200}` + "\n" +
		"not json\n" +
		"\n" +
		`{"ev":"failed","code":"NetworkError","ext":-2147012889,"msg":"boom"}` + "\n")
	var events []Event
	readEvents(input, func(event Event) { events = append(events, event) })
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Event != "opened" || events[0].DurationMS != 99150.5 {
		t.Fatalf("opened = %#v", events[0])
	}
	if events[1].Event != "position" || events[1].MS != 4200 {
		t.Fatalf("position = %#v", events[1])
	}
	if events[2].Event != "failed" || events[2].Code != "NetworkError" || events[2].Msg != "boom" {
		t.Fatalf("failed = %#v", events[2])
	}
}

// TestHelperCommandWireShape pins the JSON the helper's C# side reads.
func TestHelperCommandWireShape(t *testing.T) {
	cases := []struct {
		command Command
		want    string
	}{
		{
			Command{Cmd: "play", URL: "https://d/x.m3u8", Cookie: "domand_bid=1", UA: "ua"},
			`{"cmd":"play","url":"https://d/x.m3u8","cookie":"domand_bid=1","ua":"ua"}`,
		},
		{
			// A track whose load was already played once carries its name and
			// the play state: false installs it paused.
			Command{Cmd: "play", URL: "https://d/x.m3u8", Gen: 2, Play: true},
			`{"cmd":"play","url":"https://d/x.m3u8","gen":2,"play":true}`,
		},
		{
			// A saved track plays from disk: no url, no cookie, just the file
			// URI and the play state.
			Command{Cmd: "play", File: "file:///C:/audio/sm1.m4a", Gen: 3, Play: true},
			`{"cmd":"play","file":"file:///C:/audio/sm1.m4a","gen":3,"play":true}`,
		},
		{Command{Cmd: "position"}, `{"cmd":"position"}`},
		{Command{Cmd: "seek", MS: 1500}, `{"cmd":"seek","ms":1500}`},
		{Command{Cmd: "volume", Value: 0.4}, `{"cmd":"volume","value":0.4}`},
	}
	for _, tc := range cases {
		got, err := json.Marshal(tc.command)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("marshal = %s; want %s", got, tc.want)
		}
	}
}

// TestAmsHelperRunsTheProtocol is a smoke test of the built helper binary:
// it starts, answers position, and quits cleanly. It is skipped until
// `mise run helper` has produced resources/AmsHelper.exe.
func TestAmsHelperRunsTheProtocol(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "..", "resources", "AmsHelper.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("AmsHelper.exe is not built; run `mise run helper`")
	}
	events := make(chan Event, 16)
	exited := make(chan error, 1)
	proc, err := Start(path, filepath.Join("..", ".."), func(event Event) { events <- event }, func(err error) { exited <- err })
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Shutdown()
	if err := proc.Send(Command{Cmd: "volume", Value: 0.3}); err != nil {
		t.Fatal(err)
	}
	if err := proc.Send(Command{Cmd: "position"}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Event != "position" || event.MS != 0 {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the helper did not answer the position pull")
	}
	if err := proc.Send(Command{Cmd: "quit"}); err != nil {
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
