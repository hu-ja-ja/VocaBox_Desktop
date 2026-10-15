package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// content builds the page the router shows: the home, or a mylist.
func (app *nativeApp) content(c *ui.Context) {
	app.router.View(c, func(r *ui.Route) {
		switch {
		case r.Match(homePath):
			r.Title("ホーム")
			app.homeView(c)
		case r.Match("/mylist/{id}"):
			app.mylistView(c, r)
		default:
			app.homeView(c)
		}
	})
}

// mylistView fills the content area with the mylist its route names, loading it
// when it is not the one loaded; the loaded mylist stays as pages change, so
// only the page shown asks for one.
func (app *nativeApp) mylistView(c *ui.Context, r *ui.Route) {
	id := r.Param("id")
	app.openMylist(id)
	if app.page == nil || app.page.ID != id {
		r.Title("読み込み中")
		ui.Column(c).Grow(1).FillHeight().Padding(24, 24, 16).Children(func() {
			if app.status == "" {
				ui.Text(c, "マイリストを読み込んでいます…").FontSize(13).TextColor(app.theme.TextMuted)
			} else {
				app.statusLine(c)
			}
		})
		return
	}
	r.Title(app.page.Name)
	app.mylistPage(c)
}

// mylistPage fills the content area with the loaded mylist.
func (app *nativeApp) mylistPage(c *ui.Context) {
	ui.Column(c).Grow(1).FillHeight().Padding(24, 24, 16).Gap(14).Children(func() {
		app.pumpThumbnailFill()
		app.mylistHeader(c)
		ui.Row(c).Gap(14).Justify(ui.Start).Padding(10, 12).
			BorderWidth(0, 0, 1, 0).BorderColor(app.theme.Border).Children(func() {
			ui.Text(c, "#").Width(28).FontSize(12).TextColor(app.theme.TextMuted)
			ui.Box(c).Width(thumbSlotWidth(thumbRowHeight, 0)).Shrink(0)
			ui.Text(c, "曲名 / 投稿者").FontSize(12).TextColor(app.theme.TextMuted)
			ui.Spacer(c).Grow(1)
			ui.Text(c, "時間").Width(54).FontSize(12).TextAlign(ui.End).TextColor(app.theme.TextMuted)
		})
		app.trackRows(c)
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			app.statusLine(c)
			ui.Spacer(c).Grow(1)
			if app.hasMore {
				button := ui.Button(c, "続きを読み込む").Disabled(app.loading)
				if button.Clicked() {
					app.loadMore()
				}
			}
		})
	})
}

func (app *nativeApp) mylistHeader(c *ui.Context) {
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(5).Children(func() {
			ui.Text(c, app.page.Name).FontSize(27).Bold().SingleLine().Ellipsis("…")
			meta := countMeta(app.page.TotalItemCount)
			if app.page.Owner != "" {
				meta = app.page.Owner + "  ·  " + meta
			}
			ui.Text(c, meta).FontSize(13).TextColor(app.theme.TextMuted)
			if entry := app.currentSaved(); entry != nil && entry.IsCombined() {
				ui.Text(c, "結合元: "+app.combinedMemberNames(entry.Members)).FontSize(12).
					MaxLines(2).TextColor(app.theme.TextMuted)
			}
			if app.page.Description != "" {
				ui.Text(c, app.page.Description).FontSize(12).MaxLines(2).TextColor(app.theme.TextMuted)
			}
		})

		if entry := app.currentSaved(); entry != nil {
			// A combined list lives on this PC alone: there is no niconico
			// copy for a refresh, and its tracks come from its members'
			// saved copies.
			if !entry.IsCombined() {
				if button := ui.Button(c, "更新").Disabled(app.saving || app.loading); button.Clicked() {
					app.refreshSaved(*entry)
				}
			}
			if entry.Pinned {
				if button := ui.Button(c, "ピン留めを外す").Disabled(app.saving); button.Clicked() {
					app.unpin(entry.ID)
				}
			} else if button := ui.PrimaryButton(c, "ピン留め").Disabled(app.saving); button.Clicked() {
				app.pin(entry.ID)
			}
			if !entry.Mine {
				if button := ui.Button(c, "削除").Disabled(app.saving); button.Clicked() {
					app.askDelete(savedToEntry(*entry))
				}
			}
		} else if button := ui.PrimaryButton(c, "ピン留め").Disabled(app.saving || app.loading); button.Clicked() {
			app.saveCurrent()
		}
	})
}

func (app *nativeApp) trackRows(c *ui.Context) {
	app.trackList.Key = func(row int) any { return app.page.Items[row].WatchID }
	app.trackList.Label = func(row int) string { return trackLabel(app.page.Items[row]) }
	list := ui.List(c, &app.trackList, len(app.page.Items), func(i int) {
		item := app.page.Items[i]
		playable := isPlayable(item)
		label := trackLabel(item)
		// The mylist carries no playback state: the player's own screens show
		// what plays, and a row here only reacts to the pointer.
		row := ui.Button(c, "").Label(label).FillWidth().Padding(8, 12).Radius(8).
			BorderWidth(0, 0, 1, 0).BorderColor(app.theme.Border).Justify(ui.Start).AlignItems(ui.Start)
		if row.Hovered() {
			row.Background(app.theme.SurfaceHover)
		} else {
			row.Background(app.theme.Surface)
		}
		row.Disabled(!playable)
		row.Children(func() {
			ui.Row(c).Gap(14).AlignItems(ui.Center).Justify(ui.Start).FillWidth().Children(func() {
				ui.Text(c, fmt.Sprintf("%02d", i+1)).Width(28).FontSize(12).TextColor(app.theme.TextMuted)
				app.thumbnail(c, item, thumbRowHeight)
				ui.Column(c).Gap(3).Justify(ui.Start).AlignItems(ui.Start).Children(func() {
					ui.Text(c, label).FontSize(14).Bold().SingleLine().Ellipsis("…")
					if playable && item.Uploader != "" {
						ui.Text(c, item.Uploader).FontSize(12).SingleLine().Ellipsis("…").TextColor(app.theme.TextMuted)
					}
				})
				ui.Spacer(c).Grow(1)
				if item.Duration > 0 {
					ui.Text(c, formatDuration(item.Duration)).Width(54).FontSize(12).TextAlign(ui.End).TextColor(app.theme.TextMuted)
				}
			})
		})
		if row.Clicked() {
			app.selectTrack(item)
			// Playing from the mylist hands the view to the queue screen,
			// which follows the track as it plays.
			app.queueOpen = true
		}
	}).Grow(1)
	if len(app.page.Items) == 0 {
		list.Children(func() {
			ui.Text(c, "このマイリストに曲はありません").Padding(20).TextColor(app.theme.TextMuted)
		})
	}
}

// queueView is the screen the player bar opens: the queue as it plays, with
// the track playing kept in sight. The player itself has no picture: the
// AmsHelper plays audio on its own.
func (app *nativeApp) queueView(c *ui.Context) {
	ui.Row(c).Grow(1).FillHeight().AlignItems(ui.Stretch).Children(func() {
		app.queuePanel(c)
	})
}

// queuePanel lists the tracks in the order they play, the track playing
// first under shuffle, labelled with the mylist the queue came from and its
// size.
func (app *nativeApp) queuePanel(c *ui.Context) {
	ui.Column(c).Grow(1).FillHeight().Gap(4).Padding(18, 26, 12).Children(func() {
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, "再生キュー").FontSize(21).Bold().SingleLine().Ellipsis("…")
			ui.Text(c, app.queueMeta()).FontSize(12).TextColor(app.theme.TextMuted)
		})
		app.queueRows(c)
	})
}

// queueMeta labels the queue: the mylist it came from, its size, and the
// shuffle.
func (app *nativeApp) queueMeta() string {
	parts := make([]string, 0, 3)
	if app.player.Name != "" {
		parts = append(parts, app.player.Name)
	}
	parts = append(parts, fmt.Sprintf("%d曲", len(app.player.Order)))
	if app.player.Shuffle {
		parts = append(parts, "シャッフル")
	}
	return strings.Join(parts, "  ·  ")
}

// queueRows lists the tracks of the report's order, which is the queue as
// it plays, and scrolls the track playing to the top of the queue as it
// moves on, so the queue reads in the order it plays.
func (app *nativeApp) queueRows(c *ui.Context) {
	rows := app.queueItems()
	if len(rows) == 0 {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, "キューは空です").FontSize(13).TextColor(app.theme.TextMuted)
		})
		return
	}
	app.followQueue(len(rows))
	// The list chooses a clicked row into queueChoice, which plays it
	// before the next frame: a list whose rows are not chosen at all does
	// not take the pointer, and a reorder's drag needs that.
	app.queueList.Selected = &app.queueChoice
	app.queueList.Key = func(row int) any { return rows[row].WatchID }
	app.queueList.Label = func(row int) string { return trackLabel(rows[row]) }
	app.queueList.Reorder = app.moveQueueRows
	ui.List(c, &app.queueList, len(rows), func(i int) {
		app.queueRow(c, rows[i], i)
	}).Grow(1)
	if app.queueChoice >= 0 && app.queueChoice < len(rows) {
		item := rows[app.queueChoice]
		app.queueChoice = -1
		app.selectTrack(item)
	}
}

// queueRow builds one track of the queue: its number, picture, title and
// uploader, the menu on hover, and the track playing in the pressed
// surface, with its number replaced by the play mark in the accent. Rows
// take the pointer so a click chooses one and a drag reaches the list's
// reordering.
func (app *nativeApp) queueRow(c *ui.Context, item MylistItem, i int) {
	playable := isPlayable(item)
	playing := item.WatchID != "" && item.WatchID == app.player.WatchID
	row := ui.Box(c).FillWidth().Padding(8, 12).Justify(ui.Start).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(app.theme.Border)
	hovered := row.Hovered()
	switch {
	case playing:
		row.Background(app.theme.SurfacePressed)
	case hovered:
		row.Background(app.theme.SurfaceHover)
	}
	row.Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Justify(ui.Start).FillWidth().Children(func() {
			// The track playing shows the play mark in place of its number,
			// in the accent kept for playback.
			if playing {
				ui.Box(c).Width(22).Center().Children(func() {
					ui.Icon(c, iconPlay).FontSize(12).TextColor(app.theme.Accent)
				})
			} else {
				ui.Text(c, fmt.Sprintf("%02d", i+1)).Width(22).FontSize(12).TextColor(app.theme.TextMuted)
			}
			app.thumbnail(c, item, thumbRowHeight)
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(3).Children(func() {
				title := ui.Text(c, trackLabel(item)).FontSize(13).Bold().SingleLine().Ellipsis("…")
				if playing {
					title.TextColor(app.theme.Accent)
				}
				if playable && item.Uploader != "" {
					ui.Text(c, item.Uploader).FontSize(12).TextColor(app.theme.TextMuted).SingleLine().Ellipsis("…")
				}
			})
			ui.Spacer(c).Grow(1)
			if item.Duration > 0 {
				ui.Text(c, formatDuration(item.Duration)).Width(46).FontSize(12).TextAlign(ui.End).TextColor(app.theme.TextMuted)
			}
			app.queueMenu(c, item, i, hovered)
		})
	})
}

// followQueue scrolls the track playing to the top of the queue as the
// player moves on, one follow per track or row: a queue the user is reading
// stays where they left it.
func (app *nativeApp) followQueue(n int) {
	idx := app.player.QueueIndex
	if idx < 0 || idx >= n || (idx == app.queueFollow && app.player.WatchID == app.queueFollowTrack) {
		return
	}
	app.queueFollow = idx
	app.queueFollowTrack = app.player.WatchID
	app.queueList.ScrollTo(idx, ui.Start)
}

// queueMenu shows the menu button of a row with the menu it opens: only on
// the row the pointer is over, as the three dots a queue row carries.
func (app *nativeApp) queueMenu(c *ui.Context, item MylistItem, i int, hovered bool) {
	button := ui.Button(c, "").Label("曲のメニュー").Tooltip("曲のメニュー").Padding(4, 7).Radius(6)
	if !hovered {
		button.Invisible()
	}
	if button.Clicked() {
		if app.queueMenuOpen && app.queueMenuRow == i {
			app.queueMenuOpen = false
		} else {
			app.queueMenuRow = i
			app.queueMenuOpen = true
		}
	}
	button.Children(func() {
		ui.Icon(c, iconMore).FontSize(16)
	})
	if app.queueMenuRow != i {
		return
	}
	ui.Popover(c, button, &app.queueMenuOpen, func() {
		remove := app.menuItem(c, iconTrash, "キューから削除")
		if remove.Clicked() {
			app.removeFromQueue(item)
		}
	})
}

// removeFromQueue takes the track out of the playing queue.
func (app *nativeApp) removeFromQueue(item MylistItem) {
	app.queueMenuRow = -1
	app.sendPlayer(PlayerCommand{Action: "remove", WatchID: item.WatchID})
}

// moveQueueRows places the rows a drag moved before row to, in the playing
// order the queue screen shows.
func (app *nativeApp) moveQueueRows(rows []int, to int) {
	app.sendPlayer(PlayerCommand{Action: "move", Rows: rows, To: to})
}

// trackLabel names a track, or says what its ID tells when unavailable.
func trackLabel(item MylistItem) string {
	if item.Title != "" {
		return item.Title
	}
	return "取得できません（" + item.WatchID + "）"
}

// queueItems returns the queue's entries in the order it plays: the player's
// reported items directly when they already match its order, or matched by
// watch ID in O(N) when a test supplies them separately.
func (app *nativeApp) queueItems() []MylistItem {
	order := app.player.Order
	items := app.player.Items
	if queueMatchesOrder(items, order) {
		return items
	}
	byID := make(map[string]MylistItem, len(items))
	for _, item := range items {
		byID[item.WatchID] = item
	}
	rows := make([]MylistItem, len(order))
	for i, watchID := range order {
		if item, ok := byID[watchID]; ok {
			rows[i] = item
		} else {
			rows[i] = MylistItem{WatchID: watchID}
		}
	}
	return rows
}

func queueMatchesOrder(items []MylistItem, order []string) bool {
	if len(items) != len(order) {
		return false
	}
	for i := range items {
		if items[i].WatchID != order[i] {
			return false
		}
	}
	return true
}

func formatDuration(seconds int) string {
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func isPlayable(item MylistItem) bool {
	return item.Available && item.Title != ""
}
