package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// homePath routes to the home; the route of a mylist is mylistPath of its ID.
const homePath = "/"

// settingsPath routes to the settings, which the logo opens.
const settingsPath = "/settings"

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
	// Mine is the mylist being one of the signed-in account's own; only a
	// mylist of somebody else's can be deleted.
	Mine bool
	// Combined is the entry being a list made on this PC over other saved
	// mylists: it has no niconico copy behind it.
	Combined bool
}

// homeView shows the home's cards: each carries the pictures of its first
// four tracks, and clicking one opens it.
func (app *nativeApp) homeView(c *ui.Context) {
	ui.Column(c).Grow(1).FillHeight().Padding(24, 24, 16).Gap(18).Children(func() {
		ui.Column(c).Gap(5).Children(func() {
			ui.Text(c, locales.NavHome()).FontSize(27).Bold()
			sub := locales.HomeSubtitle()
			if len(app.saved) == 0 {
				sub = locales.HomeSubtitleEmpty()
			}
			ui.Text(c, sub).FontSize(13).TextColor(app.theme.TextMuted)
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(18).Children(func() {
				app.homeCards(c)
			})
		})
		app.statusLine(c)
	})
}

// homeCards lays the home's cards out: the pinned mylists come first, then
// the rest of the account's own; the combined lists follow under their own
// heading, the mylists read in from a URL under theirs, and the import card
// comes last.
func (app *nativeApp) homeCards(c *ui.Context) {
	pinned, own, combined, read := app.mylistGroups()
	if main := append(pinned, own...); len(main) > 0 {
		app.homeCardsRow(c, main)
	}
	if len(combined) > 0 {
		app.homeSection(c, locales.NavCombined(), combined)
	}
	if len(read) > 0 {
		app.homeSection(c, locales.HomeReadTitle(), read)
	}
	app.importCard(c)
}

// homeCardsRow lays one group of cards out in a wrapping row.
func (app *nativeApp) homeCardsRow(c *ui.Context, entries []mylistEntry) {
	ui.Row(c).Wrap().Gap(16).FillWidth().Children(func() {
		for _, entry := range entries {
			app.mylistCard(c, entry)
		}
	})
}

// homeSection is one group of cards under its heading.
func (app *nativeApp) homeSection(c *ui.Context, title string, entries []mylistEntry) {
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		ui.Text(c, title).FontSize(15).Bold()
		app.homeCardsRow(c, entries)
	})
}

// mylistGroups returns the mylists in the one order the home and the sidebar
// share: the pinned ones — a combined list included, as a pin lifts it out of
// its category — then the account's own, the remaining combined ones, and the
// ones read in from a URL.
func (app *nativeApp) mylistGroups() (pinned, own, combined, read []mylistEntry) {
	for i := range app.saved {
		entry := savedToEntry(app.saved[i])
		switch {
		case entry.Pinned:
			pinned = append(pinned, entry)
		case entry.Mine:
			own = append(own, entry)
		case entry.Combined:
			combined = append(combined, entry)
		default:
			read = append(read, entry)
		}
	}
	return pinned, own, combined, read
}

// mylistEntries returns the mylists in the shared order, for the rail.
func (app *nativeApp) mylistEntries() []mylistEntry {
	pinned, own, combined, read := app.mylistGroups()
	entries := append(pinned, own...)
	entries = append(entries, combined...)
	return append(entries, read...)
}

// savedToEntry maps one saved index line to the card and sidebar view of it.
func savedToEntry(entry SavedIndexEntry) mylistEntry {
	return mylistEntry{
		ID: entry.ID, Name: entry.Name, Meta: savedMeta(entry),
		Items: entry.Sample, Pinned: entry.Pinned, Private: entry.Private,
		Mine: entry.Mine, Combined: entry.IsCombined(),
	}
}

// importCard shows the last card of the home: one big tile with a plus,
// opening the dialog that reads a mylist in by URL or ID.
func (app *nativeApp) importCard(c *ui.Context) {
	card := ui.Button(c, "").Label(locales.ImportTitle()).Width(mylistCardWidth).
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
			ui.Text(c, locales.ImportTitle()).FontSize(14).Bold().SingleLine().Ellipsis("…")
			ui.Text(c, locales.ImportCardHint()).FontSize(12).SingleLine().Ellipsis("…").TextColor(app.theme.TextMuted)
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
		ui.Text(c, locales.ImportTitle()).FontSize(20).Bold()
		ui.Text(c, locales.ImportBody()).FontSize(12).TextColor(app.theme.TextMuted)
		field := ui.TextInput(c, &app.input).Placeholder(locales.ImportPlaceholder()).AutoFocus().
			Label(locales.ImportFieldLabel()).FillWidth()
		if field.Submitted() && strings.TrimSpace(app.input) != "" {
			app.importInput()
		}
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if button := ui.Button(c, locales.ActionCancel()); button.Clicked() {
				app.importing = false
			}
			if button := ui.PrimaryButton(c, locales.ImportOpen()).Disabled(strings.TrimSpace(app.input) == ""); button.Clicked() {
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
				app.cardBadges(c, entry)
			})
			// Every card is named by its title, combined lists included.
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
	ui.PopoverBase(c, anchor, &app.menuOpen, func(panel ui.Element) {
		// A margin carries the pointer offset into the attach calculation,
		// which keeps the panel in the window at the edges.
		panel.AttachTo(anchor, ui.AnchorTopLeft, ui.AnchorTopLeft).Margin(app.menuY, 0, 0, app.menuX).
			Padding(4).Radius(app.theme.Radius+2).Background(app.theme.Background).
			Border(1, app.theme.Border).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18)).
			Role(ui.RoleMenu).Label(locales.MylistMenu())
		// A combined list is pinned like any other; its pin keeps only its
		// index line, as it has no body to save.
		label := locales.ActionPin()
		if entry.Pinned {
			label = locales.ActionUnpin()
		}
		item := app.menuItem(c, iconPin, label).Role(ui.RoleMenuItemCheckBox).Checked(entry.Pinned)
		if item.Clicked() {
			app.menuOpen = false
			if entry.Pinned {
				app.unpin(entry.ID)
			} else {
				app.pin(entry.ID)
			}
		}
		play := app.menuItem(c, iconPlay, locales.ActionPlay())
		if play.Clicked() {
			app.playMylist(entry, false)
		}
		shuffle := app.menuItem(c, iconShuffle, locales.ActionPlayShuffle())
		if shuffle.Clicked() {
			app.playMylist(entry, true)
		}
		if !entry.Combined {
			// A combined list cannot hold another one.
			create := app.menuItem(c, iconLayers, locales.CombineTitle())
			if create.Clicked() {
				app.openCombineDialog(entry.ID)
			}
		}
		// The account's own mylists live on niconico: only a mylist read in
		// from a URL can be deleted from this PC.
		if !entry.Mine {
			remove := app.menuItem(c, iconTrash, locales.ActionDelete())
			if remove.Clicked() {
				app.menuOpen = false
				app.askDelete(entry)
			}
		}
	})
}

// askDelete asks before a saved mylist of somebody else's is deleted from
// this PC.
func (app *nativeApp) askDelete(entry mylistEntry) {
	app.deleteEntry = entry
	app.confirmDelete = true
}

// deleteDialog asks before the saved copy is deleted; the mylist on niconico
// is never touched.
func (app *nativeApp) deleteDialog(c *ui.Context) {
	ui.Column(c).Width(360).Gap(14).Children(func() {
		ui.Text(c, locales.DeleteTitle()).FontSize(20).Bold()
		ui.Text(c, app.deleteEntry.Name).FontSize(13).SingleLine().Ellipsis("…")
		ui.Text(c, locales.DeleteBody()).FontSize(12).TextColor(app.theme.TextMuted)
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if button := ui.Button(c, locales.ActionCancel()); button.Clicked() {
				app.confirmDelete = false
			}
			if button := ui.PrimaryButton(c, locales.ActionDeleteConfirm()); button.Clicked() {
				app.confirmDelete = false
				app.deleteSaved(app.deleteEntry)
			}
		})
	})
}

// deleteSaved removes the saved copy of a mylist from this PC: its index line
// and its body.
func (app *nativeApp) deleteSaved(entry mylistEntry) {
	saved, err := app.mylist.RemoveSaved(entry.ID)
	if err != nil {
		app.setError(err)
		return
	}
	app.saved = saved
	app.setStatus(locales.StatusDeleted())
}

// countMeta labels a mylist with its size.
func countMeta(count int) string { return locales.SongCountTotal(count) }

// cardBadges marks a card's tile: the pin badge over the tile's top-right
// corner, the combined mark as a chip over its bottom-left, and the private
// mark as a chip over its bottom-right.
func (app *nativeApp) cardBadges(c *ui.Context, entry mylistEntry) {
	if entry.Pinned {
		app.pinBadge(c).Absolute().Top(4).Right(4)
	}
	if entry.Combined {
		app.cardChip(c, iconLayers, locales.BadgeCombined()).Absolute().Bottom(4).Left(4)
	}
	if entry.Private {
		app.cardChip(c, iconLock, locales.BadgePrivate()).Absolute().Bottom(4).Right(4)
	}
}

// cardChip is the small named mark a card carries over its tile.
func (app *nativeApp) cardChip(c *ui.Context, icon *ui.SVG, label string) ui.Element {
	return ui.Box(c).Radius(10).Padding(3, 7).
		Background(app.theme.Background).Border(1, app.theme.Border).
		Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icon).FontSize(11).TextColor(app.theme.Text)
				ui.Text(c, label).FontSize(11).TextColor(app.theme.Text)
			})
		})
}

// pinBadge is the pinned mark: the filled, leaning pin on its round dark
// chip, so it reads on a picture or on a selected row.
func (app *nativeApp) pinBadge(c *ui.Context) ui.Element {
	return ui.Box(c).Size(24, 24).Radius(12).Center().
		Background(app.theme.Background).Border(1, app.theme.Border).
		Children(func() {
			ui.Icon(c, iconPin).Label(locales.ActionPin()).Tooltip(locales.ActionPin()).
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

// savedMeta labels a saved card: its owner when known, and its size; a
// combined list is labelled with how many mylists it plays.
func savedMeta(entry SavedIndexEntry) string {
	total := entry.TotalItemCount
	if total == 0 {
		total = len(entry.Sample)
	}
	if entry.IsCombined() {
		return locales.CombinedMeta(len(entry.Members), countMeta(total))
	}
	if entry.Owner != "" {
		return entry.Owner + "  ·  " + countMeta(total)
	}
	return countMeta(total)
}
