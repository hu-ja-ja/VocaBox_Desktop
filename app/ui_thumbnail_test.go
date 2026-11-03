package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

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
