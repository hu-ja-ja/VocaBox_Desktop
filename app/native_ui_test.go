package main

import (
	"os"
	"path/filepath"
	"testing"

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
	app.playerLoaded = true
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

	app.player = PlayerReport{WatchID: "sm9", Ready: true, PositionMS: 0, DurationMS: 90_000, Volume: 1}
	if err := tester.Click("再生"); err != nil {
		t.Fatal(err)
	}
	if !hasCommand(app.pending, "play") {
		t.Fatalf("icon play button did not send a command: %#v", app.pending)
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
	if app.page != nil || len(app.pending) != 0 {
		t.Fatalf("cancel must not open anything: pending=%d page=%#v", len(app.pending), app.page)
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
	if app.atHome || app.page == nil || app.page.ID != "123" {
		t.Fatalf("card did not open the playlist: home=%v page=%#v", app.atHome, app.page)
	}
	if !tester.HasText("一曲目") {
		t.Fatalf("opened list is not shown: %q", tester.Texts())
	}

	app.sidebarOpen = true
	tester.Frame()
	if err := tester.Click("ホーム"); err != nil {
		t.Fatal(err)
	}
	if !app.atHome || app.selectedSavedID != homeItemID {
		t.Fatalf("sidebar home item did not return home: home=%v selected=%q", app.atHome, app.selectedSavedID)
	}
	if !tester.HasText("保存したプレイリストから選んで再生") {
		t.Fatalf("home view is not shown: %q", tester.Texts())
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
	app.playerLoaded = true
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
