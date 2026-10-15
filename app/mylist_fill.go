package main

import (
	"log"
	"sync"
)

// savedFill queues the mylists whose bodies the background saving still has
// to fetch, and feeds them to one worker: the metadata API sees one mylist at
// a time, however many the account has. add is safe to call from the UI
// thread while the worker runs.
type savedFill struct {
	mu      sync.Mutex
	pending []string
	queued  map[string]bool
	wake    chan struct{}
}

func newSavedFill() *savedFill {
	return &savedFill{wake: make(chan struct{}, 1)}
}

// add queues a mylist; one already queued or being saved is left alone.
func (f *savedFill) add(id string) {
	f.mu.Lock()
	if f.queued[id] {
		f.mu.Unlock()
		return
	}
	if f.queued == nil {
		f.queued = map[string]bool{}
	}
	f.queued[id] = true
	f.pending = append(f.pending, id)
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// take returns the next queued mylist, "" when none is left.
func (f *savedFill) take() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pending) == 0 {
		return ""
	}
	id := f.pending[0]
	f.pending = f.pending[1:]
	return id
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
	app.fill.add(id)
}

// fillWorker saves the queued mylists' bodies, one at a time.
func (app *nativeApp) fillWorker() {
	for {
		id := app.fill.take()
		if id == "" {
			<-app.fill.wake
			continue
		}
		app.fillSavedBody(id)
		app.fill.done(id)
	}
}

// fillSavedBody saves one mylist's body, skipping one already saved, and
// shows the refreshed index in the view. A failure is only worth a line on
// the log: the mylist opens from the API without a body.
func (app *nativeApp) fillSavedBody(id string) {
	if app.mylist.HasSavedBody(id) {
		return
	}
	if _, err := app.mylist.RefreshMylist(id); err != nil {
		log.Printf("mylists: saving %s: %v", id, err)
		return
	}
	app.update(app.reloadSaved)
}
