package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWrapListKeepsTheNamesAndTheirCommas pins the wrapping: every name of
// the list stays in it, separated by commas the line breaks follow.
func TestWrapListKeepsTheNamesAndTheirCommas(t *testing.T) {
	var names []string
	for _, name := range []string{"play", "pause", "shuffle", "repeat", "skip-back", "skip-forward",
		"volume-2", "volume-x", "menu", "plus", "queue", "ellipsis-vertical"} {
		names = append(names, name)
	}
	wrapped := wrapList(names)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 2 {
		t.Fatalf("the list did not wrap: %q", wrapped)
	}
	for _, line := range lines {
		if len(line) > wrapWidth {
			t.Errorf("line is %d wide, past %d: %q", len(line), wrapWidth, line)
		}
	}
	joined := strings.Join(lines, " ")
	for _, name := range names {
		if !strings.Contains(joined, name) {
			t.Errorf("the list lost %s: %q", name, joined)
		}
	}
	if want := "play, pause"; !strings.HasPrefix(joined, want) {
		t.Errorf("the list reads %q; want it to start with %q", joined, want)
	}
	if strings.Contains(joined, ",,") {
		t.Errorf("the list lost a comma in the wrapping: %q", joined)
	}
}

// TestIconListReadsTheEmbeds pins that the list comes from what the app
// embeds, so an icon file the source does not use stays out of it.
func TestIconListReadsTheEmbeds(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app", "icons"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := "package main\n\n//go:embed icons/pin.svg\nvar pinSVG []byte\n\n//go:embed icons/play.svg\nvar playSVG []byte\n"
	if err := os.WriteFile(filepath.Join(root, "app", "icons.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "icons", "unused.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := iconList(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := "pin, play"; got != want {
		t.Fatalf("iconList = %q; want %q", got, want)
	}
}

// TestWrapListKeepsAShortListInOneLine checks nothing breaks where it fits.
func TestWrapListKeepsAShortListInOneLine(t *testing.T) {
	if got := wrapList([]string{"a", "b", "c"}); got != "a, b, c" {
		t.Fatalf("wrapList = %q", got)
	}
}
