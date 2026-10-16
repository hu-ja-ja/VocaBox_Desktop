package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	vocabox "github.com/hu-ja-ja/VocaBox_Desktop"
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
	closeButton, ok := tester.Find("サイドバーを閉じる")
	if !ok {
		t.Fatal("close button is missing")
	}
	if err := tester.Click("サイドバーを閉じる"); err != nil {
		t.Fatal(err)
	}
	_, hasSection := tester.Find("マイリスト")
	if _, hasLogo := tester.Find("V"); app.sidebarOpen || !hasLogo || hasSection {
		t.Fatalf("sidebar did not close cleanly: open=%v texts=%q", app.sidebarOpen, tester.Texts())
	}
	// The closed sidebar keeps its way home rather than only reopening.
	if _, hasHome := tester.Find("ホーム"); !hasHome {
		t.Fatalf("the closed sidebar has no way home: %q", tester.Texts())
	}
	openButton, ok := tester.Find("サイドバーを開く")
	if !ok || openButton.X != closeButton.X || openButton.Y != closeButton.Y {
		t.Fatalf("toggle moved while closing: was=%v now=%v", closeButton, openButton)
	}
	if err := tester.Click("サイドバーを開く"); err != nil {
		t.Fatal(err)
	}
	if !app.sidebarOpen || !tester.HasText("VocaBox") {
		t.Fatalf("sidebar did not reopen: open=%v", app.sidebarOpen)
	}
	if reopenedButton, ok := tester.Find("サイドバーを閉じる"); !ok || reopenedButton.X != closeButton.X || reopenedButton.Y != closeButton.Y {
		t.Fatalf("toggle moved while reopening: was=%v now=%v", closeButton, reopenedButton)
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

// TestNativeUIShowsLicenses checks the settings say what the app is, which
// licence it is under, and which works it bundles, whose terms open in the
// list.
func TestNativeUIShowsLicenses(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("設定"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("Copyright (C) 2026 hu-ja-ja") {
		t.Fatalf("the settings do not say the copyright: %q", tester.Texts())
	}
	if !tester.HasText("サードパーティライセンス") {
		t.Fatalf("the settings do not head the third-party licences: %q", tester.Texts())
	}
	// The icons the app takes from Lucide are one of the bundled works.
	if !tester.HasText("Lucide") {
		t.Fatalf("the icons are not listed: %q", tester.Texts())
	}
	// The app's own licence opens its text.
	if err := tester.Click("MPL-2.0 License (Mozilla Public License Version 2.0)を表示"); err != nil {
		t.Fatal(err)
	}
	if !app.licenseOpenDialog || !strings.Contains(app.licenseBody, "Mozilla Public License Version 2.0") {
		t.Fatalf("the app's licence did not open: open=%v", app.licenseOpenDialog)
	}
	if err := tester.Click("閉じる"); err != nil {
		t.Fatal(err)
	}
	if app.licenseOpenDialog {
		t.Fatal("the app's licence did not close")
	}

	// The about card opens the repository in the browser.
	if err := tester.Click("リポジトリ"); err != nil {
		t.Fatal(err)
	}
	if opened := tester.OpenedURLs(); len(opened) != 1 || opened[0] != appRepo {
		t.Fatalf("the repository did not open: %#v", opened)
	}

	// A bundled work shows as one row, and opens onto the text of its terms.
	var entry vocabox.Entry
	for _, e := range vocabox.Entries {
		if e.Name == "github.com/egoist/mygo" {
			entry = e
		}
	}
	if entry.Name == "" {
		t.Fatalf("mygo is not listed: %d entries", len(vocabox.Entries))
	}
	// The list names a Go module by the package it ends in; opening the row
	// spells the module out.
	if got := licenseTitle(entry); got != "mygo v0.5.5  ·  MIT" {
		t.Fatalf("the list names the module %q; want its package name", got)
	}
	tester.Scroll(560, 500, 0, 160)
	tester.Frame()
	if err := tester.Click(licenseTitle(entry)); err != nil {
		t.Fatal(err)
	}
	if !app.licenseOpen[entry.Name+" "+entry.Version] {
		t.Fatalf("the row of %s did not open: %#v", entry.Name, app.licenseOpen)
	}
	if !tester.HasText("github.com/egoist/mygo") {
		t.Fatalf("the open row does not spell the module out: %q", tester.Texts())
	}
	if !tester.HasText("Permission is hereby granted, free of charge") {
		t.Fatalf("the row does not show the terms: %q", tester.Texts())
	}
}

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

// TestNativeUIShowsQuality checks the band the player bar reports.
func TestNativeUIShowsQuality(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.player = playerState{
		WatchID: "sm9", Title: "一曲目", Uploader: "投稿者",
		Ready: true, Playing: true, Quality: "320kbps", DurationMS: 100_000, Volume: 1,
	}
	tester := ui.NewTester(app.view, 1120, 760)
	if !tester.HasText("320kbps") {
		t.Fatalf("the player bar does not show the quality: %q", tester.Texts())
	}
}

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

// TestNativeUIQueueScreenShowsTheQueue checks the screen the bar opens: the
// queue in playing order, labelled with the mylist it came from, with a row
// click selecting the track and home leaving the screen.
func TestNativeUIQueueScreenShowsTheQueue(t *testing.T) {
	app := newNativeApp(NewMylist())
	items := []MylistItem{
		{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120},
		{WatchID: "sm2", Title: "二曲目", Available: true, Duration: 120},
		{WatchID: "sm3", Title: "三曲目", Available: true, Duration: 120},
	}
	app.saved = []SavedIndexEntry{{ID: "123", Name: "作業用キュー", TotalItemCount: 3,
		Sample: items}}
	app.player = playerState{
		WatchID: "sm2", MylistID: "123", Name: "作業用キュー", Title: "二曲目",
		Ready: true, Playing: true, Volume: 1,
		Order: []string{"sm2", "sm3", "sm1"}, Items: items, QueueIndex: 0,
	}

	tester := ui.NewTester(app.view, 1120, 760)
	// The bar's queue button opens the queue screen.
	if err := tester.Click("再生キュー"); err != nil {
		t.Fatal(err)
	}
	if !app.queueOpen {
		t.Fatal("the queue screen did not open")
	}
	if !tester.HasText("作業用キュー") || !tester.HasText("3曲") {
		t.Fatalf("the queue is not labelled with the mylist it came from: %q", tester.Texts())
	}
	// A row is chosen by the list itself, which is what a drag needs too.
	two, ok := tester.Find("二曲目")
	if !ok {
		t.Fatalf("the queue is missing tracks: %q", tester.Texts())
	}
	tester.ClickAt(two.X+10, two.Y+two.H/2)
	if !hasCommand(app.sent, "select") {
		// The label of a virtual list settles a frame late; aim again.
		tester.Frame()
		two, _ = tester.Find("二曲目")
		tester.ClickAt(two.X+10, two.Y+two.H/2)
	}
	selected, ok := lastCommand(app.sent, "select")
	if !ok || selected.WatchID != "sm2" {
		t.Fatalf("a queue row did not play its track: %#v", app.sent)
	}

	// The queue keeps the track playing on top as it moves on.
	first, _ := app.queueList.Visible()
	if app.player.QueueIndex != 0 || first != 0 {
		t.Fatalf("the queue does not start at the track playing: first=%d", first)
	}

	if err := tester.Click("ホーム"); err != nil {
		t.Fatal(err)
	}
	if app.queueOpen {
		t.Fatal("home left the queue screen open")
	}
}

// TestNativeUIQueueDragReorders checks the queue rows move by dragging
// them, the way the mylist pages' rows do not.
func TestNativeUIQueueDragReorders(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.queueOpen = true
	app.player = playerState{
		Name:  "作業用ドラッグ",
		Order: []string{"sm1", "sm2", "sm3"},
		Items: []MylistItem{
			{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120},
			{WatchID: "sm2", Title: "二曲目", Available: true, Duration: 120},
			{WatchID: "sm3", Title: "三曲目", Available: true, Duration: 120},
		},
	}
	tester := ui.NewTester(app.view, 1120, 760)
	first, ok := tester.Find("一曲目")
	if !ok {
		t.Fatalf("the queue is not shown: %q", tester.Texts())
	}
	last, ok := tester.Find("三曲目")
	if !ok {
		t.Fatalf("the queue is not shown: %q", tester.Texts())
	}
	tester.Press(first.X+20, first.Y+first.H/2)
	for k := 1; k <= 4; k++ {
		f := float32(k) / 4
		tester.Move(first.X+20, first.Y+first.H/2+(last.Y+last.H-first.Y)*f)
	}
	tester.Release(first.X+20, last.Y+last.H)
	moved, ok := lastCommand(app.sent, "move")
	if !ok || len(moved.Rows) != 1 || moved.Rows[0] != 0 {
		t.Fatalf("dragging a row did not move it: %#v", app.sent)
	}
}

// TestNativeUIQueueCanScrollAfterFollowingThePlayingTrack makes sure the
// automatic scroll to the playing track is a one-time follow, not a lock on
// the queue's position.
func TestNativeUIQueueCanScrollAfterFollowingThePlayingTrack(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.queueOpen = true
	items := make([]MylistItem, 300)
	order := make([]string, 300)
	for i := range items {
		id := fmt.Sprintf("sm%d", i+1)
		items[i] = MylistItem{WatchID: id, Title: id, Available: true, Duration: 120}
		order[i] = id
	}
	app.player = playerState{
		Name: "作業用スクロール", WatchID: "sm200", Order: order, Items: items,
		QueueIndex: 199,
	}
	tester := ui.NewTester(app.view, 1120, 760)
	first, last := app.queueList.Visible()
	if first != 199 || last <= first {
		t.Fatalf("queue did not start at the playing track: visible rows %d..%d", first, last)
	}
	tester.Scroll(700, 500, 0, 120)
	after, _ := app.queueList.Visible()
	if after == first {
		t.Fatalf("queue did not scroll from row %d", first)
	}
}

func TestNativeUIQueueMenuTogglesOnSecondClick(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.queueOpen = true
	app.player = playerState{
		Name:  "作業用メニュー",
		Order: []string{"sm1", "sm2"},
		Items: []MylistItem{
			{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120},
			{WatchID: "sm2", Title: "二曲目", Available: true, Duration: 120},
		},
	}
	tester := ui.NewTester(app.view, 1120, 760)
	row, ok := tester.Find("一曲目")
	if !ok {
		t.Fatalf("the queue row is missing: %q", tester.Texts())
	}
	tester.Move(row.X+row.W-30, row.Y+row.H/2)
	if err := tester.Click("曲のメニュー"); err != nil {
		t.Fatal(err)
	}
	if !app.queueMenuOpen || !tester.HasText("キューから削除") {
		t.Fatal("the queue row menu did not open")
	}
	if err := tester.Click("曲のメニュー"); err != nil {
		t.Fatal(err)
	}
	if app.queueMenuOpen || tester.HasText("キューから削除") {
		t.Fatal("the second click did not close the queue row menu")
	}
}

func TestNativeUIPlayerBarBlankAreaTogglesQueue(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)
	volume, ok := tester.Find("音量")
	if !ok {
		t.Fatal("volume control is missing")
	}
	queue, ok := tester.Find("再生キュー")
	if !ok {
		t.Fatal("queue button is missing")
	}
	x := volume.X + volume.W + (queue.X-volume.X-volume.W)/2
	y := queue.Y + queue.H/2
	tester.ClickAt(x, y)
	if !app.queueOpen {
		t.Fatal("clicking empty player-bar space did not open the queue")
	}
	tester.ClickAt(x, y)
	if app.queueOpen {
		t.Fatal("clicking empty player-bar space did not close the queue")
	}
}

// TestNativeUIQueueCommands checks the queue rows drive the player:
// removing a track and reordering rows.
func TestNativeUIQueueCommands(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.queueOpen = true
	app.player = playerState{
		Name:  "作業用コマンド",
		Order: []string{"sm1", "sm2"},
		Items: []MylistItem{
			{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120},
			{WatchID: "sm2", Title: "二曲目", Available: true, Duration: 120},
		},
	}

	app.removeFromQueue(MylistItem{WatchID: "sm2"})
	removed, ok := lastCommand(app.sent, "remove")
	if !ok || removed.WatchID != "sm2" {
		t.Fatalf("removing did not reach the player: %#v", app.sent)
	}

	app.moveQueueRows([]int{0}, 2)
	moved, ok := lastCommand(app.sent, "move")
	if !ok || len(moved.Rows) != 1 || moved.Rows[0] != 0 || moved.To != 2 {
		t.Fatalf("reordering did not reach the player: %#v", app.sent)
	}
}

// TestQueueItemsComeFromTheReport checks the queue screen follows the
// player's own state alone: the mylist the app happens to show neither
// reorders the queue nor fills a track the player does not carry.
func TestQueueItemsComeFromTheReport(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.page = &MylistPage{ID: "9", Items: []MylistItem{
		{WatchID: "sm2", Title: "表示中マイリストの曲", Available: true, Duration: 120},
		{WatchID: "sm1", Title: "表示中マイリストの曲", Available: true, Duration: 120},
	}}
	app.player = playerState{
		Order: []string{"sm2", "sm9", "sm1"},
		Items: []MylistItem{{WatchID: "sm9", Title: "再生中マイリストの曲", Available: true, Duration: 120}},
	}
	got := app.queueItems()
	ids := make([]string, 0, len(got))
	for _, item := range got {
		ids = append(ids, item.WatchID)
	}
	if len(ids) != 3 || ids[0] != "sm2" || ids[1] != "sm9" || ids[2] != "sm1" {
		t.Fatalf("queue order = %#v; want sm2, sm9, sm1", ids)
	}
	if got[1].Title != "再生中マイリストの曲" {
		t.Fatalf("the report's track lost its metadata: %#v", got[1])
	}
	if got[0].Title != "" || got[2].Title != "" {
		t.Fatalf("a track the report does not carry was filled in from the shown mylist: %#v", got)
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
	app.queueOpen = true
	tester.Frame()
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
	if err := tester.Click("マイリストB"); err != nil {
		t.Fatal(err)
	}
	if app.queueOpen {
		t.Fatal("choosing the shown mylist again left the queue screen open")
	}
}

// TestNativeUIQueueFollowsTheTrackPlaying checks the queue returns to the
// track playing when it changes at the same row, as playing under shuffle
// pins it to the top, and when a shuffle lays the queue out anew.
func TestNativeUIQueueFollowsTheTrackPlaying(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.queueOpen = true
	items := make([]MylistItem, 300)
	order := make([]string, 300)
	for i := range items {
		id := fmt.Sprintf("sm%d", i+1)
		items[i] = MylistItem{WatchID: id, Title: id, Available: true, Duration: 120}
		order[i] = id
	}
	app.player = playerState{
		Name: "作業用追従", WatchID: "sm1", Order: order, Items: items,
		QueueIndex: 0,
	}
	tester := ui.NewTester(app.view, 1120, 760)
	tester.Scroll(700, 500, 0, 240)
	if first, _ := app.queueList.Visible(); first == 0 {
		t.Fatal("the queue did not scroll away from the track playing")
	}

	// The track playing changes at the same row.
	app.player.WatchID = "sm9"
	tester.Frame()
	if first, _ := app.queueList.Visible(); first != 0 {
		t.Fatalf("the queue did not follow the track playing: first=%d", first)
	}

	// A shuffle lays the queue out anew.
	tester.Scroll(700, 500, 0, 240)
	if first, _ := app.queueList.Visible(); first == 0 {
		t.Fatal("the queue did not scroll away")
	}
	if err := tester.Click("シャッフル"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if first, _ := app.queueList.Visible(); first != 0 {
		t.Fatalf("the queue did not return to the track playing after a shuffle: first=%d", first)
	}
}

func TestNativeUITrackClickPlaysIt(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "123", Name: "作業用クリック"}, []MylistItem{{
		WatchID:   "sm9",
		Title:     "サンプル曲",
		Duration:  120,
		Available: true,
	}})

	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("作業用クリック"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("サンプル曲"); err != nil {
		t.Fatal(err)
	}
	selected, ok := lastCommand(app.sent, "select")
	if !ok || selected.WatchID != "sm9" {
		t.Fatalf("clicking a track did not play it: %#v", app.sent)
	}
	if !app.queueOpen {
		t.Fatal("playing from the mylist did not open the queue screen")
	}
}

// TestNativeUIPlayerBarCentersTransport checks the transport buttons sit in
// the middle of the bar rather than following what shows beside them.
func TestNativeUIPlayerBarCentersTransport(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)
	shuffle, ok := tester.Find("シャッフル")
	if !ok {
		t.Fatalf("shuffle is missing: %q", tester.Texts())
	}
	loop, ok := tester.Find("ループ")
	if !ok {
		t.Fatalf("loop is missing: %q", tester.Texts())
	}
	play, ok := tester.Find("再生")
	if !ok {
		t.Fatalf("play is missing: %q", tester.Texts())
	}
	// The window is 1120 wide, so the middle of the bar is at 560; the
	// queue button on one side is allowed a little asymmetry.
	tolerance := float32(40)
	middle := (shuffle.X + loop.X + loop.W) / 2
	if delta := middle - 560; delta < -tolerance || delta > tolerance {
		t.Fatalf("transport is centered at %v; want 560", middle)
	}
	centered := play.X + play.W/2

	// A long track playing leaves the transport where it is.
	app.player = playerState{
		WatchID: "sm9", Title: "とても長い曲名がここに来ても再生ボタンは動かないでほしい",
		Uploader: "投稿者", Ready: true, Volume: 1,
	}
	tester.Frame()
	again, ok := tester.Find("再生")
	if !ok {
		t.Fatalf("play is missing after the track changed: %q", tester.Texts())
	}
	if delta := again.X + again.W/2 - centered; delta < -0.5 || delta > 0.5 {
		t.Fatalf("the transport followed the track shown: was %v, now %v", centered, again.X+again.W/2)
	}
}

func TestNativeUIPlayerBarFitsMinimumWidth(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.player = playerState{
		WatchID: "sm9", Title: "ウィンドウ幅が狭いときも表示が崩れない長い曲名",
		Uploader: "投稿者", Ready: true, Volume: 1,
	}
	tester := ui.NewTester(app.view, 760, 560)
	for _, label := range []string{"前へ", "再生", "次へ", "音量", "再生キュー"} {
		bounds, ok := tester.Find(label)
		if !ok || bounds.X < 0 || bounds.X+bounds.W > 760 {
			t.Fatalf("%q is missing or outside the minimum-width window: bounds=%v", label, bounds)
		}
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

func TestThumbWidthKeepsAspect(t *testing.T) {
	cases := []struct {
		h, w, hh int
		want     float32
	}{
		{45, 320, 180, 80},   // the usual 16:9 picture
		{45, 0, 0, 80},       // the 16:9 placeholder
		{45, 180, 180, 45},   // a squarish picture
		{45, 130, 100, 58.5}, // an older 4:3 picture
		{36, 320, 180, 64},   // the player bar's 16:9
	}
	for _, tc := range cases {
		if got := thumbWidth(float32(tc.h), tc.w, tc.hh); got != tc.want {
			t.Errorf("thumbWidth(%d, %d, %d) = %v; want %v", tc.h, tc.w, tc.hh, got, tc.want)
		}
	}
}

func TestThumbSlotHoldsTitlesInLine(t *testing.T) {
	cases := []struct {
		h    float32
		w    float32
		want float32
	}{
		{45, 80, 88},   // 16:9 keeps the slot's slack
		{45, 58.5, 88}, // a 4:3 picture still leaves the title in line
		{45, 45, 88},   // and so does a square one
		{45, 150, 150}, // a wider picture takes its own room
		{36, 64, 72},   // the player bar's 16:9 with its slack
	}
	for _, tc := range cases {
		if got := thumbSlotWidth(tc.h, tc.w); got != tc.want {
			t.Errorf("thumbSlotWidth(%v, %v) = %v; want %v", tc.h, tc.w, got, tc.want)
		}
	}
}

func TestNativeUIThumbnailPlaceholderNeedsWindow(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "123", Name: "作業用サムネ"}, []MylistItem{{
		WatchID:   "sm27718606",
		Title:     "サンプル曲",
		Available: true,
		ThumbURL:  "https://nicovideo.cdn.nimg.jp/thumbnails/27718606/27718606",
	}})
	indexSaved(t, app, SavedIndexEntry{ID: "456", Name: "別のサムネ"}, []MylistItem{{
		WatchID:   "sm9",
		Title:     "別の曲",
		Duration:  120,
		Available: true,
	}})

	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("作業用サムネ"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("サンプル曲") {
		t.Fatalf("track is not shown: %q", tester.Texts())
	}
	// Without a window there is nothing to load into: the row keeps its
	// placeholder, the mylist's pictures wait in the fill queue, and no load
	// starts.
	if len(app.thumbBusy) != 0 || len(app.thumbs) != 0 {
		t.Fatalf("unexpected thumbnail load: busy=%v thumbs=%d", app.thumbBusy, len(app.thumbs))
	}
	if len(app.fillQueue) != 1 || app.fillQueue[0].WatchID != "sm27718606" {
		t.Fatalf("fill queue = %+v; want the mylist's entry queued", app.fillQueue)
	}
	if err := tester.Click("別のサムネ"); err != nil {
		t.Fatal(err)
	}
	if len(app.fillQueue) != 1 || app.fillQueue[0].WatchID != "sm9" {
		t.Fatalf("fill queue = %+v; want the new mylist's entry replacing the old", app.fillQueue)
	}
}

func TestNeedsThumbnail(t *testing.T) {
	dir := t.TempDir()
	app := newNativeApp(&Mylist{configDir: dir})
	item := MylistItem{WatchID: "sm1", Available: true}

	if !app.needsThumbnail(item) {
		t.Error("an available entry without a cached picture needs one")
	}
	if err := os.MkdirAll(filepath.Join(dir, "thumbs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "thumbs", "sm1.jpg"), []byte("picture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if app.needsThumbnail(item) {
		t.Error("a picture already on disk must not be fetched again")
	}
	app.thumbFail["sm2"] = true
	if app.needsThumbnail(MylistItem{WatchID: "sm2", Available: true}) {
		t.Error("a failed picture must not retry until the mylist reloads")
	}
	if app.needsThumbnail(MylistItem{WatchID: "sm3"}) {
		t.Error("an unavailable entry has no picture to fetch")
	}
	if app.needsThumbnail(MylistItem{Available: true}) {
		t.Error("an entry without a watch ID cannot be fetched")
	}
}

// TestNativeUISeekBarScrubsWithoutFightingReports checks that dragging the
// seek bar holds the user's position while the track keeps reporting, sends
// one seek when the pointer lets go, and leaves playback alone meanwhile.
func TestNativeUISeekBarScrubsWithoutFightingReports(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.player = playerState{
		WatchID: "sm1", Title: "Aの曲", Ready: true, Playing: true,
		PositionMS: 30_000, DurationMS: 120_000, Volume: 1,
	}
	tester := ui.NewTester(app.view, 1120, 760)
	app.sent = nil

	bar, ok := tester.Find("再生位置")
	if !ok {
		t.Fatal("the seek bar is missing")
	}
	x, y := bar.X+bar.W/2, bar.Y+bar.H/2
	tester.Move(x, y)
	tester.Press(x, y)
	if !app.seekHeld || app.seekValue <= 0 || app.seekValue >= app.player.DurationMS {
		t.Fatalf("pressing the bar did not take the position: held=%v value=%v", app.seekHeld, app.seekValue)
	}
	dragged := app.seekValue

	// The track reports as it plays on: the thumb must not fall back, and
	// scrubbing must not stop or pause the track.
	app.player.PositionMS = 31_000
	tester.Frame()
	if app.seekValue != dragged {
		t.Fatalf("a report tugged the thumb from %v to %v", dragged, app.seekValue)
	}
	if len(app.sent) != 0 {
		t.Fatalf("scrubbing sent commands before the pointer let go: %#v", app.sent)
	}

	// Letting go, even away from the bar's thin strip, seeks once, to the
	// position the user chose.
	tester.Release(x, bar.Y-30)
	seek, ok := lastCommand(app.sent, "seek")
	if !ok || seek.PositionMS != dragged {
		t.Fatalf("releasing did not seek to the dragged position: %#v", app.sent)
	}
	if !app.seekSettling {
		t.Fatal("the bar did not wait for the player to report the seek")
	}

	// Until the player reports the seek landing, the thumb holds the
	// position rather than snapping back to a stale report.
	app.sent = nil
	tester.Frame()
	if app.seekValue != dragged {
		t.Fatalf("the thumb snapped back to %v before the seek landed", app.seekValue)
	}
	app.player.PositionMS = dragged
	tester.Frame()
	if app.seekSettling || app.seekValue != dragged {
		t.Fatalf("the bar did not follow the player after the seek: settling=%v value=%v", app.seekSettling, app.seekValue)
	}
}

// TestNativeUIVolumeBarStepsWhileDragging checks that dragging the volume
// bar tells the player in steps rather than on every pointer frame, sends
// the level the pointer chose when it lets go, and never lets the reports
// of the track playing tug the thumb from under the pointer.
func TestNativeUIVolumeBarStepsWhileDragging(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.player = playerState{
		WatchID: "sm1", Title: "Aの曲", Ready: true, Playing: true,
		PositionMS: 30_000, DurationMS: 120_000, Volume: 0.5,
	}
	tester := ui.NewTester(app.view, 1120, 760)
	app.sent = nil

	bar, ok := tester.Find("音量")
	if !ok {
		t.Fatal("the volume bar is missing")
	}
	x, y := bar.X+bar.W/2, bar.Y+bar.H/2
	tester.Move(x, y)
	tester.Press(x, y)
	if !app.volumeHeld {
		t.Fatal("pressing the bar did not take the hold")
	}
	if len(app.sent) != 0 {
		t.Fatalf("pressing sent a command: %#v", app.sent)
	}

	// A move inside the step keeps the player's level where it is.
	tester.Move(x+2, y)
	if len(app.sent) != 0 {
		t.Fatalf("a move inside the step sent a command: %#v", app.sent)
	}

	// A step-sized move tells the player once.
	tester.Move(x+12, y)
	stepped := app.volumeValue
	volume, ok := lastCommand(app.sent, "volume")
	if !ok || volume.Volume != stepped || len(app.sent) != 1 {
		t.Fatalf("a step-sized move did not send the level once: %#v value=%v", app.sent, stepped)
	}

	// A fast drag outruns the interval: the next step waits its turn rather
	// than sending another command in the same instant.
	tester.Move(x+30, y)
	if len(app.sent) != 1 {
		t.Fatalf("a drag inside the interval sent again: %#v", app.sent)
	}
	time.Sleep(volumeSendInterval + 20*time.Millisecond)
	tester.Move(x+45, y)
	volume, ok = lastCommand(app.sent, "volume")
	if !ok || len(app.sent) != 2 || volume.Volume != app.volumeValue {
		t.Fatalf("a drag past the interval did not send the moved level: %#v value=%v", app.sent, app.volumeValue)
	}

	// The track reports as it plays on: the thumb must not fall back.
	held := app.volumeValue
	app.player.Volume = 0.9
	tester.Frame()
	if app.volumeValue != held || len(app.sent) != 2 {
		t.Fatalf("a report tugged the thumb from %v to %v: %#v", held, app.volumeValue, app.sent)
	}

	// Letting go sends the level the pointer chose, and the bar settles: no
	// command per frame, and none after.
	tester.Move(x+14, y)
	chosen := app.volumeValue
	tester.Release(x+14, y)
	tester.Frame()
	tester.Frame()
	volume, ok = lastCommand(app.sent, "volume")
	if !ok || volume.Volume != chosen {
		t.Fatalf("releasing did not send the chosen level: %#v want %v", app.sent, chosen)
	}
	after := len(app.sent)
	tester.Frame()
	tester.Frame()
	if len(app.sent) != after {
		t.Fatalf("the bar kept sending after the release: %#v", app.sent)
	}
}

// TestNativeUIMuteButton checks that the volume icon silences the player and
// brings the level back: muting sends a level of zero, unmuting the level
// the mute was taken at.
func TestNativeUIMuteButton(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.player = playerState{
		WatchID: "sm1", Title: "Aの曲", Ready: true, Playing: true,
		PositionMS: 30_000, DurationMS: 120_000, Volume: 0.6,
	}
	tester := ui.NewTester(app.view, 1120, 760)
	app.sent = nil

	if err := tester.Click("ミュート"); err != nil {
		t.Fatal(err)
	}
	volume, ok := lastCommand(app.sent, "volume")
	if !ok || !app.muted || app.player.Volume != 0 || volume.Volume != 0 {
		t.Fatalf("muting did not silence the player: %#v muted=%v volume=%v", app.sent, app.muted, app.player.Volume)
	}

	if err := tester.Click("ミュート解除"); err != nil {
		t.Fatal(err)
	}
	volume, ok = lastCommand(app.sent, "volume")
	if app.muted || !ok || volume.Volume != 0.6 || app.player.Volume != 0.6 {
		t.Fatalf("unmuting did not come back to the mute's level: %#v muted=%v volume=%v", app.sent, app.muted, app.player.Volume)
	}
}

// TestNativeUIMuteMarkAtZero checks that a level dragged down to zero shows
// the mute mark, and that the icon brings the level back to half.
func TestNativeUIMuteMarkAtZero(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.player = playerState{
		WatchID: "sm1", Title: "Aの曲", Ready: true, Playing: true,
		PositionMS: 30_000, DurationMS: 120_000, Volume: 0.6,
	}
	tester := ui.NewTester(app.view, 1120, 760)

	app.player.Volume = 0
	tester.Frame()
	if _, ok := tester.Find("ミュート解除"); !ok {
		t.Fatal("a level of zero did not show the mute mark")
	}

	app.sent = nil
	if err := tester.Click("ミュート解除"); err != nil {
		t.Fatal(err)
	}
	volume, ok := lastCommand(app.sent, "volume")
	if !ok || app.silent() || volume.Volume != 0.5 || app.player.Volume != 0.5 {
		t.Fatalf("the icon did not come back to half: %#v volume=%v", app.sent, app.player.Volume)
	}
}

// TestNativeUICreatesCombinedMylist checks the card menu's 結合マイリストを作成:
// the dialog lists the saved mylists, one still saving cannot join, and 決定
// leaves a combined card under its heading that plays the members' tracks as
// one.
func TestNativeUICreatesCombinedMylist(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", TotalItemCount: 2}, []MylistItem{
		{WatchID: "sm1", Title: "朝1", Available: true, Duration: 120},
		{WatchID: "sm2", Title: "共有", Available: true, Duration: 120},
	})
	indexSaved(t, app, SavedIndexEntry{ID: "222", Name: "夜のリスト", TotalItemCount: 2}, []MylistItem{
		{WatchID: "sm2", Title: "共有", Available: true, Duration: 120},
		{WatchID: "sm3", Title: "夜3", Available: true, Duration: 120},
	})
	// A mylist whose body the background saving has not written yet, and a
	// combined list already made.
	indexSaved(t, app, SavedIndexEntry{ID: "333", Name: "保存中のリスト", TotalItemCount: 5}, nil)
	indexSaved(t, app, SavedIndexEntry{ID: "combine-1", Name: "既存の結合", Members: []string{"111"}, TotalItemCount: 2}, nil)

	tester := ui.NewTester(app.view, 1120, 760)

	// A mylist still saving cannot join, so nothing can be decided; the
	// dialog offers the mylists a combined list can hold, not combined lists
	// themselves.
	counts := func() map[string]int {
		m := map[string]int{}
		for _, text := range tester.Texts() {
			m[text]++
		}
		return m
	}
	onHome := counts()
	app.openCombineDialog("333")
	tester.Frame()
	if !app.combining || !tester.HasText("保存待ち") {
		t.Fatalf("the dialog did not mark the mylist still saving: %q", tester.Texts())
	}
	withDialog := counts()
	if withDialog["既存の結合"] != onHome["既存の結合"] {
		t.Fatal("the dialog offers a combined list as a member")
	}
	for _, name := range []string{"朝のリスト", "夜のリスト", "保存中のリスト"} {
		if withDialog[name] <= onHome[name] {
			t.Fatalf("the dialog does not offer %q: home=%d dialog=%d", name, onHome[name], withDialog[name])
		}
	}
	if on := app.combineSel["333"]; on != nil && *on {
		t.Fatal("a mylist still saving was chosen")
	}
	if err := tester.Click("決定"); err != nil {
		t.Fatal(err)
	}
	if !app.combining {
		t.Fatal("決定 with nothing chosen created a combined list")
	}
	app.combining = false
	tester.Frame()

	// The card menu opens the dialog with its mylist already chosen.
	if err := tester.RightClick("朝のリスト"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("結合マイリストを作成"); err != nil {
		t.Fatal(err)
	}
	if !app.combining {
		t.Fatalf("結合マイリストを作成 did not open the dialog: %q", tester.Texts())
	}
	if on := app.combineSel["111"]; on == nil || !*on {
		t.Fatalf("the menu's mylist is not chosen: %+v", app.combineSel)
	}

	// Choosing the second mylist and deciding saves the combined list.
	*app.combineCheck("222") = true
	app.combineName = "朝と夜"
	if err := tester.Click("決定"); err != nil {
		t.Fatal(err)
	}
	if app.combining {
		t.Fatal("決定 did not close the dialog")
	}
	var combined SavedIndexEntry
	for _, entry := range app.saved {
		if entry.Name == "朝と夜" {
			combined = entry
		}
	}
	if !combined.IsCombined() || len(combined.Members) != 2 || combined.TotalItemCount != 3 {
		t.Fatalf("combined entry = %+v; want the chosen mylists under the heading", combined)
	}

	// The home shows it as a card under the 結合マイリスト heading, the
	// sidebar under the same one; the menu offers a pin, and shuffling plays
	// across the members.
	tester.Frame()
	if !tester.HasText("朝と夜") || !tester.HasText("2個のマイリスト") {
		t.Fatalf("the home does not show the combined card: %q", tester.Texts())
	}
	if counts()["結合マイリスト"] < 2 {
		t.Fatalf("the home and the sidebar do not both group the combined lists: %q", tester.Texts())
	}
	if err := tester.RightClick("朝と夜"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("ピン留め"); !ok {
		t.Fatal("a combined list has no pin")
	}
	if _, ok := tester.Find("結合マイリストを作成"); ok {
		t.Fatal("a combined list offers combining again")
	}
	if err := tester.Click("シャッフル再生"); err != nil {
		t.Fatal(err)
	}
	if shuffle, ok := lastCommand(app.sent, "shuffle"); !ok || !shuffle.Enabled {
		t.Fatalf("シャッフル再生 did not turn shuffle on: %#v", app.sent)
	}
	if selected, ok := lastCommand(app.sent, "select"); !ok ||
		(selected.WatchID != "sm1" && selected.WatchID != "sm2" && selected.WatchID != "sm3") {
		t.Fatalf("シャッフル再生 did not start a track of the combined list: %#v", app.sent)
	}
	if !app.queueOpen {
		t.Fatal("シャッフル再生 did not open the queue screen")
	}

	// Its page names the member mylists, offers a pin, and has no refresh of
	// its own.
	app.queueOpen = false
	tester.Frame()
	if app.page == nil || app.page.ID != combined.ID || len(app.page.Items) != 3 {
		t.Fatalf("opened page = %+v; want the members' tracks merged", app.page)
	}
	if _, ok := tester.Find("更新"); ok {
		t.Fatal("a combined list offers a refresh")
	}
	if !tester.HasText("結合元: 朝のリスト、夜のリスト") {
		t.Fatalf("the page does not name the combined mylists: %q", tester.Texts())
	}
	if err := tester.Click("ピン留め"); err != nil {
		t.Fatal(err)
	}
	if entry := app.savedEntry(combined.ID); entry == nil || !entry.Pinned {
		t.Fatalf("pinning the combined list did not take: %+v", app.saved)
	}
	// A pinned combined list joins the pinned mylists at the top rather than
	// staying in its own category.
	pinned, own, grouped, read := app.mylistGroups()
	if len(pinned) != 1 || len(own) != 0 || len(grouped) != 1 || len(read) != 3 {
		t.Fatalf("groups with a pinned combined list: pinned=%d own=%d combined=%d read=%d",
			len(pinned), len(own), len(grouped), len(read))
	}
	if !pinned[0].Combined {
		t.Fatalf("the pinned list is not the combined one: %+v", pinned[0])
	}

	// Deleting it from the sidebar keeps its members.
	app.router.Push(homePath)
	tester.Frame()
	if err := tester.RightClick("朝と夜"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("削除"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("削除する"); err != nil {
		t.Fatal(err)
	}
	if app.savedEntry(combined.ID) != nil {
		t.Fatalf("the combined list stays: %+v", app.saved)
	}
	if app.savedEntry("111") == nil || app.savedEntry("222") == nil {
		t.Fatalf("deleting the combined list took a member: %+v", app.saved)
	}
}

// TestNativeUICombineDialogCheckboxes checks the dialog's check boxes: a click
// on a mylist chooses and unchoses it, and a mylist whose saved copy is not
// written yet cannot be chosen.
func TestNativeUICombineDialogCheckboxes(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", TotalItemCount: 1},
		[]MylistItem{{WatchID: "sm1", Title: "朝1", Available: true, Duration: 120}})
	indexSaved(t, app, SavedIndexEntry{ID: "222", Name: "夜のリスト", TotalItemCount: 1},
		[]MylistItem{{WatchID: "sm2", Title: "夜2", Available: true, Duration: 120}})
	indexSaved(t, app, SavedIndexEntry{ID: "333", Name: "保存中のリスト", TotalItemCount: 5}, nil)

	app.openCombineDialog("111")
	tester := ui.NewTester(func(c *ui.Context) { app.combineDialog(c) }, 480, 460)
	if on := app.combineSel["111"]; on == nil || !*on {
		t.Fatalf("the mylist the dialog opened on is not chosen: %+v", app.combineSel)
	}
	if err := tester.Click("夜のリスト"); err != nil {
		t.Fatal(err)
	}
	if on := app.combineSel["222"]; on == nil || !*on {
		t.Fatalf("clicking a mylist did not choose it: %+v", app.combineSel)
	}
	if err := tester.Click("夜のリスト"); err != nil {
		t.Fatal(err)
	}
	if on := app.combineSel["222"]; on != nil && *on {
		t.Fatalf("clicking a chosen mylist did not unchoose it: %+v", app.combineSel)
	}
	if err := tester.Click("保存中のリスト"); err != nil {
		t.Fatal(err)
	}
	if on := app.combineSel["333"]; on != nil && *on {
		t.Fatal("a mylist whose saved copy is not written was chosen")
	}
	if !app.combineReady() {
		t.Fatal("the dialog is not ready with a heading and the first mylist")
	}
}

// TestNativeUICombineDialogSubmitsWithEnter checks Enter in the heading field
// creates the combined list, and that the frame it happens in leaves the
// dialog's state in one piece.
func TestNativeUICombineDialogSubmitsWithEnter(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", TotalItemCount: 1},
		[]MylistItem{{WatchID: "sm1", Title: "朝1", Available: true, Duration: 120}})
	indexSaved(t, app, SavedIndexEntry{ID: "222", Name: "夜のリスト", TotalItemCount: 1},
		[]MylistItem{{WatchID: "sm2", Title: "夜2", Available: true, Duration: 120}})

	app.openCombineDialog("111")
	*app.combineCheck("222") = true
	app.combineName = "朝と夜"
	tester := ui.NewTester(func(c *ui.Context) { app.combineDialog(c) }, 480, 460)
	tester.Key(0, ui.KeyEnter)
	if app.combining {
		t.Fatal("Enter in the heading did not create the combined list")
	}
	var combined SavedIndexEntry
	for _, entry := range app.saved {
		if entry.IsCombined() {
			combined = entry
		}
	}
	if combined.Name != "朝と夜" || len(combined.Members) != 2 {
		t.Fatalf("Enter's combined entry = %+v; want the chosen mylists", combined)
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

// noAudioServer points the app's nico at a port nothing answers, so a test
// that turns offline saving on does not reach niconico.
func noAudioServer(app *nativeApp) {
	app.nico.baseURL = "http://127.0.0.1:1"
	app.nico.apiBase = "http://127.0.0.1:1"
}

// TestNativeUITogglesOfflineSaving checks the mylist menu's オフライン保存 and
// the mylist page's own button: the flag is written either way, and the
// mylist reads as saved afterwards.
func TestNativeUITogglesOfflineSaving(t *testing.T) {
	app := savedApp(t)
	noAudioServer(app)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト"}, testItems("sm1"))
	tester := ui.NewTester(app.view, 1120, 760)

	if err := tester.RightClick("朝のリスト"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("オフライン保存"); !ok {
		t.Fatalf("the menu has no offline saving: %q", tester.Texts())
	}
	if err := tester.Click("オフライン保存"); err != nil {
		t.Fatal(err)
	}
	index, err := app.mylist.ListSaved()
	if err != nil {
		t.Fatal(err)
	}
	if at, ok := indexAt(index, "111"); !ok || !index[at].Download {
		t.Fatalf("the menu did not turn offline saving on: %+v", index)
	}
	if entry := app.savedEntry("111"); entry == nil || !entry.Download {
		t.Fatal("the view does not know the mylist is saved")
	}

	// The mylist page says it is saved, and its button turns the saving off
	// again.
	if err := tester.Click("朝のリスト"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("オフライン保存をやめる"); !ok {
		t.Fatalf("the page does not offer to stop: %q", tester.Texts())
	}
	if err := tester.Click("オフライン保存をやめる"); err != nil {
		t.Fatal(err)
	}
	index, err = app.mylist.ListSaved()
	if err != nil {
		t.Fatal(err)
	}
	if at, ok := indexAt(index, "111"); !ok || index[at].Download {
		t.Fatalf("the page's button did not turn offline saving off: %+v", index)
	}
}

// TestNativeUISettingsQuality checks the quality the settings dropdown keeps:
// the choice is stored in the form the downloads pick their band by, and a
// change asks for the saved audio to be fetched again.
func TestNativeUISettingsQuality(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("設定"); err != nil {
		t.Fatal(err)
	}
	// The dropdown shows the current preset, and opening it lists every
	// preset, the estimates included.
	if !tester.HasText(audioQualityLabel(320)) {
		t.Fatalf("the settings show no quality choice: %q", tester.Texts())
	}
	if err := tester.Click(audioQualityLabel(320)); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText(audioQualityLabel(64)) {
		t.Fatalf("the dropdown lists no low preset: %q", tester.Texts())
	}
	if err := tester.Click(audioQualityLabel(64)); err != nil {
		t.Fatal(err)
	}
	if app.audioKbps.Load() != 64 || app.qualityOption != audioQualityLabel(64) {
		t.Fatalf("the choice did not reach the downloads: %d/%q", app.audioKbps.Load(), app.qualityOption)
	}
	if got := audioQualityValue(int(app.audioKbps.Load())); got != "64" {
		t.Fatalf("the stored quality = %q; want the low preset", got)
	}
}

// TestNativeUIShowsOfflineSavingChip checks that a download-enabled mylist
// carries its saving state — the saved count included — on its card and on
// its page.
func TestNativeUIShowsOfflineSavingChip(t *testing.T) {
	app := savedApp(t)
	noAudioServer(app)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト"}, testItems("sm1", "sm2", "sm3"))
	saved, err := app.mylist.SetDownload("111", true)
	if err != nil {
		t.Fatal(err)
	}
	app.saved = saved
	app.audioStats["111"] = audioStat{saved: 2, total: 3}

	tester := ui.NewTester(app.view, 1120, 760)
	if !tester.HasText("2/3") {
		t.Fatalf("the card has no offline saving chip: %q", tester.Texts())
	}
	if err := tester.Click("朝のリスト"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("2/3") {
		t.Fatalf("the page has no offline saving chip: %q", tester.Texts())
	}
}
