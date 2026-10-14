package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestNativeUITogglesSidebar(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)
	if !tester.HasText("VocaBox") || !tester.HasText("ライブラリ") {
		t.Fatalf("sidebar is not shown initially: %q", tester.Texts())
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
	if _, hasLogo := tester.Find("V"); app.sidebarOpen || !hasLogo || tester.HasText("ライブラリ") {
		t.Fatalf("sidebar did not close cleanly: open=%v texts=%q", app.sidebarOpen, tester.Texts())
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

func TestNativeUIOpensSavedSnapshot(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.saved = []SavedMylist{{
		ID:   "123",
		Name: "作業用プレイリスト",
		Items: []MylistItem{{
			WatchID:   "sm9",
			Title:     "サンプル曲",
			Uploader:  "投稿者",
			Duration:  90,
			Available: true,
		}},
	}}

	tester := ui.NewTester(app.view, 1120, 760)
	if !tester.HasText("VocaBox") {
		t.Fatalf("initial view is missing VocaBox: %q", tester.Texts())
	}
	if err := tester.Click("作業用プレイリスト"); err != nil {
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

	// An empty library still offers the import card.
	if !tester.HasText("マイリストを読み込む") {
		t.Fatalf("import card is missing: %q", tester.Texts())
	}
	if tester.HasText("公開マイリストのURLまたはID") {
		t.Fatalf("the sidebar still carries the import field: %q", tester.Texts())
	}
	if err := tester.Click("マイリストを読み込む"); err != nil {
		t.Fatal(err)
	}
	if !app.importing {
		t.Fatal("import card did not open the dialog")
	}
	if !tester.HasText("公開マイリストを読み込む") {
		t.Fatalf("dialog is not shown: %q", tester.Texts())
	}
	if !tester.Focused("公開マイリストのURLまたはID") {
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

// TestNativeUIKeepsPlayingTrackAcrossLists checks that opening another list
// neither stops the track playing nor hands the queue to the shown list.
func TestNativeUIKeepsPlayingTrackAcrossLists(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.saved = []SavedMylist{
		{ID: "111", Name: "リストA", Items: []MylistItem{{WatchID: "sm1", Title: "Aの曲", Available: true}}},
		{ID: "222", Name: "リストB", Items: []MylistItem{{WatchID: "sm2", Title: "Bの曲", Available: true}}},
	}
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("リストA"); err != nil {
		t.Fatal(err)
	}
	// The player reports a track of A playing while list B is shown.
	app.player = playerState{WatchID: "sm1", ListID: "111", Title: "Aの曲", Ready: true, Playing: true, Volume: 1}
	app.sent = nil

	if err := tester.Click("リストB"); err != nil {
		t.Fatal(err)
	}
	if app.player.WatchID != "sm1" || !app.player.Playing {
		t.Fatalf("switching lists stopped the playing track: %#v", app.player)
	}
	set, ok := lastCommand(app.sent, "setItems")
	if !ok || set.ID != "222" {
		t.Fatalf("the opened list was not sent: %#v", app.sent)
	}
	if !tester.HasText("Aの曲") {
		t.Fatalf("the player bar lost the playing track: %q", tester.Texts())
	}
}
// TestNativeUICanLoadNextOnlyForPlayingList checks that only the queue built
// from the shown list takes its next page: a list merely shown must not feed
// the queue of the list playing.
func TestNativeUICanLoadNextOnlyForPlayingList(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.page = &MylistPage{ID: "222"}
	app.hasMore = true
	app.player = playerState{WatchID: "sm1", ListID: "111"}

	if app.canLoadNext() {
		t.Fatal("loading the shown list must not extend another list's queue")
	}
	app.advance()
	if app.pendingAdvance || app.loading || hasCommand(app.sent, "next") {
		t.Fatalf("advancing left the playing queue: pending=%v loading=%v", app.pendingAdvance, app.loading)
	}

	app.player.ListID = "222"
	if !app.canLoadNext() {
		t.Fatal("the playing list's next page can extend its queue")
	}
	app.page = nil
	if app.canLoadNext() {
		t.Fatal("no page means no next page to load")
	}
}

// TestNativeUIQueueScreenShowsTheQueue checks the screen the bar opens: the
// queue in playing order, labelled with the list it came from, with a row
// click selecting the track and home leaving the screen.
func TestNativeUIQueueScreenShowsTheQueue(t *testing.T) {
	app := newNativeApp(NewMylist())
	items := []MylistItem{
		{WatchID: "sm1", Title: "一曲目", Available: true},
		{WatchID: "sm2", Title: "二曲目", Available: true},
		{WatchID: "sm3", Title: "三曲目", Available: true},
	}
	app.saved = []SavedMylist{{ID: "123", Name: "作業用キュー", Items: items}}
	app.player = playerState{
		WatchID: "sm2", ListID: "123", Name: "作業用キュー", Title: "二曲目",
		Ready: true, Playing: true, Volume: 1,
		Order: []string{"sm2", "sm3", "sm1"}, Items: items, QueueIndex: 0, QueuePos: 0,
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
		t.Fatalf("the queue is not labelled with the list it came from: %q", tester.Texts())
	}
	// A row is chosen by the list itself, which is what a drag needs too.
	two, ok := tester.Find("二曲目")
	if !ok {
		t.Fatalf("the playlist is missing tracks: %q", tester.Texts())
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
		t.Fatalf("a playlist row did not play its track: %#v", app.sent)
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

// TestNativeUIQueueDragReorders checks the playlist rows move by dragging
// them, the way the list pages' rows do not.
func TestNativeUIQueueDragReorders(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.queueOpen = true
	app.player = playerState{
		Name:  "作業用ドラッグ",
		Order: []string{"sm1", "sm2", "sm3"},
		Items: []MylistItem{
			{WatchID: "sm1", Title: "一曲目", Available: true},
			{WatchID: "sm2", Title: "二曲目", Available: true},
			{WatchID: "sm3", Title: "三曲目", Available: true},
		},
	}
	tester := ui.NewTester(app.view, 1120, 760)
	first, ok := tester.Find("一曲目")
	if !ok {
		t.Fatalf("the playlist is not shown: %q", tester.Texts())
	}
	last, ok := tester.Find("三曲目")
	if !ok {
		t.Fatalf("the playlist is not shown: %q", tester.Texts())
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
		items[i] = MylistItem{WatchID: id, Title: id, Available: true}
		order[i] = id
	}
	app.player = playerState{
		Name: "作業用スクロール", WatchID: "sm200", Order: order, Items: items,
		QueueIndex: 199, QueuePos: 199,
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
			{WatchID: "sm1", Title: "一曲目", Available: true},
			{WatchID: "sm2", Title: "二曲目", Available: true},
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

// TestNativeUIQueueCommands checks the playlist rows drive the player:
// removing a track and reordering rows.
func TestNativeUIQueueCommands(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.queueOpen = true
	app.player = playerState{
		Name:  "作業用コマンド",
		Order: []string{"sm1", "sm2"},
		Items: []MylistItem{
			{WatchID: "sm1", Title: "一曲目", Available: true},
			{WatchID: "sm2", Title: "二曲目", Available: true},
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
// player's own state alone: the list the app happens to show neither
// reorders the queue nor fills a track the player does not carry.
func TestQueueItemsComeFromTheReport(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.page = &MylistPage{ID: "9", Items: []MylistItem{
		{WatchID: "sm2", Title: "表示中リストの曲", Available: true},
		{WatchID: "sm1", Title: "表示中リストの曲", Available: true},
	}}
	app.player = playerState{
		Order: []string{"sm2", "sm9", "sm1"},
		Items: []MylistItem{{WatchID: "sm9", Title: "再生中リストの曲", Available: true}},
	}
	got := app.queueItems()
	ids := make([]string, 0, len(got))
	for _, item := range got {
		ids = append(ids, item.WatchID)
	}
	if len(ids) != 3 || ids[0] != "sm2" || ids[1] != "sm9" || ids[2] != "sm1" {
		t.Fatalf("queue order = %#v; want sm2, sm9, sm1", ids)
	}
	if got[1].Title != "再生中リストの曲" {
		t.Fatalf("the report's track lost its metadata: %#v", got[1])
	}
	if got[0].Title != "" || got[2].Title != "" {
		t.Fatalf("a track the report does not carry was filled in from the shown list: %#v", got)
	}
}

// TestNativeUIListDoesNotFollowPlayback checks the playlist screen carries
// no playback state: opening a list keeps its top in view however far down
// the track playing sits.
func TestNativeUIListDoesNotFollowPlayback(t *testing.T) {
	app := newNativeApp(NewMylist())
	items := make([]MylistItem, 100)
	for i := range items {
		items[i] = MylistItem{WatchID: fmt.Sprintf("sm%d", i+1), Title: fmt.Sprintf("曲%d", i+1), Available: true}
	}
	app.saved = []SavedMylist{{ID: "123", Name: "作業用リスト", Items: items}}

	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("作業用リスト"); err != nil {
		t.Fatal(err)
	}
	app.player = playerState{WatchID: "sm80", Ready: true, Volume: 1}
	tester.Frame()
	if first, _ := app.trackList.Visible(); first != 0 {
		t.Fatalf("the list followed the track playing: first=%d", first)
	}
}

// TestNativeUISidebarListLeavesQueueScreen checks choosing a list in the
// sidebar leaves the queue screen for it, as choosing home does, even the
// list already showing, whose click changes no route of its own.
func TestNativeUISidebarListLeavesQueueScreen(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.saved = []SavedMylist{
		{ID: "111", Name: "リストA", Items: []MylistItem{{WatchID: "sm1", Title: "Aの曲", Available: true}}},
		{ID: "222", Name: "リストB", Items: []MylistItem{{WatchID: "sm2", Title: "Bの曲", Available: true}}},
	}
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("リストA"); err != nil {
		t.Fatal(err)
	}
	app.queueOpen = true
	tester.Frame()
	if err := tester.Click("リストB"); err != nil {
		t.Fatal(err)
	}
	if app.queueOpen {
		t.Fatal("choosing another list left the queue screen open")
	}
	if app.router.Path() != "/list/222" || app.page == nil || app.page.ID != "222" || !tester.HasText("Bの曲") {
		t.Fatalf("the chosen list was not opened: path=%q page=%#v texts=%q", app.router.Path(), app.page, tester.Texts())
	}

	app.queueOpen = true
	tester.Frame()
	if err := tester.Click("リストB"); err != nil {
		t.Fatal(err)
	}
	if app.queueOpen {
		t.Fatal("choosing the shown list again left the queue screen open")
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
		items[i] = MylistItem{WatchID: id, Title: id, Available: true}
		order[i] = id
	}
	app.player = playerState{
		Name: "作業用追従", WatchID: "sm1", Order: order, Items: items,
		QueueIndex: 0, QueuePos: 0,
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
	app := newNativeApp(NewMylist())
	app.saved = []SavedMylist{{
		ID:   "123",
		Name: "作業用クリック",
		Items: []MylistItem{{
			WatchID:   "sm9",
			Title:     "サンプル曲",
			Available: true,
		}},
	}}

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
		t.Fatal("playing from the list did not open the queue screen")
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

func TestNativeUIHomeOpensPlaylistCards(t *testing.T) {
	app := newNativeApp(NewMylist())
	// With the sidebar closed the card is the only element named after the
	// playlist, so a click targets the card.
	app.sidebarOpen = false
	app.saved = []SavedMylist{{
		ID:             "123",
		Name:           "作業用ホーム",
		TotalItemCount: 6,
		Items: []MylistItem{
			{WatchID: "sm1", Title: "一曲目", Available: true},
			{WatchID: "sm2", Title: "二曲目", Available: true},
			{WatchID: "sm3", Title: "三曲目", Available: true},
			{WatchID: "sm4", Title: "四曲目", Available: true},
			{WatchID: "sm5", Title: "五曲目", Available: true},
		},
	}}

	tester := ui.NewTester(app.view, 1120, 760)
	if !tester.HasText("ホーム") || !tester.HasText("作業用ホーム") || !tester.HasText("全6曲") {
		t.Fatalf("home does not show the playlist card: %q", tester.Texts())
	}
	if err := tester.Click("作業用ホーム"); err != nil {
		t.Fatal(err)
	}
	if app.router.Path() != "/list/123" || app.page == nil || app.page.ID != "123" {
		t.Fatalf("card did not open the playlist: path=%q page=%#v", app.router.Path(), app.page)
	}
	if !tester.HasText("一曲目") {
		t.Fatalf("opened list is not shown: %q", tester.Texts())
	}

	// The side buttons of a mouse arrive as the back and forward keys.
	tester.Key(0, ui.KeyBack)
	if app.router.Path() != "/" || !tester.HasText("保存したプレイリストから選んで再生") {
		t.Fatalf("back did not return home: path=%q", app.router.Path())
	}
	if app.page == nil || app.page.ID != "123" {
		t.Fatalf("the opened list was dropped: %#v", app.page)
	}
	tester.Key(0, ui.KeyForward)
	if app.router.Path() != "/list/123" || !tester.HasText("一曲目") {
		t.Fatalf("forward did not return to the list: path=%q", app.router.Path())
	}

	app.sidebarOpen = true
	tester.Frame()
	if err := tester.Click("ホーム"); err != nil {
		t.Fatal(err)
	}
	if app.router.Path() != "/" || !tester.HasText("保存したプレイリストから選んで再生") {
		t.Fatalf("sidebar home item did not return home: path=%q", app.router.Path())
	}
	if app.page == nil || app.page.ID != "123" {
		t.Fatalf("the opened list was dropped: %#v", app.page)
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
	app := newNativeApp(NewMylist())
	app.saved = []SavedMylist{{
		ID:   "123",
		Name: "作業用サムネ",
		Items: []MylistItem{{
			WatchID:   "sm27718606",
			Title:     "サンプル曲",
			Available: true,
			ThumbURL:  "https://nicovideo.cdn.nimg.jp/thumbnails/27718606/27718606",
		}},
	}}

	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("作業用サムネ"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("サンプル曲") {
		t.Fatalf("track is not shown: %q", tester.Texts())
	}
	// Without a window there is nothing to load into: the row keeps its
	// placeholder, the list's pictures wait in the fill queue, and no load
	// starts.
	if len(app.thumbBusy) != 0 || len(app.thumbs) != 0 {
		t.Fatalf("unexpected thumbnail load: busy=%v thumbs=%d", app.thumbBusy, len(app.thumbs))
	}
	if len(app.fillQueue) != 1 {
		t.Fatalf("fill queue = %d; want the list's entry queued", len(app.fillQueue))
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
		t.Error("a failed picture must not retry until the list reloads")
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
	before := app.volumeValue
	tester.Move(x+45, y)
	volume, ok = lastCommand(app.sent, "volume")
	if !ok || len(app.sent) != 2 || volume.Volume != before {
		t.Fatalf("a drag past the interval did not send: %#v value=%v", app.sent, before)
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
