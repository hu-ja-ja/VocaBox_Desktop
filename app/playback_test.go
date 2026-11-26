package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
	"github.com/hu-ja-ja/VocaBox_Desktop/modules/ams"
)

// fakePlayer is the module's Player: it records what the playback asks of it.
type fakePlayer struct {
	mu    sync.Mutex
	calls []fakeCall
	// failPlay makes the next Play fail, as a player the module lost does.
	failPlay error
}

// fakeCall is one call of the playback, named as the old pipe protocol named
// it.
type fakeCall struct {
	Name string
	ams.Load
	MS    float64       // seek
	Value float64       // volume
	Rate  time.Duration // rate
}

func (f *fakePlayer) Play(load ams.Load) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPlay != nil {
		err := f.failPlay
		f.failPlay = nil
		return err
	}
	f.calls = append(f.calls, fakeCall{Name: "play", Load: load})
	return nil
}

func (f *fakePlayer) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{Name: name})
}

func (f *fakePlayer) Pause() error  { f.record("pause"); return nil }
func (f *fakePlayer) Resume() error { f.record("resume"); return nil }
func (f *fakePlayer) Stop() error   { f.record("stop"); return nil }

func (f *fakePlayer) Seek(ms float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{Name: "seek", MS: ms})
	return nil
}

func (f *fakePlayer) SetVolume(v float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{Name: "volume", Value: v})
	return nil
}

func (f *fakePlayer) SetPositionRate(every time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{Name: "rate", Rate: every})
	return nil
}

// OnEvent records nothing: tests call p.handleEvent directly.
func (f *fakePlayer) OnEvent(func(ams.Event)) io.Closer {
	return funcCloser(func() {})
}

func (f *fakePlayer) Close() error {
	return nil
}

// funcCloser is an io.Closer around a function.
type funcCloser func()

func (c funcCloser) Close() error {
	c()
	return nil
}

func (f *fakePlayer) all() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeCall(nil), f.calls...)
}

func (f *fakePlayer) count(action string) int {
	n := 0
	for _, call := range f.all() {
		if call.Name == action {
			n++
		}
	}
	return n
}

func (f *fakePlayer) last(action string) (fakeCall, bool) {
	calls := f.all()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Name == action {
			return calls[i], true
		}
	}
	return fakeCall{}, false
}

// newTestPlayback is a playback whose player and acquisition are stubs and
// whose events run inline: engine tests need no module and no network.
func newTestPlayback(t *testing.T) (*playback, *fakePlayer, *int) {
	t.Helper()
	mylist := NewMylist()
	// The engine may forget a stored session; tests never touch the real
	// config directory.
	mylist.configDir = t.TempDir()
	app := newNativeApp(mylist)
	p := newPlayback(app)
	app.playback = p
	player := &fakePlayer{}
	makes := 0
	p.newPlayer = func() (ams.Player, error) {
		makes++
		return player, nil
	}
	p.acquire = func(item MylistItem) (playbackSources, error) {
		return playbackSources{
			URIs:      []string{"https://x/hi.m3u8", "https://x/lo.m3u8"},
			Cookie:    "domand_bid=1",
			UserAgent: "ua",
		}, nil
	}
	return p, player, &makes
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

// playerPlay is the last play the player received.
func playerPlay(t *testing.T, h *fakePlayer) fakeCall {
	t.Helper()
	play, ok := h.last("play")
	if !ok {
		t.Fatal("the player received no play command")
	}
	return play
}

// selectPlayed starts item's track and waits for the player to receive its
// load.
func selectPlayed(t *testing.T, p *playback, player *fakePlayer, sm string) fakeCall {
	t.Helper()
	p.selectTrack(sm)
	waitFor(t, "the play command", func() bool { return player.count("play") == 1 })
	return playerPlay(t, player)
}

// TestPlaybackCarriesTheWatchLyricsAnswer checks the watch API's answer
// reaches the player state: known for a fetched track, unknown for a saved
// file, whose playback never asks.
func TestPlaybackCarriesTheWatchLyricsAnswer(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.acquire = func(MylistItem) (playbackSources, error) {
		return playbackSources{URIs: []string{"https://x/hi.m3u8"}, HasLyrics: true}, nil
	}
	p.setItems("111", "A", []MylistItem{{
		WatchID: "sm1", VideoID: "sm1", Title: "曲", Available: true, Duration: 120,
	}})
	selectPlayed(t, p, player, "sm1")
	st := p.app.player
	if !st.LyricsKnown || !st.HasLyrics || st.VideoID != "sm1" {
		t.Fatalf("player = %#v; want the watch answer known", st)
	}

	// A saved file plays without the watch call: its answer is unknown.
	p.acquire = func(MylistItem) (playbackSources, error) {
		return playbackSources{File: filepath.Join(t.TempDir(), "saved.m4a")}, nil
	}
	p.selectTrack("sm1")
	waitFor(t, "the saved play command", func() bool { return player.count("play") == 2 })
	if p.app.player.LyricsKnown {
		t.Fatal("a saved file's lyrics answer reads as known")
	}
}

// TestPositionRateFollowsTheLyrics checks the rate the player reports at: the
// idle one while it plays, the fast one while a timed document follows, and
// none while nothing plays.
func TestPositionRateFollowsTheLyrics(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.player = player
	p.app.player.Ready, p.app.player.Playing = true, true
	if got := p.positionRateFor(); got != positionRateIdle {
		t.Fatalf("rate = %v; want the idle %v", got, positionRateIdle)
	}
	p.app.lyricsSync = true
	if got := p.positionRateFor(); got != positionRateLyrics {
		t.Fatalf("rate = %v; want the lyrics %v", got, positionRateLyrics)
	}
	p.app.player.Playing = false
	if got := p.positionRateFor(); got != 0 {
		t.Fatalf("rate = %v; want none while paused", got)
	}
}

func TestPlaybackSelectAcquiresAndPlaysTopBand(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, player, "sm1")

	if play.URL != "https://x/hi.m3u8" || play.Cookie != "domand_bid=1" || play.UserAgent != "ua" {
		t.Fatalf("play = %#v", play)
	}
	if !play.Start {
		t.Fatal("the track playing must ask the player to play")
	}
	// The level goes over too, so a restarted player hears it before playing.
	volume, ok := player.last("volume")
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
	p, player, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, player, "sm1")

	p.handleEvent(ams.Event{Event: "opened", DurationMS: 120000, Generation: play.Generation})
	if !p.app.player.Ready || !p.app.player.Playing || p.app.player.DurationMS != 120000 {
		t.Fatalf("player = %#v", p.app.player)
	}
	// Playing tells the player to report its position: the idle rate while
	// no timed lyrics follow.
	if rate, ok := player.last("rate"); !ok || rate.Rate != positionRateIdle {
		t.Fatalf("position rate = %#v, %v; want the idle %v", rate, ok, positionRateIdle)
	}
	p.handleEvent(ams.Event{Event: "position", MS: 42000, Generation: play.Generation})
	if p.app.player.PositionMS != 42000 {
		t.Fatalf("position = %v", p.app.player.PositionMS)
	}
	p.pause()
	if rate, ok := player.last("rate"); !ok || rate.Rate != 0 {
		t.Fatalf("position rate while paused = %#v, %v; want none", rate, ok)
	}
}

func TestPlaybackBandFailureFallsBackThenReports(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, player, "sm1")
	gen := play.Generation
	p.handleEvent(ams.Event{Event: "opened", DurationMS: 120000, Generation: gen})
	p.applyPosition(30000)

	// The top band fails: the next one down resumes where the track was.
	p.handleEvent(ams.Event{Event: "failed", Code: "DecodingError", Ext: -1073479653, Msg: "no decoder", Generation: gen})
	waitFor(t, "the fallback band", func() bool { return player.count("play") == 2 })
	play, _ = player.last("play")
	if play.URL != "https://x/lo.m3u8" || play.Generation != gen {
		t.Fatalf("fallback play = %#v", play)
	}
	if !play.Start {
		t.Fatal("the fallback band of a track playing must keep playing")
	}
	p.handleEvent(ams.Event{Event: "opened", DurationMS: 120000, Generation: gen})
	seek, ok := player.last("seek")
	if !ok || seek.MS != 30000 {
		t.Fatalf("fallback did not resume at the position: %#v, %v", seek, ok)
	}
	if !p.app.player.Playing || p.app.player.PositionMS != 30000 {
		t.Fatalf("player = %#v", p.app.player)
	}

	// The last band fails too: the track reports the error, and the band is
	// not retried.
	p.handleEvent(ams.Event{Event: "failed", Code: "NetworkError", Ext: -2147012889, Msg: "boom", Generation: gen})
	if player.count("play") != 2 {
		t.Fatalf("a failed band was retried: %d plays", player.count("play"))
	}
	if p.app.playerError == "" || p.app.player.Ready || p.app.player.Playing {
		t.Fatalf("player = %#v error=%q", p.app.player, p.app.playerError)
	}

	// Selecting again starts the top band over.
	p.selectTrack("sm1")
	waitFor(t, "the replayed track", func() bool { return player.count("play") == 3 })
	play, _ = player.last("play")
	if play.URL != "https://x/hi.m3u8" || p.app.playerError != "" {
		t.Fatalf("play = %#v error=%q", play, p.app.playerError)
	}
}

// TestPlaybackReportsQuality checks the audio band the player bar shows,
// which follows a fallback to the next band down.
func TestPlaybackReportsQuality(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.acquire = func(MylistItem) (playbackSources, error) {
		return playbackSources{
			URIs:     []string{"https://x/320.m3u8", "https://x/192.m3u8"},
			BitRates: []int{320, 192},
		}, nil
	}
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, player, "sm1")
	if quality := p.app.player.Quality; quality != "" {
		t.Fatalf("quality before the track opens = %q", quality)
	}
	p.handleEvent(ams.Event{Event: "opened", DurationMS: 120000, Generation: play.Generation})
	if quality := p.app.player.Quality; quality != "320kbps" {
		t.Fatalf("quality = %q", quality)
	}
	p.handleEvent(ams.Event{Event: "failed", Code: "DecodingError", Ext: -1, Msg: "no decoder", Generation: play.Generation})
	if quality := p.app.player.Quality; quality != "192kbps" {
		t.Fatalf("fallback quality = %q", quality)
	}
}

// TestPlaybackPlaysTheSavedAudio checks that a track this PC has saved plays
// from disk: the player is handed the file rather than a manifest, the bar
// says the track is offline, and an open that names no length (a saved file
// from before the movie header carried one) leaves the track's own standing.
func TestPlaybackPlaysTheSavedAudio(t *testing.T) {
	p, player, _ := newTestPlayback(t)
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
	play := selectPlayed(t, p, player, "sm1")
	if play.File != savedAudioURI(path) || play.URL != "" || play.Cookie != "" {
		t.Fatalf("play = %#v; want the saved file", play)
	}
	p.handleEvent(ams.Event{Event: "opened", Generation: play.Generation})
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
	p, player, _ := newTestPlayback(t)
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
	play := selectPlayed(t, p, player, "sm1")
	p.handleEvent(ams.Event{Event: "opened", Generation: play.Generation})
	want := locales.PlayerOfflineQuality("192kbps")
	if quality := p.app.player.Quality; quality != want {
		t.Fatalf("quality = %q; want %q", quality, want)
	}
}

// TestPlaybackMarksBrokenSavedAudio checks that a saved track the media stack
// cannot open is dropped and recorded: the list shows it as broken, and the
// next pass fetches it again.
func TestPlaybackMarksBrokenSavedAudio(t *testing.T) {
	p, player, _ := newTestPlayback(t)
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
	play := selectPlayed(t, p, player, "sm1")
	if play.File == "" {
		t.Fatal("the track did not play from its saved copy")
	}
	p.handleEvent(ams.Event{Event: "failed", Code: "DecodingError", Ext: -1, Msg: "no decoder", Generation: play.Generation})

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

// TestPlaybackSkipsTracksNiconicoNoLongerServes checks that the queue moves
// on when it lands on a track that cannot play: shuffle and next pass it over
// rather than stopping on it, and a queue with nothing playable starts
// nothing.
func TestPlaybackSkipsTracksNiconicoNoLongerServes(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.queue.reset(testQueueItems())
	deleted := MylistItem{WatchID: "sm9", Title: "削除された動画", Available: true}

	p.play(deleted)

	waitFor(t, "the play command", func() bool { return player.count("play") == 1 })
	if p.track.WatchID != "sm1" {
		t.Fatalf("the player landed on %q; want the track after the deleted one", p.track.WatchID)
	}

	// A queue left with nothing playable starts nothing at all.
	p2, player2, _ := newTestPlayback(t)
	p2.queue.reset([]MylistItem{deleted})
	p2.play(deleted)
	if player2.count("play") != 0 {
		t.Fatal("a track niconico no longer serves was handed to the player")
	}
	if p2.track.WatchID != "" {
		t.Fatalf("the player shows %q; want nothing playing", p2.track.WatchID)
	}
}

// TestPlaybackForgetsExpiredSession checks that a session niconico refuses
// is forgotten, so the next track plays as a guest.
func TestPlaybackForgetsExpiredSession(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	if err := p.app.session.Save("user_session=abc"); err != nil {
		t.Fatal(err)
	}
	p.app.loggedIn = true
	if err := p.nico.SetSession("user_session=abc"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	// The playback's periodic Discord resend posts through the same hook
	// after this test's own post, so the channel closes once.
	var once sync.Once
	p.post = func(fn func()) {
		fn()
		once.Do(func() { close(done) })
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
	if p.app.playerError == "" || player.count("play") != 0 {
		t.Fatalf("error=%q plays=%d", p.app.playerError, player.count("play"))
	}
}

func TestPlaybackPauseDuringLoadHolds(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, player, "sm1")
	if !play.Start {
		t.Fatal("the load started playing")
	}
	p.pause()
	p.handleEvent(ams.Event{Event: "opened", DurationMS: 120000, Generation: play.Generation})
	if p.app.player.Playing {
		t.Fatal("a pause during the load must hold once the track opens")
	}
	if _, ok := player.last("pause"); !ok {
		t.Fatal("the pause did not reach the player")
	}

	// The track fails at its top band while paused: the fallback band must
	// be installed paused too, so a pause never turns into sound.
	p.handleEvent(ams.Event{Event: "failed", Code: "NetworkError", Ext: -1, Msg: "boom", Generation: play.Generation})
	waitFor(t, "the fallback band", func() bool { return player.count("play") == 2 })
	fallback := playerPlay(t, player)
	if fallback.Start {
		t.Fatalf("a fallback band played on without being asked: %#v", fallback)
	}
	if fallback.URL != "https://x/lo.m3u8" {
		t.Fatalf("fallback play = %#v", fallback)
	}
}

func TestPlaybackPreviousRestartsThenMovesBack(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, player, "sm2")
	p.handleEvent(ams.Event{Event: "opened", DurationMS: 120000, Generation: play.Generation})

	// Past the opening, previous restarts the track.
	p.applyPosition(5000)
	p.previous()
	if seek, ok := player.last("seek"); !ok || seek.MS != 0 {
		t.Fatalf("previous did not restart the track: %#v, %v", seek, ok)
	}
	if p.app.player.WatchID != "sm2" {
		t.Fatalf("the restart changed the track: %q", p.app.player.WatchID)
	}

	// From the start it moves to the one before.
	p.applyPosition(1000)
	p.previous()
	if p.app.player.WatchID != "sm1" {
		t.Fatalf("previous did not move back: %q", p.app.player.WatchID)
	}
	waitFor(t, "the previous track", func() bool { return player.count("play") == 2 })
}

// TestPlaybackDropsStaleEventsOfAReplacedTrack makes sure the events of a
// track the user already left cannot speak for the new one: a late ended of
// the old track must not skip the new, and a late failure must not mark it
// failed.
func TestPlaybackDropsStaleEventsOfAReplacedTrack(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	old := selectPlayed(t, p, player, "sm1")

	p.selectTrack("sm2")
	waitFor(t, "the new track's play", func() bool { return player.count("play") == 2 })
	current := playerPlay(t, player)
	if current.URL != "https://x/hi.m3u8" {
		t.Fatalf("play = %#v", current)
	}

	// The old track's events, held up past the switch, must be dropped.
	p.handleEvent(ams.Event{Event: "ended", Generation: old.Generation})
	if p.app.player.WatchID != "sm2" {
		t.Fatalf("a stale ended skipped the new track: %#v", p.app.player)
	}
	p.handleEvent(ams.Event{Event: "failed", Code: "NetworkError", Msg: "stale", Generation: old.Generation})
	if p.app.playerError != "" {
		t.Fatalf("a stale failure marked the new track failed: %q", p.app.playerError)
	}
	// A stale opened must not ready or replace the new track.
	p.handleEvent(ams.Event{Event: "opened", DurationMS: 111, Generation: old.Generation})
	if p.app.player.Ready || p.app.player.WatchID != "sm2" {
		t.Fatalf("a stale opened touched the new track: %#v", p.app.player)
	}

	// The new track's own events still reach it.
	p.handleEvent(ams.Event{Event: "opened", DurationMS: 120000, Generation: current.Generation})
	if !p.app.player.Ready || p.app.player.DurationMS != 120000 {
		t.Fatalf("player = %#v", p.app.player)
	}
	p.applyPosition(7000)
	if p.app.player.PositionMS != 7000 {
		t.Fatalf("position = %v; want the new track's", p.app.player.PositionMS)
	}
}

func TestPlaybackEndedAdvances(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	play := selectPlayed(t, p, player, "sm1")
	p.handleEvent(ams.Event{Event: "opened", DurationMS: 120000, Generation: play.Generation})
	p.handleEvent(ams.Event{Event: "ended", Generation: play.Generation})
	if p.app.player.WatchID != "sm2" {
		t.Fatalf("ended did not advance: %q", p.app.player.WatchID)
	}
	waitFor(t, "the next track", func() bool { return player.count("play") == 2 })
}

func TestPlaybackKeepsThePlayingMylistAcrossMylists(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems()[:1])
	p.selectTrack("sm1")
	waitFor(t, "the play command", func() bool { return player.count("play") == 1 })

	// Opening another mylist keeps the queue and the track playing.
	p.setItems("222", "マイリストB", []MylistItem{{WatchID: "sm2", Title: "Bの曲", Available: true, Duration: 120}})
	if p.app.player.WatchID != "sm1" || p.app.player.MylistID != "111" || p.app.player.Name != "マイリストA" {
		t.Fatalf("opening a mylist took the queue over: %#v", p.app.player)
	}

	// Playing a track of the shown mylist moves the queue to it.
	p.selectTrack("sm2")
	waitFor(t, "the new mylist's track", func() bool { return player.count("play") == 2 })
	if p.app.player.MylistID != "222" || p.app.player.Name != "マイリストB" || p.app.player.WatchID != "sm2" {
		t.Fatalf("player = %#v", p.app.player)
	}
	if got := p.app.player.Order; len(got) != 1 || got[0] != "sm2" {
		t.Fatalf("order = %v", got)
	}
}

// TestPlaybackDropsThePlayerWhenTheModuleFails checks that a player the
// module lost (a panic at its boundary, a closed player) is dropped rather
// than failing every track after it: the track reports the error, and the
// next play makes a fresh player.
func TestPlaybackDropsThePlayerWhenTheModuleFails(t *testing.T) {
	p, player, makes := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	selectPlayed(t, p, player, "sm1")

	player.mu.Lock()
	player.failPlay = errors.New("ams: panicked")
	player.mu.Unlock()
	p.selectTrack("sm2")
	waitFor(t, "the failed play", func() bool { return p.app.playerError != "" })
	if p.player != nil {
		t.Fatal("the failed player was kept")
	}
	if p.app.player.Playing {
		t.Fatalf("player = %#v", p.app.player)
	}

	player.mu.Lock()
	player.failPlay = nil
	player.mu.Unlock()
	p.selectTrack("sm1")
	waitFor(t, "the fresh player", func() bool { return *makes == 2 })
	waitFor(t, "the replayed track", func() bool { return player.count("play") == 2 })
	if p.app.playerError != "" {
		t.Fatalf("the fresh player kept the error: %q", p.app.playerError)
	}
}

// TestPlaybackQueueSelectKeepsShuffleOrder checks the queue screen's choice
// under shuffle: the track plays where the screen shows it, and the order
// stays as it is.
func TestPlaybackQueueSelectKeepsShuffleOrder(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	p.handle(PlayerCommand{Action: "shuffle", Enabled: true})
	selectPlayed(t, p, player, "sm1")
	order := append([]string(nil), p.app.player.Order...)
	if len(order) != 2 || order[0] != "sm1" {
		t.Fatalf("shuffle order = %v; want sm1 first", order)
	}

	// The queue screen chooses the other entry: it plays at its row, and the
	// queue keeps the order the screen shows.
	p.app.queueOpen = true
	p.handle(PlayerCommand{Action: "select", WatchID: "sm2"})
	waitFor(t, "the queue's play command", func() bool { return player.count("play") == 2 })
	if got := p.app.player.Order; len(got) != len(order) || got[0] != order[0] || got[1] != order[1] {
		t.Fatalf("the queue's choice reshuffled the order: got %v want %v", got, order)
	}
	if p.app.player.QueueIndex != 1 {
		t.Fatalf("the queue's choice sits at %d; want 1", p.app.player.QueueIndex)
	}
}

func TestPlaybackSetItemsKeepsShuffleAndTrack(t *testing.T) {
	p, player, _ := newTestPlayback(t)
	p.setItems("111", "マイリストA", testQueueItems())
	p.handle(PlayerCommand{Action: "shuffle", Enabled: true})
	p.selectTrack("sm1")
	waitFor(t, "the play command", func() bool { return player.count("play") == 1 })
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
