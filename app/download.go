package main

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// audioSegmentInterval spaces the segment request starts of one track. The
// delivery edge is built for streaming, but a download runs as fast as the
// network answers; a small pause keeps the request rate a player's look-ahead
// rather than a crawler's sweep, and the fetch window hides the edge's answer
// under it. It is a variable so that tests run the saves without the pauses.
var audioSegmentInterval = 100 * time.Millisecond

// audioTrackInterval spaces the tracks of a download pass. The delivery edge
// serves one track in player-like bursts; a small pause between tracks keeps
// a long mylist from looking like a crawler. It is a variable so that tests
// run the passes without the pause.
var audioTrackInterval = time.Second

// audioState is how one track's offline saving stands, as the list marks it.
type audioState int

const (
	// audioNone is a track nothing is saved for and nothing is planned for.
	audioNone audioState = iota
	// audioPlanned is a track of a download-enabled mylist, not saved yet.
	audioPlanned
	// audioDownloading is the track the worker is fetching right now.
	audioDownloading
	// audioSaved is a track whose audio this PC holds.
	audioSaved
	// audioBroken is a track whose saved audio could not be played.
	audioBroken
)

// audioStat is how one mylist's offline saving stands: the playable tracks it
// holds, how many of them are saved, and how many of those saved are marked
// broken.
type audioStat struct {
	saved, total, broken int
}

// audioJob is one thing for the download worker: the audio of a mylist to
// save, or — with no id — a recount of what every enabled mylist holds.
type audioDownloads struct {
	mu          sync.Mutex
	pending     []string
	queued      map[string]bool
	statsQueued bool
	wake        chan struct{}
}

func newAudioDownloads() *audioDownloads {
	return &audioDownloads{wake: make(chan struct{}, 1)}
}

// add queues a mylist's audio pass; one already queued is left alone.
func (q *audioDownloads) add(id string) {
	q.mu.Lock()
	if q.queued[id] {
		q.mu.Unlock()
		return
	}
	if q.queued == nil {
		q.queued = map[string]bool{}
	}
	q.queued[id] = true
	q.pending = append(q.pending, id)
	q.mu.Unlock()
	q.wakeWorker()
}

// addStats queues a pass with nothing to fetch, so the counts the cards show
// follow a change the app made itself: a broken mark, a dropped mylist.
func (q *audioDownloads) addStats() {
	q.mu.Lock()
	if q.statsQueued {
		q.mu.Unlock()
		return
	}
	q.statsQueued = true
	q.pending = append(q.pending, "")
	q.mu.Unlock()
	q.wakeWorker()
}

// take returns the next job, false when none is left. An empty id names the
// recount. The job leaves the queue as it is taken, so a request arriving
// while it runs queues itself again rather than being dropped: a refresh, or
// a quality change, made mid-pass is the next pass's business.
func (q *audioDownloads) take() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return "", false
	}
	id := q.pending[0]
	q.pending = q.pending[1:]
	if id == "" {
		q.statsQueued = false
	} else {
		delete(q.queued, id)
	}
	return id, true
}

func (q *audioDownloads) wakeWorker() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// downloadWorker saves the queued mylists' audio, one track at a time, counts
// what the passes touched, and sweeps the saved audio once the queue drains:
// the passes and the sweep read the same bodies, so a queue of many mylists
// reads them twice rather than once per pass.
func (app *nativeApp) downloadWorker() {
	dirty := false
	for {
		id, ok := app.downloads.take()
		if !ok {
			if dirty {
				dirty = false
				app.cleanupAudio()
				app.refreshAudioStats(nil)
			}
			<-app.downloads.wake
			continue
		}
		if id == "" {
			// A recount the app asked for itself, as when a mylist was turned
			// off: the counts and the sweep follow at once.
			app.cleanupAudio()
			app.refreshAudioStats(nil)
			continue
		}
		app.downloadMylist(id)
		// The mylist's own counts, so its card follows the pass; the sweep
		// and the other mylists' counts wait for the queue to drain.
		app.refreshAudioStats([]string{id})
		dirty = true
	}
}

// setAudioQuality stores the bitrate downloads pick their band by: the label
// the settings dropdown shows, and the target in the form the worker reads
// off the UI thread. The stored settings carry the value across runs.
func (app *nativeApp) setAudioQuality(kbps int) {
	app.qualityOption = audioQualityLabel(kbps)
	app.audioKbps.Store(int32(kbps))
}

// queueAudio queues a mylist's audio pass, saving its body first when none
// is saved yet.
func (app *nativeApp) queueAudio(id string) {
	if app.mylist.HasSavedBody(id) {
		app.downloads.add(id)
		return
	}
	app.fill.add(id, false)
}

// enqueueSavedAudio queues the audio of a mylist the index says is enabled.
// The fill worker and the refresh path call it once a body is saved.
func (app *nativeApp) enqueueSavedAudio(id string) {
	index, err := app.mylist.ListSaved()
	if err != nil {
		log.Printf("downloads: %v", err)
		return
	}
	if at, ok := indexAt(index, id); ok && index[at].Download {
		app.downloads.add(id)
	}
}

// startAudioDownloads queues every mylist whose offline saving is on: what
// the app starts with, and what a quality change re-fetches.
func (app *nativeApp) startAudioDownloads() {
	index, err := app.mylist.ListSaved()
	if err != nil {
		log.Printf("downloads: %v", err)
		return
	}
	for _, entry := range index {
		if entry.Download && !entry.IsCombined() {
			app.queueAudio(entry.ID)
		}
	}
}

// setDownload turns a mylist's offline saving on or off. Turning it on
// fetches what is missing; turning it off drops the audio no enabled mylist
// holds any more.
func (app *nativeApp) setDownload(id string, on bool) {
	saved, err := app.mylist.SetDownload(id, on)
	if err != nil {
		app.setError(err)
		return
	}
	app.saved = saved
	app.audioProgress = false
	if on {
		app.setStatus(locales.StatusAudioQueued())
		app.queueAudio(id)
		return
	}
	app.setStatus(locales.StatusAudioStopped())
	app.downloads.addStats()
}

// markAudioBroken drops a saved track the media stack could not open and
// records it: the list shows it as broken, and the next pass fetches it
// again. It runs on the UI thread, with the playback that found it.
func (app *nativeApp) markAudioBroken(watchID string) {
	if path := app.savedAudioPath(watchID); path != "" {
		if err := os.Remove(path); err != nil {
			log.Printf("downloads: removing %s: %v", path, err)
		}
	}
	if app.broken == nil {
		app.broken = map[string]bool{}
	}
	if app.broken[watchID] {
		return
	}
	app.broken[watchID] = true
	if err := app.mylist.SaveBroken(app.brokenIDs()); err != nil {
		log.Printf("downloads: %v", err)
	}
	app.downloads.addStats()
}

// clearAudioBroken forgets a broken mark, after a pass saved the track again.
func (app *nativeApp) clearAudioBroken(watchID string) {
	if !app.broken[watchID] {
		return
	}
	delete(app.broken, watchID)
	if err := app.mylist.SaveBroken(app.brokenIDs()); err != nil {
		log.Printf("downloads: %v", err)
	}
}

// brokenIDs is the marked tracks in a stable order, as the problems file
// holds them.
func (app *nativeApp) brokenIDs() []string {
	ids := make([]string, 0, len(app.broken))
	for id := range app.broken {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// audioStateOf returns how one track's offline saving stands: a broken mark
// first, the track being fetched, the saved file, and the plan of a
// download-enabled mylist last.
func (app *nativeApp) audioStateOf(watchID string, planned bool) audioState {
	switch {
	case app.broken[watchID]:
		return audioBroken
	case watchID != "" && watchID == app.audioNow:
		return audioDownloading
	case app.savedAudioPath(watchID) != "":
		return audioSaved
	case planned:
		return audioPlanned
	}
	return audioNone
}

// audioMylistState is how a mylist's offline saving stands, from its counts
// and the pass running.
func (app *nativeApp) audioMylistState(id string) audioState {
	stat := app.audioStats[id]
	switch {
	case stat.broken > 0:
		return audioBroken
	case app.audioNowList == id:
		return audioDownloading
	case stat.total > 0 && stat.saved == stat.total:
		return audioSaved
	}
	return audioPlanned
}

// downloadMylist saves the audio the mylist holds and this PC has not saved
// at the quality the settings name, in the order the body lists it. A track
// whose file is at another quality — or marked broken — is fetched again. A
// track that cannot be fetched is worth a line on the log: the next pass
// picks it up again.
func (app *nativeApp) downloadMylist(id string) {
	body, err := app.mylist.LoadSavedBody(id)
	if err != nil {
		// A mylist deleted between its queueing and this pass is no error.
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("downloads: %s: %v", id, err)
		}
		return
	}
	// The quality is read once: a change made while this pass runs is the
	// next pass's business.
	kbps := int(app.audioKbps.Load())
	force := app.audioRefetch(id, kbps)
	missing := func(item MylistItem) bool {
		return force || app.savedAudioPath(item.WatchID) == ""
	}
	total := 0
	for _, item := range body.Items {
		if isPlayable(item) && missing(item) {
			total++
		}
	}
	done, failed := 0, false
	for _, item := range body.Items {
		if !isPlayable(item) || !missing(item) {
			continue
		}
		if !app.downloadEnabled(id) {
			// The user turned the mylist off while this pass ran; what is
			// already saved stays, and the sweep after the pass decides the
			// rest.
			app.reportAudioDone()
			return
		}
		if done > 0 {
			time.Sleep(audioTrackInterval)
		}
		done++
		app.reportAudioTrack(id, item.WatchID, done, total)
		// A track this PC already held is one the quality change must
		// replace: failing there leaves the pass to run again. A track with
		// nothing saved has nothing at the old quality to lose, and the next
		// pass finds it missing again, so its failure does not hold the
		// mylist's record back.
		hadFile := app.savedAudioPath(item.WatchID) != ""
		if err := app.saveAudio(item, kbps); err != nil {
			log.Printf("downloads: %s: %v", item.WatchID, err)
			if hadFile {
				failed = true
			}
			continue
		}
		watchID := item.WatchID
		app.update(func() { app.clearAudioBroken(watchID) })
	}
	app.reportAudioDone()
	if !failed {
		// Only a pass that fetched everything it needed may say the mylist's
		// saved audio stands at the quality the settings name; a failure
		// leaves the pass to run again.
		if err := app.mylist.SetAudioQuality(id, audioQualityValue(kbps)); err != nil {
			log.Printf("downloads: %s: %v", id, err)
		}
	}
}

// audioRefetch reports whether a mylist's saved audio stands at another
// quality than the settings now name, so a pass fetches it again. A mylist
// whose saving never finished is left alone: its files are what the passes
// put there.
func (app *nativeApp) audioRefetch(id string, kbps int) bool {
	index, err := app.mylist.ListSaved()
	if err != nil {
		return false
	}
	at, ok := indexAt(index, id)
	return ok && index[at].AudioQuality != "" && index[at].AudioQuality != audioQualityValue(kbps)
}

// downloadEnabled reports whether a mylist's offline saving is still on: the
// worker runs off the UI thread, so the flag is read from the index rather
// than from the view's copy, which the UI owns.
func (app *nativeApp) downloadEnabled(id string) bool {
	index, err := app.mylist.ListSaved()
	if err != nil {
		return false
	}
	at, ok := indexAt(index, id)
	return ok && index[at].Download
}

// saveAudio fetches one track's audio into the app's audio directory.
func (app *nativeApp) saveAudio(item MylistItem, kbps int) error {
	dest, err := app.mylist.audioPath(item.WatchID)
	if err != nil {
		return err
	}
	videoID := item.VideoID
	if videoID == "" {
		videoID = item.WatchID
	}
	return app.nico.DownloadAudio(videoID, kbps, dest)
}

// savedAudioPath returns where the track's saved audio is, "" when the track
// is not saved for offline playing.
func (app *nativeApp) savedAudioPath(watchID string) string {
	path, err := app.mylist.audioPath(watchID)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// reportAudioTrack shows the track a pass is fetching now, which the list
// marks as in progress, and how far the pass is.
func (app *nativeApp) reportAudioTrack(id, watchID string, done, total int) {
	app.update(func() {
		app.audioProgress = true
		app.audioNow, app.audioNowList = watchID, id
		app.setStatus(locales.StatusSavingAudio(done, total))
	})
}

// reportAudioDone clears the track a pass was fetching and the progress line
// it wrote, leaving any other message in place.
func (app *nativeApp) reportAudioDone() {
	app.update(func() {
		app.audioNow, app.audioNowList = "", ""
		if app.audioProgress {
			app.setStatus("")
		}
		app.audioProgress = false
	})
}

// refreshAudioStats recounts the offline saving of the mylists given — every
// enabled one when none is — from their saved bodies and the audio directory,
// and hands the counts to the view. It also drops the broken marks of tracks
// no enabled mylist holds any more, when every body could be read.
func (app *nativeApp) refreshAudioStats(ids []string) {
	index, err := app.mylist.ListSaved()
	if err != nil {
		log.Printf("downloads: %v", err)
		return
	}
	broken, err := app.mylist.LoadBroken()
	if err != nil {
		// The marks are the view's business, not the files': a file that is
		// there still plays.
		log.Printf("downloads: %v", err)
		broken = nil
	}
	marked := make(map[string]bool, len(broken))
	for _, id := range broken {
		marked[id] = true
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	all := len(ids) == 0
	complete := true
	held := map[string]bool{}
	stats := map[string]audioStat{}
	for _, entry := range index {
		if !entry.Download || entry.IsCombined() || (!all && !want[entry.ID]) {
			continue
		}
		body, err := app.mylist.LoadSavedBody(entry.ID)
		if err != nil {
			log.Printf("downloads: %s: %v", entry.ID, err)
			complete = false
			continue
		}
		stat := audioStat{}
		for _, item := range body.Items {
			if !isPlayable(item) {
				continue
			}
			stat.total++
			if app.savedAudioPath(item.WatchID) != "" {
				stat.saved++
			}
			if marked[item.WatchID] {
				stat.broken++
			}
			held[item.WatchID] = true
		}
		stats[entry.ID] = stat
	}
	app.update(func() {
		if app.audioStats == nil || all {
			app.audioStats = stats
		} else {
			for id, stat := range stats {
				app.audioStats[id] = stat
			}
		}
		if !all || !complete {
			return
		}
		pruned := false
		for id := range app.broken {
			if !held[id] {
				delete(app.broken, id)
				pruned = true
			}
		}
		if pruned {
			if err := app.mylist.SaveBroken(app.brokenIDs()); err != nil {
				log.Printf("downloads: %v", err)
			}
		}
	})
}

// cleanupAudio drops the saved audio no download-enabled mylist holds any
// more. A video niconico no longer serves is no save target, so what is saved
// for one goes too. A saved body that cannot be read cancels the sweep: a
// file whose mylist went missing must not be dropped on a guess.
func (app *nativeApp) cleanupAudio() {
	index, err := app.mylist.ListSaved()
	if err != nil {
		log.Printf("downloads: %v", err)
		return
	}
	wanted := map[string]bool{}
	for _, entry := range index {
		if !entry.Download || entry.IsCombined() {
			continue
		}
		body, err := app.mylist.LoadSavedBody(entry.ID)
		if err != nil {
			log.Printf("downloads: %s: %v; keeping the saved audio", entry.ID, err)
			return
		}
		for _, item := range body.Items {
			if isPlayable(item) && isWatchID(item.WatchID) {
				wanted[item.WatchID] = true
			}
		}
	}
	dir, err := app.mylist.audioDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // nothing saved yet
	}
	for _, entry := range entries {
		name := entry.Name()
		if id, ok := strings.CutSuffix(name, audioExtension); ok {
			if id == "" || wanted[id] {
				continue
			}
		} else if _, ok := strings.CutSuffix(name, audioExtension+audioPartExtension); !ok {
			continue // not saved audio
		}
		// A part file is one a crash left: this worker writes the passes and
		// sweeps one after the other, so nothing here is still being saved.
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil {
			log.Printf("downloads: removing %s: %v", path, err)
		}
	}
}
