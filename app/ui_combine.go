package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// combinedDefaultName is the heading a new combined list is offered until the
// user types one.
const combinedDefaultName = "結合リスト"

// openCombineDialog shows the dialog creating a combined list, with the
// mylist the menu was opened on already chosen. The chosen set of a dialog
// that was open before starts over.
func (app *nativeApp) openCombineDialog(id string) {
	app.menuOpen = false
	app.combineSel = map[string]*bool{}
	if entry := app.savedEntry(id); entry != nil && !entry.IsCombined() && app.mylist.HasSavedBody(id) {
		*app.combineCheck(id) = true
	}
	app.combineName = combinedDefaultName
	app.combining = true
}

// combineDialog builds the dialog creating a combined list: the heading the
// list carries on the home, and the saved mylists to play as one.
func (app *nativeApp) combineDialog(c *ui.Context) {
	ui.Column(c).Width(420).Gap(14).Children(func() {
		ui.Text(c, "結合マイリストを作成").FontSize(20).Bold()
		ui.Text(c, "選んだマイリストをまたいで再生できるようになります。").FontSize(12).TextColor(app.theme.TextMuted)
		ui.Column(c).Gap(8).FillWidth().Children(func() {
			ui.Text(c, "見出し").FontSize(12).TextColor(app.theme.TextMuted)
			field := ui.TextInput(c, &app.combineName).Placeholder(combinedDefaultName).AutoFocus().
				Label("見出し").FillWidth()
			if field.Submitted() && app.combineReady() {
				app.createCombined()
			}
		})
		ui.Column(c).Gap(8).FillWidth().Children(func() {
			ui.Text(c, "マイリスト").FontSize(12).TextColor(app.theme.TextMuted)
			ui.Scroll(c).MaxHeight(300).Children(func() {
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					for _, entry := range app.combinableEntries() {
						app.combineRow(c, entry)
					}
				})
			})
		})
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if button := ui.Button(c, "キャンセル"); button.Clicked() {
				app.combining = false
			}
			if button := ui.PrimaryButton(c, "決定").Disabled(!app.combineReady()); button.Clicked() {
				app.createCombined()
			}
		})
	})
}

// combineRow is one mylist the dialog can choose. A mylist whose saved copy
// is not written yet cannot join: it shows disabled, with the reason, as a
// combined list holds whole mylists.
func (app *nativeApp) combineRow(c *ui.Context, entry SavedIndexEntry) {
	saved := app.mylist.HasSavedBody(entry.ID)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		box := ui.Checkbox(c, app.combineCheck(entry.ID), entry.Name)
		if !saved {
			box.Disabled(true)
		}
		ui.Spacer(c).Grow(1)
		if !saved {
			ui.Text(c, "保存待ち").FontSize(11).TextColor(app.theme.TextMuted)
		}
		ui.Text(c, countMeta(entry.TotalItemCount)).FontSize(11).TextColor(app.theme.TextMuted)
	})
}

// combinableEntries returns the saved mylists a combined list can hold: every
// one that is not combined itself.
func (app *nativeApp) combinableEntries() []SavedIndexEntry {
	out := make([]SavedIndexEntry, 0, len(app.saved))
	for _, entry := range app.saved {
		if !entry.IsCombined() {
			out = append(out, entry)
		}
	}
	return out
}

// combineCheck is the check box binding of a mylist in the dialog: the box
// writes through the pointer across frames, so it outlives one build.
func (app *nativeApp) combineCheck(id string) *bool {
	on := app.combineSel[id]
	if on == nil {
		on = new(bool)
		app.combineSel[id] = on
	}
	return on
}

// selectedMylists returns the chosen mylists in the order the index holds
// them, the order the dialog lists them in.
func (app *nativeApp) selectedMylists() []string {
	ids := make([]string, 0, len(app.combineSel))
	for _, entry := range app.saved {
		if on := app.combineSel[entry.ID]; on != nil && *on && !entry.IsCombined() {
			ids = append(ids, entry.ID)
		}
	}
	return ids
}

// combineReady reports whether the dialog can be decided: a heading, and at
// least one mylist chosen.
func (app *nativeApp) combineReady() bool {
	return strings.TrimSpace(app.combineName) != "" && len(app.selectedMylists()) > 0
}

// createCombined saves the combined list the dialog set up, under the heading
// it was given.
func (app *nativeApp) createCombined() {
	name := strings.TrimSpace(app.combineName)
	saved, err := app.mylist.CreateCombined(name, app.selectedMylists())
	if err != nil {
		app.setError(err)
		return
	}
	app.saved = saved
	app.combining = false
	app.setStatus("結合マイリスト「" + name + "」を作成しました。")
}

// combinedMemberNames names the mylists a combined list plays, in order.
func (app *nativeApp) combinedMemberNames(members []string) string {
	names := make([]string, 0, len(members))
	for _, id := range members {
		if entry := app.savedEntry(id); entry != nil {
			names = append(names, entry.Name)
		} else {
			names = append(names, id)
		}
	}
	return strings.Join(names, "、")
}

// openCombined shows a combined list: its members' tracks merged, resolved
// from the saved copies rather than from a body of its own.
func (app *nativeApp) openCombined(entry SavedIndexEntry) {
	items := app.mylist.CombinedItems(entry)
	app.loadToken++
	app.loading = false
	app.pendingAdvance = false
	app.playerError = ""
	app.setStatus("")
	app.adoptSavedPage(SavedMylist{
		ID:   entry.ID,
		Name: entry.Name,
		// The page counts what it shows: a member's copy can have grown
		// since the card's summary was written.
		TotalItemCount: len(items),
		Items:          items,
	})
	app.startPendingMylist(entry.ID, items)
}
