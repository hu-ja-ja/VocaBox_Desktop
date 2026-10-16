package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// fakeHelper records what a playback sends it, and lets a test fire events
// and an exit into the engine.
type fakeHelper struct {
	mu       sync.Mutex
	commands []helperCommand
	onEvent  func(helperEvent)
	onExit   func(error)
}

func (h *fakeHelper) send(command helperCommand) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.commands = append(h.commands, command)
	return nil
}

func (h *fakeHelper) shutdown() {}

func (h *fakeHelper) all() []helperCommand {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]helperCommand(nil), h.commands...)
}

func (h *fakeHelper) count(action string) int {
	n := 0
	for _, command := range h.all() {
		if command.Cmd == action {
			n++
		}
	}
	return n
}

func (h *fakeHelper) last(action string) (helperCommand, bool) {
	commands := h.all()
	for i := len(commands) - 1; i >= 0; i-- {
		if commands[i].Cmd == action {
			return commands[i], true
		}
	}
	return helperCommand{}, false
}

// newTestPlayback is a playback whose helper and acquisition are stubs and
// whose events run inline: engine tests need no process and no network.
func newTestPlayback(t *testing.T) (*playback, *fakeHelper, *int) {
	t.Helper()
	mylist := NewMylist()
	// The engine may forget a stored session; tests never touch the real
	// config directory.
	mylist.configDir = t.TempDir()
	app := newNativeApp(mylist)
	p := newPlayback(app)
	app.playback = p
	helper := &fakeHelper{}
	spawns := 0
	p.spawn = func(onEvent func(helperEvent), onExit func(error)) (helperConn, error) {
		spawns++
		helper.mu.Lock()
		helper.onEvent, helper.onExit = onEvent, onExit
		helper.mu.Unlock()
		return helper, nil
	}
	p.acquire = func(item MylistItem) (playbackSources, error) {
		return playbackSources{
			URIs:      []string{"https://x/hi.m3u8", "https://x/lo.m3u8"},
			Cookie:    "domand_bid=1",
			UserAgent: "ua",
		}, nil
	}
	return p, helper, &spawns
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// waitForApp waits for a condition the app's own work produces: it runs the
// work the background goroutines handed over before each check, as a frame
// build does, the test having no window to run it.
func waitForApp(t *testing.T, app *nativeApp, what string, ok func() bool) {
	t.Helper()
	waitFor(t, what, func() bool {
		app.runHeld()
		return ok()
	})
}

func testQueueItems() []MylistItem {
	return []MylistItem{
		{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120},
		{WatchID: "sm2", Title: "二曲目", Available: true, Duration: 120},
	}
}

// helperPlay is the last play command the helper received.
func helperPlay(t *testing.T, h *fakeHelper) helperCommand {
	t.Helper()
	play, ok := h.last("play")
	if !ok {
		t.Fatal("the helper received no play command")
	}
	return play
}

// selectPlayed starts item's track and waits for the helper to receive its
// play command.
func selectPlayed(t *testing.T, p *playback, helper *fakeHelper, sm string) helperCommand {
	t.Helper()
	p.selectTrack(sm)
	waitFor(t, "the play command", func() bool { return helper.count("play") == 1 })
	return helperPlay(t, helper)
}

func TestPlaybackSelectAcquiresAndPlaysTopBand(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm1")

	if play.URL != "https://x/hi.m3u8" || play.Cookie != "domand_bid=1" || play.UA != "ua" {
		t.Fatalf("play = %#v", play)
	}
	if !play.Play {
		t.Fatal("the track playing must ask the helper to play")
	}
	// The level goes over too, so a restarted helper hears it before playing.
	volume, ok := helper.last("volume")
	if !ok || volume.Value != 1 {
		t.Fatalf("volume = %#v, %v", volume, ok)
	}
	if p.app.player.WatchID != "sm1" || p.app.player.Title != "一曲目" {
		t.Fatalf("player = %#v", p.app.player)
	}
	if p.app.player.Ready || p.app.player.Playing {
		t.Fatal("a track still opening is neither ready nor playing")
	}
	if got := p.app.player.Order; len(got) != 2 || got[0] != "sm1" {
		t.Fatalf("order = %v", got)
	}
	if p.app.player.QueueIndex != 0 {
		t.Fatalf("queue position = %d", p.app.player.QueueIndex)
	}
}

func TestPlaybackOpenedReportsDuration(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm1")

	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000, Gen: play.Gen})
	if !p.app.player.Ready || !p.app.player.Playing || p.app.player.DurationMS != 120000 {
		t.Fatalf("player = %#v", p.app.player)
	}
	p.handleEvent(helperEvent{Event: "position", MS: 42000, Gen: play.Gen})
	if p.app.player.PositionMS != 42000 {
		t.Fatalf("position = %v", p.app.player.PositionMS)
	}
}

func TestPlaybackBandFailureFallsBackThenReports(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm1")
	gen := play.Gen
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000, Gen: gen})
	p.handleEvent(helperEvent{Event: "position", MS: 30000, Gen: gen})

	// The top band fails: the next one down resumes where the track was.
	p.handleEvent(helperEvent{Event: "failed", Code: "DecodingError", Ext: -1073479653, Msg: "no decoder", Gen: gen})
	waitFor(t, "the fallback band", func() bool { return helper.count("play") == 2 })
	play, _ = helper.last("play")
	if play.URL != "https://x/lo.m3u8" || play.Gen != gen {
		t.Fatalf("fallback play = %#v", play)
	}
	if !play.Play {
		t.Fatal("the fallback band of a track playing must keep playing")
	}
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000, Gen: gen})
	seek, ok := helper.last("seek")
	if !ok || seek.MS != 30000 {
		t.Fatalf("fallback did not resume at the position: %#v, %v", seek, ok)
	}
	if !p.app.player.Playing || p.app.player.PositionMS != 30000 {
		t.Fatalf("player = %#v", p.app.player)
	}

	// The last band fails too: the track reports the error, and the band is
	// not retried.
	p.handleEvent(helperEvent{Event: "failed", Code: "NetworkError", Ext: -2147012889, Msg: "boom", Gen: gen})
	if helper.count("play") != 2 {
		t.Fatalf("a failed band was retried: %d plays", helper.count("play"))
	}
	if p.app.playerError == "" || p.app.player.Ready || p.app.player.Playing {
		t.Fatalf("player = %#v error=%q", p.app.player, p.app.playerError)
	}

	// Selecting again starts the top band over.
	p.selectTrack("sm1")
	waitFor(t, "the replayed track", func() bool { return helper.count("play") == 3 })
	play, _ = helper.last("play")
	if play.URL != "https://x/hi.m3u8" || p.app.playerError != "" {
		t.Fatalf("play = %#v error=%q", play, p.app.playerError)
	}
}

// TestPlaybackReportsQuality checks the audio band the player bar shows,
// which follows a fallback to the next band down.
func TestPlaybackReportsQuality(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	p.acquire = func(MylistItem) (playbackSources, error) {
		return playbackSources{
			URIs:     []string{"https://x/320.m3u8", "https://x/192.m3u8"},
			BitRates: []int{320, 192},
		}, nil
	}
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm1")
	if quality := p.app.player.Quality; quality != "" {
		t.Fatalf("quality before the track opens = %q", quality)
	}
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000, Gen: play.Gen})
	if quality := p.app.player.Quality; quality != "320kbps" {
		t.Fatalf("quality = %q", quality)
	}
	p.handleEvent(helperEvent{Event: "failed", Code: "DecodingError", Ext: -1, Msg: "no decoder", Gen: play.Gen})
	if quality := p.app.player.Quality; quality != "192kbps" {
		t.Fatalf("fallback quality = %q", quality)
	}
}

// TestPlaybackPlaysTheSavedAudio checks that a track this PC has saved plays
// from disk: the helper is handed the file rather than a manifest, the bar
// says the track is offline, and an open that names no length (a saved file
// from before the movie header carried one) leaves the track's own standing.
func TestPlaybackPlaysTheSavedAudio(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	path, err := p.app.mylist.audioPath("sm1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("saved"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The saved copy is found before the network: the acquisition under test
	// is the engine's own.
	p.acquire = p.prepareSources

	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm1")
	if play.File != savedAudioURI(path) || play.URL != "" || play.Cookie != "" {
		t.Fatalf("play = %#v; want the saved file", play)
	}
	p.handleEvent(helperEvent{Event: "opened", Gen: play.Gen})
	if quality := p.app.player.Quality; quality != locales.PlayerOffline() {
		t.Fatalf("quality = %q; want %q", quality, locales.PlayerOffline())
	}
	if duration := p.app.player.DurationMS; duration != 120_000 {
		t.Fatalf("duration = %vms; want the track's 120000", duration)
	}
}

// TestPlaybackShowsSavedAudioQuality checks that a saved file the bar can name
// a band for shows it with the offline mark. The band comes from the file's
// size and the track's length, so a file saved before this app recorded
// anything shows one too.
func TestPlaybackShowsSavedAudioQuality(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	path, err := p.app.mylist.audioPath("sm1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// A sparse file needs only its size: 120s at 192kbps is what a band of
	// that length costs.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(2_880_000); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	p.acquire = p.prepareSources

	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm1")
	p.handleEvent(helperEvent{Event: "opened", Gen: play.Gen})
	want := locales.PlayerOfflineQuality("192kbps")
	if quality := p.app.player.Quality; quality != want {
		t.Fatalf("quality = %q; want %q", quality, want)
	}
}

// TestPlaybackMarksBrokenSavedAudio checks that a saved track the media stack
// cannot open is dropped and recorded: the list shows it as broken, and the
// next pass fetches it again.
func TestPlaybackMarksBrokenSavedAudio(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	path, err := p.app.mylist.audioPath("sm1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("saved"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.acquire = p.prepareSources

	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm1")
	if play.File == "" {
		t.Fatal("the track did not play from its saved copy")
	}
	p.handleEvent(helperEvent{Event: "failed", Code: "DecodingError", Ext: -1, Msg: "no decoder", Gen: play.Gen})

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the broken file is still there: %v", err)
	}
	if !p.app.broken["sm1"] {
		t.Fatal("the track was not recorded as broken")
	}
	if p.app.playerError == "" {
		t.Fatal("the failure was not reported to the player")
	}
}

// TestPlaybackKeepsTheSavedFileWhenTheHelperTookNoSource checks a failure
// that never reached the file: a helper older than the app ignores the file
// command and answers that it has no source, which must leave the saved copy
// where it is. The file is not what failed, and dropping it would have the
// download pass fetch it again only for the same helper to fail it again.
func TestPlaybackKeepsTheSavedFileWhenTheHelperTookNoSource(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	path := writeAudio(t, p.app, "sm1")
	p.acquire = p.prepareSources

	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm1")
	if play.File == "" {
		t.Fatal("the track did not play from its saved copy")
	}
	p.handleEvent(helperEvent{Event: "failed", Code: "NoSource", Gen: play.Gen})

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a helper that took no source dropped the saved file: %v", err)
	}
	if p.app.broken["sm1"] {
		t.Fatal("a helper that took no source marked the track broken")
	}
	if p.app.playerError == "" {
		t.Fatal("the failure was not reported to the player")
	}
}

// TestPlaybackSkipsTracksNiconicoNoLongerServes checks that the queue moves
// on when it lands on a track that cannot play: shuffle and next pass it over
// rather than stopping on it, and a queue with nothing playable starts
// nothing.
func TestPlaybackSkipsTracksNiconicoNoLongerServes(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	p.queue.reset(testQueueItems())
	deleted := MylistItem{WatchID: "sm9", Title: "削除された動画", Available: true}

	p.play(deleted)

	waitFor(t, "the play command", func() bool { return helper.count("play") == 1 })
	if p.track.WatchID != "sm1" {
		t.Fatalf("the player landed on %q; want the track after the deleted one", p.track.WatchID)
	}

	// A queue left with nothing playable starts nothing at all.
	p2, helper2, _ := newTestPlayback(t)
	p2.queue.reset([]MylistItem{deleted})
	p2.play(deleted)
	if helper2.count("play") != 0 {
		t.Fatal("a track niconico no longer serves was handed to the helper")
	}
	if p2.track.WatchID != "" {
		t.Fatalf("the player shows %q; want nothing playing", p2.track.WatchID)
	}
}

// TestPlaybackForgetsExpiredSession checks that a session niconico refuses
// is forgotten, so the next track plays as a guest.
func TestPlaybackForgetsExpiredSession(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	if err := p.app.session.Save("user_session=abc"); err != nil {
		t.Fatal(err)
	}
	p.app.loggedIn = true
	if err := p.nico.SetSession("user_session=abc"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	p.post = func(fn func()) {
		fn()
		close(done)
	}
	p.acquire = func(MylistItem) (playbackSources, error) {
		return playbackSources{}, errSessionExpired
	}
	p.setItems("111", "マイリストA", testQueueItems())
	p.selectTrack("sm1")
	<-done

	if got, err := p.app.session.Load(); err != nil || got != "" {
		t.Fatalf("the stored session was kept: %q, %v", got, err)
	}
	if p.nico.LoggedIn() {
		t.Fatal("the player still carries the session")
	}
	if p.app.playerError == "" || helper.count("play") != 0 {
		t.Fatalf("error=%q plays=%d", p.app.playerError, helper.count("play"))
	}
}

func TestPlaybackPauseDuringLoadHolds(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm1")
	if !play.Play {
		t.Fatal("the load started playing")
	}
	p.pause()
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000, Gen: play.Gen})
	if p.app.player.Playing {
		t.Fatal("a pause during the load must hold once the track opens")
	}
	if _, ok := helper.last("pause"); !ok {
		t.Fatal("the pause did not reach the helper")
	}

	// The track fails at its top band while paused: the fallback band must
	// be installed paused too, so a pause never turns into sound.
	p.handleEvent(helperEvent{Event: "failed", Code: "NetworkError", Ext: -1, Msg: "boom", Gen: play.Gen})
	waitFor(t, "the fallback band", func() bool { return helper.count("play") == 2 })
	fallback := helperPlay(t, helper)
	if fallback.Play {
		t.Fatalf("a fallback band played on without being asked: %#v", fallback)
	}
	if fallback.URL != "https://x/lo.m3u8" {
		t.Fatalf("fallback play = %#v", fallback)
	}
}

func TestPlaybackPreviousRestartsThenMovesBack(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm2")
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000, Gen: play.Gen})

	// Past the opening, previous restarts the track.
	p.handleEvent(helperEvent{Event: "position", MS: 5000, Gen: play.Gen})
	p.previous()
	if seek, ok := helper.last("seek"); !ok || seek.MS != 0 {
		t.Fatalf("previous did not restart the track: %#v, %v", seek, ok)
	}
	if p.app.player.WatchID != "sm2" {
		t.Fatalf("the restart changed the track: %q", p.app.player.WatchID)
	}

	// From the start it moves to the one before.
	p.handleEvent(helperEvent{Event: "position", MS: 1000, Gen: play.Gen})
	p.previous()
	if p.app.player.WatchID != "sm1" {
		t.Fatalf("previous did not move back: %q", p.app.player.WatchID)
	}
	waitFor(t, "the previous track", func() bool { return helper.count("play") == 2 })
}

// TestPlaybackDropsStaleEventsOfAReplacedTrack makes sure the events of a
// track the user already left cannot speak for the new one: a late ended of
// the old track must not skip the new, and a late failure must not mark it
// failed.
func TestPlaybackDropsStaleEventsOfAReplacedTrack(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	old := selectPlayed(t, p, helper, "sm1")

	p.selectTrack("sm2")
	waitFor(t, "the new track's play", func() bool { return helper.count("play") == 2 })
	current := helperPlay(t, helper)
	if current.URL != "https://x/hi.m3u8" {
		t.Fatalf("play = %#v", current)
	}

	// The old track's events, held up past the switch, must be dropped.
	p.handleEvent(helperEvent{Event: "ended", Gen: old.Gen})
	if p.app.player.WatchID != "sm2" {
		t.Fatalf("a stale ended skipped the new track: %#v", p.app.player)
	}
	p.handleEvent(helperEvent{Event: "failed", Code: "NetworkError", Msg: "stale", Gen: old.Gen})
	if p.app.playerError != "" {
		t.Fatalf("a stale failure marked the new track failed: %q", p.app.playerError)
	}
	// A stale opened must not ready or replace the new track.
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 111, Gen: old.Gen})
	if p.app.player.Ready || p.app.player.WatchID != "sm2" {
		t.Fatalf("a stale opened touched the new track: %#v", p.app.player)
	}

	// The new track's own events still reach it.
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000, Gen: current.Gen})
	if !p.app.player.Ready || p.app.player.DurationMS != 120000 {
		t.Fatalf("player = %#v", p.app.player)
	}
	p.handleEvent(helperEvent{Event: "position", MS: 7000, Gen: current.Gen})
	if p.app.player.PositionMS != 7000 {
		t.Fatalf("position = %v; want the new track's", p.app.player.PositionMS)
	}
}

func TestPlaybackEndedAdvances(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm1")
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000, Gen: play.Gen})
	p.handleEvent(helperEvent{Event: "ended", Gen: play.Gen})
	if p.app.player.WatchID != "sm2" {
		t.Fatalf("ended did not advance: %q", p.app.player.WatchID)
	}
	waitFor(t, "the next track", func() bool { return helper.count("play") == 2 })
}

func TestPlaybackKeepsThePlayingMylistAcrossMylists(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems()[:1])
	p.selectTrack("sm1")
	waitFor(t, "the play command", func() bool { return helper.count("play") == 1 })

	// Opening another mylist keeps the queue and the track playing.
	p.setItems("222", "マイリストB", []MylistItem{{WatchID: "sm2", Title: "Bの曲", Available: true, Duration: 120}})
	if p.app.player.WatchID != "sm1" || p.app.player.MylistID != "111" || p.app.player.Name != "マイリストA" {
		t.Fatalf("opening a mylist took the queue over: %#v", p.app.player)
	}

	// Playing a track of the shown mylist moves the queue to it.
	p.selectTrack("sm2")
	waitFor(t, "the new mylist's track", func() bool { return helper.count("play") == 2 })
	if p.app.player.MylistID != "222" || p.app.player.Name != "マイリストB" || p.app.player.WatchID != "sm2" {
		t.Fatalf("player = %#v", p.app.player)
	}
	if got := p.app.player.Order; len(got) != 1 || got[0] != "sm2" {
		t.Fatalf("order = %v", got)
	}
}

func TestPlaybackHelperCrashResumesTheTrack(t *testing.T) {
	p, helper, spawns := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, helper, "sm1")
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000, Gen: play.Gen})
	p.handleEvent(helperEvent{Event: "position", MS: 40000, Gen: play.Gen})

	// The helper dies: one restart, resuming where the track was.
	helper.onExit(errors.New("boom"))
	waitFor(t, "the restarted helper", func() bool { return *spawns == 2 && p.proc != nil })
	waitFor(t, "the resumed track", func() bool { return helper.count("play") == 2 })
	restarted := helperPlay(t, helper)
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000, Gen: restarted.Gen})
	seek, ok := helper.last("seek")
	if !ok || seek.MS != 40000 {
		t.Fatalf("the restart did not resume: %#v, %v", seek, ok)
	}
}

func TestPlaybackHelperCrashLoopGivesUp(t *testing.T) {
	p, helper, spawns := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	p.selectTrack("sm1")
	waitFor(t, "the play command", func() bool { return helper.count("play") == 1 })

	// Die once: restarted. Die again before the track ever opened: the one
	// restart is used up, so the track reports the failure rather than
	// spinning on crashes.
	helper.onExit(errors.New("boom"))
	waitFor(t, "the restarted helper", func() bool { return *spawns == 2 })
	waitFor(t, "the resumed track", func() bool { return helper.count("play") == 2 })
	helper.onExit(errors.New("boom again"))
	if *spawns != 2 {
		t.Fatalf("the helper was restarted into a crash loop: %d spawns", *spawns)
	}
	if p.app.playerError == "" || p.app.player.Playing {
		t.Fatalf("player = %#v error=%q", p.app.player, p.app.playerError)
	}
}

// TestPlaybackHelperCrashKeepsAPausedTrackPaused: a restarted helper must
// not turn a pause into sound.
func TestPlaybackHelperCrashKeepsAPausedTrackPaused(t *testing.T) {
	p, helper, spawns := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	selectPlayed(t, p, helper, "sm1")
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000})
	p.pause()

	helper.onExit(errors.New("boom"))
	waitFor(t, "the restarted helper", func() bool { return *spawns == 2 })
	waitFor(t, "the resumed track", func() bool { return helper.count("play") == 2 })
	restarted := helperPlay(t, helper)
	if restarted.Play {
		t.Fatalf("the restarted helper played on without being asked: %#v", restarted)
	}
	p.handleEvent(helperEvent{Event: "opened", DurationMS: 120000, Gen: restarted.Gen})
	if p.app.player.Playing {
		t.Fatal("the restarted helper started a paused track")
	}
}

func TestPlaybackSetItemsKeepsShuffleAndTrack(t *testing.T) {
	p, helper, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	p.handle(PlayerCommand{Action: "shuffle", Enabled: true})
	p.selectTrack("sm1")
	waitFor(t, "the play command", func() bool { return helper.count("play") == 1 })
	if !p.app.player.Shuffle {
		t.Fatal("shuffle did not reach the player state")
	}
	if got := p.app.player.Order; len(got) != 2 || got[0] != "sm1" {
		t.Fatalf("shuffle did not pin the track playing: %v", got)
	}

	// Revisiting or refreshing the same mylist keeps shuffle, the queue order,
	// and the track playing.
	p.handle(PlayerCommand{Action: "move", Rows: []int{1}, To: 0})
	wantOrder := p.app.player.Order
	p.setItems("111", "マイリストA", testQueueItems())
	if !p.app.player.Shuffle || p.app.player.WatchID != "sm1" {
		t.Fatalf("player = %#v", p.app.player)
	}
	if got := p.app.player.Order; len(got) != len(wantOrder) || got[0] != wantOrder[0] || got[1] != wantOrder[1] {
		t.Fatalf("revisiting the playing mylist reset the queue order: got=%v want=%v", got, wantOrder)
	}
}
