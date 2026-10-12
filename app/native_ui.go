package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// PlayerCommandEvent sends controls and queue changes to the player-only page.
var PlayerCommandEvent = mygo.NewEvent[PlayerCommand]("playerCommand")

type PlayerCommand struct {
	Action     string       `json:"action"`
	Items      []MylistItem `json:"items,omitempty"`
	WatchID    string       `json:"watchId,omitempty"`
	Autoplay   bool         `json:"autoplay,omitempty"`
	PositionMS float64      `json:"positionMs,omitempty"`
	Volume     float64      `json:"volume"`
	Enabled    bool         `json:"enabled,omitempty"`
}

type PlayerReport struct {
	Type       string  `json:"type"`
	Session    string  `json:"session"`
	Sequence   uint64  `json:"sequence"`
	WatchID    string  `json:"watchId"`
	Ready      bool    `json:"ready"`
	Playing    bool    `json:"playing"`
	CanPrev    bool    `json:"canPrev"`
	CanNext    bool    `json:"canNext"`
	Shuffle    bool    `json:"shuffle"`
	Repeat     bool    `json:"repeat"`
	PositionMS float64 `json:"positionMs"`
	DurationMS float64 `json:"durationMs"`
	Volume     float64 `json:"volume"`
	Error      string  `json:"error"`
}

type nativeApp struct {
	mylist       *Mylist
	window       *mygo.Window
	playerView   *mygo.WebView
	playerLoaded bool
	pending      []PlayerCommand
	theme        *ui.Theme

	saved           []SavedMylist
	selectedSavedID string
	sidebarOpen     bool
	// atHome shows the home view while the opened list stays in place, so
	// playback and the player bar survive going home.
	atHome bool
	// importing shows the dialog the home's import card opens.
	importing      bool
	input          string
	page           *MylistPage
	nextPage       int
	hasMore        bool
	loading        bool
	saving         bool
	loadToken      uint64
	status         string
	statusError    bool
	playerError    string
	trackList      ui.ListState
	player         PlayerReport
	playerSession  string
	playerSequence uint64
	pendingAdvance bool

	thumbs    map[string]*ui.Bitmap
	thumbBusy map[string]bool
	thumbFail map[string]bool
	// fillQueue holds the entries of the loaded list whose pictures are not
	// cached yet; the view fills them one at a time in the background.
	fillQueue  []MylistItem
	fillActive bool
}

func newNativeApp(mylist *Mylist) *nativeApp {
	t := ui.DarkTheme()
	t.Background = ui.Hex("#121217")
	t.Surface = ui.Hex("#1b1b23")
	t.SurfaceHover = ui.Hex("#25252f")
	t.SurfacePressed = ui.Hex("#31212d")
	t.Border = ui.Hex("#30303a")
	t.Text = ui.Hex("#f4f0f5")
	t.TextMuted = ui.Hex("#a6a0ad")
	t.Accent = ui.Hex("#e45b9b")
	t.AccentHover = ui.Hex("#f073ad")
	t.AccentPressed = ui.Hex("#c84684")
	t.AccentText = ui.Hex("#1a0e15")
	t.Focus = ui.Hex("#f29bc1")
	t.Radius = 8
	t.Spacing = 4
	return &nativeApp{
		mylist:          mylist,
		theme:           t,
		sidebarOpen:     true,
		selectedSavedID: homeItemID,
		player:          PlayerReport{Volume: 1},
		thumbs:          map[string]*ui.Bitmap{},
		thumbBusy:       map[string]bool{},
		thumbFail:       map[string]bool{},
	}
}

func (app *nativeApp) view(c *ui.Context) {
	c.SetTheme(app.theme)
	ui.Column(c).Fill().Background(app.theme.Background).Children(func() {
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			if app.sidebarOpen {
				app.sidebar(c)
			} else {
				app.sidebarRail(c)
			}
			app.content(c)
		})
		app.playerBar(c)
		if app.playerView != nil {
			// ponytail: 1×1 transparent player keeps the official video out of the audio UI;
			// confirm MyGo WebView2 continues playback at this size before depending on it.
			ui.WebView(c, app.playerView).Absolute().Left(0).Top(0).Size(1, 1).Opacity(0).PassThrough()
		}
	})
	ui.Modal(c, &app.importing, func() { app.importDialog(c) })
}

func (app *nativeApp) sidebar(c *ui.Context) {
	ui.Column(c).Width(250).FillHeight().Padding(12, 20, 20, 12).Gap(16).
		Background(app.theme.Surface).BorderWidth(0, 1, 0, 0).BorderColor(app.theme.Border).
		Children(func() {
			ui.Row(c).FillWidth().Height(48).AlignItems(ui.Center).Gap(12).Children(func() {
				app.sidebarToggle(c, false)
				ui.Row(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(12).Children(func() {
					app.brandMark(c)
					ui.Column(c).Gap(2).Children(func() {
						ui.Text(c, "VocaBox").FontSize(25).Bold()
						ui.Text(c, "NICONICO MUSIC").FontSize(10).FontWeight(700).LetterSpacing(1.2).TextColor(app.theme.TextMuted)
					})
				})
			})

			ui.Divider(c)
			sidebar := ui.Sidebar(c, &app.selectedSavedID, func() {
				ui.SidebarItem(c, homeItemID, nil, "ホーム")
				ui.SidebarSection(c, "ライブラリ", nil, func() {
					for _, entry := range app.saved {
						ui.SidebarItem(c, entry.ID, nil, entry.Name)
					}
				})
			}).Grow(1)
			if sidebar.Changed() {
				if app.selectedSavedID == homeItemID {
					app.showHome()
				} else {
					app.openSaved(app.selectedSavedID)
				}
			}
			if len(app.saved) == 0 {
				ui.Text(c, "保存したリストはありません").FontSize(12).TextColor(app.theme.TextMuted)
			}
		})
}

func (app *nativeApp) sidebarRail(c *ui.Context) {
	ui.Column(c).Width(64).FillHeight().PaddingY(12).Gap(12).
		Background(app.theme.Surface).BorderWidth(0, 1, 0, 0).BorderColor(app.theme.Border).
		Children(func() {
			ui.Row(c).FillWidth().Height(48).Justify(ui.Start).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(12).Shrink(0)
				app.sidebarToggle(c, true)
			})
			ui.Row(c).FillWidth().Justify(ui.Start).Children(func() {
				ui.Box(c).Width(12).Shrink(0)
				app.brandMark(c)
			})
			ui.Divider(c)
		})
}

// brandMark shows the VocaBox mark, the accent tile in the sidebar header
// and the collapsed rail.
func (app *nativeApp) brandMark(c *ui.Context) {
	ui.Box(c).Size(36, 36).Center().Background(app.theme.Accent).Radius(10).Children(func() {
		ui.Text(c, "V").FontSize(18).Bold().TextColor(app.theme.AccentText)
	})
}

// sidebarToggle shows an icon button opening or closing the sidebar; the
// label follows the current state for assistive technology.
func (app *nativeApp) sidebarToggle(c *ui.Context, opening bool) {
	label := "サイドバーを閉じる"
	if opening {
		label = "サイドバーを開く"
	}
	button := ui.Button(c, "").Label(label).Tooltip(label).BorderWidth(0).Padding(6, 9).Children(func() {
		ui.Icon(c, iconMenu).FontSize(18)
	})
	if button.Clicked() {
		app.sidebarOpen = opening
	}
}

// homeItemID selects the home view in the sidebar; saved mylist IDs are
// digits, so it cannot clash with one.
const homeItemID = "home"

// playlistCardWidth is the width of one playlist card: two 16:9 tiles wide
// plus its own padding. Two cards fit even at the narrowest window with the
// sidebar open.
const playlistCardWidth = 200

// showHome returns to the home view, leaving the opened list and its
// playback in place.
func (app *nativeApp) showHome() {
	app.atHome = true
	app.selectedSavedID = homeItemID
}

// homeView shows the import card and the saved playlists as cards, each
// carrying the pictures of its first four tracks; clicking a playlist card
// opens it.
func (app *nativeApp) homeView(c *ui.Context) {
	ui.Column(c).Grow(1).FillHeight().Gap(16).Children(func() {
		ui.Column(c).Gap(5).Children(func() {
			ui.Text(c, "ホーム").FontSize(27).Bold()
			sub := "保存したプレイリストから選んで再生"
			if len(app.saved) == 0 {
				sub = "公開マイリストは「+」から読み込めます"
			}
			ui.Text(c, sub).FontSize(13).TextColor(app.theme.TextMuted)
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Row(c).Wrap().Gap(16).FillWidth().Children(func() {
				for i := range app.saved {
					app.playlistCard(c, app.saved[i])
				}
				app.importCard(c)
			})
		})
		app.statusLine(c)
	})
}

// importCard shows the last card of the home: one big tile with a plus,
// opening the dialog that reads a public mylist in.
func (app *nativeApp) importCard(c *ui.Context) {
	card := ui.Button(c, "").Label("マイリストを読み込む").Width(playlistCardWidth).
		Padding(12).Radius(12).Justify(ui.Start).AlignItems(ui.Start)
	if card.Hovered() {
		card.Background(app.theme.SurfaceHover)
	} else {
		card.Background(app.theme.Surface)
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

// importDialog builds the dialog reading a public mylist in: a field for
// its URL or ID, and the buttons opening or dismissing it.
func (app *nativeApp) importDialog(c *ui.Context) {
	ui.Column(c).Width(380).Gap(14).Children(func() {
		ui.Text(c, "公開マイリストを読み込む").FontSize(20).Bold()
		ui.Text(c, "ニコニコ動画の公開マイリストのURLまたはIDを入力してください。").FontSize(12).TextColor(app.theme.TextMuted)
		field := ui.TextInput(c, &app.input).Placeholder("URL または ID").AutoFocus().
			Label("公開マイリストのURLまたはID").FillWidth()
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
	app.lookup()
}

// statusLine shows the app's status, silent when it has none. List pages
// show it in their footer instead.
func (app *nativeApp) statusLine(c *ui.Context) {
	if app.status == "" {
		return
	}
	color := app.theme.TextMuted
	if app.statusError {
		color = app.theme.Danger
	}
	ui.Text(c, app.status).FontSize(12).TextColor(color)
}

// playlistCard shows one saved playlist: its first four track pictures in
// a 2×2 tile, its name and its size. Clicking the card opens the playlist.
func (app *nativeApp) playlistCard(c *ui.Context, entry SavedMylist) {
	card := ui.Button(c, "").Label(entry.Name).Width(playlistCardWidth).
		Padding(12).Radius(12).Justify(ui.Start).AlignItems(ui.Start)
	if card.Hovered() {
		card.Background(app.theme.SurfaceHover)
	} else {
		card.Background(app.theme.Surface)
	}
	card.Children(func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			app.cardTiles(c, entry.Items)
			ui.Text(c, entry.Name).FontSize(14).Bold().SingleLine().Ellipsis("…")
			ui.Text(c, savedMeta(entry)).FontSize(12).SingleLine().Ellipsis("…").TextColor(app.theme.TextMuted)
		})
	})
	if card.Clicked() {
		app.openSaved(entry.ID)
	}
}

// cardTiles lays a playlist's first four track pictures out as a 2×2 tile,
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

// savedMeta labels a playlist card: its owner when known, and its size.
func savedMeta(entry SavedMylist) string {
	total := entry.TotalItemCount
	if total == 0 {
		total = len(entry.Items)
	}
	if entry.Owner != "" {
		return fmt.Sprintf("%s  ·  全%d曲", entry.Owner, total)
	}
	return fmt.Sprintf("全%d曲", total)
}

func (app *nativeApp) content(c *ui.Context) {
	ui.Column(c).Grow(1).FillHeight().Padding(28, 30, 18).Gap(18).Children(func() {
		if app.page == nil || app.atHome {
			app.homeView(c)
			return
		}

		app.pumpThumbnailFill()
		app.listHeader(c)
		ui.Row(c).Gap(14).Justify(ui.Start).Padding(10, 12).Children(func() {
			ui.Text(c, "#").Width(28).FontSize(12).TextColor(app.theme.TextMuted)
			ui.Box(c).Width(thumbSlotWidth(thumbRowHeight, 0)).Shrink(0)
			ui.Text(c, "曲名 / 投稿者").FontSize(12).TextColor(app.theme.TextMuted)
			ui.Spacer(c).Grow(1)
			ui.Text(c, "時間").Width(54).FontSize(12).TextAlign(ui.End).TextColor(app.theme.TextMuted)
		})
		app.trackRows(c)
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			if app.status != "" {
				color := app.theme.TextMuted
				if app.statusError {
					color = app.theme.Danger
				}
				ui.Text(c, app.status).FontSize(12).TextColor(color).Grow(1)
			} else {
				ui.Spacer(c).Grow(1)
			}
			if app.hasMore {
				button := ui.Button(c, "続きを読み込む").Disabled(app.loading)
				if button.Clicked() {
					app.loadMore()
				}
			}
		})
	})
}

func (app *nativeApp) listHeader(c *ui.Context) {
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(5).Children(func() {
			ui.Text(c, app.page.Name).FontSize(27).Bold().SingleLine().Ellipsis("…")
			meta := fmt.Sprintf("全%d曲", app.page.TotalItemCount)
			if app.page.Owner != "" {
				meta = app.page.Owner + "  ·  " + meta
			}
			ui.Text(c, meta).FontSize(13).TextColor(app.theme.TextMuted)
			if app.page.Description != "" {
				ui.Text(c, app.page.Description).FontSize(12).MaxLines(2).TextColor(app.theme.TextMuted)
			}
		})

		if entry := app.currentSaved(); entry != nil {
			if button := ui.Button(c, "更新").Disabled(app.saving || app.loading); button.Clicked() {
				app.refreshSaved(*entry)
			}
			if button := ui.Button(c, "削除").Disabled(app.saving); button.Clicked() {
				app.removeSaved(*entry)
			}
		} else if button := ui.PrimaryButton(c, "このリストを保存").Disabled(app.saving || app.loading); button.Clicked() {
			app.saveCurrent()
		}
	})
}

func (app *nativeApp) trackRows(c *ui.Context) {
	app.trackList.Key = func(row int) any { return app.page.Items[row].WatchID }
	app.trackList.Label = func(row int) string {
		item := app.page.Items[row]
		if item.Title != "" {
			return item.Title
		}
		return "取得できません " + item.WatchID
	}
	list := ui.List(c, &app.trackList, len(app.page.Items), func(i int) {
		item := app.page.Items[i]
		playable := isPlayable(item)
		label := item.Title
		if !playable {
			label = "取得できません（" + item.WatchID + "）"
		}
		row := ui.Button(c, "").Label(label).FillWidth().Padding(8, 12).Radius(8).Justify(ui.Start).AlignItems(ui.Start)
		switch {
		case item.WatchID == app.player.WatchID:
			row.Background(app.theme.SurfacePressed)
		case row.Hovered():
			row.Background(app.theme.SurfaceHover)
		default:
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
		}
	}).Grow(1).Gap(4)
	if len(app.page.Items) == 0 {
		list.Children(func() {
			ui.Text(c, "このマイリストに曲はありません").Padding(20).TextColor(app.theme.TextMuted)
		})
	}
}

// maxThumbsInMemory caps the decoded pictures kept in memory; at the cap
// the whole map is dropped and pictures reload from the disk cache as rows
// ask again.
const maxThumbsInMemory = 300

// Picture sizes: a row and the player bar fix the height, and the width
// follows each picture's own aspect ratio.
const (
	thumbRowHeight = 45
	thumbBarHeight = 36
)

// thumbnail shows one video's picture with a fixed height and a width that
// follows the picture's aspect ratio, inside a slot that keeps titles lined
// up however narrow the picture is.
func (app *nativeApp) thumbnail(c *ui.Context, item MylistItem, h float32) {
	bitmap := app.thumbs[item.WatchID]
	width := thumbWidth(h, 0, 0)
	if bitmap != nil {
		w, hh := bitmap.Size()
		width = thumbWidth(h, w, hh)
	} else if item.Available {
		app.requestThumbnail(item)
	}
	ui.Row(c).Width(thumbSlotWidth(h, width)).Shrink(0).Children(func() {
		if bitmap != nil {
			ui.Image(c, bitmap).Size(width, h).Fit(ui.Cover).Radius(4).Shrink(0)
		} else {
			ui.Box(c).Size(width, h).Radius(4).Shrink(0).Background(app.theme.SurfaceHover)
		}
		ui.Spacer(c).Grow(1)
	})
}

// thumbWidth returns how wide a picture shows at height h: its own aspect
// ratio, 16:9 for a picture whose size is not known.
func thumbWidth(h float32, w, hh int) float32 {
	if w <= 0 || hh <= 0 {
		return h * 16 / 9
	}
	return h * float32(w) / float32(hh)
}

// thumbSlotSlack is the room a slot keeps past the usual 16:9 picture, so a
// picture a little wider than 16:9 does not move the title either.
const thumbSlotSlack = 8

// thumbSlotWidth returns the width of the slot a picture of width w at
// height h sits in: the usual 16:9 width plus thumbSlotSlack, or the
// picture's own width when that is wider, so a wider picture takes the room
// it needs instead of being squeezed.
func thumbSlotWidth(h, w float32) float32 {
	return max(w, h*16/9+thumbSlotSlack)
}

// queueThumbnailFill schedules the pictures of a loaded list to be cached in
// the background, so a list refreshed or reopened gains its pictures without
// every row having to be scrolled into view.
func (app *nativeApp) queueThumbnailFill(items []MylistItem) {
	app.fillQueue = append(app.fillQueue, items...)
}

// pumpThumbnailFill caches the next missing picture of the queued list, one
// load at a time, as the view builds. Keeping to one load keeps the shared
// limiter's queue short, so a row the user scrolls to is never stuck behind
// the whole list.
func (app *nativeApp) pumpThumbnailFill() {
	if app.window == nil || app.fillActive {
		return
	}
	for len(app.fillQueue) > 0 {
		item := app.fillQueue[0]
		app.fillQueue = app.fillQueue[1:]
		if !app.needsThumbnail(item) {
			continue
		}
		app.fillActive = true
		app.fillThumbnail(item)
		return
	}
}

// needsThumbnail reports whether item's picture still has to be cached.
func (app *nativeApp) needsThumbnail(item MylistItem) bool {
	return item.WatchID != "" && item.Available &&
		!app.thumbFail[item.WatchID] && !app.thumbBusy[item.WatchID] &&
		app.thumbs[item.WatchID] == nil && !app.mylist.HasThumbnail(item.WatchID)
}

// fillThumbnail caches one picture on disk without decoding it: a row that
// shows it later reads the disk cache.
func (app *nativeApp) fillThumbnail(item MylistItem) {
	app.thumbBusy[item.WatchID] = true
	watchID, thumbURL := item.WatchID, item.ThumbURL
	go func() {
		_, err := app.mylist.Thumbnail(watchID, thumbURL)
		app.window.Update(func() {
			app.fillActive = false
			delete(app.thumbBusy, watchID)
			if err != nil {
				app.thumbFail[watchID] = true
			}
		})
	}()
}

// requestThumbnail loads item's picture in the background: the memory
// cache, the disk cache, then the CDN. Loads of one video collapse into one
// goroutine, and a failure is remembered until the list is loaded anew.
func (app *nativeApp) requestThumbnail(item MylistItem) {
	if app.window == nil || item.WatchID == "" || app.thumbBusy[item.WatchID] || app.thumbFail[item.WatchID] {
		return
	}
	if len(app.thumbs) >= maxThumbsInMemory {
		// ponytail: drop the whole memory cache at the cap; pictures reload from
		// the disk cache, so an LRU can wait for a profile that asks for it.
		app.thumbs = map[string]*ui.Bitmap{}
	}
	app.thumbBusy[item.WatchID] = true
	watchID, thumbURL := item.WatchID, item.ThumbURL
	go func() {
		body, err := app.mylist.Thumbnail(watchID, thumbURL)
		if err != nil {
			app.window.Update(func() {
				delete(app.thumbBusy, watchID)
				app.thumbFail[watchID] = true
			})
			return
		}
		app.window.Update(func() {
			delete(app.thumbBusy, watchID)
			bitmap, err := ui.DecodeBitmap(body)
			if err != nil || !thumbSizeOK(bitmap) {
				app.thumbFail[watchID] = true
				return
			}
			app.thumbs[watchID] = bitmap
		})
	}()
}

// thumbSizeOK rejects absurd pictures, so neither a corrupt cache file nor
// a strange server response becomes a giant texture.
func thumbSizeOK(b *ui.Bitmap) bool {
	if b == nil {
		return false
	}
	w, h := b.Size()
	return w > 0 && h > 0 && w <= 2048 && h <= 2048
}

// resetThumbFailures lets pictures that once failed try again, after the
// list was loaded anew.
func (app *nativeApp) resetThumbFailures() {
	app.thumbFail = map[string]bool{}
}

func (app *nativeApp) playerBar(c *ui.Context) {
	ui.Column(c).Padding(14, 22, 16).Gap(8).Background(app.theme.Surface).
		BorderWidth(1, 0, 0, 0).BorderColor(app.theme.Border).Children(func() {
		max := app.player.DurationMS
		if max <= 0 || math.IsNaN(max) || math.IsInf(max, 0) {
			max = 1
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, formatMillis(app.player.PositionMS)).Width(42).FontSize(11).TextColor(app.theme.TextMuted)
			seek := ui.Slider(c, &app.player.PositionMS, 0, max).Label("再生位置").Grow(1).Disabled(!app.player.Ready)
			if seek.Changed() {
				app.sendPlayer(PlayerCommand{Action: "seek", PositionMS: app.player.PositionMS})
			}
			ui.Text(c, formatMillis(app.player.DurationMS)).Width(42).FontSize(11).TextAlign(ui.End).TextColor(app.theme.TextMuted)
		})
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Width(320).Gap(10).AlignItems(ui.Center).Children(func() {
				if item := app.currentTrack(); item != nil {
					app.thumbnail(c, *item, thumbBarHeight)
					ui.Column(c).Grow(1).Gap(3).Children(func() {
						ui.Text(c, item.Title).FontSize(14).Bold().SingleLine().Ellipsis("…")
						ui.Text(c, item.Uploader).FontSize(12).SingleLine().Ellipsis("…").TextColor(app.theme.TextMuted)
					})
				} else {
					ui.Box(c).Size(thumbSlotWidth(thumbBarHeight, 0), thumbBarHeight).Shrink(0)
					ui.Text(c, "再生する曲を選んでください").FontSize(13).TextColor(app.theme.TextMuted)
				}
			})
			ui.Spacer(c).Grow(1)

			shuffle := ui.Toggle(c, &app.player.Shuffle, "").Label("シャッフル").Tooltip("シャッフル").Children(func() {
				ui.Icon(c, iconShuffle).FontSize(18)
			})
			if shuffle.Changed() {
				app.sendPlayer(PlayerCommand{Action: "shuffle", Enabled: app.player.Shuffle})
			}
			previous := ui.Button(c, "").Label("前へ").Tooltip("前へ").Children(func() {
				ui.Icon(c, iconSkipBack).FontSize(18)
			}).Disabled(!app.player.CanPrev)
			if previous.Clicked() {
				app.sendPlayer(PlayerCommand{Action: "previous"})
			}
			playLabel := "再生"
			playIcon := iconPlay
			if app.player.Playing {
				playLabel = "一時停止"
				playIcon = iconPause
			}
			play := ui.PrimaryButton(c, "").Label(playLabel).Tooltip(playLabel).Children(func() {
				ui.Icon(c, playIcon).FontSize(19)
			}).Disabled(!app.player.Ready)
			if play.Clicked() {
				action := "play"
				if app.player.Playing {
					action = "pause"
				}
				app.sendPlayer(PlayerCommand{Action: action})
			}
			next := ui.Button(c, "").Label("次へ").Tooltip("次へ").Children(func() {
				ui.Icon(c, iconSkipForward).FontSize(18)
			}).Disabled(app.player.WatchID == "" || (!app.player.CanNext && !app.hasMore && !app.player.Repeat))
			if next.Clicked() {
				app.advance(true)
			}
			repeat := ui.Toggle(c, &app.player.Repeat, "").Label("ループ").Tooltip("ループ").Children(func() {
				ui.Icon(c, iconRepeat).FontSize(18)
			})
			if repeat.Changed() {
				app.sendPlayer(PlayerCommand{Action: "repeat", Enabled: app.player.Repeat})
			}

			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconVolume).FontSize(16).TextColor(app.theme.TextMuted)
				ui.Text(c, "音量").FontSize(11).TextColor(app.theme.TextMuted)
			})
			volume := app.player.Volume
			if math.IsNaN(volume) || math.IsInf(volume, 0) {
				volume = 1
			}
			if volume < 0 {
				volume = 0
			}
			if volume > 1 {
				volume = 1
			}
			app.player.Volume = volume
			volumeSlider := ui.Slider(c, &app.player.Volume, 0, 1).Label("音量").Width(110).Disabled(!app.player.Ready)
			if volumeSlider.Changed() {
				app.sendPlayer(PlayerCommand{Action: "volume", Volume: app.player.Volume})
			}
		})
		if app.playerError != "" {
			ui.Text(c, app.playerError).FontSize(12).TextColor(app.theme.Danger)
		}
	})
}

func (app *nativeApp) currentSaved() *SavedMylist {
	if app.page == nil {
		return nil
	}
	for i := range app.saved {
		if app.saved[i].ID == app.page.ID {
			return &app.saved[i]
		}
	}
	return nil
}

func (app *nativeApp) currentTrack() *MylistItem {
	if app.page == nil || app.player.WatchID == "" {
		return nil
	}
	for i := range app.page.Items {
		if app.page.Items[i].WatchID == app.player.WatchID {
			return &app.page.Items[i]
		}
	}
	return nil
}

func (app *nativeApp) openSaved(id string) {
	if id == "" {
		return
	}
	for _, entry := range app.saved {
		if entry.ID != id {
			continue
		}
		app.loadToken++
		app.loading = false
		app.pendingAdvance = false
		app.atHome = false
		app.selectedSavedID = entry.ID
		total := entry.TotalItemCount
		if total == 0 {
			total = len(entry.Items)
		}
		page := MylistPage{
			ID:             entry.ID,
			Name:           entry.Name,
			Owner:          entry.Owner,
			Description:    entry.Description,
			TotalItemCount: total,
			Items:          append([]MylistItem(nil), entry.Items...),
		}
		app.page = &page
		app.nextPage = 1
		app.hasMore = false
		app.resetThumbFailures()
		app.queueThumbnailFill(page.Items)
		app.setStatus("保存内容から表示中（更新するまで通信しません）")
		app.resetPlayerState()
		app.sendPlayer(PlayerCommand{Action: "setItems", Items: page.Items})
		return
	}
}

func (app *nativeApp) lookup() {
	if app.loading || strings.TrimSpace(app.input) == "" {
		if strings.TrimSpace(app.input) == "" {
			app.setStatus("マイリストのURLまたはIDを入力してください。")
			app.statusError = true
		}
		return
	}
	input := strings.TrimSpace(app.input)
	app.loadToken++
	token := app.loadToken
	app.loading = true
	app.atHome = false
	app.selectedSavedID = ""
	app.pendingAdvance = false
	app.page = nil
	app.hasMore = false
	app.setStatus("マイリストを読み込んでいます…")
	app.resetPlayerState()
	app.sendPlayer(PlayerCommand{Action: "setItems"})
	go func() {
		page, err := app.mylist.GetPage(input, 1)
		app.window.Update(func() {
			if token != app.loadToken {
				return
			}
			app.loading = false
			if err != nil {
				app.setError(err)
				return
			}
			app.page = &page
			app.nextPage = 2
			app.hasMore = page.HasNext
			app.resetThumbFailures()
			app.queueThumbnailFill(page.Items)
			app.setStatus("")
			app.sendPlayer(PlayerCommand{Action: "setItems", Items: page.Items})
		})
	}()
}

func (app *nativeApp) loadMore() {
	if app.loading || !app.hasMore || app.page == nil {
		return
	}
	pageID, pageNo, token := app.page.ID, app.nextPage, app.loadToken
	app.loading = true
	app.setStatus("続きを読み込んでいます…")
	go func() {
		page, err := app.mylist.GetPage(pageID, pageNo)
		app.window.Update(func() {
			if token != app.loadToken {
				return
			}
			app.loading = false
			if err != nil {
				app.setError(err)
				app.pendingAdvance = false
				return
			}
			app.page.Items = append(app.page.Items, page.Items...)
			app.page.TotalItemCount = page.TotalItemCount
			app.nextPage++
			app.hasMore = page.HasNext
			app.page.HasNext = page.HasNext
			app.resetThumbFailures()
			app.queueThumbnailFill(page.Items)
			app.setStatus("")
			app.sendPlayer(PlayerCommand{Action: "appendItems", Items: page.Items})
		})
	}()
}

func (app *nativeApp) advance(autoplay bool) {
	if app.player.CanNext {
		app.sendPlayer(PlayerCommand{Action: "next", Autoplay: autoplay})
		return
	}
	if app.hasMore {
		app.pendingAdvance = autoplay
		app.loadMore()
	} else if app.player.Repeat && app.player.WatchID != "" {
		app.sendPlayer(PlayerCommand{Action: "next", Autoplay: autoplay})
	}
}

func (app *nativeApp) selectTrack(item MylistItem) {
	if !isPlayable(item) {
		return
	}
	app.pendingAdvance = false
	app.playerError = ""
	app.sendPlayer(PlayerCommand{Action: "select", WatchID: item.WatchID})
}

func (app *nativeApp) resetPlayerState() {
	volume := app.player.Volume
	if math.IsNaN(volume) || math.IsInf(volume, 0) || volume < 0 || volume > 1 {
		volume = 1
	}
	app.player = PlayerReport{Volume: volume}
	app.playerError = ""
}

func (app *nativeApp) saveCurrent() {
	if app.page == nil || app.saving {
		return
	}
	app.saveEntry(app.page.ID, app.page.Name)
}

func (app *nativeApp) refreshSaved(entry SavedMylist) {
	app.saveEntry(entry.ID, entry.Name)
}

func (app *nativeApp) saveEntry(id, name string) {
	if app.saving {
		return
	}
	app.saving = true
	app.setStatus("マイリストを更新しています…")
	go func() {
		saved, err := app.mylist.SaveMylist(id, name)
		app.window.Update(func() {
			app.saving = false
			if err != nil {
				app.setError(err)
				return
			}
			app.saved = saved
			for _, entry := range saved {
				if entry.ID == id && app.page != nil && app.page.ID == id {
					app.selectedSavedID = id
					page := MylistPage{
						ID:             entry.ID,
						Name:           entry.Name,
						Owner:          entry.Owner,
						Description:    entry.Description,
						TotalItemCount: entry.TotalItemCount,
						Items:          append([]MylistItem(nil), entry.Items...),
					}
					app.page = &page
					app.nextPage = 1
					app.hasMore = false
					app.resetThumbFailures()
					app.queueThumbnailFill(page.Items)
					app.sendPlayer(PlayerCommand{Action: "replaceItems", Items: page.Items})
				}
			}
			app.setStatus("保存しました。")
		})
	}()
}

func (app *nativeApp) removeSaved(entry SavedMylist) {
	saved, err := app.mylist.RemoveSaved(entry.ID)
	if err != nil {
		app.setError(err)
		return
	}
	app.saved = saved
	app.selectedSavedID = ""
	app.setStatus("保存した参照を削除しました。")
}

func (app *nativeApp) sendPlayer(command PlayerCommand) {
	if !app.playerLoaded || app.playerView == nil {
		app.pending = append(app.pending, command)
		return
	}
	if err := PlayerCommandEvent.EmitPage(app.playerView.Page(), command); err != nil {
		app.playerError = "プレイヤーに操作を送れませんでした。"
	}
}

func (app *nativeApp) setStatus(status string) {
	app.status = status
	app.statusError = false
}

func (app *nativeApp) setError(err error) {
	app.status = err.Error()
	app.statusError = true
}

func (app *nativeApp) flushPlayerCommands() {
	for _, command := range app.pending {
		if err := PlayerCommandEvent.EmitPage(app.playerView.Page(), command); err != nil {
			app.playerError = "プレイヤーに操作を送れませんでした。"
			break
		}
	}
	app.pending = nil
}

// PlayerBridge reports events from the isolated WebView running the official
// embed player. All view state changes return to the window's UI thread.
type PlayerBridge struct {
	app *nativeApp
}

func (b *PlayerBridge) Report(report PlayerReport) {
	app := b.app
	if app == nil || app.window == nil {
		return
	}
	app.window.Update(func() {
		if report.Type == "ready" {
			wasLoaded := app.playerLoaded
			currentID := app.player.WatchID
			wasPlaying := app.player.Playing
			shuffle, repeat, volume := app.player.Shuffle, app.player.Repeat, app.player.Volume
			app.playerLoaded = true
			app.playerSession = report.Session
			app.playerSequence = report.Sequence
			if wasLoaded {
				app.pending = nil
				app.player = PlayerReport{Shuffle: shuffle, Repeat: repeat, Volume: volume}
				items := []MylistItem(nil)
				if app.page != nil {
					items = app.page.Items
				}
				app.pending = append(app.pending, PlayerCommand{Action: "setItems", Items: items})
				if shuffle {
					app.pending = append(app.pending, PlayerCommand{Action: "shuffle", Enabled: true})
				}
				if repeat {
					app.pending = append(app.pending, PlayerCommand{Action: "repeat", Enabled: true})
				}
				app.pending = append(app.pending, PlayerCommand{Action: "volume", Volume: volume})
				if currentID != "" {
					app.pending = append(app.pending, PlayerCommand{Action: "select", WatchID: currentID, Autoplay: wasPlaying})
				}
			}
			app.flushPlayerCommands()
			return
		}
		if report.Session != app.playerSession || report.Sequence <= app.playerSequence {
			return
		}
		app.playerSequence = report.Sequence
		app.player = report
		if report.Error != "" {
			app.playerError = report.Error
		}
		if report.Type == "ended" || report.Type == "needsNext" {
			app.advance(true)
		} else if app.pendingAdvance && !app.loading {
			if report.CanNext {
				app.pendingAdvance = false
				app.sendPlayer(PlayerCommand{Action: "next", Autoplay: true})
			} else if app.hasMore {
				app.loadMore()
			} else {
				app.pendingAdvance = false
				if report.Repeat && report.WatchID != "" {
					app.sendPlayer(PlayerCommand{Action: "next", Autoplay: true})
				}
			}
		}
	})
}

func formatMillis(ms float64) string {
	if math.IsNaN(ms) || math.IsInf(ms, 0) || ms < 0 {
		return "--:--"
	}
	seconds := int(ms / 1000)
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func formatDuration(seconds int) string {
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func isPlayable(item MylistItem) bool {
	return item.Available && item.Title != ""
}
