package main

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// TestTrayCardShowsTheTrack checks the card the icon's right click opens:
// the track playing, how far it has come, and the buttons, whose commands
// are the player bar's.
func TestTrayCardShowsTheTrack(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.player = playerState{
		WatchID: "sm1", Title: "夜に駆ける", Uploader: "YOASOBI", Ready: true, Playing: true,
		PositionMS: 47_000, DurationMS: 261_000, Quality: "320kbps", Volume: 1,
	}
	tester := ui.NewTester(app.trayCardView, trayCardWidth, trayCardHeight)
	for _, want := range []string{"夜に駆ける", "YOASOBI", "0:47 / 4:21", "320kbps"} {
		if !tester.HasText(want) {
			t.Fatalf("the card does not show %q: %q", want, tester.Texts())
		}
	}
	// Everything the card carries has to fit it: a flyout does not scroll.
	for _, label := range []string{"夜に駆ける", "前へ", "一時停止", "次へ", "ミュート", "ウィンドウを表示", "終了"} {
		box, ok := tester.Find(label)
		if !ok {
			t.Fatalf("the card has no %q: %q", label, tester.Texts())
		}
		if bottom := box.Y + box.H; bottom > trayCardHeight {
			t.Fatalf("%q ends at %v, past the card's %d", label, bottom, trayCardHeight)
		}
		if right := box.X + box.W; right > trayCardWidth {
			t.Fatalf("%q ends at %v, past the card's %d", label, right, trayCardWidth)
		}
	}

	app.sent = nil
	if err := tester.Click("一時停止"); err != nil {
		t.Fatal(err)
	}
	if !hasCommand(app.sent, "pause") {
		t.Fatalf("the card's pause was not sent: %#v", app.sent)
	}
	app.player.Playing = false
	tester.Frame()
	app.sent = nil
	if err := tester.Click("再生"); err != nil {
		t.Fatal(err)
	}
	if !hasCommand(app.sent, "play") {
		t.Fatalf("the card's play was not sent: %#v", app.sent)
	}
	app.player.CanNext = true
	tester.Frame()
	app.sent = nil
	if err := tester.Click("次へ"); err != nil {
		t.Fatal(err)
	}
	if !hasCommand(app.sent, "next") {
		t.Fatalf("the card's next was not sent: %#v", app.sent)
	}
	app.player.CanNext = false
	tester.Frame()
	if app.canNext() {
		t.Fatal("canNext is true with nothing next to play")
	}
}

// TestTrayCardWithoutATrack checks the card with nothing playing: the idle
// mark and its lines, no transport that would do nothing, and the way out
// kept.
func TestTrayCardWithoutATrack(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.trayCardView, trayCardWidth, trayCardHeight)
	for _, text := range []string{"再生していません", "マイリストから曲を選んで再生してください"} {
		if !tester.HasText(text) {
			t.Fatalf("the empty card = %q; want %q", tester.Texts(), text)
		}
	}
	for _, label := range []string{"ウィンドウを表示", "ミュート", "終了"} {
		box, ok := tester.Find(label)
		if !ok {
			t.Fatalf("the empty card has no %q: %q", label, tester.Texts())
		}
		if bottom := box.Y + box.H; bottom > trayCardHeight {
			t.Fatalf("%q ends at %v, past the card's %d", label, bottom, trayCardHeight)
		}
	}
	app.sent = nil
	if err := tester.Click("再生"); err == nil && hasCommand(app.sent, "play") {
		t.Fatalf("the card's play works with no track: %#v", app.sent)
	}
	app.sent = nil
	if err := tester.Click("次へ"); err == nil && hasCommand(app.sent, "next") {
		t.Fatalf("the card's next works with no track: %#v", app.sent)
	}
}

// TestTrayCardTogglesMute checks the card's way into and out of silence.
func TestTrayCardTogglesMute(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.player.Volume, app.volumeValue = 0.6, 0.6

	tester := ui.NewTester(app.trayCardView, trayCardWidth, trayCardHeight)
	if err := tester.Click("ミュート"); err != nil {
		t.Fatal(err)
	}
	if !app.muted || app.volumeValue != 0 {
		t.Fatalf("the card did not mute the player: muted=%v volume=%v", app.muted, app.volumeValue)
	}

	app.sent = nil
	tester.Frame()
	if err := tester.Click("ミュート解除"); err != nil {
		t.Fatal(err)
	}
	volume, ok := lastCommand(app.sent, "volume")
	if app.muted || !ok || volume.Volume != 0.6 {
		t.Fatalf("unmuting did not come back to the mute's level: %#v muted=%v", app.sent, app.muted)
	}
}

// TestTrayCardCornersAreTransparent checks the card's rounding: its window
// is transparent, so the corners must be left untouched for what is behind
// them to show, rather than a square of the theme's background.
func TestTrayCardCornersAreTransparent(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.trayCardView, trayCardWidth, trayCardHeight)
	img := tester.Image()
	b := img.Bounds()
	for _, p := range [][2]int{
		{b.Min.X, b.Min.Y}, {b.Max.X - 1, b.Min.Y},
		{b.Min.X, b.Max.Y - 1}, {b.Max.X - 1, b.Max.Y - 1},
	} {
		if a := img.RGBAAt(p[0], p[1]).A; a != 0 {
			t.Errorf("the corner at %v is opaque: a square shows behind the card", p)
		}
	}
	// The middle of the card's own surface, in the padding above the
	// content, is what an opaque card shows there.
	if a := img.RGBAAt(b.Min.X+b.Dx()/2, b.Min.Y+4).A; a != 255 {
		t.Error("the card's surface is see-through")
	}
}

// TestTrayHidesTheWindowOnlyWithATrayToReturnFrom checks the two cases that
// must still close the window: no tray was created, and the app is quitting.
func TestTrayHidesTheWindowOnlyWithATrayToReturnFrom(t *testing.T) {
	app := newNativeApp(NewMylist())
	if app.hideOnClose() {
		t.Fatal("a close hides the window without a tray to come back from")
	}
	app.tray = &mygo.Tray{}
	if !app.hideOnClose() {
		t.Fatal("a close does not hide the window with a tray up")
	}
	app.quitting = true
	if app.hideOnClose() {
		t.Fatal("a quit was turned into a hide")
	}
}

// TestTrayIconIsDrawnPerPlayState checks the icon itself: 32 pixels square,
// drawn rather than blank, and different while a track plays.
func TestTrayIconIsDrawnPerPlayState(t *testing.T) {
	playing, err := png.Decode(bytes.NewReader(trayIconPNG(true)))
	if err != nil {
		t.Fatal(err)
	}
	paused, err := png.Decode(bytes.NewReader(trayIconPNG(false)))
	if err != nil {
		t.Fatal(err)
	}
	if b := playing.Bounds(); b.Dx() != trayIconSize || b.Dy() != trayIconSize {
		t.Fatalf("the icon is %v; want %d pixels square", b, trayIconSize)
	}
	// A mark, not a blank canvas and not a filled square.
	if ink := trayIconSize*trayIconSize - transparentPixels(paused); ink < 100 || ink > trayIconSize*trayIconSize/2 {
		t.Fatalf("the icon draws %d of %d pixels; want a mark", ink, trayIconSize*trayIconSize)
	}
	if bytes.Equal(trayIconPNG(true), trayIconPNG(false)) {
		t.Fatal("both play states share one icon")
	}
}

func transparentPixels(img image.Image) int {
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a == 0 {
				n++
			}
		}
	}
	return n
}

// TestPlayRatioBounds checks the bar the card draws, whose value must stay
// within the 0 to 1 the progress bar takes, however odd the report is.
func TestPlayRatioBounds(t *testing.T) {
	app := newNativeApp(NewMylist())
	cases := []struct {
		position, duration float64
		want               float64
	}{
		{47_000, 261_000, 0.18},
		{200_000, 100_000, 1},
		{-5, 100_000, 0},
		{47_000, 0, 0},
	}
	for _, tc := range cases {
		app.player.PositionMS, app.player.DurationMS = tc.position, tc.duration
		if got := app.playRatio(); math.Abs(got-tc.want) > 0.01 {
			t.Errorf("playRatio(%v, %v) = %v; want %v", tc.position, tc.duration, got, tc.want)
		}
	}
}
