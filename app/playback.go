package main

import (
	"errors"
	"io"
	"log"
	"math"
	"strconv"
	"time"

	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
	"github.com/hu-ja-ja/VocaBox_Desktop/modules/ams"
)

// playback is the app's player: it keeps the queue, acquires what a track
// needs from niconico, and drives the ams native module that plays it. It is
// the Go home of the former WebView player (src/audio-page.ts).
//
// Every mutation happens on the window's UI thread: UI callbacks call the
// command handler directly, and background work (acquisition, module events,
// the position reports) comes back through post. In tests, without a window,
// post runs inline.
type playback struct {
	app  *nativeApp
	nico *Nico

	queue playQueue
	// items and mylistID are the shown mylist: they answer a row click and the
	// app's page commands. mylistName is the mylist playing, the name the queue
	// screen labels the queue with. The queue belongs to the mylist playing,
	// which another mylist opening does not take over.
	items      []MylistItem
	mylistID   string
	mylistName string
	queueID    string
	queueName  string
	// queueFed counts how many entries of queueID's items have already been
	// handed to the queue, so deletions from the queue never shift where
	// the next page appends.
	queueFed int

	// track is what plays, apart from the queue: it keeps playing, and its
	// metadata keeps being reported, while the shown mylist changes.
	track MylistItem
	// wantPlaying is the play state the app last asked for: opened events
	// and restarts report through it rather than guessing.
	wantPlaying bool

	// uris are the playable bands of the track, highest first, bitrates
	// their kbps for the quality shown, and variant the band playing: a
	// band whose playback fails falls back to the next. cookie and ua are
	// what the player's own requests carry. file is the track's saved copy,
	// which plays instead of the bands when it is there.
	uris     []string
	bitrates []int
	variant  int
	cookie   string
	ua       string
	file     string
	// fileQuality is the band the saved file stands at, "192kbps"; "" when
	// its size and the track's length name none.
	fileQuality string
	// resumeMS is where a fallback band picks the track up; zero means from
	// the start.
	resumeMS float64

	// player is the native module playing tracks, nil before the first play
	// and after the module failed. events is its subscription, closed with
	// it.
	player ams.Player
	events io.Closer
	// closing says the app is quitting: a module failure is then no error.
	closing bool
	// loadToken invalidates the acquisitions of earlier tracks.
	loadToken uint64
	// gen names the track load the player's current source was started for:
	// events carrying another gen belong to a replaced track and are
	// dropped.
	gen int

	// discordKey is the presence last handed to Discord, so a renewal that
	// changes nothing is skipped against it; "" with nothing sent yet.
	discordKey string

	// newPlayer makes the module's player; acquire fetches a track's
	// sources. Tests replace both with stubs.
	newPlayer func() (ams.Player, error)
	acquire   func(item MylistItem) (playbackSources, error)
	// post runs fn on the UI thread; tests (no window) run it inline.
	post func(fn func())

	// positionRate is the rate last given to the player: zero while it
	// reports none.
	positionRate time.Duration
}

// positionRateLyrics is how often the player reports its position while a
// timed lyrics document follows the track: often enough for the lines to turn
// with the vocal. positionRateIdle is the rate otherwise, rare enough that
// each report's window rebuild does not crowd the player.
const (
	positionRateLyrics = 100 * time.Millisecond
	positionRateIdle   = 3 * positionRateLyrics
)

// previousRestartMS is how far into a track the previous button restarts it
// rather than going to the one before, as most players do: a press a short
// way in is more often a wish to hear the track again.
const previousRestartMS = 3000

func newPlayback(app *nativeApp) *playback {
	p := &playback{app: app, nico: app.nico}
	// The mylist lookup rides the same session: the account's own mylists,
	// private ones included, answer only with its cookies.
	app.mylist.session = p.nico.SessionCookie
	p.acquire = p.prepareSources
	p.newPlayer = ams.NewPlayer
	p.post = func(fn func()) {
		if app.window != nil {
			app.window.Update(fn)
			return
		}
		fn()
	}
	go p.resendDiscord()
	return p
}

// handle runs one app command. It is called on the UI thread.
func (p *playback) handle(command PlayerCommand) {
	switch command.Action {
	case "setItems":
		p.setItems(command.ID, command.Name, command.Items)
	case "appendItems":
		p.appendItems(command.Items)
	case "select":
		p.selectTrack(command.WatchID)
	case "play":
		p.resume()
	case "pause":
		p.pause()
	case "seek":
		p.seek(command.PositionMS)
	case "volume":
		p.setVolume(command.Volume)
	case "shuffle":
		p.queue.setShuffle(command.Enabled)
		p.publish()
	case "repeat":
		p.queue.repeat = command.Enabled
		p.publish()
	case "remove":
		p.remove(command.WatchID)
	case "move":
		p.queue.move(command.Rows, command.To)
		p.publish()
	case "next":
		p.next()
	case "previous":
		p.previous()
	}
}

// setItems adopts a mylist as the shown mylist; when nothing plays yet, the
// mylist becomes the queue too, keeping shuffle and repeat. While a track is
// already playing, the existing queue keeps its order and loaded pages, and
// any newly fetched tail entries of the playing mylist (as when pinning or
// refreshing it) are appended.
func (p *playback) setItems(id, name string, items []MylistItem) {
	p.mylistID, p.mylistName = id, name
	p.items = playableItems(items)
	if p.track.WatchID == "" {
		p.adoptQueue()
	} else if p.queueID == p.mylistID {
		p.queueName = p.mylistName
		// The queue's copies of this mylist's entries follow the page just
		// arrived from, so a track deleted since the queue was built stops
		// reading as playable.
		p.queue.update(items)
		if len(p.items) > p.queueFed {
			p.queue.append(p.items[p.queueFed:])
			p.queueFed = len(p.items)
		}
	}
	// A next that was waiting for a page clears here: the queue it waited
	// for either has one now or never will.
	if p.app.pendingAdvance {
		p.app.pendingAdvance = false
		if p.queue.hasLoadedNext() {
			p.next()
			return
		}
	}
	p.publish()
}

// appendItems adds a mylist's next page to the shown mylist, and to the queue
// when that is the shown mylist's own.
func (p *playback) appendItems(items []MylistItem) {
	more := playableItems(items)
	p.items = append(p.items, more...)
	if p.queueID == p.mylistID && len(p.items) > p.queueFed {
		p.queue.append(p.items[p.queueFed:])
		p.queueFed = len(p.items)
	}
	// The app asked for the next track while the page loaded: with the page
	// in the queue it can move on now.
	if p.app.pendingAdvance {
		p.app.pendingAdvance = false
		p.next()
		return
	}
	p.publish()
}

// adoptQueue makes the shown mylist the playback queue, keeping shuffle,
// repeat, and the track playing when the mylist still holds it.
func (p *playback) adoptQueue() {
	shuffle := p.queue.isShuffle()
	repeat := p.queue.repeat
	p.queue.reset(p.items)
	p.queueFed = len(p.items)
	if shuffle {
		p.queue.setShuffle(true)
	}
	p.queue.repeat = repeat
	p.queueID, p.queueName = p.mylistID, p.mylistName
	if p.track.WatchID != "" {
		if again, ok := findItem(p.items, p.track.WatchID); ok {
			p.queue.selectTrack(again)
			p.track = again
		}
	}
}

// selectTrack plays a track from its start: directly from the queue when chosen
// on the queue screen, keeping the shuffled order the screen shows, or from the
// shown mylist otherwise, adopting the shown mylist's queue when it differs from
// the one playing.
func (p *playback) selectTrack(watchID string) {
	if p.app.queueOpen {
		if item, ok := p.queue.selectTrackKeepingOrder(MylistItem{WatchID: watchID}); ok {
			p.play(item)
			return
		}
	}
	item, ok := findItem(p.items, watchID)
	if !ok {
		return
	}
	if p.queueID != p.mylistID {
		p.adoptQueue()
	}
	if _, ok := p.queue.selectTrack(item); !ok {
		p.adoptQueue()
		if _, ok := p.queue.selectTrack(item); !ok {
			return
		}
	}
	p.play(item)
}

// play starts item from its beginning: the sources are acquired in the
// background, and the best band goes to the player as soon as they arrive.
func (p *playback) play(item MylistItem) {
	item, ok := p.skippable(item)
	if !ok {
		// Nothing playable is left: the player stays where it is rather than
		// stopping on what cannot play.
		p.publish()
		return
	}
	p.loadToken++
	token := p.loadToken
	// The load gets its own name: the events of the track just replaced, and
	// of the sources being replaced for this one, must not speak for it.
	p.gen++
	p.track = item
	p.wantPlaying = true
	p.resumeMS = 0
	p.variant = 0
	p.uris, p.bitrates, p.cookie, p.ua, p.file = nil, nil, "", "", ""
	p.fileQuality = ""
	st := &p.app.player
	st.WatchID = item.WatchID
	st.VideoID = item.VideoID
	if st.VideoID == "" {
		st.VideoID = item.WatchID
	}
	st.Title = item.Title
	st.Uploader = item.Uploader
	st.ThumbURL = item.ThumbURL
	st.HasLyrics, st.LyricsKnown = false, false
	st.Ready = false
	st.Playing = false
	st.PositionMS = 0
	st.DurationMS = float64(item.Duration) * 1000
	p.app.playerError = ""
	// The old track stops at once rather than playing on under the new one.
	if p.player != nil {
		_ = p.player.Stop()
	}
	p.publish()
	go func() {
		sources, err := p.acquire(item)
		p.post(func() {
			if token != p.loadToken {
				return
			}
			if err != nil {
				p.fail(err)
				return
			}
			p.uris, p.bitrates, p.cookie, p.ua, p.file = sources.URIs, sources.BitRates, sources.Cookie, sources.UserAgent, sources.File
			p.fileQuality = sources.FileQuality
			p.variant = 0
			st := &p.app.player
			st.HasLyrics = sources.HasLyrics
			st.LyricsKnown = sources.File == ""
			p.sendPlay()
		})
	}()
}

// skippable passes over the tracks a queue picked that niconico no longer
// serves: shuffle and next move on to the entry after rather than stopping on
// what cannot play. It reports false when nothing playable is left. A queue
// is built from playable entries, so this meets one only when it died with
// its mylist open, or when an entry is handed in from outside.
func (p *playback) skippable(item MylistItem) (MylistItem, bool) {
	for tries := p.queue.len(); !isPlayable(item) && tries > 0; tries-- {
		next, ok := p.queue.moveNext()
		if !ok {
			return MylistItem{}, false
		}
		item = next
	}
	return item, isPlayable(item)
}

// prepareSources fetches what playing item needs from niconico, or hands
// over the track's saved copy when this PC has one: a saved track plays
// without touching the network.
func (p *playback) prepareSources(item MylistItem) (playbackSources, error) {
	if p.app != nil && p.app.mylist != nil {
		if file := p.app.savedAudioPath(item.WatchID); file != "" {
			// A track saved before the segment indexes were dropped plays a
			// few seconds short; the rewrite happens once, off the UI thread.
			repairSavedAudio(file)
			return playbackSources{File: file, FileQuality: savedAudioQuality(file, float64(item.Duration)*1000)}, nil
		}
	}
	videoID := item.VideoID
	if videoID == "" {
		videoID = item.WatchID
	}
	return p.nico.Prepare(videoID)
}

// sendPlay hands the current band to the player, making it when it is not
// there. The track's load names the attempt, and wantPlaying says whether it
// starts playing: a paused track is installed paused, so a fallback band does
// not play on its own.
func (p *playback) sendPlay() {
	if err := p.ensurePlayer(); err != nil {
		p.fail(err)
		return
	}
	load := ams.Load{Generation: p.gen, Start: p.wantPlaying}
	if p.file != "" {
		load.File = savedAudioURI(p.file)
	} else {
		if p.variant >= len(p.uris) {
			p.fail(errors.New(locales.ErrNoAudio()))
			return
		}
		load.URL = p.uris[p.variant]
		load.Cookie = p.cookie
		load.UserAgent = p.ua
	}
	if err := p.player.Play(load); err != nil {
		// A player the module lost, as a panic at its boundary, is dropped
		// so the next play makes a fresh one.
		p.closePlayer()
		p.fail(errors.New(locales.ErrPlayerConnect()))
		return
	}
	_ = p.player.SetVolume(p.app.player.Volume)
}

// ensurePlayer makes the module's player when none runs.
func (p *playback) ensurePlayer() error {
	if p.player != nil {
		return nil
	}
	player, err := p.newPlayer()
	if err != nil {
		return err
	}
	p.player = player
	p.events = player.OnEvent(p.handleEvent)
	p.positionRate = 0
	return nil
}

// closePlayer ends the player and its event subscription.
func (p *playback) closePlayer() {
	if p.events != nil {
		_ = p.events.Close()
	}
	if p.player != nil {
		_ = p.player.Close()
	}
	p.player, p.events = nil, nil
	p.positionRate = 0
}

// positionRateFor is the rate the player should report its position at: the
// fast one while a timed lyrics document follows the track, the idle one
// otherwise, and none while nothing plays.
func (p *playback) positionRateFor() time.Duration {
	if p.player == nil || !p.app.player.Ready || !p.app.player.Playing {
		return 0
	}
	if p.app.lyricsSync.Load() {
		return positionRateLyrics
	}
	return positionRateIdle
}

// syncPositionRate tells the player the rate the app reads its position at,
// when it changed. The player reports only while a track plays, so a paused
// app leaves it silent.
func (p *playback) syncPositionRate() {
	if p.player == nil {
		return
	}
	rate := p.positionRateFor()
	if rate == p.positionRate {
		return
	}
	if err := p.player.SetPositionRate(rate); err != nil {
		return
	}
	p.positionRate = rate
}

// handleEvent queues one player event for the UI thread.
func (p *playback) handleEvent(event ams.Event) { p.post(func() { p.applyEvent(event) }) }

// staleEvent reports whether an event belongs to a replaced track load,
// after a new track was selected: the events of the replaced source must not
// speak for the new one. Every event carries the load it was started for;
// only the current load's events pass.
func (p *playback) staleEvent(event ams.Event) bool {
	return event.Generation != p.gen
}

func (p *playback) applyEvent(event ams.Event) {
	if p.staleEvent(event) {
		if event.Event == "failed" {
			log.Printf("stale playback failed: code=%s ext=0x%08X msg=%s", event.Code, event.Ext, event.Msg)
		}
		return
	}
	switch event.Event {
	case "opened":
		st := &p.app.player
		st.Ready = true
		if event.DurationMS > 0 {
			st.DurationMS = event.DurationMS
		}
		st.Playing = p.wantPlaying
		// A fallback band resumes where the track was, when that is not
		// already the end.
		if p.resumeMS > 0 && p.resumeMS < st.DurationMS-1000 {
			st.PositionMS = p.resumeMS
			p.sendSeek(p.resumeMS)
		}
		p.resumeMS = 0
		p.publish()
	case "ended":
		p.wantPlaying = false
		p.app.player.Playing = false
		p.publish()
		p.app.advance()
	case "failed":
		p.bandFailed(event)
	case "position":
		p.applyPosition(event.MS)
	}
}

// applyPosition records where the player reports it is heard. While a lyric
// seek is on its way, the position lags the seek: the target holds until the
// media stack gets there.
func (p *playback) applyPosition(position float64) {
	if !p.app.player.Ready {
		return
	}
	if p.app.lyricsSeekPending {
		if position < p.app.lyricsSeekTargetMS {
			position = p.app.lyricsSeekTargetMS
		} else {
			p.app.lyricsSeekPending = false
		}
	}
	p.app.player.PositionMS = position
}

// bandFailed handles a failed band: the next one down is tried from the
// position reached, and with none left the track reports the error.
func (p *playback) bandFailed(event ams.Event) {
	log.Printf("playback failed: code=%s ext=0x%08X msg=%s", event.Code, event.Ext, event.Msg)
	p.app.player.Ready = false
	p.app.player.Playing = false
	if p.variant+1 < len(p.uris) && p.player != nil {
		p.variant++
		p.resumeMS = p.app.player.PositionMS
		p.sendPlay()
		// The bar shows the band now playing rather than the one that just
		// failed.
		p.publish()
		return
	}
	// A saved copy that failed is dropped and recorded, so the list shows it
	// as broken and the next pass fetches it again. A play that never took
	// the file on at all — an unknown request — leaves it where it is: the
	// file is not what failed.
	if p.file != "" && event.Code != "NoSource" {
		p.app.markAudioBroken(p.track.WatchID)
	}
	p.fail(playbackErrorMessage(event))
}

// playbackErrorMessage maps a player failure to what the player shows.
func playbackErrorMessage(event ams.Event) error {
	switch event.Code {
	case "NetworkError", "ManifestDownloadFailure":
		return errors.New(locales.ErrTrackLoadNetwork())
	case "SourceNotSupported", "DecodingError", "SourceFailed":
		return errors.New(locales.ErrTrackUnsupported())
	case "Aborted":
		return errors.New(locales.ErrPlaybackInterrupted())
	default:
		return errors.New(locales.ErrPlaybackStart())
	}
}

// resume plays on, pause stops; both run what the app last asked for, so a
// pause that lands while a track is still opening holds.
func (p *playback) resume() {
	if p.track.WatchID == "" {
		return
	}
	p.wantPlaying = true
	p.app.player.Playing = true
	if p.player != nil {
		_ = p.player.Resume()
	}
	p.publish()
}

func (p *playback) pause() {
	p.wantPlaying = false
	p.app.player.Playing = false
	if p.player != nil {
		_ = p.player.Pause()
	}
	p.publish()
}

// seek moves the track playing, clamped to the track's bounds.
func (p *playback) seek(positionMS float64) {
	if p.player == nil {
		return
	}
	if math.IsNaN(positionMS) || positionMS < 0 {
		positionMS = 0
	}
	if max := p.app.player.DurationMS; max > 0 && positionMS > max {
		positionMS = max
	}
	p.sendSeek(positionMS)
}

// sendSeek passes an already clamped position to the player.
func (p *playback) sendSeek(positionMS float64) {
	if p.player != nil {
		_ = p.player.Seek(positionMS)
	}
}

func (p *playback) setVolume(volume float64) {
	volume = clampVolume(volume)
	p.app.player.Volume = volume
	if p.player != nil {
		_ = p.player.SetVolume(volume)
	}
	p.publish()
}

// next moves the queue on, and when it has no entry left asks the app for
// the playing mylist's next page, which extends the queue itself.
func (p *playback) next() {
	if item, ok := p.queue.moveNext(); ok {
		p.play(item)
		return
	}
	if p.app.canLoadNext() {
		p.app.pendingAdvance = true
		p.app.loadMore()
		return
	}
	p.publish()
}

// previous restarts the track playing when it is past its opening, as most
// players do, and moves back otherwise.
func (p *playback) previous() {
	if p.app.player.PositionMS > previousRestartMS {
		// The reported position must say the restart at once: the old, late
		// position must not make the next press move back instead.
		p.app.player.PositionMS = 0
		p.sendSeek(0)
		p.publish()
		return
	}
	if item, ok := p.queue.movePrev(); ok {
		p.play(item)
		return
	}
	p.publish()
}

// remove takes the track out of the playing queue; removing the track
// playing stops it.
func (p *playback) remove(watchID string) {
	p.queue.remove(watchID)
	if p.track.WatchID == watchID {
		p.track = MylistItem{}
		p.wantPlaying = false
		p.loadToken++
		p.uris = nil
		if p.player != nil {
			_ = p.player.Stop()
		}
		st := &p.app.player
		st.WatchID, st.VideoID, st.Title, st.Uploader, st.ThumbURL = "", "", "", "", ""
		st.HasLyrics, st.LyricsKnown = false, false
		st.Ready, st.Playing, st.PositionMS, st.DurationMS = false, false, 0, 0
	}
	p.publish()
}

// fail reports a track that cannot play; a session niconico no longer
// accepts is forgotten, so the next track plays as a guest.
func (p *playback) fail(err error) {
	if errors.Is(err, errSessionExpired) {
		p.app.sessionExpired()
	}
	p.wantPlaying = false
	st := &p.app.player
	st.Ready = false
	st.Playing = false
	p.app.playerError = err.Error()
	p.publish()
}

// qualityLabel names the audio band playing, "320kbps" for the 320kbps
// rendition; "" while the band is not known.
func qualityLabel(bitrates []int, variant int) string {
	if variant < 0 || variant >= len(bitrates) || bitrates[variant] <= 0 {
		return ""
	}
	kbps := bitrates[variant]
	if kbps >= 1000 {
		kbps /= 1000
	}
	return kbpsLabel(kbps)
}

// kbpsLabel names a bitrate as the bar shows it: "192kbps" for 192.
func kbpsLabel(kbps int) string { return strconv.Itoa(kbps) + "kbps" }

// publish refreshes the app's player state from the queue and the track; the
// UI reads it on every frame.
func (p *playback) publish() {
	st := &p.app.player
	st.MylistID = p.queueID
	st.Name = p.queueName
	st.Shuffle = p.queue.isShuffle()
	st.Repeat = p.queue.repeat
	st.CanNext = p.queue.hasLoadedNext()
	st.Order = p.queue.orderIDs()
	st.Items = p.queue.orderedItems()
	st.QueueIndex = p.queue.currentIndex()
	if p.file != "" {
		// A saved file names the band it was fetched at when its size and
		// length do; without one the bar says only that it plays offline.
		if p.fileQuality != "" {
			st.Quality = locales.PlayerOfflineQuality(p.fileQuality)
		} else {
			st.Quality = locales.PlayerOffline()
		}
	} else {
		st.Quality = qualityLabel(p.bitrates, p.variant)
	}
	p.syncPositionRate()
	// The tray is the only place the app speaks while its window is hidden.
	p.app.refreshTray()
	// The tray and the window show the same player: a change made from the
	// tray has no frame of the window's own to show it.
	if p.app.window != nil {
		p.app.window.Invalidate()
	}
	p.syncDiscord(false)
}

// shutdown ends the player for good, as the app quits.
func (p *playback) shutdown() {
	p.closing = true
	p.closePlayer()
}

// playableItems keeps the entries that can be played: callers of the mylist
// pages hand over whole pages, including deleted videos.
func playableItems(items []MylistItem) []MylistItem {
	out := make([]MylistItem, 0, len(items))
	for _, item := range items {
		if isPlayable(item) {
			out = append(out, item)
		}
	}
	return out
}

// findItem returns the entry of items carrying the watch ID.
func findItem(items []MylistItem, watchID string) (MylistItem, bool) {
	for i := range items {
		if items[i].WatchID == watchID {
			return items[i], true
		}
	}
	return MylistItem{}, false
}
