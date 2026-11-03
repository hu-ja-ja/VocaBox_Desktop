package main

import (
	"image/color"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

func TestNativeUIHomeImportsFromCard(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)

	// An empty home still offers the import card.
	if !tester.HasText("マイリストを読み込む") {
		t.Fatalf("import card is missing: %q", tester.Texts())
	}
	if tester.HasText("マイリストのURLまたはID") {
		t.Fatalf("the sidebar still carries the import field: %q", tester.Texts())
	}
	if err := tester.Click("マイリストを読み込む"); err != nil {
		t.Fatal(err)
	}
	if !app.importing {
		t.Fatal("import card did not open the dialog")
	}
	if !tester.HasText("マイリストを読み込む") {
		t.Fatalf("dialog is not shown: %q", tester.Texts())
	}
	if !tester.Focused("マイリストのURLまたはID") {
		t.Fatal("the dialog did not focus its field")
	}
	tester.Type("27411728")
	if app.input != "27411728" {
		t.Fatalf("typing did not reach the field: %q", app.input)
	}
	if err := tester.Click("キャンセル"); err != nil {
		t.Fatal(err)
	}
	if app.importing {
		t.Fatal("cancel did not close the dialog")
	}
	if app.page != nil || len(app.sent) != 0 {
		t.Fatalf("cancel must not open anything: sent=%d page=%#v", len(app.sent), app.page)
	}
}

// TestNativeUIHomePinsFirst checks the home's cards: the pinned mylists come
// first with their pin mark, a private mylist carries the lock mark, the
// account's other mylists follow, the import card stays last, and signing out
// keeps the saved local copies, the account's own ones included.
func TestNativeUIHomePinsFirst(t *testing.T) {
	app := savedApp(t)
	// The sidebar shows its own pin marks for the same mylists, so close it
	// and let the test look at the cards alone.
	app.sidebarOpen = false
	app.loggedIn = true
	// The account's own mylists as the sign-in sync leaves them, the private
	// one pinned.
	syncTestOwn(t, app,
		OwnMylist{ID: "71043760", Name: "自分の非公開マイリスト", ItemCount: 2,
			Items: []MylistItem{{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120}}},
		OwnMylist{ID: "71043761", Name: "自分の公開マイリスト", ItemCount: 0, IsPublic: true},
	)
	if saved, err := app.mylist.SetPinned("71043760", true); err != nil {
		t.Fatal(err)
	} else {
		app.saved = saved
	}
	// A pinned mylist of somebody else's, saved earlier.
	indexSaved(t, app, SavedIndexEntry{ID: "555", Name: "ピン留めした公開マイリスト", TotalItemCount: 3, Pinned: true},
		[]MylistItem{{WatchID: "sm5", Title: "公開の曲", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)

	for _, text := range []string{"全2曲", "全3曲", "全0曲"} {
		if !tester.HasText(text) {
			t.Fatalf("home does not show %q: %q", text, tester.Texts())
		}
	}
	// The pin and the private mark are badges over the tile: the pin on the
	// right, the private chip on the left, both above the meta line.
	pin, ok := tester.Find("ピン留め")
	if !ok {
		t.Fatalf("the pin badge is missing: %q", tester.Texts())
	}
	lock, ok := tester.Find("非公開")
	if !ok {
		t.Fatalf("the private badge is missing: %q", tester.Texts())
	}
	meta, ok := tester.Find("全2曲")
	if !ok {
		t.Fatalf("the size line is missing: %q", tester.Texts())
	}
	if pin.Y >= meta.Y || lock.Y >= meta.Y {
		t.Fatalf("the badges are not over the tile: pin=%v lock=%v meta=%v", pin, lock, meta)
	}
	right := meta.X + meta.W/2
	if pin.X <= right || lock.X <= right {
		t.Fatalf("the badges are not on the tile's right: pin=%v lock=%v meta=%v", pin, lock, meta)
	}
	if lock.Y <= pin.Y {
		t.Fatalf("the private badge is not below the pin: pin=%v lock=%v", pin, lock)
	}

	// Pinned first, the account's other mylists after them, import last; the
	// pinned ones are only put at the front, without a section of their own,
	// and nothing was read in from a URL here.
	texts := tester.Texts()
	index := func(s string) int {
		for i, text := range texts {
			if text == s {
				return i
			}
		}
		return -1
	}
	if index("ピン留めしたマイリスト") >= 0 || index("自分のマイリスト") >= 0 {
		t.Fatalf("the pinned and own mylists are still split into sections: %q", texts)
	}
	if tester.HasText("読み込んだマイリスト") {
		t.Fatalf("an empty group is shown: %q", texts)
	}
	if !(index("全2曲") < index("全0曲") && index("全3曲") < index("全0曲") &&
		index("全0曲") < index("マイリストを読み込む")) {
		t.Fatalf("home order is wrong: %q", texts)
	}

	// Signing out keeps every saved mylist, the account's own ones included:
	// the bodies are local.
	app.clearSession()
	tester.Frame()
	for _, name := range []string{"自分の非公開マイリスト", "自分の公開マイリスト", "ピン留めした公開マイリスト"} {
		if !tester.HasText(name) {
			t.Fatalf("logout dropped the saved mylist %q: %q", name, tester.Texts())
		}
	}
}

// TestNativeUIHomeUnpinsFromMenu checks the card's right-click menu: a
// pinned card offers ピン留めを外す, and choosing it unpins the mylist while
// its saved copy stays.
func TestNativeUIHomeUnpinsFromMenu(t *testing.T) {
	app := savedApp(t)
	app.sidebarOpen = false
	indexSaved(t, app, SavedIndexEntry{ID: "555", Name: "ピン留めした公開マイリスト", TotalItemCount: 3, Pinned: true},
		[]MylistItem{{WatchID: "sm5", Title: "公開の曲", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)

	if err := tester.RightClick("ピン留めした公開マイリスト"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("ピン留めを外す"); !ok {
		t.Fatalf("card menu did not open: %q", tester.Texts())
	}
	if err := tester.Click("ピン留めを外す"); err != nil {
		t.Fatal(err)
	}
	if len(app.saved) != 1 || app.saved[0].Pinned {
		t.Fatalf("the mylist must stay saved unpinned: %+v", app.saved)
	}
	if !tester.HasText("ピン留めした公開マイリスト") {
		t.Fatalf("unpinning dropped the saved card: %q", tester.Texts())
	}
	if _, ok := tester.Find("ピン留め"); ok {
		t.Fatalf("the pin badge stayed: %q", tester.Texts())
	}
}

// TestNativeUIHomePinsFromMenu checks the other direction: an unpinned card
// offers ピン留め, and choosing it starts the pin.
func TestNativeUIHomePinsFromMenu(t *testing.T) {
	// The fetch holds until the test ends, so the assertion sees the pin
	// still starting rather than racing the fetch; the release refuses it, so
	// the abandoned save leaves nothing behind for the cleanup.
	hold := make(chan struct{})
	var fail atomic.Bool
	var release sync.Once
	releaseHold := func() { release.Do(func() { close(hold) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hold
		if fail.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(savedFixture))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		fail.Store(true)
		releaseHold()
	})
	mylist := &Mylist{client: srv.Client(), baseURL: srv.URL, configDir: t.TempDir()}

	app := newNativeApp(mylist)
	app.sidebarOpen = false
	syncTestOwn(t, app, OwnMylist{ID: "71043760", Name: "自分の公開マイリスト", ItemCount: 2, IsPublic: true})
	tester := ui.NewTester(app.view, 1120, 760)

	if err := tester.RightClick("自分の公開マイリスト"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("ピン留め"); !ok {
		t.Fatalf("card menu did not open: %q", tester.Texts())
	}
	if err := tester.Click("ピン留め"); err != nil {
		t.Fatal(err)
	}
	if !app.saving {
		t.Fatalf("choosing ピン留め did not start pinning: saving=%v status=%q", app.saving, app.status)
	}
}

// TestNativeUIHomeGroupsAndDeletes checks the home's groups — the pinned
// mylists, the account's own, the ones read in from a URL — and that a mylist
// read in can be deleted from its card's menu after a confirmation, while an
// own mylist offers no delete.
func TestNativeUIHomeGroupsAndDeletes(t *testing.T) {
	app := savedApp(t)
	app.sidebarOpen = false
	syncTestOwn(t, app, OwnMylist{ID: "71043760", Name: "自分のリストA", ItemCount: 2, IsPublic: true})
	indexSaved(t, app, SavedIndexEntry{ID: "555", Name: "ピン留めした読み込み", TotalItemCount: 3, Pinned: true}, nil)
	indexSaved(t, app, SavedIndexEntry{ID: "556", Name: "読み込み済みの他人リスト", TotalItemCount: 1},
		[]MylistItem{{WatchID: "sm7", Title: "読み込み曲", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)

	texts := tester.Texts()
	indexOf := func(s string) int {
		for i, text := range texts {
			if text == s {
				return i
			}
		}
		return -1
	}
	// Pinning only moves a mylist to the front: no section of its own. The
	// mylists read in from a URL keep their own group after the account's own.
	if indexOf("ピン留めしたマイリスト") >= 0 || indexOf("自分のマイリスト") >= 0 {
		t.Fatalf("the pinned and own mylists are still split into sections: %q", texts)
	}
	if !(indexOf("ピン留めした読み込み") < indexOf("自分のリストA")) {
		t.Fatalf("the pinned mylist does not come first: %q", texts)
	}
	if readAt := indexOf("読み込んだマイリスト"); readAt < 0 || readAt < indexOf("自分のリストA") {
		t.Fatalf("the read-in group is missing or out of order: %q", texts)
	}

	// rightClickCard scrolls the home until the card is on screen, then
	// right-clicks its middle: the later groups sit below the fold.
	rightClickCard := func(name string) {
		t.Helper()
		for i := 0; i < 12; i++ {
			if r, ok := tester.Find(name); ok && r.W > 0 && r.Y > 90 && r.Y+r.H < 730 {
				tester.RightClickAt(r.X+r.W/2, r.Y+r.H/2)
				return
			}
			tester.Scroll(700, 400, 0, 200)
		}
		t.Fatalf("the card %q never came on screen: %q", name, tester.Texts())
	}

	// The account's own mylists are not ours to delete: no 削除 in the menu.
	rightClickCard("自分のリストA")
	if _, ok := tester.Find("マイリストのメニュー"); !ok {
		t.Fatalf("the card menu did not open: %q", tester.Texts())
	}
	if _, ok := tester.Find("削除"); ok {
		t.Fatalf("an own mylist offers to be deleted: %q", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)

	// A mylist read in can be deleted, but only after asking.
	rightClickCard("読み込み済みの他人リスト")
	if _, ok := tester.Find("削除"); !ok {
		t.Fatalf("a mylist read in cannot be deleted: %q", tester.Texts())
	}
	if err := tester.Click("削除"); err != nil {
		t.Fatal(err)
	}
	if !app.confirmDelete || !tester.HasText("保存したマイリストを削除しますか？") {
		t.Fatalf("the delete did not ask first: %q", tester.Texts())
	}
	if err := tester.Click("キャンセル"); err != nil {
		t.Fatal(err)
	}
	if app.confirmDelete || len(app.saved) != 3 {
		t.Fatalf("cancelling the dialog changed the saved mylists: %+v", app.saved)
	}

	rightClickCard("読み込み済みの他人リスト")
	if err := tester.Click("削除"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("削除する"); err != nil {
		t.Fatal(err)
	}
	if len(app.saved) != 2 || app.saved[0].ID != "71043760" {
		t.Fatalf("the deleted mylist stays: %+v", app.saved)
	}
	if app.mylist.HasSavedBody("556") {
		t.Error("the deleted mylist keeps its body")
	}
	if tester.HasText("読み込み済みの他人リスト") {
		t.Fatalf("the deleted card stays on the home: %q", tester.Texts())
	}
}

// TestNativeUIMylistHeaderDeletesReadInMylist checks the mylist screen's own
// 削除 button: a mylist read in carries one, and the confirmation deletes the
// saved copy.
func TestNativeUIMylistHeaderDeletesReadInMylist(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "556", Name: "ヘッダーの読み込み", TotalItemCount: 1},
		[]MylistItem{{WatchID: "sm7", Title: "読み込み曲", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)

	if err := tester.Click("ヘッダーの読み込み"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("削除"); !ok {
		t.Fatalf("the mylist screen has no delete: %q", tester.Texts())
	}
	if err := tester.Click("削除"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("保存したマイリストを削除しますか？") {
		t.Fatalf("the delete did not ask first: %q", tester.Texts())
	}
	if err := tester.Click("削除する"); err != nil {
		t.Fatal(err)
	}
	if len(app.saved) != 0 {
		t.Fatalf("the delete did not take: %+v", app.saved)
	}
}

// TestNativeUIMylistMenuOpensAtPointer checks the card's menu opens where the
// pointer right-clicked rather than below the card, and moves in from the
// window's edge.
func TestNativeUIMylistMenuOpensAtPointer(t *testing.T) {
	app := savedApp(t)
	app.sidebarOpen = false
	syncTestOwn(t, app,
		OwnMylist{ID: "71043760", Name: "位置のカード", ItemCount: 2, IsPublic: true},
		OwnMylist{ID: "71043761", Name: "別のカード", ItemCount: 2, IsPublic: true},
	)
	tester := ui.NewTester(app.view, 1120, 760)

	card, ok := tester.Find("位置のカード")
	if !ok {
		t.Fatalf("the card was not found: %q", tester.Texts())
	}
	near := func(got, want float32) bool {
		return got > want-1 && got < want+1
	}
	x, y := card.X+16, card.Y+card.H-16
	tester.RightClickAt(x, y)
	// The menu pops in at its scale of a moment ago; past the motion, it
	// reads from the pointer's edge.
	tester.Advance(300 * time.Millisecond)
	panel, ok := tester.Find("マイリストのメニュー")
	if !ok {
		t.Fatalf("the menu did not open: %q", tester.Texts())
	}
	if !near(panel.X, x) || !near(panel.Y, y) {
		t.Fatalf("the menu opened at %v, %v, want the click at %v, %v", panel.X, panel.Y, x, y)
	}

	// Right-clicking another card moves the menu with it.
	other, ok := tester.Find("別のカード")
	if !ok {
		t.Fatalf("the second card was not found: %q", tester.Texts())
	}
	tester.RightClickAt(other.X+16, other.Y+16)
	// The menu pops in again, moving at its entering scale; past the
	// motion, it reads from the pointer.
	tester.Advance(300 * time.Millisecond)
	if app.menuEntry.ID != "71043761" {
		t.Fatalf("the menu still belongs to %q", app.menuEntry.ID)
	}
	panel, ok = tester.Find("マイリストのメニュー")
	if !ok || !near(panel.X, other.X+16) || !near(panel.Y, other.Y+16) {
		t.Fatalf("the menu did not move to the second card: %v, %v", panel, ok)
	}
	tester.Key(0, ui.KeyEscape)
	if _, ok := tester.Find("マイリストのメニュー"); ok {
		t.Fatal("Escape did not close the menu")
	}

	// A click at the card's right edge in a narrow window leaves no room:
	// the menu stays inside it.
	narrow := ui.NewTester(app.view, 380, 760)
	card, ok = narrow.Find("位置のカード")
	if !ok {
		t.Fatalf("the card was not found in the narrow window: %q", narrow.Texts())
	}
	tester = narrow
	tester.RightClickAt(card.X+card.W-4, card.Y+card.H-16)
	tester.Advance(300 * time.Millisecond)
	panel, ok = tester.Find("マイリストのメニュー")
	if !ok {
		t.Fatalf("the menu did not open in the narrow window: %q", tester.Texts())
	}
	if panel.X+panel.W > 380 {
		t.Fatalf("the menu leaves the window: %v", panel)
	}
}

// TestNativeUIMylistMenuKeyboard checks the card's menu opens from the
// keyboard — the menu key and Shift+F10 — and its item works from there.
func TestNativeUIMylistMenuKeyboard(t *testing.T) {
	app := savedApp(t)
	app.sidebarOpen = false
	indexSaved(t, app, SavedIndexEntry{ID: "555", Name: "キーボードのピン", TotalItemCount: 2, Pinned: true},
		[]MylistItem{{WatchID: "sm5", Title: "公開の曲", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)

	focused := false
	for i := 0; i < 40 && !focused; i++ {
		tester.Key(0, ui.KeyTab)
		focused = tester.Focused("キーボードのピン")
	}
	if !focused {
		t.Fatal("Tab did not reach the card")
	}
	tester.Key(0, ui.KeyContextMenu)
	if _, ok := tester.Find("ピン留めを外す"); !ok {
		t.Fatalf("the menu key did not open the menu: %q", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	if _, ok := tester.Find("ピン留めを外す"); ok {
		t.Fatal("Escape did not close the menu")
	}
	tester.Key(ui.Shift, ui.KeyF10)
	if _, ok := tester.Find("ピン留めを外す"); !ok {
		t.Fatalf("Shift+F10 did not open the menu: %q", tester.Texts())
	}
	tester.Key(0, ui.KeyTab)
	if !tester.Focused("ピン留めを外す") {
		t.Fatal("Tab did not move the focus into the menu")
	}
	tester.Key(0, ui.KeyEnter)
	if len(app.saved) != 1 || app.saved[0].Pinned {
		t.Fatalf("Enter did not unpin the mylist: %+v", app.saved)
	}
}

// TestNativeUIMylistMenuPlays checks the card's right-click menu starts a
// mylist: 再生 plays its first track in order, シャッフル再生 leaves shuffle on
// and starts from a random one. A pinned snapshot loads without the API, so
// the play begins on the click.
func TestNativeUIMylistMenuPlays(t *testing.T) {
	newCard := func() *nativeApp {
		app := savedApp(t)
		app.sidebarOpen = false
		indexSaved(t, app, SavedIndexEntry{ID: "555", Name: "再生するマイリスト", TotalItemCount: 3},
			[]MylistItem{
				{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120},
				{WatchID: "sm2", Title: "二曲目", Available: true, Duration: 120},
				{WatchID: "sm3", Title: "三曲目", Available: true, Duration: 120},
			})
		return app
	}

	app := newCard()
	// The player bar's own play button reads 再生 too; mark the player as
	// playing so the bar shows 一時停止 and the menu item is the only 再生
	// the tester can find.
	app.player.Playing = true
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.RightClick("再生するマイリスト"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("再生"); err != nil {
		t.Fatal(err)
	}
	if shuffle, ok := lastCommand(app.sent, "shuffle"); !ok || shuffle.Enabled {
		t.Fatalf("再生 did not select in order: %#v", app.sent)
	}
	if selected, ok := lastCommand(app.sent, "select"); !ok || selected.WatchID != "sm1" {
		t.Fatalf("再生 did not start the first track: %#v", app.sent)
	}
	if !app.queueOpen {
		t.Fatal("再生 did not open the queue screen")
	}

	app = newCard()
	tester = ui.NewTester(app.view, 1120, 760)
	if err := tester.RightClick("再生するマイリスト"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("シャッフル再生"); err != nil {
		t.Fatal(err)
	}
	if shuffle, ok := lastCommand(app.sent, "shuffle"); !ok || !shuffle.Enabled {
		t.Fatalf("シャッフル再生 did not turn shuffle on: %#v", app.sent)
	}
	selected, ok := lastCommand(app.sent, "select")
	if !ok || (selected.WatchID != "sm1" && selected.WatchID != "sm2" && selected.WatchID != "sm3") {
		t.Fatalf("シャッフル再生 did not start a track of the mylist: %#v", app.sent)
	}
	if !app.queueOpen {
		t.Fatal("シャッフル再生 did not open the queue screen")
	}

	// Starting an unpinned mylist from the sidebar while another mylist is
	// shown keeps the route on the new mylist, so the next frame does not
	// cancel the load.
	hold := make(chan struct{})
	var fail atomic.Bool
	var release sync.Once
	releaseHold := func() { release.Do(func() { close(hold) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hold
		// Once the test is done the server refuses: the load the test left
		// pending stops without leaving files behind for the cleanup.
		if fail.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(savedFixture))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		fail.Store(true)
		releaseHold()
	})
	app = newNativeApp(&Mylist{client: srv.Client(), baseURL: srv.URL, configDir: t.TempDir()})
	app.player.Playing = true
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "表示中のマイリスト"},
		[]MylistItem{{WatchID: "sm9", Title: "表示曲", Available: true, Duration: 120}})
	syncTestOwn(t, app, OwnMylist{ID: "27411728", Name: "未ピンのマイリスト", ItemCount: 2, IsPublic: true})
	tester = ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("表示中のマイリスト"); err != nil {
		t.Fatal(err)
	}
	item, ok := tester.Find("未ピンのマイリスト")
	if !ok {
		t.Fatal("the unpinned mylist is missing from the sidebar")
	}
	tester.RightClickAt(item.X+item.W/2, item.Y+item.H/2)
	if err := tester.Click("再生"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if app.playOnLoadID != "27411728" || app.router.Path() != "/mylist/27411728" {
		t.Fatalf("the next frame cancelled the pending play: playOnLoadID=%q path=%q", app.playOnLoadID, app.router.Path())
	}
}

func TestNativeUIHomeOpensMylistCards(t *testing.T) {
	app := savedApp(t)
	// With the sidebar closed the card is the only element named after the
	// mylist, so a click targets the card.
	app.sidebarOpen = false
	indexSaved(t, app, SavedIndexEntry{ID: "123", Name: "作業用ホーム", TotalItemCount: 6},
		[]MylistItem{
			{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120},
			{WatchID: "sm2", Title: "二曲目", Available: true, Duration: 120},
			{WatchID: "sm3", Title: "三曲目", Available: true, Duration: 120},
			{WatchID: "sm4", Title: "四曲目", Available: true, Duration: 120},
			{WatchID: "sm5", Title: "五曲目", Available: true, Duration: 120},
		})

	tester := ui.NewTester(app.view, 1120, 760)
	if !tester.HasText("ホーム") || !tester.HasText("作業用ホーム") || !tester.HasText("全6曲") {
		t.Fatalf("home does not show the mylist card: %q", tester.Texts())
	}
	if err := tester.Click("作業用ホーム"); err != nil {
		t.Fatal(err)
	}
	if app.router.Path() != "/mylist/123" || app.page == nil || app.page.ID != "123" {
		t.Fatalf("card did not open the mylist: path=%q page=%#v", app.router.Path(), app.page)
	}
	if !tester.HasText("一曲目") {
		t.Fatalf("opened mylist is not shown: %q", tester.Texts())
	}

	// The side buttons of a mouse arrive as the back and forward keys.
	tester.Key(0, ui.KeyBack)
	if app.router.Path() != "/" || !tester.HasText("聴きたいマイリストを選んで再生") {
		t.Fatalf("back did not return home: path=%q", app.router.Path())
	}
	if app.page == nil || app.page.ID != "123" {
		t.Fatalf("the opened mylist was dropped: %#v", app.page)
	}
	tester.Key(0, ui.KeyForward)
	if app.router.Path() != "/mylist/123" || !tester.HasText("一曲目") {
		t.Fatalf("forward did not return to the mylist: path=%q", app.router.Path())
	}

	app.sidebarOpen = true
	tester.Frame()
	// The sidebar unfolds from the rail; past it, its home item is where
	// the pointer finds it.
	tester.Advance(350 * time.Millisecond)
	if err := tester.Click("ホーム"); err != nil {
		t.Fatal(err)
	}
	if app.router.Path() != "/" || !tester.HasText("聴きたいマイリストを選んで再生") {
		t.Fatalf("sidebar home item did not return home: path=%q", app.router.Path())
	}
	if app.page == nil || app.page.ID != "123" {
		t.Fatalf("the opened mylist was dropped: %#v", app.page)
	}
}

// TestNativeUIPinnedCardSlides checks that pinning a card slides it before
// the first one rather than jumping: the pinned card starts where it was
// and arrives only as the motion completes.
func TestNativeUIPinnedCardSlides(t *testing.T) {
	app := savedApp(t)
	app.sidebarOpen = false
	items := []MylistItem{{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120}}
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "一覧", Mine: true}, items)
	indexSaved(t, app, SavedIndexEntry{ID: "222", Name: "二覧", Mine: true}, items)
	tester := ui.NewTester(app.view, 1120, 760)
	if _, ok := tester.Find("一覧"); !ok {
		t.Fatalf("cards are not shown: %q", tester.Texts())
	}
	second, ok := tester.Find("二覧")
	if !ok {
		t.Fatalf("cards are not shown: %q", tester.Texts())
	}
	app.setPinned("222", true)
	tester.Frame()
	pinned, _ := tester.Find("二覧")
	if pinned.X != second.X || pinned.Y != second.Y {
		t.Fatalf("pinned card jumped rather than sliding: was=%v now=%v", second, pinned)
	}
	tester.Advance(300 * time.Millisecond)
	pinned, _ = tester.Find("二覧")
	other, _ := tester.Find("一覧")
	if pinned.X >= other.X {
		t.Fatalf("pinned card did not slide before the other: pinned=%v other=%v", pinned, other)
	}
}

// TestNativeUIMenuPanelFadesIn checks that the card menu's panel enters
// through its transition: its items show faintly at first, and fully once
// the motion completes.
func TestNativeUIMenuPanelFadesIn(t *testing.T) {
	app := savedApp(t)
	app.sidebarOpen = false
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "一覧", Mine: true},
		[]MylistItem{{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.RightClick("一覧"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find(locales.MylistMenu()); !ok {
		t.Fatalf("menu panel is not shown: %q", tester.Texts())
	}
	// The brightest pixel of the menu's first item reads as the panel's
	// opacity there: the item's text against the panel's dark background.
	item, ok := tester.Find(locales.ActionPin())
	if !ok {
		t.Fatalf("menu item is not shown: %q", tester.Texts())
	}
	bright := func(r ui.Rect) uint32 {
		img := tester.Image()
		var m uint32
		for x := int(r.X); x < int(r.X+r.W); x += 2 {
			for y := int(r.Y); y < int(r.Y+r.H); y += 2 {
				c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
				if v := uint32(c.R) + uint32(c.G) + uint32(c.B); v > m {
					m = v
				}
			}
		}
		return m
	}
	open := bright(item)
	tester.Advance(100 * time.Millisecond)
	rising := bright(item)
	tester.Advance(200 * time.Millisecond)
	done := bright(item)
	if open >= done || rising >= done {
		t.Fatalf("menu panel does not fade in: at open=%v rising=%v done=%v", open, rising, done)
	}
}
