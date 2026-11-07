package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/app/lyrics"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// fakeLyricsProvider is one source the lyrics UI tests control. onFetch
// answers per call where a test needs the first failure and the next answer.
type fakeLyricsProvider struct {
	id      string
	name    string
	doc     lyrics.Doc
	err     error
	calls   int
	onFetch func(call int) (lyrics.Doc, error)
}

func (f *fakeLyricsProvider) ID() string {
	if f.id != "" {
		return f.id
	}
	return f.name
}

func (f *fakeLyricsProvider) Name() string { return f.name }

func (f *fakeLyricsProvider) Fetch(lyrics.Query) (lyrics.Doc, error) {
	f.calls++
	if f.onFetch != nil {
		return f.onFetch(f.calls)
	}
	return f.doc, f.err
}

func timedLyricsDoc() lyrics.Doc {
	return lyrics.Doc{Timed: true, Lines: []lyrics.Line{
		{Text: "一行目", StartMS: 0, EndMS: 5000},
		{Text: "二行目", StartMS: 6000, EndMS: 10000},
		{Text: "三行目", StartMS: 11000, EndMS: 15000},
	}}
}

// lyricsUIApp is an app with a track at 6.5 seconds and a lyrics service
// over the test's providers, in a throwaway config directory.
func lyricsUIApp(t *testing.T, providers ...*fakeLyricsProvider) *nativeApp {
	t.Helper()
	app := newNativeApp(&Mylist{configDir: t.TempDir()})
	list := make([]lyrics.Provider, 0, len(providers))
	for _, provider := range providers {
		list = append(list, provider)
	}
	app.lyrics = lyrics.NewService(t.TempDir(), list...)
	app.player = playerState{
		WatchID: "sm1", VideoID: "sm1", Title: "歌詞の曲", Uploader: "投稿者",
		Ready: true, Playing: true, Volume: 1, PositionMS: 6500,
		DurationMS: 20000, LyricsKnown: true, HasLyrics: true,
	}
	return app
}

// openLyricsUI opens the lyrics panel and waits for the lookup to answer.
func openLyricsUI(t *testing.T, app *nativeApp) *ui.Tester {
	t.Helper()
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("歌詞を表示"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(300 * time.Millisecond)
	waitForApp(t, app, "the lyrics lookup", func() bool {
		return app.lyricsDoc != nil || app.lyricsErr != nil
	})
	tester.Frame()
	return tester
}

func TestNativeUILyricsFloatsOverTheQueue(t *testing.T) {
	provider := &fakeLyricsProvider{name: "niconico", doc: timedLyricsDoc()}
	app := lyricsUIApp(t, provider)
	tester := openLyricsUI(t, app)
	if !app.queueOpen || !app.lyricsOpen {
		t.Fatalf("the lyrics card did not open over the queue: queue=%v lyrics=%v", app.queueOpen, app.lyricsOpen)
	}
	title, ok := tester.Find("歌詞")
	if !ok {
		t.Fatalf("the lyrics card has no title: %q", tester.Texts())
	}
	queue, ok := tester.Find("キューは空です")
	if !ok {
		t.Fatalf("the queue is not shown: %q", tester.Texts())
	}
	if title.X <= queue.X {
		t.Fatalf("the lyrics card is not over the right of the queue: lyrics=%v queue=%v", title, queue)
	}
	if !tester.HasText("二行目") {
		t.Fatalf("the current line is not shown: %q", tester.Texts())
	}
	if !tester.HasText(locales.LyricsSource(lyrics.SourceNiconico)) {
		t.Fatalf("the source is not credited: %q", tester.Texts())
	}
	// A line-timed document shows lines by itself: there is no unit to pick.
	for _, label := range []string{"文字", "行", "全体"} {
		if _, ok := tester.Find(label); ok {
			t.Fatalf("the panel offered a manual unit %q: %q", label, tester.Texts())
		}
	}
	if provider.calls != 1 {
		t.Fatalf("the provider was called %d times; want the document reused", provider.calls)
	}
	if !app.lyricsSync.Load() {
		t.Fatal("a timed document did not ask for the fast position pull")
	}
}

// TestNativeUILyricsFollowsTheSidebar checks the card keeps step with the
// sidebar's fold: once the fold is over, the card is already where it ends,
// rather than sliding on for a while of its own.
func TestNativeUILyricsFollowsTheSidebar(t *testing.T) {
	provider := &fakeLyricsProvider{name: "niconico", doc: timedLyricsDoc()}
	app := lyricsUIApp(t, provider)
	tester := openLyricsUI(t, app)
	before, ok := tester.Find(locales.LyricsTitle())
	if !ok {
		t.Fatalf("the lyrics card has no title: %q", tester.Texts())
	}
	if err := tester.Click("サイドバーを開く"); err != nil {
		t.Fatal(err)
	}
	// The sidebar's fold is over; the card must be there with it.
	tester.Advance(sidebarShift.Duration + 20*time.Millisecond)
	moved, ok := tester.Find(locales.LyricsTitle())
	if !ok || moved.X <= before.X {
		t.Fatalf("the card did not follow the sidebar: before=%v moved=%v", before, moved)
	}
	tester.Advance(300 * time.Millisecond)
	settled, ok := tester.Find(locales.LyricsTitle())
	if !ok || moved.X != settled.X {
		t.Fatalf("the card kept moving after the sidebar settled: moved=%v settled=%v", moved, settled)
	}
}

// TestNativeUILyricsWordTimingShowsTheCharacterView checks the view the
// document picks: word timing shows the large character view, and line
// timing the line list.
// TestNativeUILyricsWordTimingUsesTheLineList checks a word-timed document
// takes the line list like every other timed document: its current line
// colors its words, and lines far from it stay in the list.
func TestNativeUILyricsWordTimingUsesTheLineList(t *testing.T) {
	doc := lyrics.Doc{Timed: true}
	for i := range 40 {
		doc.Lines = append(doc.Lines, lyrics.Line{
			Text:    fmt.Sprintf("歌詞の行 %d", i),
			StartMS: float64(i * 2000),
			EndMS:   float64(i*2000 + 1500),
		})
	}
	doc.Lines[3].Words = []lyrics.Word{
		{Text: "歌詞", StartMS: 6000, EndMS: 7000},
		{Text: "の行 3", StartMS: 7000, EndMS: 7500},
	}
	provider := &fakeLyricsProvider{name: "niconico", doc: doc}
	app := lyricsUIApp(t, provider)
	app.player.PositionMS = 6500
	tester := openLyricsUI(t, app)
	// The line list keeps lines far from the current one; a centered view
	// would show only the neighbors.
	if !tester.HasText(doc.Lines[0].Text) || !tester.HasText(doc.Lines[39].Text) {
		t.Fatalf("the word-timed document is not the line list: %q", tester.Texts())
	}
	if !tester.HasText("歌詞の行 3") {
		t.Fatalf("the current line is missing: %q", tester.Texts())
	}
	// The word-timed line seeks like any other line of the list.
	if err := tester.Click("歌詞の行 3"); err != nil {
		t.Fatal(err)
	}
	seek, ok := lastCommand(app.sent, "seek")
	if !ok || seek.PositionMS != 6000-lyricsSeekLeadMS {
		t.Fatalf("a word-timed line did not seek: %#v", app.sent)
	}
}

// TestNativeUILyricsClickSeeksToTheLine checks a clicked line jumps the
// player to a moment before its start, so the opening is heard.
func TestNativeUILyricsClickSeeksToTheLine(t *testing.T) {
	provider := &fakeLyricsProvider{name: "niconico", doc: timedLyricsDoc()}
	app := lyricsUIApp(t, provider)
	// Start at the top, so the click on the second line is a seek the bar
	// keeps, not one it already reads as landed.
	app.player.PositionMS = 0
	tester := openLyricsUI(t, app)
	if err := tester.Click("二行目"); err != nil {
		t.Fatal(err)
	}
	seek, ok := lastCommand(app.sent, "seek")
	if !ok || seek.PositionMS != 6000-lyricsSeekLeadMS {
		t.Fatalf("clicking a line did not seek before it: %#v", app.sent)
	}
	if !app.seekSettling || app.seekValue != seek.PositionMS {
		t.Fatalf("the seek was not taken by the player bar: value=%v settling=%v", app.seekValue, app.seekSettling)
	}
	// The first line starts at the song's beginning, so its lead clamps.
	if err := tester.Click("一行目"); err != nil {
		t.Fatal(err)
	}
	seek, ok = lastCommand(app.sent, "seek")
	if !ok || seek.PositionMS != 0 {
		t.Fatalf("the first line did not seek to the start: %#v", app.sent)
	}
}

// TestNativeUILyricsOffsetShiftsTheTiming checks the footer's number and
// stepper: a click on an arrow and a key step change the line timing at once,
// and the shift is kept in the lyrics cache.
func TestNativeUILyricsOffsetShiftsTheTiming(t *testing.T) {
	provider := &fakeLyricsProvider{name: "niconico", doc: timedLyricsDoc()}
	app := lyricsUIApp(t, provider)
	tester := openLyricsUI(t, app)
	stepper, ok := tester.Find(locales.LyricsOffset())
	if !ok {
		t.Fatalf("the card has no offset stepper: %q", tester.Texts())
	}
	// A click on the number takes the keyboard, as the field did, even
	// before the stepper has the focus.
	app.lyricsOffsetMS = 1300
	tester.Frame()
	if err := tester.Click("1300"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyUp)
	tester.Frame()
	if app.lyricsOffsetMS != 1400 {
		t.Fatalf("offset = %v; want a key step after the number's click", app.lyricsOffsetMS)
	}
	if !tester.HasText("1400") {
		t.Fatalf("the field shows %q; want 1400", tester.Texts())
	}
	if got := app.lyrics.Offset("sm1", "niconico", app.lyricsID); got != 1400 {
		t.Fatalf("saved offset = %d; want 1400", got)
	}
	// A click on an arrow steps and keeps the shift.
	tester.ClickAt(stepper.X+stepper.W/2, stepper.Y+stepper.H/4)
	tester.Frame()
	if app.lyricsOffsetMS != 1500 {
		t.Fatalf("offset = %v; want a 100 ms step", app.lyricsOffsetMS)
	}
	tester.ClickAt(stepper.X+stepper.W/2, stepper.Y+stepper.H*3/4)
	tester.Frame()
	if app.lyricsOffsetMS != 1400 {
		t.Fatalf("offset = %v; want a step back to 1400", app.lyricsOffsetMS)
	}
	if got := app.lyrics.Offset("sm1", "niconico", app.lyricsID); got != 1400 {
		t.Fatalf("saved offset = %d; want 1400", got)
	}
	// The shift moves both the line the card follows and the seek of a
	// clicked one.
	app.player.PositionMS = 6100
	app.lyricsOffsetMS = 600
	tester.Frame()
	if app.lyricsFollow != 0 {
		t.Fatalf("the card follows line %d; want the first with a 600 ms shift", app.lyricsFollow)
	}
	if err := tester.Click("二行目"); err != nil {
		t.Fatal(err)
	}
	seek, ok := lastCommand(app.sent, "seek")
	if !ok || seek.PositionMS != 6000+600-lyricsSeekLeadMS {
		t.Fatalf("the shifted line did not seek: %#v", app.sent)
	}
	// Another track keeps its own shift.
	app.player.WatchID, app.player.VideoID, app.player.Title = "sm2", "sm2", "次の曲"
	tester.Frame()
	waitForApp(t, app, "the next track's lyrics", func() bool {
		return app.lyricsVideoID == "sm2" && !app.lyricsLoading && app.lyricsDoc != nil
	})
	if app.lyricsOffsetMS != 0 {
		t.Fatalf("the next track came back with a %v ms shift", app.lyricsOffsetMS)
	}
	// The saved shift comes back with its track and source.
	app.player.WatchID, app.player.VideoID, app.player.Title = "sm1", "sm1", "歌詞の曲"
	tester.Frame()
	waitForApp(t, app, "the first track's lyrics", func() bool {
		return app.lyricsVideoID == "sm1" && !app.lyricsLoading && app.lyricsDoc != nil
	})
	if app.lyricsOffsetMS != 1400 {
		t.Fatalf("the first track came back with a %v ms shift; want the saved 1400", app.lyricsOffsetMS)
	}
}

// TestNativeUILyricsCardFitsTheMinimumWidth checks the card and the queue
// behind it stay inside the narrowest window.
func TestNativeUILyricsCardFitsTheMinimumWidth(t *testing.T) {
	provider := &fakeLyricsProvider{name: "niconico", doc: timedLyricsDoc()}
	app := lyricsUIApp(t, provider)
	tester := ui.NewTester(app.view, 760, 560)
	if err := tester.Click("歌詞を表示"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(300 * time.Millisecond)
	waitForApp(t, app, "the lyrics lookup", func() bool {
		return app.lyricsDoc != nil || app.lyricsErr != nil
	})
	tester.Frame()
	for _, label := range []string{"再生キュー", "歌詞", locales.LyricsSource(lyrics.SourceNiconico), locales.LyricsOffset(), "ms"} {
		bounds, ok := tester.Find(label)
		if !ok || bounds.X < 0 || bounds.X+bounds.W > 760 {
			t.Fatalf("%q is missing or outside the minimum-width window: bounds=%v", label, bounds)
		}
	}
}

// TestNativeUILyricsGlidesToTheLine checks the line view eases to the line
// playing rather than jumping to it.
func TestNativeUILyricsGlidesToTheLine(t *testing.T) {
	doc := lyrics.Doc{Timed: true}
	for i := range 40 {
		doc.Lines = append(doc.Lines, lyrics.Line{
			Text:    fmt.Sprintf("歌詞の行 %d", i),
			StartMS: float64(i * 2000),
			EndMS:   float64(i*2000 + 1500),
		})
	}
	provider := &fakeLyricsProvider{name: "niconico", doc: doc}
	app := lyricsUIApp(t, provider)
	app.player.PositionMS = 0
	tester := openLyricsUI(t, app)
	// Let the initial reveal settle.
	tester.Frame()
	tester.Advance(500 * time.Millisecond)
	if tester.FramePending() {
		t.Fatalf("the line view did not settle at the start")
	}
	app.player.PositionMS = 20 * 2000
	tester.Frame()
	if !tester.FramePending() {
		t.Fatalf("the line view jumped to the line playing instead of gliding")
	}
	before := app.lyricsScroll.Y
	tester.Advance(130 * time.Millisecond)
	if app.lyricsScroll.Y <= before {
		t.Fatalf("the glide did not move: %v then %v", before, app.lyricsScroll.Y)
	}
	tester.Advance(1000 * time.Millisecond)
	if tester.FramePending() {
		t.Fatalf("the glide did not settle")
	}
}

// TestNativeUILyricsCentersTheLineAfterAQuickChange checks a line that takes
// over while the list is still gliding retargets the reveal to its center.
func TestNativeUILyricsCentersTheLineAfterAQuickChange(t *testing.T) {
	doc := lyrics.Doc{Timed: true}
	for i := range 40 {
		doc.Lines = append(doc.Lines, lyrics.Line{
			Text:    fmt.Sprintf("歌詞の行 %d", i),
			StartMS: float64(i * 2000),
			EndMS:   float64(i*2000 + 1500),
		})
	}
	provider := &fakeLyricsProvider{name: "niconico", doc: doc}
	app := lyricsUIApp(t, provider)
	app.player.PositionMS = 0
	tester := openLyricsUI(t, app)
	tester.Frame()
	tester.Advance(600 * time.Millisecond)
	// The next line takes over while the list is still moving.
	app.player.PositionMS = 20 * 2000
	tester.Frame()
	tester.Advance(80 * time.Millisecond)
	app.player.PositionMS = 21 * 2000
	tester.Frame()
	tester.Advance(900 * time.Millisecond)
	tester.Frame()
	if tester.FramePending() {
		t.Fatalf("the list did not settle")
	}
	bounds, ok := tester.Find(doc.Lines[21].Text)
	if !ok {
		t.Fatalf("the current line is not shown: %q", tester.Texts())
	}
	center := bounds.Y + bounds.H/2
	want := app.lyricsViewY + app.lyricsViewH/2
	if diff := center - want; diff < -2 || diff > 2 {
		t.Fatalf("the current line is %v DIPs from the middle", diff)
	}
}

func TestNativeUILyricsManualScrollWaitsForNextLine(t *testing.T) {
	doc := lyrics.Doc{Timed: true}
	for i := range 40 {
		doc.Lines = append(doc.Lines, lyrics.Line{
			Text:    fmt.Sprintf("歌詞の行 %d", i),
			StartMS: float64(i * 2000),
			EndMS:   float64(i*2000 + 1500),
		})
	}
	provider := &fakeLyricsProvider{name: "niconico", doc: doc}
	app := lyricsUIApp(t, provider)
	app.player.PositionMS = 0
	tester := openLyricsUI(t, app)
	tester.Advance(500 * time.Millisecond)

	tester.Scroll(800, app.lyricsViewY+app.lyricsViewH/2, 0, 400)
	manualY := app.lyricsScroll.Y
	if manualY <= 0 {
		t.Fatal("the lyrics did not scroll manually")
	}
	tester.Frame()
	if app.lyricsScroll.Y != manualY {
		t.Fatalf("the current line pulled the user's scroll from %v to %v", manualY, app.lyricsScroll.Y)
	}

	app.player.PositionMS = 2000
	tester.Frame()
	if !tester.FramePending() {
		t.Fatal("the next line did not resume following")
	}
	tester.Advance(130 * time.Millisecond)
	if app.lyricsScroll.Y == manualY {
		t.Fatal("following the next line did not move the scroll")
	}
}

func TestNativeUILyricsUntimedShowsTheWhole(t *testing.T) {
	provider := &fakeLyricsProvider{name: "niconico", doc: lyrics.Doc{Lines: []lyrics.Line{
		{Text: "一行目"},
		{Text: "二行目"},
	}}}
	app := lyricsUIApp(t, provider)
	tester := openLyricsUI(t, app)
	if !tester.HasText(locales.LyricsUntimed()) {
		t.Fatalf("the panel does not say the lyrics are untimed: %q", tester.Texts())
	}
	if _, ok := tester.Find("文字"); ok {
		t.Fatal("an untimed document offered a unit it cannot follow")
	}
	if !tester.HasText("一行目") || !tester.HasText("二行目") {
		t.Fatalf("the whole document is not shown: %q", tester.Texts())
	}
	if app.lyricsSync.Load() {
		t.Fatal("an untimed document asked for the fast position pull")
	}
}

func TestNativeUILyricsNoLyrics(t *testing.T) {
	provider := &fakeLyricsProvider{name: "niconico", err: lyrics.ErrNotFound}
	app := lyricsUIApp(t, provider)
	app.player.HasLyrics = false
	tester := openLyricsUI(t, app)
	if !tester.HasText(locales.LyricsNotFoundSource()) {
		t.Fatalf("the panel does not say no lyrics were found: %q", tester.Texts())
	}
}

// TestNativeUILyricsTabsFollowTheSource checks the three provider tabs: the
// automatic order picks the source and its tab, a clicked tab asks that
// source alone, and a new track starts from the automatic order again.
func TestNativeUILyricsTabsFollowTheSource(t *testing.T) {
	nico := &fakeLyricsProvider{id: "niconico", name: "niconico", err: lyrics.ErrNotFound}
	kugou := &fakeLyricsProvider{id: "kugou", name: "KuGou", doc: timedLyricsDoc()}
	netease := &fakeLyricsProvider{id: "netease", name: "NetEase", doc: timedLyricsDoc()}
	app := lyricsUIApp(t, nico, kugou, netease)
	tester := openLyricsUI(t, app)
	if _, ok := tester.Find("自動"); ok {
		t.Fatal("the tabs offer an automatic mode; the three providers are the tabs")
	}
	if nico.calls != 1 || kugou.calls != 1 || netease.calls != 0 {
		t.Fatalf("calls = %d, %d, %d; want the automatic order to stop at KuGou", nico.calls, kugou.calls, netease.calls)
	}
	if app.lyricsTab != 1 || app.lyricsSource != "" {
		t.Fatalf("tab = %d, source = %q; want the KuGou tab in automatic mode", app.lyricsTab, app.lyricsSource)
	}
	if !tester.HasText(locales.LyricsSource("KuGou")) {
		t.Fatalf("the footer does not credit KuGou: %q", tester.Texts())
	}

	// A clicked tab asks one source alone.
	if err := tester.Click("NetEase"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	waitForApp(t, app, "the NetEase lyrics", func() bool {
		return app.lyricsVideoID == "sm1" && !app.lyricsLoading && app.lyricsDoc != nil &&
			app.lyricsDoc.Source == "netease"
	})
	tester.Frame()
	if nico.calls != 1 || kugou.calls != 1 || netease.calls != 1 {
		t.Fatalf("calls = %d, %d, %d; want the clicked source alone", nico.calls, kugou.calls, netease.calls)
	}
	if app.lyricsTab != 2 || app.lyricsSource != "netease" {
		t.Fatalf("tab = %d, source = %q; want the NetEase tab chosen", app.lyricsTab, app.lyricsSource)
	}
	if !tester.HasText(locales.LyricsSource("NetEase")) {
		t.Fatalf("the footer does not credit NetEase: %q", tester.Texts())
	}

	// The chosen source stays while the track does.
	if err := tester.Click("歌詞を閉じる"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("歌詞を表示"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if app.lyricsSource != "netease" || app.lyricsDoc == nil || app.lyricsDoc.Source != "netease" {
		t.Fatalf("reopening lost the chosen source: source=%q doc=%#v", app.lyricsSource, app.lyricsDoc)
	}
	if netease.calls != 1 {
		t.Fatalf("reopening fetched the same source again: %d calls", netease.calls)
	}

	// A new track starts from the automatic order.
	app.player.WatchID, app.player.VideoID, app.player.Title = "sm2", "sm2", "次の曲"
	tester.Frame()
	waitForApp(t, app, "the next track's lyrics", func() bool {
		return app.lyricsVideoID == "sm2" && !app.lyricsLoading && app.lyricsDoc != nil
	})
	if app.lyricsSource != "" || app.lyricsTab != 1 {
		t.Fatalf("the next track kept the manual source: tab = %d, source = %q", app.lyricsTab, app.lyricsSource)
	}
	if nico.calls != 2 || kugou.calls != 2 || netease.calls != 1 {
		t.Fatalf("calls = %d, %d, %d; want the automatic order on the next track", nico.calls, kugou.calls, netease.calls)
	}
}

// TestNativeUILyricsTabsFitTheMinimumWidth checks the three provider tabs
// stay inside the card in the narrowest window.
func TestNativeUILyricsTabsFitTheMinimumWidth(t *testing.T) {
	nico := &fakeLyricsProvider{id: "niconico", name: "niconico", doc: timedLyricsDoc()}
	kugou := &fakeLyricsProvider{id: "kugou", name: "KuGou", err: lyrics.ErrNotFound}
	netease := &fakeLyricsProvider{id: "netease", name: "NetEase", err: lyrics.ErrNotFound}
	app := lyricsUIApp(t, nico, kugou, netease)
	tester := ui.NewTester(app.view, 760, 560)
	if err := tester.Click("歌詞を表示"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(300 * time.Millisecond)
	waitForApp(t, app, "the lyrics lookup", func() bool {
		return app.lyricsDoc != nil || app.lyricsErr != nil
	})
	tester.Frame()
	for _, label := range []string{"歌詞", "niconico", "KuGou", "NetEase"} {
		bounds, ok := tester.Find(label)
		if !ok || bounds.X < 0 || bounds.X+bounds.W > 760 {
			t.Fatalf("%q is missing or outside the minimum-width window: bounds=%v", label, bounds)
		}
	}
}

func TestNativeUILyricsRetriesAfterLogin(t *testing.T) {
	provider := &fakeLyricsProvider{name: "niconico"}
	provider.onFetch = func(call int) (lyrics.Doc, error) {
		if call == 1 {
			return lyrics.Doc{}, lyrics.ErrLogin
		}
		return timedLyricsDoc(), nil
	}
	app := lyricsUIApp(t, provider)
	app.loggedIn = false
	tester := openLyricsUI(t, app)
	if !tester.HasText(locales.LyricsLogin()) {
		t.Fatalf("the panel does not ask for a sign-in: %q", tester.Texts())
	}
	if _, ok := tester.Find(locales.LoginWithNiconico()); !ok {
		t.Fatalf("the panel offers no sign-in: %q", tester.Texts())
	}
	// Signing in retries by itself, once.
	app.loggedIn = true
	tester.Frame()
	waitForApp(t, app, "the lyrics after signing in", func() bool { return app.lyricsDoc != nil })
	tester.Frame()
	if provider.calls != 2 {
		t.Fatalf("the provider was called %d times; want one retry", provider.calls)
	}
	if !tester.HasText("二行目") {
		t.Fatalf("the lyrics did not show after signing in: %q", tester.Texts())
	}
}

// TestNativeUILyricsKeepsTheSessionOnAnExpiredOne checks that a lyrics source
// refusing the session asks for a sign-in and leaves the app's own session
// alone: the rest of the app keeps playing as the account.
func TestNativeUILyricsKeepsTheSessionOnAnExpiredOne(t *testing.T) {
	provider := &fakeLyricsProvider{name: "niconico", err: lyrics.ErrUnauthorized}
	app := lyricsUIApp(t, provider)
	app.loggedIn = true
	if err := app.session.Save("user_session=abc"); err != nil {
		t.Fatal(err)
	}
	tester := openLyricsUI(t, app)
	if !app.loggedIn {
		t.Fatal("the lyrics failure signed the app out")
	}
	if cookies, err := app.session.Load(); err != nil || cookies == "" {
		t.Fatalf("the lyrics failure forgot the session: %q, %v", cookies, err)
	}
	if !tester.HasText(locales.ErrSessionExpired()) {
		t.Fatalf("the card does not say the session expired: %q", tester.Texts())
	}
	if _, ok := tester.Find(locales.LoginWithNiconico()); !ok {
		t.Fatalf("the card offers no sign-in: %q", tester.Texts())
	}
	// The same session is not asked again on every frame.
	if provider.calls != 1 {
		t.Fatalf("the provider was called %d times; want the failure kept", provider.calls)
	}
}

// TestNativeUILyricsRetriesAfterSigningInAgain checks the retry the expired
// session's sign-in brings: the card asks again once the account is back.
func TestNativeUILyricsRetriesAfterSigningInAgain(t *testing.T) {
	provider := &fakeLyricsProvider{name: "niconico"}
	provider.onFetch = func(call int) (lyrics.Doc, error) {
		if call == 1 {
			return lyrics.Doc{}, lyrics.ErrUnauthorized
		}
		return timedLyricsDoc(), nil
	}
	app := lyricsUIApp(t, provider)
	app.loggedIn = true
	tester := openLyricsUI(t, app)
	if provider.calls != 1 {
		t.Fatalf("the provider was called %d times; want one lookup", provider.calls)
	}
	app.loginSucceeded(locales.StatusLoggedIn())
	tester.Frame()
	waitForApp(t, app, "the lyrics after signing in again", func() bool { return app.lyricsDoc != nil })
	if provider.calls != 2 {
		t.Fatalf("the provider was called %d times; want one retry", provider.calls)
	}
}

// TestNativeUILyricsHidesQueueAudioAndLength checks the queue beside the
// lyrics: the title takes the room, and the save mark and length wait until
// the lyrics close.
func TestNativeUILyricsHidesQueueAudioAndLength(t *testing.T) {
	provider := &fakeLyricsProvider{name: "niconico", doc: timedLyricsDoc()}
	app := lyricsUIApp(t, provider)
	app.player.MylistID = "111"
	app.player.Name = "作業用"
	app.player.Order = []string{"sm2"}
	app.player.Items = []MylistItem{{
		WatchID: "sm2", Title: "キューの長い曲名", Uploader: "投稿者", Available: true, Duration: 120,
	}}
	app.player.QueueIndex = 0
	// A saved audio file marks the row; the mark has no business beside the
	// lyrics either.
	path, err := app.mylist.audioPath("sm2")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("saved"), 0o644); err != nil {
		t.Fatal(err)
	}
	tester := openLyricsUI(t, app)
	if !tester.HasText("キューの長い曲名") {
		t.Fatalf("the queue row is missing: %q", tester.Texts())
	}
	if tester.HasText("2:00") {
		t.Fatalf("the queue still shows the length beside the lyrics: %q", tester.Texts())
	}
	if _, ok := tester.Find(locales.AudioSavedLabel()); ok {
		t.Fatalf("the queue still shows the save mark beside the lyrics: %q", tester.Texts())
	}
	// Closing the lyrics gives the queue its marks and length back.
	if err := tester.Click("歌詞を閉じる"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("2:00") {
		t.Fatalf("the queue did not bring the length back: %q", tester.Texts())
	}
	if _, ok := tester.Find(locales.AudioSavedLabel()); !ok {
		t.Fatalf("the queue did not bring the save mark back: %q", tester.Texts())
	}
}

// TestLeadFirstLyricLine checks the official first line's lead: only the
// first line moves, the lines after it keep their timing, and another source
// is left alone. The starts are the three files this was found in.
func TestLeadFirstLyricLine(t *testing.T) {
	for _, c := range []struct {
		name string
		at   float64
		want float64
	}{
		{"sm46162690", 1215, 215},
		{"sm45960692", 810, 0},
		{"sm46212145", 1377, 377},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc := lyrics.Doc{Source: lyrics.SourceNiconico, Timed: true, Lines: []lyrics.Line{
				{Text: "first", StartMS: c.at, EndMS: c.at + 3000},
				{Text: "second", StartMS: 9000, EndMS: 12000},
			}}
			leadFirstLyricLine(&doc)
			if doc.Lines[0].StartMS != c.want {
				t.Fatalf("first line starts at %v; want %v", doc.Lines[0].StartMS, c.want)
			}
			if doc.Lines[1].StartMS != 9000 {
				t.Fatalf("the second line moved to %v", doc.Lines[1].StartMS)
			}
		})
	}

	// A word-timed first line moves its words with it.
	worded := lyrics.Doc{Source: lyrics.SourceNiconico, Timed: true, Lines: []lyrics.Line{{
		Text: "first", StartMS: 1215, EndMS: 4000,
		Words: []lyrics.Word{{Text: "first", StartMS: 1215, EndMS: 3000}},
	}}}
	leadFirstLyricLine(&worded)
	if worded.Lines[0].Words[0].StartMS != 215 || worded.Lines[0].Words[0].EndMS != 2000 {
		t.Fatalf("the first line's word = %#v; want it moved with the line", worded.Lines[0].Words[0])
	}

	// Another source, and an untimed document, keep their data.
	other := lyrics.Doc{Source: "kugou", Timed: true, Lines: []lyrics.Line{{Text: "first", StartMS: 1215}}}
	leadFirstLyricLine(&other)
	if other.Lines[0].StartMS != 1215 {
		t.Fatalf("another source moved to %v", other.Lines[0].StartMS)
	}
	untimed := lyrics.Doc{Source: lyrics.SourceNiconico, Lines: []lyrics.Line{{Text: "first"}}}
	leadFirstLyricLine(&untimed)
	if untimed.Lines[0].StartMS != 0 {
		t.Fatalf("an untimed document moved to %v", untimed.Lines[0].StartMS)
	}
}

// TestLyricSpansFollowThePosition checks the character and word spans the
// spotlight colors: the part played is white, the rest muted.
func TestLyricSpansFollowThePosition(t *testing.T) {
	app := newNativeApp(&Mylist{configDir: t.TempDir()})
	// A line the source timed only as a whole keeps one color: no guessed
	// character split.
	line := lyrics.Line{Text: "あいうえお", StartMS: 1000, EndMS: 5000}
	app.player.PositionMS = 3000
	spans := app.lyricSpans(line)
	if len(spans) != 1 || spans[0].Text != "あいうえお" || spans[0].Color != app.theme.Text {
		t.Fatalf("spans = %#v; want the whole line in one color", spans)
	}

	words := lyrics.Line{
		Text: "Hello world", StartMS: 0, EndMS: 10000,
		Words: []lyrics.Word{
			{Text: "Hello ", StartMS: 0, EndMS: 5000},
			{Text: "world", StartMS: 5000, EndMS: 10000},
		},
	}
	app.player.PositionMS = 1000
	spans = app.lyricSpans(words)
	if len(spans) != 2 || spans[0].Color != app.theme.Text || spans[1].Color != app.theme.TextMuted {
		t.Fatalf("word spans at 1s = %#v; want the first word white", spans)
	}
	app.player.PositionMS = 6000
	spans = app.lyricSpans(words)
	if len(spans) != 2 || spans[0].Color != app.theme.Text || spans[1].Color != app.theme.Text {
		t.Fatalf("word spans at 6s = %#v; want both words white", spans)
	}
	// The card's offset moves the position the words color to.
	app.lyricsOffsetMS = 5000
	spans = app.lyricSpans(words)
	if len(spans) != 2 || spans[0].Color != app.theme.Text || spans[1].Color != app.theme.TextMuted {
		t.Fatalf("word spans with a shift = %#v; want the first word white", spans)
	}
}
