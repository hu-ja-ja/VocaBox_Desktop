package main

import (
	"bytes"
	"image"
	"image/png"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

func TestNativeUITogglesSidebar(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)
	if !tester.HasText("VocaBox") {
		t.Fatalf("sidebar is not shown initially: %q", tester.Texts())
	}
	if _, ok := tester.Find("マイリスト"); !ok {
		t.Fatalf("sidebar section is not shown initially: %q", tester.Texts())
	}
	wordmark, hasWordmark := tester.Find("VocaBox")
	tagline, hasTagline := tester.Find("NICONICO MUSIC")
	if !hasWordmark || !hasTagline || wordmark.X != tagline.X {
		t.Fatalf("sidebar lockup is not left-aligned: wordmark=%v tagline=%v", wordmark, tagline)
	}
	subtitle, ok := tester.Find(locales.HomeSubtitleEmpty())
	if !ok {
		t.Fatalf("the home subtitle is not shown: %q", tester.Texts())
	}
	openX := subtitle.X
	if err := tester.Click("サイドバーを閉じる"); err != nil {
		t.Fatal(err)
	}
	// The panel moves with the sidebar rather than after it: halfway, its
	// content is between where it was and where it settles.
	tester.Advance(150 * time.Millisecond)
	moving, ok := tester.Find(locales.HomeSubtitleEmpty())
	if !ok || moving.X >= openX {
		t.Fatalf("the content did not follow the sidebar: open=%v moving=%v", openX, moving)
	}
	tester.Advance(200 * time.Millisecond)
	settled, ok := tester.Find(locales.HomeSubtitleEmpty())
	if !ok || settled.X >= moving.X {
		t.Fatalf("the content stopped before the sidebar: moving=%v settled=%v", moving, settled)
	}
	// The sidebar folds down to the rail: past it, only the rail content
	// is built and the toggle offers the way back.
	_, hasSection := tester.Find("マイリスト")
	if _, hasLogo := tester.Find("V"); app.sidebarOpen || !hasLogo || hasSection {
		t.Fatalf("sidebar did not close cleanly: open=%v texts=%q", app.sidebarOpen, tester.Texts())
	}
	// The closed sidebar keeps its way home rather than only reopening.
	if _, hasHome := tester.Find("ホーム"); !hasHome {
		t.Fatalf("the closed sidebar has no way home: %q", tester.Texts())
	}
	if _, ok := tester.Find("サイドバーを開く"); !ok {
		t.Fatalf("the toggle did not turn to open: %q", tester.Texts())
	}
	if err := tester.Click("サイドバーを開く"); err != nil {
		t.Fatal(err)
	}
	// The sidebar unfolds from the rail; past it, the toggle offers the
	// way back.
	tester.Advance(350 * time.Millisecond)
	if !app.sidebarOpen || !tester.HasText("VocaBox") {
		t.Fatalf("sidebar did not reopen: open=%v", app.sidebarOpen)
	}
	if _, ok := tester.Find("サイドバーを閉じる"); !ok {
		t.Fatalf("the toggle did not turn to close: %q", tester.Texts())
	}
}

// TestSidebarAccountLineBox checks where the line above the account runs:
// inside the open content's insets, and the full inner width at the rail.
func TestSidebarAccountLineBox(t *testing.T) {
	for _, tc := range []struct {
		name        string
		width, left float32
		length      float32
	}{
		{"open", sidebarWidth, sidebarPad, sidebarWidth - 2 - sidebarPad - sidebarGutter},
		{"rail", railWidth, 0, railWidth - 2},
		{"half", 157, 6, 139},
	} {
		left, length := accountLineBox(tc.width)
		if left != tc.left || length != tc.length {
			t.Errorf("%s: line at %v, length %v; want %v, %v", tc.name, left, length, tc.left, tc.length)
		}
	}
}

func TestNativeUIOpensSettingsFromLogo(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("設定"); err != nil {
		t.Fatal(err)
	}
	if app.router.Path() != settingsPath {
		t.Fatalf("the logo did not open the settings: path=%q", app.router.Path())
	}
	for _, row := range []string{"オフライン保存の音質", "保存する音源の音質です", audioQualityLabel(320), "更新を確認"} {
		if !tester.HasText(row) {
			t.Fatalf("the settings page is missing %q: %q", row, tester.Texts())
		}
	}
	// The settings stay reachable with the sidebar closed: the rail's mark
	// opens them too.
	if err := tester.Click("サイドバーを閉じる"); err != nil {
		t.Fatal(err)
	}
	app.router.Push(homePath)
	tester.Frame()
	if err := tester.Click("設定"); err != nil {
		t.Fatal(err)
	}
	if app.router.Path() != settingsPath {
		t.Fatalf("the rail's mark did not open the settings: path=%q", app.router.Path())
	}
}

// TestNativeUISignInRow checks the sidebar's account row and the way out.
func TestNativeUISignInRow(t *testing.T) {
	mylist := NewMylist()
	mylist.configDir = t.TempDir()
	app := newNativeApp(mylist)
	tester := ui.NewTester(app.view, 1120, 760)
	if !tester.HasText("ニコニコにログイン") {
		t.Fatalf("the sign-in button is missing: %q", tester.Texts())
	}

	app.loggedIn = true
	tester.Frame()
	if !tester.HasText("ログイン中") || tester.HasText("ニコニコにログイン") {
		t.Fatalf("a signed-in app shows %q", tester.Texts())
	}

	// The fetched account replaces the signed-in label with its name, and
	// its picture lands in the avatar beside it.
	var icon bytes.Buffer
	red := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for i := 0; i < len(red.Pix); i += 4 {
		red.Pix[i], red.Pix[i+1], red.Pix[i+2], red.Pix[i+3] = 255, 0, 0, 255
	}
	if err := png.Encode(&icon, red); err != nil {
		t.Fatal(err)
	}
	bitmap, err := ui.DecodeBitmap(icon.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	app.accountName, app.accountIcon = "テストユーザー", bitmap
	tester.Frame()
	if !tester.HasText("テストユーザーでログイン中") {
		t.Fatalf("a fetched account shows %q", tester.Texts())
	}
	avatar, ok := tester.Find("テストユーザー")
	if !ok || avatar.W != avatar.H || avatar.W < 20 {
		t.Fatalf("the fetched picture did not land in the avatar: %v", avatar)
	}

	// The account icon keeps its place as the sidebar folds to the rail:
	// the two states carry the avatar at the same corner.
	if err := tester.Click("サイドバーを閉じる"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(400 * time.Millisecond)
	closedAvatar, ok := tester.Find("テストユーザー")
	if !ok || closedAvatar.X != avatar.X || closedAvatar.Y != avatar.Y {
		t.Fatalf("the account icon moved with the rail: open=%v closed=%v", avatar, closedAvatar)
	}
	if err := tester.Click("サイドバーを開く"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(400 * time.Millisecond)

	// The way out asks first: cancelling keeps the session, confirming
	// forgets it.
	if err := tester.Click("ログアウト"); err != nil {
		t.Fatal(err)
	}
	if !app.confirmLogout || !tester.HasText("ログアウトしますか？") {
		t.Fatalf("the way out did not ask first: %q", tester.Texts())
	}
	if err := tester.Click("キャンセル"); err != nil {
		t.Fatal(err)
	}
	if app.confirmLogout || !app.loggedIn {
		t.Fatal("cancelling the dialog signed the app out")
	}
	if err := tester.Click("ログアウト"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("ログアウトする"); err != nil {
		t.Fatal(err)
	}
	if app.loggedIn {
		t.Fatal("logout kept the app signed in")
	}
	if got, err := app.session.Load(); err != nil || got != "" {
		t.Fatalf("logout kept a stored session: %q, %v", got, err)
	}
}

// TestNativeUISidebarMylistsLikeHome checks the sidebar's mylists use the
// home's order: the pinned mylists first with the account's live name, the
// account's other mylists after them, each pinnable from its menu.
func TestNativeUISidebarMylistsLikeHome(t *testing.T) {
	app := savedApp(t)
	app.loggedIn = true
	syncTestOwn(t, app,
		OwnMylist{ID: "71043760", Name: "自分の非公開マイリスト", ItemCount: 2},
		OwnMylist{ID: "71043761", Name: "自分の公開マイリスト", ItemCount: 12, IsPublic: true},
	)
	if saved, err := app.mylist.SetPinned("71043760", true); err != nil {
		t.Fatal(err)
	} else {
		app.saved = saved
	}
	tester := ui.NewTester(app.view, 1120, 760)

	pinned, ok := tester.Find("自分の非公開マイリスト")
	if !ok || pinned.X > 250 {
		t.Fatalf("the pinned mylist is missing from the sidebar: %v, %v", pinned, ok)
	}
	own, ok := tester.Find("自分の公開マイリスト")
	if !ok || own.X > 250 {
		t.Fatalf("the account's other mylist is missing from the sidebar: %v, %v", own, ok)
	}
	if tester.HasText("古い名前") {
		t.Fatalf("the sidebar shows the snapshot's stale name: %q", tester.Texts())
	}
	if pinned.Y >= own.Y {
		t.Fatalf("the pinned mylist does not come first: pinned=%v own=%v", pinned, own)
	}

	// The pinned item carries the pin mark on its right, the unpinned one
	// carries none: the sidebar mark and the pinned card's badge are the
	// only two marks shown.
	pin, ok := tester.Find("ピン留め")
	if !ok || pin.X < pinned.X+pinned.W/2 || pin.Y < pinned.Y || pin.Y >= pinned.Y+pinned.H {
		t.Fatalf("the pin mark is not on the pinned item's right: pin=%v item=%v", pin, pinned)
	}
	marks := 0
	for _, text := range tester.Texts() {
		if text == "ピン留め" {
			marks++
		}
	}
	if marks != 2 {
		t.Fatalf("pin marks = %d; want the sidebar's and the pinned card's", marks)
	}

	// An unpinned mylist can be pinned from its sidebar menu.
	tester.RightClickAt(own.X+own.W/2, own.Y+own.H/2)
	if !app.menuOpen {
		t.Fatalf("the sidebar menu did not open: %q", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	if app.menuOpen {
		t.Fatal("Escape did not close the sidebar menu")
	}
}

// TestNativeUISidebarMylistSavingMark checks the sidebar's mylists carry the
// home's saving mark without its count: a mylist saved for offline playing
// shows the round mark on the row, the count stays on the home's chip, and
// turning the saving off takes the mark away.
func TestNativeUISidebarMylistSavingMark(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "オフラインのリスト"}, []MylistItem{
		{WatchID: "sm1", Title: "保存の曲", Available: true, Duration: 120},
	})
	if saved, err := app.mylist.SetDownload("111", true); err != nil {
		t.Fatal(err)
	} else {
		app.saved = saved
	}
	app.audioStats["111"] = audioStat{saved: 1, total: 2}
	tester := ui.NewTester(app.view, 1120, 760)

	item, ok := tester.Find("オフラインのリスト")
	if !ok || item.X > 250 {
		t.Fatalf("the downloading mylist is missing from the sidebar: %v, %v", item, ok)
	}
	// The mark sits on the item's row, to the right of its name.
	mark, ok := tester.Find("オフライン保存予定")
	if !ok || mark.X < item.X || mark.X > 250 || mark.Y >= item.Y+item.H || item.Y >= mark.Y+mark.H {
		t.Fatalf("the sidebar item carries no saving mark: mark=%v item=%v", mark, item)
	}
	counts := 0
	for _, text := range tester.Texts() {
		if text == "1/2" {
			counts++
		}
	}
	if counts != 1 {
		t.Fatalf("the saving count shows %d times; want the home's chip only: %q", counts, tester.Texts())
	}

	// Turning the saving off takes the mark from the sidebar and the home.
	if saved, err := app.mylist.SetDownload("111", false); err != nil {
		t.Fatal(err)
	} else {
		app.saved = saved
	}
	tester.Frame()
	if _, ok := tester.Find("オフライン保存予定"); ok {
		t.Fatalf("the saving mark stayed on the turned-off mylist: %q", tester.Texts())
	}
}

// TestNativeUISidebarUnpinsFromMenu checks the sidebar's right-click menu:
// choosing ピン留めを外す unpins the mylist, keeping its saved copy in the
// sidebar and the home.
func TestNativeUISidebarUnpinsFromMenu(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "555", Name: "サイドバーのピン", TotalItemCount: 3, Pinned: true},
		[]MylistItem{{WatchID: "sm5", Title: "公開の曲", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)

	item, ok := tester.Find("サイドバーのピン")
	if !ok || item.X > 250 {
		t.Fatalf("the sidebar item was not found: %v, %v", item, ok)
	}
	tester.RightClickAt(item.X+item.W/2, item.Y+item.H/2)
	if _, ok := tester.Find("ピン留めを外す"); !ok {
		t.Fatalf("sidebar menu did not open: %q", tester.Texts())
	}
	if err := tester.Click("ピン留めを外す"); err != nil {
		t.Fatal(err)
	}
	if len(app.saved) != 1 || app.saved[0].Pinned {
		t.Fatalf("the mylist must stay saved unpinned: %+v", app.saved)
	}
	if !tester.HasText("サイドバーのピン") {
		t.Fatalf("the unpinned mylist dropped out of the sidebar: %q", tester.Texts())
	}
	if _, ok := tester.Find("ピン留め"); ok {
		t.Fatalf("the sidebar pin mark stayed: %q", tester.Texts())
	}
}

// TestNativeUIRailNavigates checks the closed sidebar still navigates: the
// rail carries the way home and to the pinned mylists, and choosing one
// opens it and leaves the queue screen for it.
func TestNativeUIRailNavigates(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "レールのピン", Pinned: true},
		[]MylistItem{{WatchID: "sm1", Title: "Aの曲", Available: true, Duration: 120}})
	app.sidebarOpen = false
	tester := ui.NewTester(app.view, 1120, 760)

	item, ok := tester.Find("レールのピン（ピン留め）")
	if !ok || item.X < 12 || item.X+item.W > 76 {
		t.Fatalf("the rail does not show the pinned mylist: %v, %v", item, ok)
	}
	if err := tester.Click("レールのピン（ピン留め）"); err != nil {
		t.Fatal(err)
	}
	if app.router.Path() != "/mylist/111" || app.page == nil || app.page.ID != "111" || !tester.HasText("Aの曲") {
		t.Fatalf("the rail did not open the mylist: path=%q texts=%q", app.router.Path(), tester.Texts())
	}

	// The mylist already showing leaves the queue screen for itself, as its
	// sidebar item does, and the home returns home.
	app.queueOpen = true
	tester.Frame()
	if err := tester.Click("レールのピン（ピン留め）"); err != nil {
		t.Fatal(err)
	}
	if app.queueOpen || app.router.Path() != "/mylist/111" {
		t.Fatalf("choosing the shown mylist left the queue screen open: queue=%v path=%q", app.queueOpen, app.router.Path())
	}
	if err := tester.Click("ホーム"); err != nil {
		t.Fatal(err)
	}
	if app.router.Path() != homePath || !tester.HasText("聴きたいマイリストを選んで再生") {
		t.Fatalf("the rail home did not return home: path=%q", app.router.Path())
	}

	// The rail tile carries the same right-click menu as the sidebar's item:
	// unpinning from it drops the tile while the saved copy stays.
	item, _ = tester.Find("レールのピン（ピン留め）")
	tester.RightClickAt(item.X+item.W/2, item.Y+item.H/2)
	if _, ok := tester.Find("ピン留めを外す"); !ok {
		t.Fatalf("the rail menu did not open: %q", tester.Texts())
	}
	if err := tester.Click("ピン留めを外す"); err != nil {
		t.Fatal(err)
	}
	if len(app.saved) != 1 || app.saved[0].Pinned {
		t.Fatalf("the mylist must stay saved unpinned: %+v", app.saved)
	}
	if _, ok := tester.Find("レールのピン（ピン留め）"); ok {
		t.Fatalf("the unpinned mylist still shows on the rail: %q", tester.Texts())
	}
}

// TestNativeUIRailAccount checks the rail's account foot: the avatar opens
// the account menu, whose way out asks before the session is forgotten, and
// signing out leaves the rail's way in.
func TestNativeUIRailAccount(t *testing.T) {
	mylist := NewMylist()
	mylist.configDir = t.TempDir()
	app := newNativeApp(mylist)
	app.loggedIn = true
	app.accountName = "テスト"
	app.sidebarOpen = false
	tester := ui.NewTester(app.view, 1120, 760)

	if err := tester.Click("アカウント"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if _, ok := tester.Find("アカウントのメニュー"); !ok {
		t.Fatalf("the account menu did not open: %q", tester.Texts())
	}
	if err := tester.Click("ログアウト"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if app.railAccountOpen || !app.confirmLogout {
		t.Fatalf("the way out did not ask before signing out: menu=%v confirm=%v", app.railAccountOpen, app.confirmLogout)
	}
	if _, ok := tester.Find("ログアウトしますか？"); !ok {
		t.Fatalf("the confirm dialog is missing: %q", tester.Texts())
	}

	// Signed out, the rail shows the way in.
	app.confirmLogout, app.loggedIn, app.accountName = false, false, ""
	tester.Frame()
	if _, ok := tester.Find("ニコニコにログイン"); !ok {
		t.Fatalf("the rail has no way in while signed out: %q", tester.Texts())
	}
}

// TestNativeUISidebarMylistLeavesQueueScreen checks choosing a mylist in the
// sidebar leaves the queue screen for it, as choosing home does, even the
// mylist already showing, whose click changes no route of its own.
func TestNativeUISidebarMylistLeavesQueueScreen(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "マイリストA"},
		[]MylistItem{{WatchID: "sm1", Title: "Aの曲", Available: true, Duration: 120}})
	indexSaved(t, app, SavedIndexEntry{ID: "222", Name: "マイリストB"},
		[]MylistItem{{WatchID: "sm2", Title: "Bの曲", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("マイリストA"); err != nil {
		t.Fatal(err)
	}
	// The queue folds the sidebar away; open it again to choose a mylist.
	openSidebar := func() {
		if err := tester.Click("サイドバーを開く"); err != nil {
			t.Fatal(err)
		}
		tester.Advance(300 * time.Millisecond)
	}
	app.queueOpen = true
	tester.Frame()
	openSidebar()
	if err := tester.Click("マイリストB"); err != nil {
		t.Fatal(err)
	}
	if app.queueOpen {
		t.Fatal("choosing another mylist left the queue screen open")
	}
	if app.router.Path() != "/mylist/222" || app.page == nil || app.page.ID != "222" || !tester.HasText("Bの曲") {
		t.Fatalf("the chosen mylist was not opened: path=%q page=%#v texts=%q", app.router.Path(), app.page, tester.Texts())
	}

	app.queueOpen = true
	tester.Frame()
	openSidebar()
	if err := tester.Click("マイリストB"); err != nil {
		t.Fatal(err)
	}
	if app.queueOpen {
		t.Fatal("choosing the shown mylist again left the queue screen open")
	}
}

// TestNativeUIQueueFoldsAndRestoresTheSidebar checks the queue takes the
// sidebar away while it shows, and closing it brings back the layout the
// queue was opened in.
func TestNativeUIQueueFoldsAndRestoresTheSidebar(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "キューの元"},
		[]MylistItem{{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)
	if !app.sidebarOpen {
		t.Fatal("the sidebar does not start open")
	}
	app.queueOpen = true
	tester.Frame()
	tester.Advance(300 * time.Millisecond)
	if app.sidebarOpen {
		t.Fatal("the queue left the sidebar open")
	}
	app.queueOpen = false
	tester.Frame()
	tester.Advance(300 * time.Millisecond)
	if !app.sidebarOpen {
		t.Fatal("closing the queue did not bring back the sidebar")
	}

	// A queue opened with the sidebar already folded leaves it folded.
	if err := tester.Click("サイドバーを閉じる"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(300 * time.Millisecond)
	app.queueOpen = true
	tester.Frame()
	tester.Advance(300 * time.Millisecond)
	if app.sidebarOpen {
		t.Fatal("the queue opened the folded sidebar")
	}
	app.queueOpen = false
	tester.Frame()
	tester.Advance(300 * time.Millisecond)
	if app.sidebarOpen {
		t.Fatal("closing the queue opened a sidebar it did not take")
	}
}
