package main

import (
	"log"
	"sync"
)

// fillTask is one queued mylist body: force fetches a body already saved
// again, which is how a mylist that grew on niconico catches up.
type fillTask struct {
	id    string
	force bool
}

// savedFill queues the mylists whose bodies the background saving still has
// to fetch, and feeds them to one worker: the metadata API sees one mylist at
// a time, however many the account has. add is safe to call from the UI
// thread while the worker runs.
type savedFill struct {
	mu      sync.Mutex
	pending []fillTask
	queued  map[string]bool
	wake    chan struct{}
}

func newSavedFill() *savedFill {
	return &savedFill{wake: make(chan struct{}, 1)}
}

// add queues a mylist; one already queued or being saved is left alone.
//
//	shortcut: a force request for a mylist already queued without it is
//	dropped, and its saved body is left standing; upgrade if that shows up.
func (f *savedFill) add(id string, force bool) {
	f.mu.Lock()
	if f.queued[id] {
		f.mu.Unlock()
		return
	}
	if f.queued == nil {
		f.queued = map[string]bool{}
	}
	f.queued[id] = true
	f.pending = append(f.pending, fillTask{id: id, force: force})
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// take returns the next queued mylist, an empty task when none is left.
func (f *savedFill) take() fillTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pending) == 0 {
		return fillTask{}
	}
	task := f.pending[0]
	f.pending = f.pending[1:]
	return task
}

// done forgets a mylist the worker finished, so a later add queues it again.
func (f *savedFill) done(id string) {
	f.mu.Lock()
	delete(f.queued, id)
	f.mu.Unlock()
}

// saveBody queues the mylist's body to be saved in the background, when none
// is saved yet.
func (app *nativeApp) saveBody(id string) {
	if app.mylist.HasSavedBody(id) {
		return
	}
	app.fill.add(id, false)
}

// refreshBody queues the mylist's body to be fetched again in the background,
// replacing the snapshot a mylist that grew on niconico still shows.
func (app *nativeApp) refreshBody(id string) {
	app.fill.add(id, true)
}

// fillWorker saves the queued mylists' bodies, one at a time.
func (app *nativeApp) fillWorker() {
	for {
		task := app.fill.take()
		if task.id == "" {
			<-app.fill.wake
			continue
		}
		app.fillSavedBody(task.id, task.force)
		app.fill.done(task.id)
	}
}

// fillSavedBody saves one mylist's body — replacing the one already saved
// when force is set — and shows the refreshed index in the view. A failure is
// only worth a line on the log: the mylist opens from the API without a body.
func (app *nativeApp) fillSavedBody(id string, force bool) {
	if !force && app.mylist.HasSavedBody(id) {
		return
	}
	if _, err := app.mylist.RefreshMylist(id); err != nil {
		log.Printf("mylists: saving %s: %v", id, err)
		return
	}
	app.update(app.reloadSaved)
	// The saved body is what the audio saving needs: a mylist whose offline
	// saving was just turned on starts fetching now.
	app.enqueueSavedAudio(id)
}
