package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

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
	tester.Advance(300 * time.Millisecond)
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

// TestNativeUIQueueSlidesOverThePage checks the queue's sheet: it rises from
// under the player bar, the page stays behind it while it moves, and closing
// it brings the page back.
func TestNativeUIQueueSlidesOverThePage(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "キューの元"},
		[]MylistItem{{WatchID: "sm1", Title: "ページの曲", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("キューの元"); err != nil {
		t.Fatal(err)
	}
	app.player = playerState{
		Name: "キューの元", Title: "バーの曲", WatchID: "sm2", Ready: true, Volume: 1,
		Order: []string{"sm2"},
		Items: []MylistItem{{WatchID: "sm2", Title: "キューの曲", Available: true, Duration: 120}},
	}
	tester.Frame()
	button, ok := tester.Find("再生キュー")
	if !ok {
		t.Fatal("queue button is missing")
	}
	x, y := button.X+button.W/2, button.Y+button.H/2
	tester.ClickAt(x, y)
	if !app.queueOpen {
		t.Fatal("the queue button did not open the queue")
	}
	// Mid-way, the sheet is below its place and the page still shows.
	tester.Advance(120 * time.Millisecond)
	rising, ok := tester.Find("キューの曲")
	if !ok {
		t.Fatalf("the queue is missing while it rises: %q", tester.Texts())
	}
	if !tester.HasText("ページの曲") {
		t.Fatal("the page left before the sheet covered it")
	}
	tester.Advance(200 * time.Millisecond)
	rest, ok := tester.Find("キューの曲")
	if !ok || rest.Y >= rising.Y {
		t.Fatalf("the queue did not rise: rising=%v rest=%v", rising, rest)
	}
	if tester.HasText("ページの曲") {
		t.Fatal("the page stayed behind the queue open")
	}
	// Closing brings the sheet down over the page, then leaves it.
	tester.ClickAt(x, y)
	if app.queueOpen {
		t.Fatal("the queue button did not close the queue")
	}
	tester.Advance(120 * time.Millisecond)
	sinking, ok := tester.Find("キューの曲")
	if !ok || sinking.Y <= rest.Y {
		t.Fatalf("the queue did not sink: rest=%v sinking=%v", rest, sinking)
	}
	if !tester.HasText("ページの曲") {
		t.Fatal("the page did not come back under the sheet")
	}
	tester.Advance(200 * time.Millisecond)
	if tester.HasText("キューの曲") {
		t.Fatal("the queue stayed after closing")
	}
	if !tester.HasText("ページの曲") {
		t.Fatal("the page did not return after the queue closed")
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
	menu := accessOf(t, tester, ui.RoleButton, locales.TrackMenu())
	if menu.HasPopup != ui.PopupMenu || !menu.Expanded {
		t.Fatalf("the queue row menu button is %+v", menu)
	}
	accessOf(t, tester, ui.RoleMenu, locales.TrackMenu())
	accessOf(t, tester, ui.RoleMenuItem, locales.QueueRemove())
	if err := tester.Click("曲のメニュー"); err != nil {
		t.Fatal(err)
	}
	if app.queueMenuOpen || tester.HasText("キューから削除") {
		t.Fatal("the second click did not close the queue row menu")
	}
	if menu := accessOf(t, tester, ui.RoleButton, locales.TrackMenu()); menu.Expanded {
		t.Fatalf("the closed queue row menu button is %+v", menu)
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
	lyrics, ok := tester.Find("歌詞を表示")
	if !ok {
		t.Fatal("lyrics button is missing")
	}
	// The gap between the volume and the lyrics button is the bar's blank
	// space now that the lyrics button sits beside the queue.
	x := volume.X + volume.W + (lyrics.X-volume.X-volume.W)/2
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
	// The list row and its nested button share this accessible label, so click
	// the row's center rather than asking the tester to resolve the duplicate.
	track, ok := tester.Find("サンプル曲")
	if !ok {
		t.Fatal("the track row is not shown")
	}
	tester.ClickAt(track.X+track.W/2, track.Y+track.H/2)
	tester.Frame()
	selected, ok := lastCommand(app.sent, "select")
	if !ok || selected.WatchID != "sm9" {
		t.Fatalf("clicking a track did not play it: %#v", app.sent)
	}
	if !app.queueOpen {
		t.Fatal("playing from the mylist did not open the queue screen")
	}
}

// TestNativeUIQueueKeyboardStartsAtThePlayingTrack checks that the queue's
// keys move from the track playing, rather than from no row at all, which
// played the first track whatever the user had clicked.
func TestNativeUIQueueKeyboardStartsAtThePlayingTrack(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.queueOpen = true
	items := []MylistItem{
		{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120},
		{WatchID: "sm2", Title: "二曲目", Available: true, Duration: 120},
		{WatchID: "sm3", Title: "三曲目", Available: true, Duration: 120},
		{WatchID: "sm4", Title: "四曲目", Available: true, Duration: 120},
	}
	app.player = playerState{
		WatchID: "sm2", MylistID: "123", Name: "作業用キー", Title: "二曲目",
		Ready: true, Playing: true, Volume: 1,
		Order: []string{"sm1", "sm2", "sm3", "sm4"}, Items: items, QueueIndex: 1,
	}
	tester := ui.NewTester(app.view, 1120, 760)
	app.sent = nil
	if !tester.Focused("二曲目") {
		t.Fatalf("opening the queue did not focus the track playing: %q", tester.Texts())
	}
	if len(app.sent) != 0 {
		t.Fatalf("opening the queue sent commands: %#v", app.sent)
	}

	tester.Key(0, ui.KeyDown)
	tester.Frame()
	down, ok := lastCommand(app.sent, "select")
	if !ok || down.WatchID != "sm3" {
		t.Fatalf("Down did not move to the next track: %#v", app.sent)
	}
	app.sent = nil
	tester.Key(0, ui.KeyUp)
	tester.Frame()
	up, ok := lastCommand(app.sent, "select")
	if !ok || up.WatchID != "sm2" {
		t.Fatalf("Up did not move back to the track playing: %#v", app.sent)
	}
}

// TestNativeUIQueueKeyboardFollowsTheClickedRow checks that a row clicked
// before the keys keeps the list's choice, so Up and Down move from it.
func TestNativeUIQueueKeyboardFollowsTheClickedRow(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.queueOpen = true
	items := []MylistItem{
		{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120},
		{WatchID: "sm2", Title: "二曲目", Available: true, Duration: 120},
		{WatchID: "sm3", Title: "三曲目", Available: true, Duration: 120},
		{WatchID: "sm4", Title: "四曲目", Available: true, Duration: 120},
	}
	app.player = playerState{
		WatchID: "sm2", MylistID: "123", Name: "作業用クリック", Title: "二曲目",
		Ready: true, Playing: true, Volume: 1,
		Order: []string{"sm1", "sm2", "sm3", "sm4"}, Items: items, QueueIndex: 1,
	}
	tester := ui.NewTester(app.view, 1120, 760)
	app.sent = nil
	three, ok := tester.Find("三曲目")
	if !ok {
		t.Fatalf("the queue is missing tracks: %q", tester.Texts())
	}
	tester.ClickAt(three.X+10, three.Y+three.H/2)
	tester.Frame()
	clicked, ok := lastCommand(app.sent, "select")
	if !ok || clicked.WatchID != "sm3" {
		t.Fatalf("clicking a queue row did not play it: %#v", app.sent)
	}
	if app.queueChoice != 2 {
		t.Fatalf("the clicked row is not the list's choice: %d", app.queueChoice)
	}

	app.sent = nil
	tester.Key(0, ui.KeyDown)
	tester.Frame()
	down, ok := lastCommand(app.sent, "select")
	if !ok || down.WatchID != "sm4" {
		t.Fatalf("Down did not move from the clicked row: %#v", app.sent)
	}
	app.sent = nil
	tester.Key(0, ui.KeyUp)
	tester.Frame()
	up, ok := lastCommand(app.sent, "select")
	if !ok || up.WatchID != "sm3" {
		t.Fatalf("Up did not move back to the clicked row: %#v", app.sent)
	}
}

// TestNativeUIQueueTabReachesTheChosenMenu checks the queue's stop in the Tab
// order: the list, its chosen row's menu, then the player bar, with no focus
// lost when the menu button appears.
func TestNativeUIQueueTabReachesTheChosenMenu(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.queueOpen = true
	items := []MylistItem{
		{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120},
		{WatchID: "sm2", Title: "二曲目", Available: true, Duration: 120},
	}
	app.player = playerState{
		WatchID: "sm2", MylistID: "123", Name: "作業用タブ", Title: "二曲目",
		Ready: true, Playing: true, Volume: 1, DurationMS: 120_000,
		Order: []string{"sm1", "sm2"}, Items: items, QueueIndex: 1,
	}
	tester := ui.NewTester(app.view, 1120, 760)
	if !tester.Focused("二曲目") {
		t.Fatalf("opening the queue did not focus the track playing: %q", tester.Texts())
	}

	tester.Key(0, ui.KeyTab)
	tester.Frame()
	if !tester.Focused(locales.TrackMenu()) {
		t.Fatalf("Tab did not reach the chosen row's menu: %q", tester.Texts())
	}
	tester.Key(0, ui.KeyTab)
	tester.Frame()
	if tester.Focused(locales.TrackMenu()) {
		t.Fatal("Tab stayed on the chosen row's menu")
	}
	if !tester.Focused(locales.PlayerSeek()) {
		t.Fatalf("Tab did not leave the queue for the player bar: %q", tester.Texts())
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
	for _, label := range []string{"前へ", "再生", "次へ", "音量", "歌詞を表示", "再生キュー"} {
		bounds, ok := tester.Find(label)
		if !ok || bounds.X < 0 || bounds.X+bounds.W > 760 {
			t.Fatalf("%q is missing or outside the minimum-width window: bounds=%v", label, bounds)
		}
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
