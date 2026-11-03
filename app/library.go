package main

import (
	"errors"
	"log"
	"math/rand"
	"os"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// currentSaved returns the index line of the mylist shown, when there is one.
func (app *nativeApp) currentSaved() *SavedIndexEntry {
	if app.page == nil {
		return nil
	}
	return app.savedEntry(app.page.ID)
}

// savedEntry returns the saved index line with the ID, when there is one.
func (app *nativeApp) savedEntry(id string) *SavedIndexEntry {
	for i := range app.saved {
		if app.saved[i].ID == id {
			return &app.saved[i]
		}
	}
	return nil
}

// unpin takes a mylist's pin off, keeping its saved body.
func (app *nativeApp) unpin(id string) { app.setPinned(id, false) }

// pin pins a saved mylist, writing only the index; one whose body is not
// saved yet is fetched and saved first. A combined list has no body to fetch
// and is pinned as it stands.
func (app *nativeApp) pin(id string) {
	if app.saving {
		return
	}
	if entry := app.savedEntry(id); entry != nil && (entry.IsCombined() || app.mylist.HasSavedBody(id)) {
		app.setPinned(id, true)
		return
	}
	app.saveMylist(id)
}

// setPinned writes a saved mylist's pin, touching only the index.
func (app *nativeApp) setPinned(id string, pinned bool) {
	saved, err := app.mylist.SetPinned(id, pinned)
	if err != nil {
		app.setError(err)
		return
	}
	app.saved = saved
	if pinned {
		app.setStatus(locales.StatusPinned())
	} else {
		app.setStatus(locales.StatusUnpinned())
	}
}

// openMylist shows the mylist the ID names: the one loaded, the pinned copy, else
// the API's first page.
func (app *nativeApp) openMylist(id string) {
	// A play the menu asked for belongs to its own mylist only: opening
	// another one drops it rather than starting it later.
	if app.playOnLoadID != "" && app.playOnLoadID != id {
		app.playOnLoadID, app.playOnLoadShuffle = "", false
	}
	if id == "" || app.page != nil && app.page.ID == id {
		return
	}
	if (app.loading || app.statusError) && app.loadingID == id {
		return
	}
	// Opening a mylist leaves the playing screen for it, as home does.
	app.queueOpen = false
	app.lyricsOpen = false
	// A combined list is built from its members' saved copies: there is no
	// body and no niconico mylist behind it.
	if entry := app.savedEntry(id); entry != nil && entry.IsCombined() {
		app.openCombined(*entry)
		return
	}
	if app.openSaved(id) {
		return
	}
	app.fetchMylist(id)
}

// playMylist starts a mylist the right-click menu chose: from its first track,
// or from a random one with shuffle on. A card carries only a few sample
// entries, so the mylist is loaded first and the play begins when it arrives.
func (app *nativeApp) playMylist(entry mylistEntry, shuffle bool) {
	app.menuOpen = false
	app.queueOpen = false
	app.lyricsOpen = false
	app.loadingID = ""
	if route := mylistPath(entry.ID); app.router.Path() != route {
		app.router.Push(route)
	}
	if app.page != nil && app.page.ID == entry.ID {
		app.startLoadedMylist(app.page.Items, shuffle)
		return
	}
	app.playOnLoadID, app.playOnLoadShuffle = entry.ID, shuffle
	app.openMylist(entry.ID)
}

// startLoadedMylist starts playback of a loaded mylist: its first playable
// track in order, or a random one with shuffle left on.
func (app *nativeApp) startLoadedMylist(items []MylistItem, shuffle bool) {
	playable := playableItems(items)
	if len(playable) == 0 {
		return
	}
	app.sendPlayer(PlayerCommand{Action: "shuffle", Enabled: shuffle})
	track := playable[0]
	if shuffle {
		track = playable[rand.Intn(len(playable))]
	}
	app.selectTrack(track)
	// Playing from a mylist hands the view to the queue screen, which
	// follows the track as it plays.
	app.queueOpen = true
}

// startPendingMylist starts a play the menu asked for once its mylist has
// loaded, and forgets the request.
func (app *nativeApp) startPendingMylist(id string, items []MylistItem) {
	if app.playOnLoadID != id {
		return
	}
	shuffle := app.playOnLoadShuffle
	app.playOnLoadID, app.playOnLoadShuffle = "", false
	app.startLoadedMylist(items, shuffle)
}

// adoptSavedPage loads a saved body into the shown mylist page and hands its
// entries to the player.
func (app *nativeApp) adoptSavedPage(entry SavedMylist) {
	app.resetTrackPlace(entry.ID)
	total := entry.TotalItemCount
	if total == 0 {
		total = len(entry.Items)
	}
	page := MylistPage{
		ID:             entry.ID,
		Name:           entry.Name,
		Owner:          entry.Owner,
		Description:    entry.Description,
		TotalItemCount: total,
		Items:          append([]MylistItem(nil), entry.Items...),
	}
	app.page = &page
	app.nextPage = 1
	app.hasMore = false
	app.resetThumbFailures()
	app.resetThumbnailFill(page.Items)
	app.sendPlayer(PlayerCommand{Action: "setItems", ID: page.ID, Name: page.Name, Items: page.Items})
}

// resetTrackPlace starts the track list at the top of the mylist id names,
// when that is not the one already shown: the list keeps the place of the
// mylist shown before otherwise, so another one would open mid-way down.
func (app *nativeApp) resetTrackPlace(id string) {
	if app.page != nil && app.page.ID == id {
		return
	}
	app.trackList.ScrollTo(0, ui.Start)
}

// adoptShownBody hands the saved body of the mylist to the shown page and
// the player when that is the mylist on screen.
func (app *nativeApp) adoptShownBody(id string) {
	if app.page == nil || app.page.ID != id {
		return
	}
	body, err := app.mylist.LoadSavedBody(id)
	if err != nil {
		return
	}
	app.adoptSavedPage(body)
}

// openSaved shows the saved body of the mylist id names, reporting whether
// there is one; the route is already the page shown.
func (app *nativeApp) openSaved(id string) bool {
	if app.savedEntry(id) == nil {
		return false
	}
	body, err := app.mylist.LoadSavedBody(id)
	if err != nil {
		// No body yet — the saving has not reached this mylist — or one
		// that cannot be read: the API answers instead.
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("mylists: %v", err)
		}
		return false
	}
	app.loadToken++
	app.loading = false
	app.pendingAdvance = false
	app.setStatus(locales.StatusFromSaved())
	app.playerError = ""
	app.adoptSavedPage(body)
	app.startPendingMylist(body.ID, body.Items)
	return true
}

// fetchMylist loads page 1 of the mylist the input names, an ID or a URL, into
// the player and the view; the route follows the ID the API reports.
func (app *nativeApp) fetchMylist(input string) {
	if strings.TrimSpace(input) == "" {
		return
	}
	app.loadToken++
	token := app.loadToken
	app.loading = true
	app.loadingID = input
	app.pendingAdvance = false
	app.page = nil
	app.hasMore = false
	app.setStatus(locales.StatusLoadingMylist())
	app.playerError = ""
	go func() {
		page, err := app.mylist.GetPage(input, 1)
		app.update(func() {
			if token != app.loadToken {
				return
			}
			app.loading = false
			if err != nil {
				if app.playOnLoadID == input {
					app.playOnLoadID, app.playOnLoadShuffle = "", false
				}
				app.setError(err)
				return
			}
			app.resetTrackPlace(page.ID)
			app.page = &page
			app.nextPage = 2
			app.hasMore = page.HasNext
			app.resetThumbFailures()
			app.resetThumbnailFill(page.Items)
			app.setStatus("")
			if route := mylistPath(page.ID); app.router.Path() != route {
				app.router.Push(route)
			}
			// The opened mylist belongs on the home from here on, and its
			// body is saved in the background, so opening it later needs no
			// network.
			if saved, err := app.mylist.TrackMylist(page); err != nil {
				log.Printf("mylists: %v", err)
			} else {
				app.saved = saved
			}
			app.saveBody(page.ID)
			app.sendPlayer(PlayerCommand{Action: "setItems", ID: page.ID, Name: page.Name, Items: page.Items})
			app.startPendingMylist(page.ID, page.Items)
		})
	}()
}

func (app *nativeApp) loadMore() {
	if app.loading || !app.hasMore || app.page == nil {
		return
	}
	pageID, pageNo, token := app.page.ID, app.nextPage, app.loadToken
	app.loading = true
	app.setStatus(locales.StatusLoadingMore())
	go func() {
		page, err := app.mylist.GetPage(pageID, pageNo)
		app.update(func() {
			if token != app.loadToken {
				return
			}
			app.loading = false
			if err != nil {
				app.setError(err)
				app.pendingAdvance = false
				return
			}
			app.page.Items = append(app.page.Items, page.Items...)
			app.page.TotalItemCount = page.TotalItemCount
			app.nextPage++
			app.hasMore = page.HasNext
			app.page.HasNext = page.HasNext
			app.resetThumbFailures()
			app.queueThumbnailFill(page.Items)
			app.setStatus("")
			app.sendPlayer(PlayerCommand{Action: "appendItems", Items: page.Items})
		})
	}()
}

// canLoadNext reports whether loading the next page of the shown mylist may
// extend the playing queue: only the queue built from that mylist can take it.
// The playing mylist is not always the one on screen, and its pages are not
// loaded from here.
func (app *nativeApp) canLoadNext() bool {
	return app.hasMore && app.page != nil && app.player.MylistID == app.page.ID
}

// canNext reports whether a next track can be started: the queue has one
// loaded, the playing mylist has a page to load, or repeat goes back to the
// queue's start. The player bar's button and the tray's 次へ judge alike.
func (app *nativeApp) canNext() bool {
	return app.player.WatchID != "" &&
		(app.player.CanNext || app.canLoadNext() || app.player.Repeat)
}

// advance moves to the next track, whether the button asked for it or the
// track playing ran out.
func (app *nativeApp) advance() {
	if app.player.CanNext {
		app.sendPlayer(PlayerCommand{Action: "next"})
		return
	}
	if app.canLoadNext() {
		app.pendingAdvance = true
		app.loadMore()
	} else if app.player.Repeat && app.player.WatchID != "" {
		app.sendPlayer(PlayerCommand{Action: "next"})
	}
}

// selectTrack plays a track the row was clicked on, from its start.
func (app *nativeApp) selectTrack(item MylistItem) {
	if !isPlayable(item) {
		return
	}
	app.pendingAdvance = false
	app.playerError = ""
	app.sendPlayer(PlayerCommand{Action: "select", WatchID: item.WatchID})
}

// saveCurrent pins the shown mylist, fetching and saving it when its body is
// not saved yet.
func (app *nativeApp) saveCurrent() {
	if app.page == nil || app.saving {
		return
	}
	app.saveMylist(app.page.ID)
}

// saveMylist fetches every page of the mylist, saves its body, and pins it;
// the shown page adopts the saved body as soon as it is written.
func (app *nativeApp) saveMylist(id string) {
	if app.saving {
		return
	}
	app.saving = true
	app.setStatus(locales.StatusSavingMylist())
	go func() {
		saved, err := app.mylist.SaveMylist(id)
		app.update(func() {
			app.saving = false
			if err != nil {
				app.setError(err)
				return
			}
			app.saved = saved
			app.adoptShownBody(id)
			app.setStatus(locales.StatusPinned())
		})
	}()
}

// refreshSaved replaces the saved body of the mylist from the API.
func (app *nativeApp) refreshSaved(entry SavedIndexEntry) {
	if app.saving {
		return
	}
	app.saving = true
	app.setStatus(locales.StatusRefreshingMylist())
	go func() {
		saved, err := app.mylist.RefreshMylist(entry.ID)
		app.update(func() {
			app.saving = false
			if err != nil {
				app.setError(err)
				return
			}
			app.saved = saved
			app.adoptShownBody(entry.ID)
			app.setStatus(locales.StatusRefreshed())
			// A refreshed body can hold new tracks: a download-enabled
			// mylist fetches them without waiting for the next start.
			app.enqueueSavedAudio(entry.ID)
		})
	}()
}

// reloadSaved reloads the saved index into the view; callers are in a UI
// update, so the view ends up with the disk's latest.
func (app *nativeApp) reloadSaved() {
	saved, err := app.mylist.ListSaved()
	if err != nil {
		log.Printf("mylists: %v", err)
		return
	}
	app.saved = saved
}
