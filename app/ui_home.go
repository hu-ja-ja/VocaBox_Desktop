package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// homePath routes to the home; the route of a mylist is mylistPath of its ID.
const homePath = "/"

// mylistPath is the route showing the mylist the ID names.
func mylistPath(id string) string { return "/mylist/" + id }

// mylistCardWidth is the width of one mylist card: two 16:9 tiles wide
// plus its own padding. Two cards fit even at the narrowest window with the
// sidebar open.
const mylistCardWidth = 200

// mylistEntry is one mylist the home and the sidebar show: what the sidebar
// needs (the name) and what a card needs (the label, the pictures and the
// pin and private marks).
type mylistEntry struct {
	ID    string
	Name  string
	Meta  string
	Items []MylistItem
	// Pinned is the mylist pinned to the top of the home; Private, that only
	// its owner sees it.
	Pinned  bool
	Private bool
}

// homeView shows the home's cards: each carries the pictures of its first
// four tracks, and clicking one opens it.
func (app *nativeApp) homeView(c *ui.Context) {
	ui.Column(c).Grow(1).FillHeight().Padding(24, 24, 16).Gap(18).Children(func() {
		ui.Column(c).Gap(5).Children(func() {
			ui.Text(c, "ホーム").FontSize(27).Bold()
			sub := "聴きたいマイリストを選んで再生"
			if len(app.saved) == 0 && len(app.ownMylists) == 0 {
				sub = "マイリストは「+」から読み込めます"
			}
			ui.Text(c, sub).FontSize(13).TextColor(app.theme.TextMuted)
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Row(c).Wrap().Gap(16).FillWidth().Children(func() {
				app.homeCards(c)
			})
		})
		app.statusLine(c)
	})
}

// homeCards lays the home's cards out — the shared mylist order of
// mylistEntries, then the import card — each card carrying its pictures and
// marks.
func (app *nativeApp) homeCards(c *ui.Context) {
	for _, entry := range app.mylistEntries() {
		app.mylistCard(c, entry)
	}
	app.importCard(c)
}

// mylistEntries returns the mylists to show, in the one order the home and the
// sidebar share: the pinned ones first — a pinned own mylist with the
// account's live name and size — then the account's other mylists.
func (app *nativeApp) mylistEntries() []mylistEntry {
	pinned := make(map[string]bool, len(app.saved))
	for i := range app.saved {
		pinned[app.saved[i].ID] = true
	}
	entries := make([]mylistEntry, 0, len(app.saved)+len(app.ownMylists))
	for i := range app.saved {
		entry := app.saved[i]
		e := mylistEntry{
			ID: entry.ID, Name: entry.Name, Meta: savedMeta(entry),
			Items: entry.Items, Pinned: true,
		}
		if own, ok := app.ownMylist(entry.ID); ok {
			e.Name, e.Meta = own.Name, countMeta(own.ItemCount)
			e.Items, e.Private = own.Items, !own.IsPublic
		}
		entries = append(entries, e)
	}
	for i := range app.ownMylists {
		own := app.ownMylists[i]
		if pinned[own.ID] {
			continue
		}
		entries = append(entries, mylistEntry{
			ID: own.ID, Name: own.Name, Meta: countMeta(own.ItemCount),
			Items: own.Items, Private: !own.IsPublic,
		})
	}
	return entries
}

// ownMylist returns the account's mylist with the ID, when it has one.
func (app *nativeApp) ownMylist(id string) (OwnMylist, bool) {
	for i := range app.ownMylists {
		if app.ownMylists[i].ID == id {
			return app.ownMylists[i], true
		}
	}
	return OwnMylist{}, false
}

// importCard shows the last card of the home: one big tile with a plus,
// opening the dialog that reads a mylist in by URL or ID.
func (app *nativeApp) importCard(c *ui.Context) {
	card := ui.Button(c, "").Label("マイリストを読み込む").Width(mylistCardWidth).
		Padding(12).Radius(12).Border(1, app.theme.Border).Justify(ui.Start).AlignItems(ui.Start)
	if card.Hovered() {
		card.Background(app.theme.SurfaceHover)
	} else {
		card.Background(app.theme.Background)
	}
	card.Children(func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			ui.Box(c).FillWidth().AspectRatio(1).Radius(6).Center().
				Background(app.theme.SurfaceHover).Border(1, app.theme.Border).Children(func() {
				ui.Icon(c, iconPlus).FontSize(32).TextColor(app.theme.TextMuted)
			})
			ui.Text(c, "マイリストを読み込む").FontSize(14).Bold().SingleLine().Ellipsis("…")
			ui.Text(c, "URLまたはIDから").FontSize(12).SingleLine().Ellipsis("…").TextColor(app.theme.TextMuted)
		})
	})
	if card.Clicked() {
		app.importing = true
	}
}

// importDialog builds the dialog reading a mylist in: a field for its URL
// or ID, and the buttons opening or dismissing it.
func (app *nativeApp) importDialog(c *ui.Context) {
	ui.Column(c).Width(380).Gap(14).Children(func() {
		ui.Text(c, "マイリストを読み込む").FontSize(20).Bold()
		ui.Text(c, "ニコニコ動画のマイリストのURLまたはIDを入力してください。").FontSize(12).TextColor(app.theme.TextMuted)
		field := ui.TextInput(c, &app.input).Placeholder("URL または ID").AutoFocus().
			Label("マイリストのURLまたはID").FillWidth()
		if field.Submitted() && strings.TrimSpace(app.input) != "" {
			app.importInput()
		}
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if button := ui.Button(c, "キャンセル"); button.Clicked() {
				app.importing = false
			}
			if button := ui.PrimaryButton(c, "開く").Disabled(strings.TrimSpace(app.input) == ""); button.Clicked() {
				app.importInput()
			}
		})
	})
}

// importInput opens the typed mylist and leaves the dialog; a load already
// running keeps the dialog open.
func (app *nativeApp) importInput() {
	if app.loading {
		return
	}
	app.importing = false
	app.queueOpen = false
	app.fetchMylist(strings.TrimSpace(app.input))
}

// mylistCard shows one mylist — the account's own or a pinned snapshot — as a
// card: its first four track pictures in a 2×2 tile, its name and its meta
// line, its pin and lock marks. Clicking the card opens the mylist; the
// right-click menu pins and unpins it.
func (app *nativeApp) mylistCard(c *ui.Context, entry mylistEntry) {
	card := ui.Button(c, "").Label(entry.Name).Width(mylistCardWidth).
		Padding(12).Radius(12).Border(1, app.theme.Border).Justify(ui.Start).AlignItems(ui.Start)
	if card.Hovered() {
		card.Background(app.theme.SurfaceHover)
	} else {
		card.Background(app.theme.Background)
	}
	card.Children(func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			ui.Box(c).FillWidth().Children(func() {
				app.cardTiles(c, entry.Items)
				app.cardBadges(c, entry.Pinned, entry.Private)
			})
			ui.Text(c, entry.Name).FontSize(14).Bold().SingleLine().Ellipsis("…")
			ui.Text(c, entry.Meta).FontSize(12).SingleLine().Ellipsis("…").TextColor(app.theme.TextMuted)
		})
	})
	if card.Clicked() {
		app.menuOpen = false
		app.queueOpen = false
		app.loadingID = ""
		app.router.Push(mylistPath(entry.ID))
	}
	// The keyboard opens the same menu with the menu key or Shift+F10;
	// both ask every frame, so either one stays registered.
	f10 := card.Shortcut(ui.Shift, ui.KeyF10)
	menuKey := card.Shortcut(0, ui.KeyContextMenu)
	if card.RightClicked() || f10 || menuKey {
		app.openMylistMenu(card, entry)
	}
	app.mylistMenu(c, card, entry)
}

// openMylistMenu opens the pin menu of a mylist at the pointer, or below the
// element the keyboard asked on, which the pointer may be away from.
func (app *nativeApp) openMylistMenu(anchor ui.Element, entry mylistEntry) {
	x, y, over := anchor.PointerPosition()
	if !over {
		x, y = 0, anchor.Bounds().H
	}
	app.menuOpen, app.menuEntry = true, entry
	app.menuX, app.menuY = x, y
	app.menuAnchorID = anchor.ID()
}

// mylistMenu shows the pin menu of a mylist on its anchor while it is the one
// the menu belongs to: one item that pins or unpins the mylist. The panel
// opens where the right-click was, clamped into the window, rather than the
// system's own menu, so it carries the app's look.
func (app *nativeApp) mylistMenu(c *ui.Context, anchor ui.Element, entry mylistEntry) {
	// The ID names the element, which a refresh may give another mylist.
	if !app.menuOpen || app.menuAnchorID != anchor.ID() || app.menuEntry.ID != entry.ID {
		return
	}
	app.menuBuilt = true
	label := "ピン留め"
	if entry.Pinned {
		label = "ピン留めを外す"
	}
	ui.PopoverBase(c, anchor, &app.menuOpen, func(panel ui.Element) {
		// A margin carries the pointer offset into the attach calculation,
		// which keeps the panel in the window at the edges.
		panel.AttachTo(anchor, ui.AnchorTopLeft, ui.AnchorTopLeft).Margin(app.menuY, 0, 0, app.menuX).
			Padding(4).Radius(app.theme.Radius+2).Background(app.theme.Background).
			Border(1, app.theme.Border).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18)).
			Role(ui.RoleMenu).Label("マイリストのメニュー")
		item := app.menuItem(c, iconPin, label).Role(ui.RoleMenuItemCheckBox).Checked(entry.Pinned)
		if item.Clicked() {
			app.menuOpen = false
			if entry.Pinned {
				app.unpin(entry.ID)
			} else {
				app.saveEntry(entry.ID, entry.Name)
			}
		}
		play := app.menuItem(c, iconPlay, "再生")
		if play.Clicked() {
			app.playMylist(entry, false)
		}
		shuffle := app.menuItem(c, iconShuffle, "シャッフル再生")
		if shuffle.Clicked() {
			app.playMylist(entry, true)
		}
	})
}

// countMeta labels a mylist with its size.
func countMeta(count int) string { return fmt.Sprintf("全%d曲", count) }

// cardBadges marks a card's tile: the pin badge over the tile's top-right
// corner, and the private mark as a chip over its bottom-right.
func (app *nativeApp) cardBadges(c *ui.Context, pinned, private bool) {
	if pinned {
		app.pinBadge(c).Absolute().Top(4).Right(4)
	}
	if private {
		ui.Box(c).Absolute().Bottom(4).Right(4).Radius(10).Padding(3, 7).
			Background(app.theme.Background).Border(1, app.theme.Border).
			Children(func() {
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconLock).FontSize(11).TextColor(app.theme.Text)
					ui.Text(c, "非公開").FontSize(11).TextColor(app.theme.Text)
				})
			})
	}
}

// pinBadge is the pinned mark: the filled, leaning pin on its round dark
// chip, so it reads on a picture or on a selected row.
func (app *nativeApp) pinBadge(c *ui.Context) ui.Element {
	return ui.Box(c).Size(24, 24).Radius(12).Center().
		Background(app.theme.Background).Border(1, app.theme.Border).
		Children(func() {
			ui.Icon(c, iconPin).Label("ピン留め").Tooltip("ピン留め").
				FontSize(14).TextColor(ui.Hex("#39c5bb")).Rotate(45)
		})
}

// cardTiles lays a mylist's first four track pictures out as a 2×2 tile,
// keeping the corners filled with placeholders where a track or its
// picture is missing.
func (app *nativeApp) cardTiles(c *ui.Context, items []MylistItem) {
	ui.Grid(c).Columns(2).Gap(4).Children(func() {
		for i := 0; i < 4; i++ {
			if i < len(items) {
				app.cardTile(c, items[i])
			} else {
				ui.Box(c).AspectRatio(1).Radius(6).Background(app.theme.SurfaceHover)
			}
		}
	})
}

// cardTile shows one track's picture as a square tile, a placeholder while
// it loads or where the track has none.
func (app *nativeApp) cardTile(c *ui.Context, item MylistItem) {
	bitmap := app.thumbs[item.WatchID]
	if bitmap == nil && item.Available {
		app.requestThumbnail(item)
	}
	if bitmap != nil {
		ui.Image(c, bitmap).AspectRatio(1).Fit(ui.Cover).Radius(6)
		return
	}
	ui.Box(c).AspectRatio(1).Radius(6).Background(app.theme.SurfaceHover)
}

// savedMeta labels a pinned card: its owner when known, and its size.
func savedMeta(entry SavedMylist) string {
	total := entry.TotalItemCount
	if total == 0 {
		total = len(entry.Items)
	}
	if entry.Owner != "" {
		return entry.Owner + "  ·  " + countMeta(total)
	}
	return countMeta(total)
}
