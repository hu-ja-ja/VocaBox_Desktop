package main

import (
	"testing"
)

// savedApp returns an app over a temp config dir: nothing a saved-mylist
// test does may touch the real one.
func savedApp(t *testing.T) *nativeApp {
	t.Helper()
	return newNativeApp(&Mylist{configDir: t.TempDir()})
}

// syncTestOwn merges the account's mylists into the app's index, as the
// sign-in does.
func syncTestOwn(t *testing.T, app *nativeApp, own ...OwnMylist) {
	t.Helper()
	saved, err := app.mylist.SyncOwn(own)
	if err != nil {
		t.Fatal(err)
	}
	app.saved = saved
}

// indexSaved records a saved mylist on the app, as a save or an open leaves
// it: items != nil also writes the body, the way the background saving does.
func indexSaved(t *testing.T, app *nativeApp, entry SavedIndexEntry, items []MylistItem) {
	t.Helper()
	total := entry.TotalItemCount
	if total == 0 {
		total = len(items)
	}
	page := MylistPage{ID: entry.ID, Name: entry.Name, Owner: entry.Owner, TotalItemCount: total, Items: items}
	if _, err := app.mylist.TrackMylist(page); err != nil {
		t.Fatal(err)
	}
	// A combined list has no body of its own: its tracks come from its
	// members' copies.
	if items != nil && !isCombinedID(entry.ID) {
		body := SavedMylist{ID: entry.ID, Name: entry.Name, Owner: entry.Owner, TotalItemCount: total, Items: items}
		if err := app.mylist.writeBody(body); err != nil {
			t.Fatal(err)
		}
	}
	if entry.Pinned {
		if _, err := app.mylist.SetPinned(entry.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := app.mylist.ListSaved()
	if err != nil {
		t.Fatal(err)
	}
	// Members and the mine flag are not on a page's line: a test that sets
	// them writes them into the index itself.
	if at, ok := indexAt(saved, entry.ID); ok && (entry.Members != nil || entry.Mine || entry.Private) {
		saved[at].Members, saved[at].Mine, saved[at].Private = entry.Members, entry.Mine, entry.Private
		if err := app.mylist.writeIndex(saved); err != nil {
			t.Fatal(err)
		}
	}
	app.saved = saved
}

func hasCommand(commands []PlayerCommand, action string) bool {
	for _, command := range commands {
		if command.Action == action {
			return true
		}
	}
	return false
}

// lastCommand returns the last command with the given action.
func lastCommand(commands []PlayerCommand, action string) (PlayerCommand, bool) {
	for i := len(commands) - 1; i >= 0; i-- {
		if commands[i].Action == action {
			return commands[i], true
		}
	}
	return PlayerCommand{}, false
}

// noAudioServer points the app's nico at a port nothing answers, so a test
// that turns offline saving on does not reach niconico.
func noAudioServer(app *nativeApp) {
	app.nico.baseURL = "http://127.0.0.1:1"
	app.nico.apiBase = "http://127.0.0.1:1"
}
