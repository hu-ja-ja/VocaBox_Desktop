package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNativeUIOpensSavedSnapshot(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "123", Name: "作業用マイリスト"}, []MylistItem{{
		WatchID:   "sm9",
		Title:     "サンプル曲",
		Uploader:  "投稿者",
		Duration:  90,
		Available: true,
	}})

	tester := ui.NewTester(app.view, 1120, 760)
	if !tester.HasText("VocaBox") {
		t.Fatalf("initial view is missing VocaBox: %q", tester.Texts())
	}
	if err := tester.Click("作業用マイリスト"); err != nil {
		t.Fatal(err)
	}
	if app.page == nil || app.page.ID != "123" || len(app.page.Items) != 1 {
		t.Fatalf("saved snapshot was not opened: %#v", app.page)
	}
	if !tester.HasText("サンプル曲") {
		t.Fatalf("track is not shown: %q", tester.Texts())
	}

	app.player = playerState{WatchID: "sm9", Ready: true, PositionMS: 0, DurationMS: 90_000, Volume: 1}
	if err := tester.Click("再生"); err != nil {
		t.Fatal(err)
	}
	if !hasCommand(app.sent, "play") {
		t.Fatalf("icon play button did not send a command: %#v", app.sent)
	}
}

// TestNativeUIKeepsPlayingTrackAcrossMylists checks that opening another mylist
// neither stops the track playing nor hands the queue to the shown mylist.
func TestNativeUIKeepsPlayingTrackAcrossMylists(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "マイリストA"},
		[]MylistItem{{WatchID: "sm1", Title: "Aの曲", Available: true, Duration: 120}})
	indexSaved(t, app, SavedIndexEntry{ID: "222", Name: "マイリストB"},
		[]MylistItem{{WatchID: "sm2", Title: "Bの曲", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("マイリストA"); err != nil {
		t.Fatal(err)
	}
	// The player reports a track of A playing while mylist B is shown.
	app.player = playerState{WatchID: "sm1", MylistID: "111", Title: "Aの曲", Ready: true, Playing: true, Volume: 1}
	app.sent = nil

	if err := tester.Click("マイリストB"); err != nil {
		t.Fatal(err)
	}
	if app.player.WatchID != "sm1" || !app.player.Playing {
		t.Fatalf("switching mylists stopped the playing track: %#v", app.player)
	}
	set, ok := lastCommand(app.sent, "setItems")
	if !ok || set.ID != "222" {
		t.Fatalf("the opened mylist was not sent: %#v", app.sent)
	}
	if !tester.HasText("Aの曲") {
		t.Fatalf("the player bar lost the playing track: %q", tester.Texts())
	}
}

// TestNativeUICanLoadNextOnlyForPlayingMylist checks that only the queue built
// from the shown mylist takes its next page: a mylist merely shown must not feed
// the queue of the mylist playing.
func TestNativeUICanLoadNextOnlyForPlayingMylist(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.page = &MylistPage{ID: "222"}
	app.hasMore = true
	app.player = playerState{WatchID: "sm1", MylistID: "111"}

	if app.canLoadNext() {
		t.Fatal("loading the shown mylist must not extend another mylist's queue")
	}
	app.advance()
	if app.pendingAdvance || app.loading || hasCommand(app.sent, "next") {
		t.Fatalf("advancing left the playing queue: pending=%v loading=%v", app.pendingAdvance, app.loading)
	}

	app.player.MylistID = "222"
	if !app.canLoadNext() {
		t.Fatal("the playing mylist's next page can extend its queue")
	}
	app.page = nil
	if app.canLoadNext() {
		t.Fatal("no page means no next page to load")
	}
}

// TestNativeUIMylistDoesNotFollowPlayback checks the mylist screen carries
// no playback state: opening a mylist keeps its top in view however far down
// the track playing sits.
func TestNativeUIMylistDoesNotFollowPlayback(t *testing.T) {
	app := savedApp(t)
	items := make([]MylistItem, 100)
	for i := range items {
		items[i] = MylistItem{WatchID: fmt.Sprintf("sm%d", i+1), Title: fmt.Sprintf("曲%d", i+1), Available: true, Duration: 120}
	}
	indexSaved(t, app, SavedIndexEntry{ID: "123", Name: "作業用マイリスト"}, items)

	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("作業用マイリスト"); err != nil {
		t.Fatal(err)
	}
	app.player = playerState{WatchID: "sm80", Ready: true, Volume: 1}
	tester.Frame()
	if first, _ := app.trackList.Visible(); first != 0 {
		t.Fatalf("the mylist followed the track playing: first=%d", first)
	}
}

// TestNativeUIOpensMylistAtItsTop checks a mylist opens at its first track,
// whichever it is: the track list keeps the place of the mylist shown before
// otherwise, so another one would open mid-way down.
func TestNativeUIOpensMylistAtItsTop(t *testing.T) {
	var items strings.Builder
	for i := 1; i <= 100; i++ {
		if i > 1 {
			items.WriteString(",")
		}
		fmt.Fprintf(&items, `{"watchId":"sm%d","video":{"id":"sm%d","title":"曲%d","duration":60}}`, i, i, i)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"meta":{"status":200},"data":{"mylist":{"name":"取り込むマイリスト",`+
			`"items":[%s],"totalItemCount":100,"hasNext":false}}}`, items.String())
	}))
	t.Cleanup(srv.Close)
	app := newNativeApp(&Mylist{client: srv.Client(), baseURL: srv.URL, configDir: t.TempDir()})

	page := make([]MylistItem, 100)
	for i := range page {
		page[i] = MylistItem{WatchID: fmt.Sprintf("sm%d", i+1), Title: fmt.Sprintf("曲%d", i+1), Available: true, Duration: 120}
	}
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "作業用", TotalItemCount: 100}, page)
	indexSaved(t, app, SavedIndexEntry{ID: "222", Name: "別の作業用", TotalItemCount: 100}, page)
	indexSaved(t, app, SavedIndexEntry{ID: "combine-1", Name: "結合リスト", Members: []string{"111"}, TotalItemCount: 100}, page)

	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("作業用"); err != nil {
		t.Fatal(err)
	}
	// Another saved mylist, and a combined one, each open at their top.
	for _, next := range []string{"別の作業用", "結合リスト"} {
		app.trackList.ScrollTo(50, ui.Start)
		tester.Frame()
		if first, _ := app.trackList.Visible(); first != 50 {
			t.Fatalf("the list did not scroll before %q: first=%d", next, first)
		}
		if err := tester.Click(next); err != nil {
			t.Fatal(err)
		}
		if first, _ := app.trackList.Visible(); first != 0 {
			t.Fatalf("%q did not open at its top: first=%d", next, first)
		}
	}

	// A mylist fetched from the API does too.
	app.trackList.ScrollTo(50, ui.Start)
	tester.Frame()
	app.fetchMylist("999")
	waitForApp(t, app, "the fetched mylist", func() bool { return app.page != nil && app.page.ID == "999" })
	tester.Frame()
	if first, _ := app.trackList.Visible(); first != 0 {
		t.Fatalf("the fetched mylist did not open at its top: first=%d", first)
	}
	fillIdle(t, app)
}
