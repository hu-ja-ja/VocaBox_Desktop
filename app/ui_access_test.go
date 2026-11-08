package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// accessOf returns what assistive technology sees of the element of role r
// named name, failing the test without one.
func accessOf(t *testing.T, tester *ui.Tester, role ui.Role, name string) ui.AccessInfo {
	t.Helper()
	a, ok := tester.AccessByRole(role, name)
	if !ok {
		t.Fatalf("no element of role %d named %q", role, name)
	}
	return a
}

// TestNativeUIAccessPlayerBar checks the player bar as a screen reader reads
// it: the seek and volume bars' ranges and values, the transport's state, and
// the toggles' pressed states.
func TestNativeUIAccessPlayerBar(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.player = playerState{
		WatchID: "sm1", Title: "Aの曲", Uploader: "投稿者", Ready: true, Playing: true,
		PositionMS: 30_000, DurationMS: 120_000, Volume: 0.6,
	}
	tester := ui.NewTester(app.view, 1120, 760)

	if seek := accessOf(t, tester, ui.RoleSlider, locales.PlayerSeek()); seek.Min != 0 || seek.Max != 120_000 || seek.Now != 30_000 || seek.Value != "0:30" || seek.Disabled {
		t.Fatalf("the seek bar is %+v", seek)
	}
	if volume := accessOf(t, tester, ui.RoleSlider, locales.PlayerVolume()); volume.Now != 0.6 || volume.Value != "60%" {
		t.Fatalf("the volume bar is %+v", volume)
	}
	accessOf(t, tester, ui.RoleButton, locales.ActionPause())

	if err := tester.Click(locales.PlayerShuffle()); err != nil {
		t.Fatal(err)
	}
	if a := accessOf(t, tester, ui.RoleToggleButton, locales.PlayerShuffle()); !a.Checked {
		t.Fatalf("shuffle is not pressed: %+v", a)
	}
	if err := tester.Click(locales.PlayerRepeat()); err != nil {
		t.Fatal(err)
	}
	if a := accessOf(t, tester, ui.RoleToggleButton, locales.PlayerRepeat()); !a.Checked {
		t.Fatalf("repeat is not pressed: %+v", a)
	}

	if err := tester.Click(locales.PlayerMute()); err != nil {
		t.Fatal(err)
	}
	if a := accessOf(t, tester, ui.RoleSlider, locales.PlayerVolume()); a.Now != 0 || a.Value != "0%" {
		t.Fatalf("the muted volume bar is %+v", a)
	}
	accessOf(t, tester, ui.RoleButton, locales.PlayerUnmute())
}

// TestNativeUIAccessPlayerBarEmpty checks the controls a screen reader must
// find disabled while nothing plays.
func TestNativeUIAccessPlayerBarEmpty(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)
	for _, name := range []string{locales.ActionPlay(), locales.PlayerPrevious(), locales.PlayerNext(), locales.LyricsOpen()} {
		a := accessOf(t, tester, ui.RoleButton, name)
		if !a.Disabled || a.Focusable {
			t.Fatalf("%q is %+v", name, a)
		}
	}

	app.playerError = "再生に失敗しました"
	tester.Frame()
	accessOf(t, tester, ui.RoleAlert, app.playerError)
}

// TestNativeUIAccessSidebar checks the sidebar as a tree: the chosen
// destination, and the toggle's expanded state.
func TestNativeUIAccessSidebar(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", TotalItemCount: 1},
		[]MylistItem{{WatchID: "sm1", Title: "朝1", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)

	if a := accessOf(t, tester, ui.RoleTreeItem, locales.NavHome()); !a.Selected {
		t.Fatalf("home is not the chosen item: %+v", a)
	}
	if err := tester.Click("朝のリスト"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if a := accessOf(t, tester, ui.RoleTreeItem, "朝のリスト"); !a.Selected {
		t.Fatalf("the mylist is not the chosen item: %+v", a)
	}
	if a := accessOf(t, tester, ui.RoleTreeItem, locales.NavHome()); a.Selected {
		t.Fatalf("home is still chosen: %+v", a)
	}

	if a := accessOf(t, tester, ui.RoleButton, locales.SidebarClose()); !a.Expanded {
		t.Fatalf("the open sidebar is not expanded: %+v", a)
	}
	if err := tester.Click(locales.SidebarClose()); err != nil {
		t.Fatal(err)
	}
	tester.Advance(300 * time.Millisecond)
	if a := accessOf(t, tester, ui.RoleButton, locales.SidebarOpen()); a.Expanded {
		t.Fatalf("the closed sidebar is expanded: %+v", a)
	}
}

// TestNativeUIAccessSettings checks the settings controls as a screen reader
// reads them: the quality choice's value and popup, and the Discord switch.
func TestNativeUIAccessSettings(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click(locales.NavSettings()); err != nil {
		t.Fatal(err)
	}
	if h := accessOf(t, tester, ui.RoleHeading, locales.NavSettings()); h.Level != 1 {
		t.Fatalf("the settings heading is %+v", h)
	}

	popup := accessOf(t, tester, ui.RolePopUpButton, locales.SettingsQualityTitle())
	if popup.Value != audioQualityLabel(320) || popup.HasPopup != ui.PopupListBox || popup.Expanded {
		t.Fatalf("the quality choice is %+v", popup)
	}
	if err := tester.Click(audioQualityLabel(320)); err != nil {
		t.Fatal(err)
	}
	if popup = accessOf(t, tester, ui.RolePopUpButton, locales.SettingsQualityTitle()); !popup.Expanded {
		t.Fatalf("the open quality choice is %+v", popup)
	}
	if option := accessOf(t, tester, ui.RoleOption, audioQualityLabel(320)); !option.Selected {
		t.Fatalf("the shown quality is not chosen: %+v", option)
	}
	if err := tester.Click(audioQualityLabel(64)); err != nil {
		t.Fatal(err)
	}
	if popup = accessOf(t, tester, ui.RolePopUpButton, locales.SettingsQualityTitle()); popup.Expanded || popup.Value != audioQualityLabel(64) {
		t.Fatalf("the chosen quality is %+v", popup)
	}

	if a := accessOf(t, tester, ui.RoleSwitch, locales.SettingsDiscordTitle()); a.Checked {
		t.Fatalf("Discord Rich Presence starts on: %+v", a)
	}
}

// TestNativeUIAccessDialogs checks the dialogs name themselves and take the
// focus: the import field, the combine check boxes, and the cancel of a
// destructive question.
func TestNativeUIAccessDialogs(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click(locales.ImportTitle()); err != nil {
		t.Fatal(err)
	}
	accessOf(t, tester, ui.RoleDialog, locales.ImportTitle())
	field := accessOf(t, tester, ui.RoleTextField, locales.ImportFieldLabel())
	if !field.Focused || field.Placeholder != locales.ImportPlaceholder() {
		t.Fatalf("the import field is %+v", field)
	}
	if a := accessOf(t, tester, ui.RoleButton, locales.ImportOpen()); !a.Disabled {
		t.Fatalf("the empty import opens: %+v", a)
	}
	tester.Type("27411728")
	if field = accessOf(t, tester, ui.RoleTextField, locales.ImportFieldLabel()); field.Value != "27411728" {
		t.Fatalf("the typed import field is %+v", field)
	}
	if a := accessOf(t, tester, ui.RoleButton, locales.ImportOpen()); a.Disabled {
		t.Fatalf("the filled import stays disabled: %+v", a)
	}
	if err := tester.Click(locales.ActionCancel()); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.AccessByRole(ui.RoleDialog, locales.ImportTitle()); ok {
		t.Fatal("the import dialog stayed")
	}
}

// TestNativeUIAccessCombineDialog checks the combine dialog's check boxes and
// the decision they enable, which a screen reader sees as states.
func TestNativeUIAccessCombineDialog(t *testing.T) {
	app := savedApp(t)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", TotalItemCount: 1},
		[]MylistItem{{WatchID: "sm1", Title: "朝1", Available: true, Duration: 120}})
	indexSaved(t, app, SavedIndexEntry{ID: "333", Name: "保存中のリスト", TotalItemCount: 5}, nil)
	app.openCombineDialog("333")
	tester := ui.NewTester(app.view, 1120, 760)

	accessOf(t, tester, ui.RoleDialog, locales.CombineTitle())
	if field := accessOf(t, tester, ui.RoleTextField, locales.CombineNameLabel()); !field.Focused || field.Value != locales.CombineDefaultName() {
		t.Fatalf("the combine name is %+v", field)
	}
	if box := accessOf(t, tester, ui.RoleCheckBox, "保存中のリスト"); !box.Disabled {
		t.Fatalf("a mylist still saving can be chosen: %+v", box)
	}
	if a := accessOf(t, tester, ui.RoleButton, locales.ActionCreate()); !a.Disabled {
		t.Fatalf("the empty combine decides: %+v", a)
	}
	box := accessOf(t, tester, ui.RoleCheckBox, "朝のリスト")
	tester.ClickAt(box.Bounds.X+box.Bounds.W/2, box.Bounds.Y+box.Bounds.H/2)
	if box = accessOf(t, tester, ui.RoleCheckBox, "朝のリスト"); !box.Checked {
		t.Fatalf("the chosen mylist is %+v", box)
	}
	if a := accessOf(t, tester, ui.RoleButton, locales.ActionCreate()); a.Disabled {
		t.Fatalf("the ready combine stays disabled: %+v", a)
	}
}

// TestNativeUIAccessDeleteDialog checks a destructive question's name and
// the focus on its cancel, rather than on the deletion.
func TestNativeUIAccessDeleteDialog(t *testing.T) {
	app := savedApp(t)
	app.sidebarOpen = false
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "削除のテスト", TotalItemCount: 1},
		[]MylistItem{{WatchID: "sm1", Title: "Aの曲", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.RightClick("削除のテスト"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click(locales.ActionDelete()); err != nil {
		t.Fatal(err)
	}
	accessOf(t, tester, ui.RoleDialog, locales.DeleteTitle())
	if a := accessOf(t, tester, ui.RoleButton, locales.ActionCancel()); !a.Focused {
		t.Fatalf("the cancel does not take the focus: %+v", a)
	}
}

// TestNativeUIAccessMenus checks the menus as menus: their names, their items,
// and the check box items' states.
func TestNativeUIAccessMenus(t *testing.T) {
	app := savedApp(t)
	app.sidebarOpen = false
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "ピンのテスト", TotalItemCount: 1, Pinned: true},
		[]MylistItem{{WatchID: "sm1", Title: "Aの曲", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)

	if err := tester.RightClick("ピンのテスト"); err != nil {
		t.Fatal(err)
	}
	accessOf(t, tester, ui.RoleMenu, locales.MylistMenu())
	if a := accessOf(t, tester, ui.RoleMenuItemCheckBox, locales.ActionUnpin()); !a.Checked {
		t.Fatalf("the pin item is %+v", a)
	}
	accessOf(t, tester, ui.RoleMenuItem, locales.ActionPlay())
	if err := tester.Click(locales.ActionUnpin()); err != nil {
		t.Fatal(err)
	}
	tester.Frame()

	if err := tester.RightClick("ピンのテスト"); err != nil {
		t.Fatal(err)
	}
	if a := accessOf(t, tester, ui.RoleMenuItemCheckBox, locales.ActionPin()); a.Checked {
		t.Fatalf("the unpinned item is %+v", a)
	}
}

// TestNativeUIAccessQueue checks the queue's button state, heading and rows,
// which the list reports with their places.
func TestNativeUIAccessQueue(t *testing.T) {
	app := newNativeApp(NewMylist())
	app.queueOpen = true
	app.player = playerState{
		Name: "作業用キュー", WatchID: "sm2",
		Order: []string{"sm1", "sm2"},
		Items: []MylistItem{
			{WatchID: "sm1", Title: "一曲目", Available: true, Duration: 120},
			{WatchID: "sm2", Title: "二曲目", Available: true, Duration: 120},
		},
	}
	tester := ui.NewTester(app.view, 1120, 760)

	queue := accessOf(t, tester, ui.RoleButton, locales.PlayerQueue())
	if !queue.Expanded {
		t.Fatalf("the open queue button is %+v", queue)
	}
	if h := accessOf(t, tester, ui.RoleHeading, locales.PlayerQueue()); h.Level != 1 {
		t.Fatalf("the queue heading is %+v", h)
	}
	if row := accessOf(t, tester, ui.RoleListItem, "二曲目"); row.PosInSet != 2 || row.SetSize != 2 {
		t.Fatalf("the second row is %+v", row)
	}

	tester.ClickAt(queue.Bounds.X+queue.Bounds.W/2, queue.Bounds.Y+queue.Bounds.H/2)
	tester.Advance(300 * time.Millisecond)
	if queue = accessOf(t, tester, ui.RoleButton, locales.PlayerQueue()); queue.Expanded {
		t.Fatalf("the closed queue button is %+v", queue)
	}
}

// TestNativeUIAccessHome checks the home's heading, the states a card carries
// beyond its name, and the status line.
func TestNativeUIAccessHome(t *testing.T) {
	app := savedApp(t)
	app.sidebarOpen = false
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト", TotalItemCount: 2, Pinned: true, Private: true},
		[]MylistItem{{WatchID: "sm1", Title: "朝1", Available: true, Duration: 120}})
	tester := ui.NewTester(app.view, 1120, 760)

	if h := accessOf(t, tester, ui.RoleHeading, locales.NavHome()); h.Level != 1 {
		t.Fatalf("the home heading is %+v", h)
	}
	card := accessOf(t, tester, ui.RoleButton, "朝のリスト")
	for _, want := range []string{countMeta(2), locales.ActionPin(), locales.BadgePrivate()} {
		if !strings.Contains(card.Description, want) {
			t.Fatalf("the card's description %q does not say %q", card.Description, want)
		}
	}
	app.setStatus(locales.StatusDeleted())
	tester.Frame()
	accessOf(t, tester, ui.RoleStatus, locales.StatusDeleted())
}
