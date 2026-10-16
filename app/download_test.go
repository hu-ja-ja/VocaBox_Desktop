package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withoutPauses runs the download passes without the pauses between segments
// and tracks, which only exist for the delivery edge's sake.
func withoutPauses(t *testing.T) {
	t.Helper()
	audioSegmentInterval, audioTrackInterval = 0, 0
	t.Cleanup(func() {
		audioSegmentInterval, audioTrackInterval = 100*time.Millisecond, time.Second
	})
}

// downloadTestApp is an app whose mylist state lives in a temp directory and
// whose nico points at the audio fixture server.
func downloadTestApp(t *testing.T) (*nativeApp, *audioTestServer) {
	t.Helper()
	server := newAudioTestServer(t)
	app := newNativeApp(&Mylist{configDir: t.TempDir()})
	app.nico = server.nico
	return app, server
}

// writeBody writes a mylist body and its index line, as a save would.
func writeBody(t *testing.T, app *nativeApp, entry SavedIndexEntry, items []MylistItem) {
	t.Helper()
	entry.TotalItemCount = len(items)
	body := SavedMylist{ID: entry.ID, Name: entry.Name, TotalItemCount: entry.TotalItemCount, Items: items}
	if err := app.mylist.writeBody(body); err != nil {
		t.Fatal(err)
	}
	index, err := app.mylist.readIndex()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.mylist.writeIndex(upsertIndex(index, entry, nil)); err != nil {
		t.Fatal(err)
	}
}

// writeAudio saves a stand-in audio file for the track, so a test can tell
// what the passes keep and what they drop.
func writeAudio(t *testing.T, app *nativeApp, watchID string) string {
	t.Helper()
	path, err := app.mylist.audioPath(watchID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("saved "+watchID), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func audioExists(t *testing.T, app *nativeApp, watchID string) bool {
	t.Helper()
	path, err := app.mylist.audioPath(watchID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(path)
	return err == nil
}

func testItems(ids ...string) []MylistItem {
	items := make([]MylistItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, MylistItem{WatchID: id, VideoID: id, Title: "曲 " + id, Available: true, Duration: 125})
	}
	return items
}

// TestIsPlayable checks what plays: a video niconico still serves. A deleted
// or private video keeps its title in a mylist saved before its deletion was
// noticed, and its stub comes without a duration, so a track without one
// neither plays nor is saved or counted.
func TestIsPlayable(t *testing.T) {
	cases := []struct {
		name string
		item MylistItem
		want bool
	}{
		{"a served track", MylistItem{WatchID: "sm1", Title: "曲", Available: true, Duration: 125}, true},
		{"a stub from a saved mylist", MylistItem{WatchID: "sm2", Title: "削除された動画", Available: true}, false},
		{"a track without a duration", MylistItem{WatchID: "sm3", Title: "曲", Available: true}, false},
		{"a video niconico does not serve", MylistItem{WatchID: "sm4", Title: "削除された動画"}, false},
		{"a video with no title", MylistItem{WatchID: "sm5", Available: true, Duration: 125}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPlayable(tc.item); got != tc.want {
				t.Fatalf("isPlayable(%+v) = %v; want %v", tc.item, got, tc.want)
			}
		})
	}
}

// TestDownloadMylistSavesWhatIsMissing checks one pass: the track the mylist
// holds and this PC has not saved is fetched at the preset's band, and a
// track already saved is left alone.
func TestDownloadMylistSavesWhatIsMissing(t *testing.T) {
	withoutPauses(t)
	app, server := downloadTestApp(t)
	app.setAudioQuality(192)
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", Download: true}, testItems("sm1", "sm2"))
	saved := writeAudio(t, app, "sm1")

	app.downloadMylist("111")

	kept, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != "saved sm1" {
		t.Fatalf("the pass overwrote a saved track: %q", kept)
	}
	path, err := app.mylist.audioPath("sm2")
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fetched, server.want["192"]) {
		t.Fatalf("saved %d bytes; want the 192kbps band's %d", len(fetched), len(server.want["192"]))
	}
	// A pass that fetched everything it needed says which quality the
	// mylist's saved audio stands at.
	if index, err := app.mylist.ListSaved(); err != nil {
		t.Fatal(err)
	} else if at, ok := indexAt(index, "111"); !ok || index[at].AudioQuality != "192" {
		t.Fatalf("the saved quality was not recorded: %+v", index)
	}
}

// TestDownloadMylistRefetchesAnotherQuality checks that a quality change in
// the settings makes the next pass fetch the saved audio again, at the new
// band.
func TestDownloadMylistRefetchesAnotherQuality(t *testing.T) {
	withoutPauses(t)
	app, server := downloadTestApp(t)
	app.setAudioQuality(192)
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", Download: true}, testItems("sm1"))
	app.downloadMylist("111")

	// The user lowers the quality; the same pass runs again.
	app.setAudioQuality(64)
	app.downloadMylist("111")

	path, err := app.mylist.audioPath("sm1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, server.want["64"]) {
		t.Fatalf("saved %d bytes; want the 64kbps band's %d", len(got), len(server.want["64"]))
	}
	if index, err := app.mylist.ListSaved(); err != nil {
		t.Fatal(err)
	} else if at, ok := indexAt(index, "111"); !ok || index[at].AudioQuality != "64" {
		t.Fatalf("the saved quality was not updated: %+v", index)
	}
}

// TestDownloadMylistKeepsFilesAtTheSameQuality checks that a pass at the
// quality the saved audio already stands at leaves the files alone.
func TestDownloadMylistKeepsFilesAtTheSameQuality(t *testing.T) {
	withoutPauses(t)
	app, _ := downloadTestApp(t)
	app.setAudioQuality(192)
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", Download: true, AudioQuality: "192"}, testItems("sm1"))
	writeAudio(t, app, "sm1")

	app.downloadMylist("111")

	path, err := app.mylist.audioPath("sm1")
	if err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != "saved sm1" {
		t.Fatalf("the pass fetched a track already at the quality asked for: %q", kept)
	}
}

// TestTrackMylistKeepsOfflineSaving checks the user's own choices about the
// saved copy survive the index line being rewritten, as opening a mylist or
// a refresh does.
func TestTrackMylistKeepsOfflineSaving(t *testing.T) {
	app, _ := downloadTestApp(t)
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", Download: true}, testItems("sm1"))
	if err := app.mylist.SetAudioQuality("111", "192"); err != nil {
		t.Fatal(err)
	}

	index, err := app.mylist.TrackMylist(MylistPage{ID: "111", Name: "新しい名前", Items: testItems("sm1", "sm2")})
	if err != nil {
		t.Fatal(err)
	}
	at, ok := indexAt(index, "111")
	if !ok || !index[at].Download || index[at].AudioQuality != "192" {
		t.Fatalf("the refreshed line dropped the offline saving: %+v", index)
	}
}

// TestAudioStateOf pins how one track's mark is decided.
func TestAudioStateOf(t *testing.T) {
	app, _ := downloadTestApp(t)
	writeAudio(t, app, "sm1")
	app.broken["sm2"] = true

	cases := []struct {
		watchID string
		planned bool
		want    audioState
	}{
		{"sm1", false, audioSaved},
		{"sm1", true, audioSaved},
		{"sm2", true, audioBroken},
		{"sm3", true, audioPlanned},
		{"sm3", false, audioNone},
		{"sm4", false, audioNone},
	}
	for _, tc := range cases {
		if got := app.audioStateOf(tc.watchID, tc.planned); got != tc.want {
			t.Errorf("audioStateOf(%q, %v) = %v; want %v", tc.watchID, tc.planned, got, tc.want)
		}
	}
	app.audioNow = "sm4"
	if got := app.audioStateOf("sm4", true); got != audioDownloading {
		t.Errorf("the track being fetched reads as %v; want downloading", got)
	}
}

// TestTrackAudioStateIgnoresDeletedVideos checks that a video niconico no
// longer serves carries no mark: it is no save target, so neither a file this
// PC still holds nor a broken record of it shows on its row.
func TestTrackAudioStateIgnoresDeletedVideos(t *testing.T) {
	app, _ := downloadTestApp(t)
	writeAudio(t, app, "sm9")
	app.broken["sm9"] = true
	deleted := MylistItem{WatchID: "sm9"}
	// A stub from a mylist saved before the deletion was noticed: the title
	// is still there, the duration never was.
	stub := MylistItem{WatchID: "sm8", Title: "削除された動画", Available: true}

	if got := app.trackAudioState(deleted, true); got != audioNone {
		t.Fatalf("a deleted video reads as %v; want no mark", got)
	}
	if got := app.trackAudioState(stub, true); got != audioNone {
		t.Fatalf("a stub reads as %v; want no mark", got)
	}
	// A video that is still served is marked as usual.
	playable := MylistItem{WatchID: "sm1", Title: "曲", Available: true, Duration: 125}
	if got := app.trackAudioState(playable, true); got != audioPlanned {
		t.Fatalf("a playable track reads as %v; want planned", got)
	}
}

// TestCleanupAudioDropsDeletedVideos checks that the audio saved for a video
// niconico no longer serves goes with it: it is no save target any more,
// whether the mylist was fetched after the deletion or before it.
func TestCleanupAudioDropsDeletedVideos(t *testing.T) {
	app, _ := downloadTestApp(t)
	items := append(testItems("sm1"),
		MylistItem{WatchID: "sm9"},
		MylistItem{WatchID: "sm8", Title: "削除された動画", Available: true},
	)
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", Download: true}, items)
	for _, id := range []string{"sm1", "sm8", "sm9"} {
		writeAudio(t, app, id)
	}

	app.cleanupAudio()

	if !audioExists(t, app, "sm1") {
		t.Error("a playable track lost its audio")
	}
	for _, id := range []string{"sm8", "sm9"} {
		if audioExists(t, app, id) {
			t.Errorf("the audio of a deleted video stayed: %s", id)
		}
	}
}

// TestMarkAudioBrokenDropsTheFile checks what a saved track the media stack
// cannot open leaves behind: the file goes, the mark stays, and the next
// pass sees the track as missing.
func TestMarkAudioBrokenDropsTheFile(t *testing.T) {
	app, _ := downloadTestApp(t)
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", Download: true}, testItems("sm1"))
	writeAudio(t, app, "sm1")

	app.markAudioBroken("sm1")
	if audioExists(t, app, "sm1") {
		t.Fatal("the broken file is still there")
	}
	if !app.broken["sm1"] {
		t.Fatal("the track was not marked broken")
	}
	broken, err := app.mylist.LoadBroken()
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) != 1 || broken[0] != "sm1" {
		t.Fatalf("the mark was not written: %v", broken)
	}

	app.clearAudioBroken("sm1")
	if app.broken["sm1"] {
		t.Fatal("the mark did not clear")
	}
	if broken, err := app.mylist.LoadBroken(); err != nil {
		t.Fatal(err)
	} else if len(broken) != 0 {
		t.Fatalf("the mark was not cleared on disk: %v", broken)
	}
}

// TestRefreshAudioStatsCountsSaved checks the counts the cards show: the
// playable tracks of the enabled mylists, how many are saved, and how many
// are marked broken.
func TestRefreshAudioStatsCountsSaved(t *testing.T) {
	app, _ := downloadTestApp(t)
	items := append(testItems("sm1", "sm2", "sm3"), MylistItem{WatchID: "sm9", Title: "削除された動画", Available: true})
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", Download: true}, items)
	writeBody(t, app, SavedIndexEntry{ID: "222", Name: "夜のリスト"}, testItems("sm4"))
	writeAudio(t, app, "sm1")
	writeAudio(t, app, "sm4") // only the enabled mylist counts
	writeAudio(t, app, "sm9") // a deleted video is no save target
	app.broken["sm2"] = true
	if err := app.mylist.SaveBroken([]string{"sm2"}); err != nil {
		t.Fatal(err)
	}

	app.refreshAudioStats(nil)
	// The recount runs on the UI thread: take the work the worker handed over
	// as a frame build would.
	app.runHeld()

	want := map[string]audioStat{"111": {saved: 1, total: 3, broken: 1}}
	if len(app.audioStats) != 1 || app.audioStats["111"] != want["111"] {
		t.Fatalf("stats = %+v; want %+v", app.audioStats, want)
	}
	// The mark of the track no enabled mylist holds is dropped with the
	// count: sm4 is not in any of them.
	if app.broken["sm4"] {
		t.Fatal("a mark outside the enabled mylists stayed")
	}
}

// TestDownloadMylistSkipsDisabledMylist checks that a mylist the user has not
// turned offline saving on fetches nothing.
func TestDownloadMylistSkipsDisabledMylist(t *testing.T) {
	withoutPauses(t)
	app, _ := downloadTestApp(t)
	app.setAudioQuality(192)
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト"}, testItems("sm1"))

	app.downloadMylist("111")

	if audioExists(t, app, "sm1") {
		t.Fatal("a mylist without offline saving downloaded a track")
	}
}

// TestCleanupAudioDropsWhatNoEnabledMylistHolds checks the sweep: a track two
// enabled mylists hold survives one of them, a track only a disabled mylist
// holds goes, a part file a crash left goes, and files that are not saved
// audio stay.
func TestCleanupAudioDropsWhatNoEnabledMylistHolds(t *testing.T) {
	app, _ := downloadTestApp(t)
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", Download: true}, testItems("sm1", "sm2"))
	writeBody(t, app, SavedIndexEntry{ID: "222", Name: "夜のリスト", Download: true}, testItems("sm2", "sm3"))
	writeBody(t, app, SavedIndexEntry{ID: "333", Name: "保存しないリスト"}, testItems("sm4"))
	for _, id := range []string{"sm1", "sm2", "sm3", "sm4"} {
		writeAudio(t, app, id)
	}
	dir, err := app.mylist.audioDir()
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(foreign, []byte("not audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The part file a crash during a save leaves: it is not a track this PC
	// holds, and the pass that fetches the track again starts a new one.
	torn := filepath.Join(dir, "sm1"+audioExtension+audioPartExtension)
	if err := os.WriteFile(torn, []byte("half a track"), 0o644); err != nil {
		t.Fatal(err)
	}

	app.cleanupAudio()

	for _, id := range []string{"sm1", "sm2", "sm3"} {
		if !audioExists(t, app, id) {
			t.Errorf("%s was dropped although an enabled mylist holds it", id)
		}
	}
	if audioExists(t, app, "sm4") {
		t.Error("sm4 survived although no enabled mylist holds it")
	}
	if _, err := os.Stat(torn); !os.IsNotExist(err) {
		t.Errorf("a part file a crash left survived the sweep: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("the sweep removed a file that is not saved audio: %v", err)
	}
}

// TestDownloadWorkerSweepsWhenTheQueueDrains checks what the worker does
// between jobs: it saves the mylist's pass, and once nothing is left to fetch
// it sweeps the saved audio no enabled mylist holds any more.
func TestDownloadWorkerSweepsWhenTheQueueDrains(t *testing.T) {
	withoutPauses(t)
	app, _ := downloadTestApp(t)
	app.setAudioQuality(192)
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", Download: true}, testItems("sm1"))
	writeAudio(t, app, "sm9") // the audio of a track no enabled mylist holds

	app.downloads.add("111")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if audioExists(t, app, "sm1") && !audioExists(t, app, "sm9") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the worker did not save the pass and sweep the rest (sm1=%v sm9=%v)",
		audioExists(t, app, "sm1"), audioExists(t, app, "sm9"))
}

// TestCleanupAudioKeepsFilesWhenABodyIsUnreadable checks that a mylist whose
// body went missing cancels the sweep: its tracks must not be dropped on a
// guess.
func TestCleanupAudioKeepsFilesWhenABodyIsUnreadable(t *testing.T) {
	app, _ := downloadTestApp(t)
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", Download: true}, testItems("sm1"))
	writeBody(t, app, SavedIndexEntry{ID: "222", Name: "夜のリスト", Download: true}, testItems("sm2"))
	writeAudio(t, app, "sm1")
	writeAudio(t, app, "sm2")
	body, err := app.mylist.bodyPath("222")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(body); err != nil {
		t.Fatal(err)
	}

	app.cleanupAudio()

	if !audioExists(t, app, "sm2") {
		t.Fatal("a mylist whose body is gone lost its audio")
	}
}

// TestAudioDownloadsRequeuesARunningJob checks what a request made while a
// job runs does: it queues a fresh job rather than being dropped, so a
// refresh or a quality change made mid-pass is the next pass's business,
// while a request made before the job is taken is still queued only once.
func TestAudioDownloadsRequeuesARunningJob(t *testing.T) {
	q := newAudioDownloads()
	q.add("111")
	q.add("111")
	if id, ok := q.take(); !ok || id != "111" {
		t.Fatalf("take = %q, %v; want the queued mylist", id, ok)
	}
	if _, ok := q.take(); ok {
		t.Fatal("the mylist was queued twice")
	}
	q.add("111")
	if id, ok := q.take(); !ok || id != "111" {
		t.Fatalf("the request made while the pass ran was dropped: %q, %v", id, ok)
	}

	q.addStats()
	q.addStats()
	if id, ok := q.take(); !ok || id != "" {
		t.Fatalf("take = %q, %v; want the recount", id, ok)
	}
	q.addStats()
	if id, ok := q.take(); !ok || id != "" {
		t.Fatalf("the recount asked for while one ran was dropped: %q, %v", id, ok)
	}
}

// TestDownloadMylistRecordsQualityDespiteUnreachableTracks checks what a pass
// does with the mylist's quality record when a track cannot be fetched: a
// track this PC never held has nothing at the old quality to lose and is
// fetched again by its absence, so it does not hold the record back; a saved
// copy that could not be replaced does, so the pass runs again rather than
// leaving old audio behind under the new quality's name.
func TestDownloadMylistRecordsQualityDespiteUnreachableTracks(t *testing.T) {
	withoutPauses(t)
	app, server := downloadTestApp(t)
	server.broken = true
	app.setAudioQuality(192)
	writeBody(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", Download: true}, testItems("sm1"))

	app.downloadMylist("111")

	index, err := app.mylist.ListSaved()
	if err != nil {
		t.Fatal(err)
	}
	if at, ok := indexAt(index, "111"); !ok || index[at].AudioQuality != "192" {
		t.Fatalf("a track nothing was saved for held the record back: %+v", index)
	}

	// The same failure with a saved copy to replace: the record must stand
	// still, as the file at the old quality is still what this PC holds.
	writeAudio(t, app, "sm1")
	if err := app.mylist.SetAudioQuality("111", "64"); err != nil {
		t.Fatal(err)
	}
	app.downloadMylist("111")

	index, err = app.mylist.ListSaved()
	if err != nil {
		t.Fatal(err)
	}
	if at, ok := indexAt(index, "111"); !ok || index[at].AudioQuality != "64" {
		t.Fatalf("a failed replacement was recorded as the quality fetched: %+v", index)
	}
}

// TestSetDownloadWritesTheIndex checks the flag the menu turns on is kept on
// disk, and that turning it off again is written too.
func TestSetDownloadWritesTheIndex(t *testing.T) {
	mylist := &Mylist{configDir: t.TempDir()}
	if err := mylist.writeIndex([]SavedIndexEntry{{ID: "111", Name: "朝のリスト"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := mylist.SetDownload("111", true); err != nil {
		t.Fatal(err)
	}
	if index, err := mylist.ListSaved(); err != nil {
		t.Fatal(err)
	} else if at, ok := indexAt(index, "111"); !ok || !index[at].Download {
		t.Fatalf("the flag was not written: %+v", index)
	}
	if _, err := mylist.SetDownload("111", false); err != nil {
		t.Fatal(err)
	}
	if index, err := mylist.ListSaved(); err != nil {
		t.Fatal(err)
	} else if at, ok := indexAt(index, "111"); !ok || index[at].Download {
		t.Fatalf("the flag was not cleared: %+v", index)
	}
}

// TestSyncOwnKeepsDownloadingMylists checks that a mylist the account no
// longer has stays while it is saved for offline playing: the audio must not
// be dropped under the user.
func TestSyncOwnKeepsDownloadingMylists(t *testing.T) {
	mylist := &Mylist{configDir: t.TempDir()}
	if err := mylist.writeIndex([]SavedIndexEntry{
		{ID: "111", Name: "朝のリスト", Mine: true, Download: true},
		{ID: "222", Name: "夜のリスト", Mine: true},
	}); err != nil {
		t.Fatal(err)
	}
	index, err := mylist.SyncOwn(nil)
	if err != nil {
		t.Fatal(err)
	}
	if at, ok := indexAt(index, "111"); !ok || !index[at].Download {
		t.Fatalf("a downloading mylist was dropped: %+v", index)
	}
	if _, ok := indexAt(index, "222"); ok {
		t.Fatalf("a plain mylist outlived the account: %+v", index)
	}
}
