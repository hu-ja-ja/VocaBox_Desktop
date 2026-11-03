package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

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
	tester.Advance(300 * time.Millisecond)
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
