package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// PlayerCommand carries one player control or queue change from the UI to
// the playback engine.
type PlayerCommand struct {
	Action string `json:"action"`
	// ID names the list the items belong to: the player keeps the queue of
	// the list the playing track came from, so it has to tell the lists apart.
	ID string `json:"id,omitempty"`
	// Name is the list's display name, which the queue screen labels the
	// queue with.
	Name       string       `json:"name,omitempty"`
	Items      []MylistItem `json:"items,omitempty"`
	WatchID    string       `json:"watchId,omitempty"`
	PositionMS float64      `json:"positionMs,omitempty"`
	Volume     float64      `json:"volume"`
	Enabled    bool         `json:"enabled,omitempty"`
	// Rows are the rows of the playlist a drag moves, and To the row they
	// go before.
	Rows []int `json:"rows,omitempty"`
	To   int   `json:"to,omitempty"`
}

// playerState is what the playback engine reports about the player, the
// fields the UI reads on every frame: the queue as it plays, the track
// playing, and its position.
type playerState struct {
	WatchID string
	// ListID names the list the playback queue was built from; it can differ
	// from the list shown while its track keeps playing.
	ListID string
	// Name is the display name of the list playing; the queue screen labels
	// the queue with it.
	Name string
	// Title, Uploader and ThumbURL describe the playing track, so the bar
	// shows it even where the loaded list does not hold it. Quality names
	// the audio band playing, "320kbps" with a premium session.
	Title    string
	Uploader string
	ThumbURL string
	Quality  string
	Ready    bool
	Playing  bool
	CanNext  bool
	Shuffle  bool
	Repeat   bool
	// PositionMS and DurationMS are the track's, and Volume the 0..1 level.
	PositionMS float64
	DurationMS float64
	Volume     float64
	// Order is the queue as it plays: the watch ID of each track in playlist
	// order, the track playing first under shuffle. Items carries the tracks
	// themselves, so the queue screen shows the queue without the loaded
	// list.
	Order []string
	Items []MylistItem
	// QueueIndex is the row of the track playing in Order, and QueuePos is
	// where that track sits in the queue, both -1 for none.
	QueueIndex int
	QueuePos   int
}

type nativeApp struct {
	mylist *Mylist
	window *mygo.Window
	// playback is the player: the queue and the AmsHelper child process.
	// Tests leave it nil and inspect the commands the UI sent instead.
	playback *playback
	// session keeps the niconico session cookies across runs; loggedIn
	// says one is loaded, and loginWindow is the sign-in window while one
	// is open.
	session     sessionStore
	loggedIn    bool
	loginWindow *mygo.Window
	// accountName and accountIcon are the signed-in account's display name
	// and picture, fetched once after sign-in; empty until they arrive.
	accountName string
	accountIcon *ui.Bitmap
	// sent records the commands the UI sent, for tests; the app itself
	// hands them to playback.
	sent  []PlayerCommand
	theme *ui.Theme
	// router keeps the pages of the window, the home and the lists, so the
	// side buttons of a mouse and Alt+Left/Right go back and forward
	// between them.
	router *ui.Router

	saved       []SavedMylist
	sidebarOpen bool
	// queueOpen shows the queue in place of the home and list pages.
	queueOpen bool
	// queueMenuRow is the playlist row whose menu is open, -1 for none, and
	// queueMenuOpen drives the popover until it is dismissed.
	queueMenuRow  int
	queueMenuOpen bool
	// queueChoice is the playlist row the list last chose, -1 for none: a
	// choice plays the track before the next frame.
	queueChoice int
	// queueFollow and queueFollowTrack remember the track playing the queue
	// screen was scrolled to, so it follows the player onto the next one;
	// -2 and "" with nothing followed yet.
	queueFollow      int
	queueFollowTrack string
	// importing shows the dialog the home's import card opens.
	importing bool
	input     string
	// confirmLogout shows the dialog the sidebar's way out opens, asking
	// before the session is forgotten.
	confirmLogout bool
	page      *MylistPage
	nextPage  int
	hasMore   bool
	loading   bool
	// loadingID names the list a load is fetching, so a page waiting for it
	// asks once rather than every frame.
	loadingID   string
	saving      bool
	loadToken   uint64
	status      string
	statusError bool
	playerError string
	trackList   ui.ListState
	// queueList scrolls the playlist view of the playing screen.
	queueList      ui.ListState
	player         playerState
	pendingAdvance bool
	// seekValue is the position the bar shows: the player's while it plays,
	// and the user's while they scrub the bar, so the reports of the track
	// playing never tug the thumb back from under the pointer. seekHeld
	// says the pointer is on the bar; seekSettling that a seek was sent and
	// the player has yet to report it landing.
	seekValue    float64
	seekHeld     bool
	seekSettling bool
	// volumeValue is the level the volume bar shows: the player's, and the
	// user's while they drag it, so the reports of the track playing never
	// tug the thumb from under the pointer. volumeHeld says the pointer is
	// on the bar; volumeSent is the level the player was last told, and
	// volumeSentAt when it was told it.
	volumeValue  float64
	volumeHeld   bool
	volumeSent   float64
	volumeSentAt time.Time

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
	t.Border = ui.Hex("#383842")
	t.Text = ui.Hex("#f4f0f5")
	t.TextMuted = ui.Hex("#a6a0ad")
	t.Accent = ui.Hex("#e45b9b")
	t.AccentHover = ui.Hex("#f073ad")
	t.AccentPressed = ui.Hex("#c84684")
	t.AccentText = ui.Hex("#1a0e15")
	t.Focus = ui.Hex("#f29bc1")
	t.Radius = 8
	t.Spacing = 4
	router := ui.NewRouter(homePath)
	// Pages show at once: the list a page waits for does not load twice.
	router.Transition = ui.TransitionNone
	return &nativeApp{
		mylist:       mylist,
		session:      sessionStore{mylist: mylist},
		router:       router,
		theme:        t,
		sidebarOpen:  true,
		player:       playerState{Volume: 1},
		thumbs:       map[string]*ui.Bitmap{},
		thumbBusy:    map[string]bool{},
		thumbFail:    map[string]bool{},
		queueMenuRow: -1,
		queueFollow:  -2,
		queueChoice:  -1,
	}
}

func (app *nativeApp) view(c *ui.Context) {
	c.SetTheme(app.theme)
	ui.Column(c).Fill().Background(app.theme.Background).Padding(12).Gap(12).Children(func() {
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(12).Children(func() {
			if app.sidebarOpen {
				app.sidebar(c)
			} else {
				app.sidebarRail(c)
			}
			panel := ui.Column(c).Grow(1).FillHeight().MinWidth(0).Radius(12).
				Background(app.theme.Surface).Border(1, app.theme.Border)
			panel.Children(func() {
				if app.queueOpen {
					app.queueView(c)
				} else {
					app.content(c)
				}
			})
		})
		app.playerBar(c)
	})
	ui.Modal(c, &app.importing, func() { app.importDialog(c) })
	ui.Modal(c, &app.confirmLogout, func() { app.logoutDialog(c) })
}

func (app *nativeApp) sidebar(c *ui.Context) {
	ui.Column(c).Width(250).FillHeight().Padding(12, 20, 20, 12).Gap(16).
		Background(app.theme.Surface).Border(1, app.theme.Border).Radius(12).
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
			page := app.router.Path()
			// Choosing a destination leaves the queue screen for it, even the
			// list already showing, whose click changes no route of its own.
			homeClicked, listClicked := false, false
			sidebar := ui.Sidebar(c, &page, func() {
				item := ui.SidebarItem(c, homePath, nil, "ホーム")
				homeClicked = item.Clicked()
				ui.SidebarSection(c, "ライブラリ", nil, func() {
					for _, entry := range app.saved {
						if ui.SidebarItem(c, listPath(entry.ID), nil, entry.Name).Clicked() {
							listClicked = true
						}
					}
				})
			}).Grow(1)
			if homeClicked || listClicked {
				app.queueOpen = false
			}
			if sidebar.Changed() {
				app.router.Push(page)
			}
			if len(app.saved) == 0 {
				ui.Text(c, "保存したリストはありません").FontSize(12).TextColor(app.theme.TextMuted)
			}
			app.accountRow(c)
		})
}

// accountRow shows the sign-in state at the foot of the sidebar: the way
// in while signed out, and the way out while signed in, with the account's
// picture and name once they are fetched.
func (app *nativeApp) accountRow(c *ui.Context) {
	ui.Divider(c)
	if app.loggedIn {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			app.accountAvatar(c)
			label := "ログイン中"
			if app.accountName != "" {
				label = app.accountName + "でログイン中"
			}
			ui.Text(c, label).FontSize(12).TextColor(app.theme.TextMuted).
				SingleLine().Ellipsis("…").Grow(1).Basis(0).MinWidth(0)
			if button := ui.Button(c, "ログアウト").Padding(6, 9); button.Clicked() {
				app.confirmLogout = true
			}
		})
		return
	}
	label := "ニコニコにログイン"
	if app.loginWindow != nil {
		label = "ログイン画面を開いています…"
	}
	if button := ui.Button(c, label).FillWidth().Disabled(app.loginWindow != nil); button.Clicked() {
		app.openLogin()
	}
}

// logoutDialog asks before the session is forgotten, so one click on the
// sidebar's way out does not sign the user out.
func (app *nativeApp) logoutDialog(c *ui.Context) {
	ui.Column(c).Width(320).Gap(14).Children(func() {
		ui.Text(c, "ログアウトしますか？").FontSize(20).Bold()
		ui.Text(c, "保存したログイン情報を削除します。").FontSize(12).TextColor(app.theme.TextMuted)
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if button := ui.Button(c, "キャンセル"); button.Clicked() {
				app.confirmLogout = false
			}
			if button := ui.PrimaryButton(c, "ログアウトする"); button.Clicked() {
				app.confirmLogout = false
				app.logout()
			}
		})
	})
}

// accountAvatar shows the signed-in account's picture: an empty circle of
// the same size until it arrives, so the row does not shift when it does.
func (app *nativeApp) accountAvatar(c *ui.Context) {
	if app.accountIcon != nil {
		ui.Avatar(c, app.accountName, app.accountIcon)
		return
	}
	size := app.theme.Rem(2.25)
	ui.Box(c).Size(size, size).Radius(size / 2).Shrink(0).Background(app.theme.SurfaceHover)
}

func (app *nativeApp) sidebarRail(c *ui.Context) {
	ui.Column(c).Width(64).FillHeight().PaddingY(12).Gap(12).
		Background(app.theme.Surface).Border(1, app.theme.Border).Radius(12).
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

// homePath routes to the home; the route of a list is listPath of its ID.
const homePath = "/"

// listPath is the route showing the list a mylist ID names.
func listPath(id string) string { return "/list/" + id }

// playlistCardWidth is the width of one playlist card: two 16:9 tiles wide
// plus its own padding. Two cards fit even at the narrowest window with the
// sidebar open.
const playlistCardWidth = 200

// queueView is the screen the player bar opens: the queue as it plays, with
// the track playing kept in sight. The player itself has no picture: the
// AmsHelper plays audio on its own.
func (app *nativeApp) queueView(c *ui.Context) {
	ui.Row(c).Grow(1).FillHeight().AlignItems(ui.Stretch).Children(func() {
		app.queuePanel(c)
	})
}

// queuePanel lists the tracks in the order they play, the track playing
// first under shuffle, labelled with the list the queue came from and its
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

// queueMeta labels the queue: the list it came from, its size, and the
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
// it plays, and scrolls the track playing to the top of the list as it
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
	app.queueList.Label = func(row int) string { return app.queueLabel(rows[row]) }
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
				title := ui.Text(c, app.queueLabel(item)).FontSize(13).Bold().SingleLine().Ellipsis("…")
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
	if app.player.QueueIndex < 0 ||
		app.player.QueuePos == app.queueFollow && app.player.WatchID == app.queueFollowTrack {
		return
	}
	if app.player.QueueIndex >= n {
		return
	}
	app.queueFollow = app.player.QueuePos
	app.queueFollowTrack = app.player.WatchID
	app.queueList.ScrollTo(app.player.QueueIndex, ui.Start)
}

// queueMenu shows the menu button of a row with the menu it opens: only on
// the row the pointer is over, as the three dots a playlist row carries.
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
		if menuItem := ui.Button(c, "キューから削除").FillWidth(); menuItem.Clicked() {
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

// queueLabel names a track of the queue, or says what its ID tells.
func (app *nativeApp) queueLabel(item MylistItem) string {
	if item.Title != "" {
		return item.Title
	}
	return "取得できません（" + item.WatchID + "）"
}

// queueItems returns the queue's entries in the order it plays: the player's
// reported items matched to its reported order.
func (app *nativeApp) queueItems() []MylistItem {
	order := app.player.Order
	rows := make([]MylistItem, 0, len(order))
	for _, watchID := range order {
		rows = append(rows, app.listItem(watchID))
	}
	return rows
}

// listItem returns the entry of watchID the player's report carries, or one
// carrying what the ID alone tells, which is enough for a row.
func (app *nativeApp) listItem(watchID string) MylistItem {
	for i := range app.player.Items {
		if app.player.Items[i].WatchID == watchID {
			return app.player.Items[i]
		}
	}
	return MylistItem{WatchID: watchID}
}

// homeView shows the import card and the saved playlists as cards, each
// carrying the pictures of its first four tracks; clicking a playlist card
// opens it.
func (app *nativeApp) homeView(c *ui.Context) {
	ui.Column(c).Grow(1).FillHeight().Padding(24, 24, 16).Gap(18).Children(func() {
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
	app.queueOpen = false
	app.fetchList(strings.TrimSpace(app.input))
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
		Padding(12).Radius(12).Border(1, app.theme.Border).Justify(ui.Start).AlignItems(ui.Start)
	if card.Hovered() {
		card.Background(app.theme.SurfaceHover)
	} else {
		card.Background(app.theme.Background)
	}
	card.Children(func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			app.cardTiles(c, entry.Items)
			ui.Text(c, entry.Name).FontSize(14).Bold().SingleLine().Ellipsis("…")
			ui.Text(c, savedMeta(entry)).FontSize(12).SingleLine().Ellipsis("…").TextColor(app.theme.TextMuted)
		})
	})
	if card.Clicked() {
		app.queueOpen = false
		app.router.Push(listPath(entry.ID))
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

// content builds the page the router shows: the home, or a list.
func (app *nativeApp) content(c *ui.Context) {
	app.router.View(c, func(r *ui.Route) {
		switch {
		case r.Match(homePath):
			r.Title("ホーム")
			app.homeView(c)
		case r.Match("/list/{id}"):
			app.listView(c, r)
		default:
			app.homeView(c)
		}
	})
}

// listView fills the content area with the list its route names, loading it
// when it is not the one loaded; the loaded list stays as pages change, so
// only the page shown asks for one.
func (app *nativeApp) listView(c *ui.Context, r *ui.Route) {
	id := r.Param("id")
	app.openList(id)
	if app.page == nil || app.page.ID != id {
		r.Title("読み込み中")
		ui.Column(c).Grow(1).FillHeight().Padding(24, 24, 16).Children(func() {
			if app.status == "" {
				ui.Text(c, "リストを読み込んでいます…").FontSize(13).TextColor(app.theme.TextMuted)
			} else {
				app.statusLine(c)
			}
		})
		return
	}
	r.Title(app.page.Name)
	app.listPage(c)
}

// listPage fills the content area with the loaded list.
func (app *nativeApp) listPage(c *ui.Context) {
	ui.Column(c).Grow(1).FillHeight().Padding(24, 24, 16).Gap(14).Children(func() {
		app.pumpThumbnailFill()
		app.listHeader(c)
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
		// The list carries no playback state: the player's own screens show
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
			// Playing from the list hands the view to the queue screen,
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

// seekSettleMS is how near the player's reported position must come to the
// seek's before the bar follows the player again, rather than holding the
// position the user let go at.
const seekSettleMS = 1000

// volumeSendStep is how far the level must leave the last level the player
// was told before a drag tells it again: a command per pointer frame floods
// the page with commands and the window with the reports they bring.
const volumeSendStep = 0.05

// volumeSendInterval bounds how fast a drag tells the player: a fast drag
// crosses a step on every pointer frame, and the player page and the window
// behind it feel every command.
const volumeSendInterval = 100 * time.Millisecond

func (app *nativeApp) playerBar(c *ui.Context) {
	ui.Column(c).Padding(12, 20, 14).Gap(8).Background(app.theme.Surface).
		Border(1, app.theme.Border).Radius(12).Children(func() {
		max := app.player.DurationMS
		if max <= 0 || math.IsNaN(max) || math.IsInf(max, 0) {
			max = 1
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			// The bar shows the player's position, except while the user
			// scrubs it and until the player reports the seek landing: then
			// it keeps its own position, so the reports of the track playing
			// never tug the thumb back from under the pointer.
			if app.seekSettling && math.Abs(app.player.PositionMS-app.seekValue) < seekSettleMS {
				app.seekSettling = false
			}
			if !app.seekHeld && !app.seekSettling {
				app.seekValue = app.player.PositionMS
			}
			ui.Text(c, formatMillis(app.seekValue)).Width(42).FontSize(11).TextColor(app.theme.TextMuted)
			seek := ui.Slider(c, &app.seekValue, 0, max).Label("再生位置").Grow(1).Disabled(!app.player.Ready)
			// The bar seeks once, when the pointer lets go or the keys move
			// it: seeking on every frame of the drag stutters the player and
			// leaves playback to be restarted. Dragged keeps the hold past
			// the bar's thin strip, so a drag that leaves it still commits.
			_, _, held := seek.Dragged()
			if !held && (app.seekHeld || seek.Changed()) {
				app.sendPlayer(PlayerCommand{Action: "seek", PositionMS: app.seekValue})
				app.seekSettling = true
			}
			app.seekHeld = held
			ui.Text(c, formatMillis(app.player.DurationMS)).Width(42).FontSize(11).TextAlign(ui.End).TextColor(app.theme.TextMuted)
		})
		controls := ui.Row(c).Gap(16).AlignItems(ui.Center)
		controls.Children(func() {
			// The track playing and the volume grow by the same amount,
			// which keeps the transport buttons in the middle of the bar.
			ui.Row(c).Grow(1).Basis(0).MinWidth(0).Gap(10).AlignItems(ui.Center).Children(func() {
				if app.player.WatchID != "" {
					app.thumbnail(c, app.playingTrack(), thumbBarHeight)
					ui.Column(c).Grow(1).Gap(3).Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).MinWidth(0).Children(func() {
							ui.Text(c, app.player.Title).FontSize(14).Bold().SingleLine().Ellipsis("…").Grow(1).Basis(0).MinWidth(0)
							if app.player.Quality != "" {
								ui.Text(c, app.player.Quality).FontSize(11).TextColor(app.theme.TextMuted).Shrink(0)
							}
						})
						ui.Text(c, app.player.Uploader).FontSize(12).SingleLine().Ellipsis("…").TextColor(app.theme.TextMuted)
					})
				} else {
					ui.Box(c).Size(thumbSlotWidth(thumbBarHeight, 0), thumbBarHeight).Shrink(0)
					ui.Text(c, "再生する曲を選んでください").FontSize(13).SingleLine().Ellipsis("…").TextColor(app.theme.TextMuted)
				}
			})

			shuffle := ui.Toggle(c, &app.player.Shuffle, "").Label("シャッフル").Tooltip("シャッフル").Children(func() {
				ui.Icon(c, iconShuffle).FontSize(18)
			})
			if shuffle.Changed() {
				app.sendPlayer(PlayerCommand{Action: "shuffle", Enabled: app.player.Shuffle})
				// A shuffle lays the queue out anew: the next report brings
				// the queue back to the track playing.
				app.queueFollowTrack = ""
			}
			// The player restarts the track first while it is past its
			// opening, so the button needs no can-previous state.
			previous := ui.Button(c, "").Label("前へ").Tooltip("前へ").Children(func() {
				ui.Icon(c, iconSkipBack).FontSize(18)
			}).Disabled(app.player.WatchID == "")
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
			}).Disabled(app.player.WatchID == "" || (!app.player.CanNext && !app.canLoadNext() && !app.player.Repeat))
			if next.Clicked() {
				app.advance()
			}
			repeat := ui.Toggle(c, &app.player.Repeat, "").Label("ループ").Tooltip("ループ").Children(func() {
				ui.Icon(c, iconRepeat).FontSize(18)
			})
			if repeat.Changed() {
				app.sendPlayer(PlayerCommand{Action: "repeat", Enabled: app.player.Repeat})
			}

			ui.Row(c).Grow(1).Basis(0).MinWidth(0).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconVolume).FontSize(16).TextColor(app.theme.TextMuted)
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
				// The bar shows the player's level, except while the user
				// drags it: then it keeps its own level, so the reports of
				// the track playing never tug the thumb from under the
				// pointer.
				if !app.volumeHeld {
					app.volumeValue, app.volumeSent = app.player.Volume, app.player.Volume
				}
				volumeSlider := ui.Slider(c, &app.volumeValue, 0, 1).Label("音量").Width(110)
				// A drag tells the player in volumeSendStep steps rather than
				// on every pointer frame, which floods the page with commands
				// and the window with the reports they bring; letting go sends
				// the exact level. Dragged keeps the hold past the bar's thin
				// strip, so a drag that leaves it still commits.
				_, _, held := volumeSlider.Dragged()
				switch {
				case held:
					if math.Abs(app.volumeValue-app.volumeSent) >= volumeSendStep &&
						time.Since(app.volumeSentAt) >= volumeSendInterval {
						app.volumeSent, app.volumeSentAt = app.volumeValue, time.Now()
						app.sendPlayer(PlayerCommand{Action: "volume", Volume: app.volumeValue})
					}
				case app.volumeHeld || volumeSlider.Changed():
					if app.volumeValue != app.volumeSent {
						app.volumeSent, app.volumeSentAt = app.volumeValue, time.Now()
						app.sendPlayer(PlayerCommand{Action: "volume", Volume: app.volumeValue})
					}
				}
				app.volumeHeld = held
			})

			queue := ui.Button(c, "").Label("再生キュー").Tooltip("再生キュー").Padding(6, 9).BorderWidth(0)
			if app.queueOpen {
				queue.Background(app.theme.SurfacePressed)
			}
			queue.Children(func() {
				ui.Icon(c, iconQueue).FontSize(18)
			})
			if queue.Clicked() {
				app.queueOpen = !app.queueOpen
			}
		})
		if controls.Clicked() {
			app.queueOpen = !app.queueOpen
		}
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

// playingTrack is the track the report names, built without looking at the
// loaded list: the playing list is not always the one on screen.
func (app *nativeApp) playingTrack() MylistItem {
	return MylistItem{
		WatchID:   app.player.WatchID,
		Available: true,
		Title:     app.player.Title,
		Uploader:  app.player.Uploader,
		ThumbURL:  app.player.ThumbURL,
	}
}

// openList shows the list id names: the one loaded, the saved copy, else
// the API's first page.
func (app *nativeApp) openList(id string) {
	if id == "" || app.page != nil && app.page.ID == id {
		return
	}
	if app.loading && app.loadingID == id {
		return
	}
	// Opening a list leaves the playing screen for it, as home does.
	app.queueOpen = false
	if app.openSaved(id) {
		return
	}
	app.fetchList(id)
}

// openSaved shows the saved snapshot of the mylist id names, reporting
// whether there is one; the route is already the page shown.
func (app *nativeApp) openSaved(id string) bool {
	for _, entry := range app.saved {
		if entry.ID != id {
			continue
		}
		app.loadToken++
		app.loading = false
		app.pendingAdvance = false
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
		app.playerError = ""
		// The player keeps the queue and the track it is playing; only a
		// track of this list played from here moves the queue to it.
		app.sendPlayer(PlayerCommand{Action: "setItems", ID: page.ID, Name: page.Name, Items: page.Items})
		return true
	}
	return false
}

// fetchList loads page 1 of the list the input names, an ID or a URL, into
// the player and the view; the route follows the ID the API reports.
func (app *nativeApp) fetchList(input string) {
	if strings.TrimSpace(input) == "" {
		return
	}
	app.loadToken++
	token := app.loadToken
	app.loading = true
	app.loadingID = input
	app.pendingAdvance = false
	app.page = nil
	app.hasMore = false
	app.setStatus("マイリストを読み込んでいます…")
	app.playerError = ""
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
			app.router.Push(listPath(page.ID))
			app.sendPlayer(PlayerCommand{Action: "setItems", ID: page.ID, Name: page.Name, Items: page.Items})
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

// canLoadNext reports whether loading the next page of the shown list may
// extend the playing queue: only the queue built from that list can take it.
// The playing list is not always the one on screen, and its pages are not
// loaded from here.
func (app *nativeApp) canLoadNext() bool {
	return app.hasMore && app.page != nil && app.player.ListID == app.page.ID
}

// advance moves to the next track, whether the button asked for it or the
// track playing ran out.
func (app *nativeApp) advance() {
	if app.player.CanNext {
		app.sendPlayer(PlayerCommand{Action: "next"})
		return
	}
	if app.canLoadNext() {
		app.pendingAdvance = true
		app.loadMore()
	} else if app.player.Repeat && app.player.WatchID != "" {
		app.sendPlayer(PlayerCommand{Action: "next"})
	}
}

// selectTrack plays a track the row was clicked on, from its start.
func (app *nativeApp) selectTrack(item MylistItem) {
	if !isPlayable(item) {
		return
	}
	app.pendingAdvance = false
	app.playerError = ""
	app.sendPlayer(PlayerCommand{Action: "select", WatchID: item.WatchID})
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
					app.sendPlayer(PlayerCommand{Action: "setItems", ID: page.ID, Name: page.Name, Items: page.Items})
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
	app.setStatus("保存した参照を削除しました。")
}

// sendPlayer hands one command to the playback engine; in tests, without an
// engine, the command is only recorded, which is what the UI tests assert on.
func (app *nativeApp) sendPlayer(command PlayerCommand) {
	if app.playback != nil {
		app.playback.handle(command)
		return
	}
	app.sent = append(app.sent, command)
}

func (app *nativeApp) setStatus(status string) {
	app.status = status
	app.statusError = false
}

func (app *nativeApp) setError(err error) {
	app.status = err.Error()
	app.statusError = true
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
