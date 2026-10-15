package main

import (
	"math/rand"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// PlayerCommand carries one player control or queue change from the UI to
// the playback engine.
type PlayerCommand struct {
	Action string `json:"action"`
	// ID names the mylist the items belong to: the player keeps the queue of
	// the mylist the playing track came from, so it has to tell the mylists apart.
	ID string `json:"id,omitempty"`
	// Name is the mylist's display name, which the queue screen labels the
	// queue with.
	Name       string       `json:"name,omitempty"`
	Items      []MylistItem `json:"items,omitempty"`
	WatchID    string       `json:"watchId,omitempty"`
	PositionMS float64      `json:"positionMs,omitempty"`
	Volume     float64      `json:"volume"`
	Enabled    bool         `json:"enabled,omitempty"`
	// Rows are the rows of the queue a drag moves, and To the row they
	// go before.
	Rows []int `json:"rows,omitempty"`
	To   int   `json:"to,omitempty"`
}

// playerState is what the playback engine reports about the player, the
// fields the UI reads on every frame: the queue as it plays, the track
// playing, and its position.
type playerState struct {
	WatchID string
	// MylistID names the mylist the playback queue was built from; it can differ
	// from the mylist shown while its track keeps playing.
	MylistID string
	// Name is the display name of the mylist playing; the queue screen labels
	// the queue with it.
	Name string
	// Title, Uploader and ThumbURL describe the playing track, so the bar
	// shows it even where the loaded mylist does not hold it. Quality names
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
	// Order is the queue as it plays: the watch ID of each track in queue
	// order, the track playing first under shuffle. Items carries the tracks
	// themselves, so the queue screen shows the queue without the loaded
	// mylist.
	Order []string
	Items []MylistItem
	// QueueIndex is the row of the track playing in Order, -1 for none.
	QueueIndex int
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
	// router keeps the pages of the window, the home and the mylists, so the
	// side buttons of a mouse and Alt+Left/Right go back and forward
	// between them.
	router *ui.Router

	// saved holds the pinned mylists' local snapshots; they show first on the
	// home.
	saved []SavedMylist
	// ownMylists is the signed-in account's mylists, private ones included,
	// fetched for the home; nil while signed out and until they arrive.
	ownMylists  []OwnMylist
	sidebarOpen bool
	// queueOpen shows the queue in place of the home and mylist pages.
	queueOpen bool
	// queueMenuRow is the queue row whose menu is open, -1 for none, and
	// queueMenuOpen drives the popover until it is dismissed.
	queueMenuRow  int
	queueMenuOpen bool
	// menuOpen drives the right-click menu of a mylist, menuEntry the mylist it
	// pins, and menuX and menuY where it opened within its anchor, the
	// sidebar item or card named by menuAnchorID. menuBuilt says a frame
	// built the menu, so one whose anchor left the screen closes.
	menuOpen     bool
	menuEntry    mylistEntry
	menuX, menuY float32
	menuAnchorID uint64
	menuBuilt    bool
	// queueChoice is the queue row the list last chose, -1 for none: a
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
	// confirmLogout shows the dialog the way out opens — the sidebar's
	// account row and the rail's account menu — asking before the session
	// is forgotten.
	confirmLogout bool
	// railAccountOpen drives the account menu of the rail's avatar.
	railAccountOpen bool
	page            *MylistPage
	nextPage        int
	hasMore         bool
	loading         bool
	// loadingID names the mylist a load is fetching, so a page waiting for it
	// asks once rather than every frame.
	loadingID   string
	saving      bool
	loadToken   uint64
	status      string
	statusError bool
	playerError string
	trackList   ui.ListState
	// playOnLoadID names a mylist the right-click menu asked to start once
	// it loads, playOnLoadShuffle whether it starts from a random track with
	// shuffle on; "" for none.
	playOnLoadID      string
	playOnLoadShuffle bool
	// queueList scrolls the queue view of the playing screen.
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
	// muted is the user silencing the player by the volume icon, and
	// volumeBeforeMute the level the mute was taken at, to come back to.
	muted            bool
	volumeBeforeMute float64

	thumbs    map[string]*ui.Bitmap
	thumbBusy map[string]bool
	thumbFail map[string]bool
	// fillQueue holds the entries of the loaded mylist whose pictures are not
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
	// Tooltips (and toasts): the surface turned over, kept dark so a chip
	// does not flash white over the dark app.
	t.Inverse = ui.Hex("#2f2f3a")
	t.InverseText = ui.Hex("#f4f0f5")
	t.Accent = ui.Hex("#e45b9b")
	t.AccentHover = ui.Hex("#f073ad")
	t.AccentPressed = ui.Hex("#c84684")
	t.AccentText = ui.Hex("#1a0e15")
	t.Focus = ui.Hex("#f29bc1")
	t.Radius = 8
	t.Spacing = 4
	router := ui.NewRouter(homePath)
	// Pages show at once: the mylist a page waits for does not load twice.
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
				// The rail's account menu goes with the rail; one left open
				// would show again when the sidebar closes.
				app.railAccountOpen = false
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
	// A menu whose mylist left the screen, as after a refresh, does not stay
	// open on the element that took its place.
	if app.menuOpen && !app.menuBuilt {
		app.menuOpen = false
	}
	app.menuBuilt = false
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
			// mylist already showing, whose click changes no route of its own.
			// The sidebar lists the same mylists as the home, in its order.
			entries := app.mylistEntries()
			homeClicked, mylistClicked := false, false
			sidebar := ui.Sidebar(c, &page, func() {
				item := ui.SidebarItem(c, homePath, nil, "ホーム")
				homeClicked = item.Clicked()
				ui.SidebarSection(c, "マイリスト", nil, func() {
					for _, entry := range entries {
						item := ui.SidebarItem(c, mylistPath(entry.ID), nil, entry.Name)
						if entry.Pinned {
							item.Children(func() { app.pinBadge(c) })
						}
						if item.RightClicked() {
							app.openMylistMenu(item, entry)
						}
						app.mylistMenu(c, item, entry)
						if item.Clicked() {
							app.menuOpen = false
							app.loadingID = ""
							mylistClicked = true
						}
					}
				})
			}).Grow(1)
			if homeClicked || mylistClicked {
				app.queueOpen = false
			}
			if sidebar.Changed() {
				app.router.Push(page)
			}
			if len(entries) == 0 {
				ui.Text(c, "マイリストはありません").FontSize(12).TextColor(app.theme.TextMuted)
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

// sidebarRail is the sidebar closed: a narrow strip that still navigates —
// the way to the home and to the pinned mylists, and the account — rather
// than only reopening the sidebar.
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
			// The same destinations as the open sidebar's list, the pinned
			// ones: each mylist shows as the picture of its first track.
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).FillWidth().PaddingX(12).Gap(6).Children(func() {
					app.railHome(c)
					for _, entry := range app.mylistEntries() {
						if entry.Pinned {
							app.railMylist(c, entry)
						}
					}
				})
			})
			app.railAccount(c)
		})
}

// railHome is the rail's way back to the home, lit while the home shows.
func (app *nativeApp) railHome(c *ui.Context) {
	item := ui.Button(c, "").Label("ホーム").Tooltip("ホーム").
		Size(36, 36).Padding(0).BorderWidth(0).Radius(10)
	if app.router.Path() == homePath {
		item.Background(app.theme.SurfacePressed)
	}
	item.Children(func() { ui.Icon(c, iconHome).FontSize(20) })
	if item.Clicked() {
		app.railNavigate(homePath)
	}
}

// railMylist shows one pinned mylist as its first track's picture, the way
// to it and its menu; the tooltip names it, and the label marks it as the
// pinned shortcut, apart from the same mylist's name on the home.
func (app *nativeApp) railMylist(c *ui.Context, entry mylistEntry) {
	item := ui.Button(c, "").Label(entry.Name+"（ピン留め）").Tooltip(entry.Name).
		Size(36, 36).Padding(0).BorderWidth(0).Radius(10)
	if app.router.Path() == mylistPath(entry.ID) {
		item.Background(app.theme.SurfacePressed)
	}
	item.Children(func() { app.railTile(c, entry.Items) })
	if item.RightClicked() {
		app.openMylistMenu(item, entry)
	}
	app.mylistMenu(c, item, entry)
	if item.Clicked() {
		app.railNavigate(mylistPath(entry.ID))
	}
}

// railTile shows a mylist's first track's picture, and a list mark until
// the picture arrives, or where the mylist has no track to show.
func (app *nativeApp) railTile(c *ui.Context, items []MylistItem) {
	if len(items) > 0 {
		item := items[0]
		bitmap := app.thumbs[item.WatchID]
		if bitmap == nil && item.Available {
			app.requestThumbnail(item)
		}
		if bitmap != nil {
			ui.Image(c, bitmap).Size(32, 32).Fit(ui.Cover).Radius(8)
			return
		}
	}
	ui.Box(c).Size(32, 32).Radius(8).Center().Background(app.theme.SurfaceHover).Children(func() {
		ui.Icon(c, iconQueue).FontSize(15).TextColor(app.theme.TextMuted)
	})
}

// railNavigate goes to a destination chosen on the rail, leaving the queue
// screen for it even the destination already showing, as the open sidebar's
// items do.
func (app *nativeApp) railNavigate(path string) {
	app.menuOpen = false
	app.queueOpen = false
	app.loadingID = ""
	if app.router.Path() != path {
		app.router.Push(path)
	}
}

// railAccount is the rail's foot: the avatar opening the account menu
// while signed in, the way in while not.
func (app *nativeApp) railAccount(c *ui.Context) {
	ui.Divider(c)
	ui.Row(c).FillWidth().Justify(ui.Start).Children(func() {
		ui.Box(c).Width(12).Shrink(0)
		if app.loggedIn {
			app.railAvatar(c)
			return
		}
		label := "ニコニコにログイン"
		button := ui.Button(c, "").Label(label).Tooltip(label).
			Size(36, 36).Padding(0).BorderWidth(0).Radius(10).Disabled(app.loginWindow != nil)
		button.Children(func() { ui.Icon(c, iconLogIn).FontSize(18) })
		if button.Clicked() {
			app.openLogin()
		}
	})
}

// railAvatar is the signed-in account's picture on the rail, opening the
// account menu.
func (app *nativeApp) railAvatar(c *ui.Context) {
	tip := app.accountName
	if tip == "" {
		tip = "アカウント"
	}
	button := ui.Button(c, "").Label("アカウント").Tooltip(tip).
		Size(36, 36).Padding(0).BorderWidth(0).Radius(10)
	button.Children(func() { app.accountAvatar(c) })
	if button.Clicked() {
		app.railAccountOpen = !app.railAccountOpen
	}
	app.railAccountMenu(c, button)
}

// railAccountMenu is the small menu the rail's avatar opens: who is signed
// in, and the way out that asks before the session is forgotten.
func (app *nativeApp) railAccountMenu(c *ui.Context, anchor ui.Element) {
	ui.PopoverBase(c, anchor, &app.railAccountOpen, func(panel ui.Element) {
		panel.Padding(4).Radius(app.theme.Radius+2).Background(app.theme.Background).
			Border(1, app.theme.Border).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18)).
			Role(ui.RoleMenu).Label("アカウントのメニュー")
		name := app.accountName
		if name == "" {
			name = "ログイン中"
		}
		ui.Text(c, name).FontSize(12).TextColor(app.theme.TextMuted).
			Padding(6, 8).MaxWidth(200).SingleLine().Ellipsis("…")
		item := app.menuItem(c, iconLogOut, "ログアウト")
		if item.Clicked() {
			app.railAccountOpen = false
			app.confirmLogout = true
		}
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

// menuItem is one item of an app menu: a leading icon with a left-aligned
// label, and no face of its own until the pointer is on it, so the panel is
// the only outline the menu draws.
func (app *nativeApp) menuItem(c *ui.Context, icon *ui.SVG, label string) ui.Element {
	item := ui.Button(c, "").Label(label).FillWidth().BorderWidth(0).
		Background(app.theme.Background).Justify(ui.Start).Gap(10)
	if item.Hovered() {
		item.Background(app.theme.SurfaceHover)
	}
	item.Children(func() {
		ui.Icon(c, icon).FontSize(15).TextColor(app.theme.TextMuted)
		ui.Text(c, label).FontSize(13)
	})
	return item
}

// statusLine shows the app's status, silent when it has none.
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

func (app *nativeApp) currentSaved() *SavedMylist {
	if app.page == nil {
		return nil
	}
	return app.savedEntry(app.page.ID)
}

// savedEntry returns the pinned snapshot with the ID, when there is one.
func (app *nativeApp) savedEntry(id string) *SavedMylist {
	for i := range app.saved {
		if app.saved[i].ID == id {
			return &app.saved[i]
		}
	}
	return nil
}

// unpin drops the local snapshot of the pinned mylist id; one of the
// account's own mylists returns to its live place on the home.
func (app *nativeApp) unpin(id string) {
	if entry := app.savedEntry(id); entry != nil {
		app.removeSaved(*entry)
	}
}

// openMylist shows the mylist the ID names: the one loaded, the pinned copy, else
// the API's first page.
func (app *nativeApp) openMylist(id string) {
	// A play the menu asked for belongs to its own mylist only: opening
	// another one drops it rather than starting it later.
	if app.playOnLoadID != "" && app.playOnLoadID != id {
		app.playOnLoadID, app.playOnLoadShuffle = "", false
	}
	if id == "" || app.page != nil && app.page.ID == id {
		return
	}
	if (app.loading || app.statusError) && app.loadingID == id {
		return
	}
	// Opening a mylist leaves the playing screen for it, as home does.
	app.queueOpen = false
	if app.openSaved(id) {
		return
	}
	app.fetchMylist(id)
}

// playMylist starts a mylist the right-click menu chose: from its first track,
// or from a random one with shuffle on. A card carries only a few sample
// entries, so the mylist is loaded first and the play begins when it arrives.
func (app *nativeApp) playMylist(entry mylistEntry, shuffle bool) {
	app.menuOpen = false
	app.queueOpen = false
	app.loadingID = ""
	if route := mylistPath(entry.ID); app.router.Path() != route {
		app.router.Push(route)
	}
	if app.page != nil && app.page.ID == entry.ID {
		app.startLoadedMylist(app.page.Items, shuffle)
		return
	}
	app.playOnLoadID, app.playOnLoadShuffle = entry.ID, shuffle
	app.openMylist(entry.ID)
}

// startLoadedMylist starts playback of a loaded mylist: its first playable
// track in order, or a random one with shuffle left on.
func (app *nativeApp) startLoadedMylist(items []MylistItem, shuffle bool) {
	playable := playableItems(items)
	if len(playable) == 0 {
		return
	}
	app.sendPlayer(PlayerCommand{Action: "shuffle", Enabled: shuffle})
	track := playable[0]
	if shuffle {
		track = playable[rand.Intn(len(playable))]
	}
	app.selectTrack(track)
	// Playing from a mylist hands the view to the queue screen, which
	// follows the track as it plays.
	app.queueOpen = true
}

// startPendingMylist starts a play the menu asked for once its mylist has
// loaded, and forgets the request.
func (app *nativeApp) startPendingMylist(id string, items []MylistItem) {
	if app.playOnLoadID != id {
		return
	}
	shuffle := app.playOnLoadShuffle
	app.playOnLoadID, app.playOnLoadShuffle = "", false
	app.startLoadedMylist(items, shuffle)
}

// adoptSavedPage loads a pinned snapshot into the shown mylist page and
// hands its entries to the player.
func (app *nativeApp) adoptSavedPage(entry SavedMylist) {
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
	app.resetThumbnailFill(page.Items)
	app.sendPlayer(PlayerCommand{Action: "setItems", ID: page.ID, Name: page.Name, Items: page.Items})
}

// openSaved shows the pinned snapshot of the mylist id names, reporting
// whether there is one; the route is already the page shown.
func (app *nativeApp) openSaved(id string) bool {
	for _, entry := range app.saved {
		if entry.ID != id {
			continue
		}
		app.loadToken++
		app.loading = false
		app.pendingAdvance = false
		app.setStatus("ピン留めした内容から表示中（更新するまで通信しません）")
		app.playerError = ""
		app.adoptSavedPage(entry)
		app.startPendingMylist(entry.ID, entry.Items)
		return true
	}
	return false
}

// fetchMylist loads page 1 of the mylist the input names, an ID or a URL, into
// the player and the view; the route follows the ID the API reports.
func (app *nativeApp) fetchMylist(input string) {
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
	go func() {
		page, err := app.mylist.GetPage(input, 1)
		app.update(func() {
			if token != app.loadToken {
				return
			}
			app.loading = false
			if err != nil {
				if app.playOnLoadID == input {
					app.playOnLoadID, app.playOnLoadShuffle = "", false
				}
				app.setError(err)
				return
			}
			app.page = &page
			app.nextPage = 2
			app.hasMore = page.HasNext
			app.resetThumbFailures()
			app.resetThumbnailFill(page.Items)
			app.setStatus("")
			if route := mylistPath(page.ID); app.router.Path() != route {
				app.router.Push(route)
			}
			app.sendPlayer(PlayerCommand{Action: "setItems", ID: page.ID, Name: page.Name, Items: page.Items})
			app.startPendingMylist(page.ID, page.Items)
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
		app.update(func() {
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

// canLoadNext reports whether loading the next page of the shown mylist may
// extend the playing queue: only the queue built from that mylist can take it.
// The playing mylist is not always the one on screen, and its pages are not
// loaded from here.
func (app *nativeApp) canLoadNext() bool {
	return app.hasMore && app.page != nil && app.player.MylistID == app.page.ID
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
		app.update(func() {
			app.saving = false
			if err != nil {
				app.setError(err)
				return
			}
			app.saved = saved
			for _, entry := range saved {
				if entry.ID == id && app.page != nil && app.page.ID == id {
					app.adoptSavedPage(entry)
				}
			}
			app.setStatus("ピン留めしました。")
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
	app.setStatus("ピン留めを外しました。")
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
