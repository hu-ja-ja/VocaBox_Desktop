package main

import (
	"errors"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// playback is the app's player: it keeps the queue, acquires what a track
// needs from niconico, and drives the AmsHelper child process that plays it.
// It is the Go home of the former WebView player (src/audio-page.ts).
//
// Every mutation happens on the window's UI thread: UI callbacks call the
// command handler directly, and background work (acquisition, helper events,
// the position pull) comes back through post. In tests, without a window,
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
	// what the helper's own requests carry. file is the track's saved copy,
	// which plays instead of the bands when it is there.
	uris     []string
	bitrates []int
	variant  int
	cookie   string
	ua       string
	file     string
	// resumeMS is where a fallback replay or a restarted helper picks the
	// track up; zero means from the start.
	resumeMS float64

	// proc is the running helper, nil before the first play and after a
	// crash loop gave up.
	proc helperConn
	// lastProc is the process gen was counted for, so a fresh helper after
	// a crash starts the generation over.
	lastProc helperConn
	// restarted says the helper was already restarted once for the track
	// playing, so a crash loop gives up rather than spinning.
	restarted bool
	// closing says the app is quitting: an exit is then no crash.
	closing bool
	// loadToken invalidates the acquisitions of earlier tracks.
	loadToken uint64
	// gen names the track load the helper's current source was started for:
	// events carrying another gen belong to a replaced track or helper and
	// are dropped.
	gen int

	// spawn launches the helper; acquire fetches a track's sources. Tests
	// replace both with stubs.
	spawn   func(onEvent func(helperEvent), onExit func(error)) (helperConn, error)
	acquire func(item MylistItem) (playbackSources, error)
	// post runs fn on the UI thread; tests (no window) run it inline.
	post func(fn func())

	pollOnce sync.Once
	polling  atomic.Bool
}

// helperConn is the engine's view of the running helper: the JSON-line link
// and the kill switch.
type helperConn interface {
	send(helperCommand) error
	shutdown()
}

// positionPollInterval is how often the app pulls the helper's position:
// often enough for a smooth seek bar, rare enough that each pull's window
// rebuild does not crowd the player.
const positionPollInterval = 250 * time.Millisecond

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
	p.spawn = p.spawnHelper
	p.post = func(fn func()) {
		if app.window != nil {
			app.window.Update(fn)
			return
		}
		fn()
	}
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
// on the queue screen, or from the shown mylist otherwise, adopting the shown
// mylist's queue when it differs from the one playing.
func (p *playback) selectTrack(watchID string) {
	if p.app.queueOpen {
		if item, ok := p.queue.selectTrack(MylistItem{WatchID: watchID}); ok {
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
// background, and the best band goes to the helper as soon as they arrive.
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
	p.restarted = false
	st := &p.app.player
	st.WatchID = item.WatchID
	st.Title = item.Title
	st.Uploader = item.Uploader
	st.ThumbURL = item.ThumbURL
	st.Ready = false
	st.Playing = false
	st.PositionMS = 0
	st.DurationMS = float64(item.Duration) * 1000
	p.app.playerError = ""
	// The old track stops at once rather than playing on under the new one.
	if p.proc != nil {
		_ = p.proc.send(helperCommand{Cmd: "stop"})
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
			p.variant = 0
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
			return playbackSources{File: file}, nil
		}
	}
	videoID := item.VideoID
	if videoID == "" {
		videoID = item.WatchID
	}
	return p.nico.Prepare(videoID)
}

// sendPlay hands the current band to the helper, starting the helper when it
// is not running. The track's load names the attempt, and wantPlaying says
// whether it starts playing: a paused track is installed paused, so neither
// a fallback band nor a restarted helper plays on its own. A restarted helper
// takes a new name for the same load: the events of the process it replaced
// must not speak for it either.
func (p *playback) sendPlay() {
	if err := p.ensureHelper(); err != nil {
		p.fail(err)
		return
	}
	if p.proc != p.lastProc {
		p.gen++
		p.lastProc = p.proc
	}
	command := helperCommand{Cmd: "play", Gen: p.gen, Play: p.wantPlaying}
	if p.file != "" {
		command.File = savedAudioURI(p.file)
	} else {
		if p.variant >= len(p.uris) {
			p.fail(errors.New(locales.ErrNoAudio()))
			return
		}
		command.URL = p.uris[p.variant]
		command.Cookie = p.cookie
		command.UA = p.ua
	}
	if err := p.proc.send(command); err != nil {
		p.fail(errors.New(locales.ErrPlayerConnect()))
		return
	}
	_ = p.proc.send(helperCommand{Cmd: "volume", Value: p.app.player.Volume})
}

// ensureHelper starts the helper process when none runs, and the position
// pull with it.
func (p *playback) ensureHelper() error {
	if p.proc != nil {
		return nil
	}
	helper, err := p.spawn(p.handleEvent, p.helperExited)
	if err != nil {
		return err
	}
	p.proc = helper
	p.pollOnce.Do(func() { go p.pollPosition() })
	return nil
}

// spawnHelper launches the AmsHelper beside the app's resources.
func (p *playback) spawnHelper(onEvent func(helperEvent), onExit func(error)) (helperConn, error) {
	path, err := helperExecutable()
	if err != nil {
		return nil, err
	}
	return startHelper(path, filepath.Dir(path), onEvent, onExit)
}

// helperExecutable is the helper beside the app's other resources.
func helperExecutable() (string, error) {
	dir, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return "", errors.New(locales.ErrPlayerMissing())
	}
	path := filepath.Join(dir, "AmsHelper.exe")
	if _, err := os.Stat(path); err != nil {
		return "", errors.New(locales.ErrPlayerMissingHelper())
	}
	return path, nil
}

// pollPosition pulls the helper's position while a track is playing: the app
// asks, the helper answers with a position event.
func (p *playback) pollPosition() {
	for range time.Tick(positionPollInterval) {
		if !p.polling.Load() {
			continue
		}
		p.post(func() {
			if p.closing || p.proc == nil || !p.app.player.Ready || !p.app.player.Playing {
				return
			}
			_ = p.proc.send(helperCommand{Cmd: "position"})
		})
	}
}

// handleEvent queues one helper event for the UI thread.
func (p *playback) handleEvent(event helperEvent) { p.post(func() { p.applyEvent(event) }) }

// staleEvent reports whether an event belongs to a replaced track load,
// after a new track was selected or a new helper replaced a dead one: the
// events of the replaced source must not speak for the new one. Every event
// carries the load it was started for; only the current load's events pass.
func (p *playback) staleEvent(event helperEvent) bool {
	return event.Gen != p.gen
}

func (p *playback) applyEvent(event helperEvent) {
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
		p.restarted = false
		// A fallback band or a restarted helper resumes where the track
		// was, when that is not already the end.
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
		if p.app.player.Ready {
			p.app.player.PositionMS = event.MS
		}
	}
}

// bandFailed handles a failed band: the next one down is tried from the
// position reached, and with none left the track reports the error.
func (p *playback) bandFailed(event helperEvent) {
	log.Printf("playback failed: code=%s ext=0x%08X msg=%s", event.Code, event.Ext, event.Msg)
	p.app.player.Ready = false
	p.app.player.Playing = false
	if p.variant+1 < len(p.uris) && p.proc != nil {
		p.variant++
		p.resumeMS = p.app.player.PositionMS
		p.sendPlay()
		// The bar shows the band now playing rather than the one that just
		// failed.
		p.publish()
		return
	}
	// A saved copy that failed is dropped and recorded, so the list shows it
	// as broken and the next pass fetches it again. A helper that never took
	// the file on at all — one older than the command, an unknown request —
	// leaves it where it is: the file is not what failed.
	if p.file != "" && event.Code != "NoSource" {
		p.app.markAudioBroken(p.track.WatchID)
	}
	p.fail(playbackErrorMessage(event))
}

// playbackErrorMessage maps a helper failure to what the player shows.
func playbackErrorMessage(event helperEvent) error {
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

// helperExited handles an unexpected helper death: one restart per track,
// resuming where the track was, and an error when the restarted helper dies
// too.
func (p *playback) helperExited(error) {
	p.post(func() {
		if p.closing {
			return
		}
		log.Printf("AmsHelper died; restarting (track=%q retry=%v)", p.track.WatchID, !p.restarted)
		p.proc = nil
		p.polling.Store(false)
		if p.track.WatchID == "" {
			return
		}
		if p.restarted {
			p.fail(errors.New(locales.ErrPlayerHang()))
			return
		}
		p.restarted = true
		if len(p.uris) == 0 {
			// The acquisition is still running; its sendPlay starts a fresh
			// helper.
			return
		}
		p.resumeMS = p.app.player.PositionMS
		p.app.player.Ready = false
		p.app.player.Playing = false
		p.sendPlay()
	})
}

// resume plays on, pause stops; both run what the app last asked for, so a
// pause that lands while a track is still opening holds.
func (p *playback) resume() {
	if p.track.WatchID == "" {
		return
	}
	p.wantPlaying = true
	p.app.player.Playing = true
	if p.proc != nil {
		_ = p.proc.send(helperCommand{Cmd: "resume"})
	}
	p.publish()
}

func (p *playback) pause() {
	p.wantPlaying = false
	p.app.player.Playing = false
	if p.proc != nil {
		_ = p.proc.send(helperCommand{Cmd: "pause"})
	}
	p.publish()
}

// seek moves the track playing, clamped to the track's bounds.
func (p *playback) seek(positionMS float64) {
	if p.proc == nil {
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

// sendSeek passes an already clamped position to the helper.
func (p *playback) sendSeek(positionMS float64) {
	if p.proc != nil {
		_ = p.proc.send(helperCommand{Cmd: "seek", MS: positionMS})
	}
}

func (p *playback) setVolume(volume float64) {
	volume = clampVolume(volume)
	p.app.player.Volume = volume
	if p.proc != nil {
		_ = p.proc.send(helperCommand{Cmd: "volume", Value: volume})
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
		if p.proc != nil {
			_ = p.proc.send(helperCommand{Cmd: "stop"})
		}
		st := &p.app.player
		st.WatchID, st.Title, st.Uploader, st.ThumbURL = "", "", "", ""
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
	return strconv.Itoa(kbps) + "kbps"
}

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
		st.Quality = locales.PlayerOffline()
	} else {
		st.Quality = qualityLabel(p.bitrates, p.variant)
	}
	p.polling.Store(!p.closing && p.proc != nil && st.Ready && st.Playing)
	// The tray is the only place the app speaks while its window is hidden.
	p.app.refreshTray()
}

// shutdown ends the helper for good, as the app quits.
func (p *playback) shutdown() {
	p.closing = true
	p.polling.Store(false)
	if p.proc != nil {
		p.proc.shutdown()
	}
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
