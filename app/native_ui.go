package main

import (
	"errors"
	"log"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
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
	// nico is the app's one client to niconico: playback, the mylist
	// lookups, and the audio saving all ride the same session cookies.
	nico   *Nico
	window *mygo.Window
	// held is the work the background goroutines handed over while no window
	// was there to run it: the UI thread takes it when it builds a frame, and
	// a test, which never has a window, with runHeld. It is what keeps the
	// view's state to one goroutine, in the app and in a test alike.
	heldMu sync.Mutex
	held   []func()
	// playback is the player: the queue and the AmsHelper child process.
	// Tests leave it nil and inspect the commands the UI sent instead.
	playback *playback
	// discordOn says the Discord Rich Presence is on, and discord the client
	// that shows it; a test stands its own client in its place.
	discordOn bool
	discord   discordConn
	// tray is the notification-area icon the app keeps playing behind: nil
	// when none could be created, and a close then quits as it used to.
	// quitting says the app is on its way out, so a close request closes
	// the window rather than hiding it. trayCard is the now-playing card
	// the icon's click opens, and trayPlaying the play state the icon
	// itself shows, so it is only told when it changes.
	tray        *mygo.Tray
	quitting    bool
	trayCard    *mygo.Window
	trayPlaying bool
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
	// cardTheme is theme with a see-through background, which the card
	// draws its rounded corners with.
	cardTheme *ui.Theme
	// router keeps the pages of the window, the home and the mylists, so the
	// side buttons of a mouse and Alt+Left/Right go back and forward
	// between them.
	router *ui.Router

	// saved is the saved mylists index; the home and the sidebar show it, and
	// each mylist's body loads from disk when the mylist opens.
	saved []SavedIndexEntry
	// fill holds the mylists whose saved bodies the background worker still
	// has to fetch, one at a time.
	fill        *savedFill
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
	// combining shows the dialog building a combined list; combineSel holds
	// one check box per selectable mylist, and combineName the heading the
	// list will carry.
	combining   bool
	combineSel  map[string]*bool
	combineName string
	// confirmDelete shows the dialog that asks before a saved mylist of
	// somebody else's is deleted from this PC; deleteEntry is the mylist it
	// would delete.
	confirmDelete bool
	deleteEntry   mylistEntry
	// confirmLogout shows the dialog the way out opens — the sidebar's
	// account row and the rail's account menu — asking before the session
	// is forgotten.
	confirmLogout bool
	// railAccountOpen drives the account menu of the rail's avatar.
	railAccountOpen bool
	// qualityOption is the saved-audio preset the settings dropdown shows,
	// and audioKbps the same choice in the form the download worker reads
	// off the UI thread. downloads queues the mylists whose audio is still
	// to be fetched: audioNow and audioNowList are the track and the mylist a
	// pass is fetching right now, and audioProgress says whether the status
	// line is the progress one. audioStats and broken are what the lists
	// mark tracks by, the counts of the enabled mylists and the tracks whose
	// saved audio could not be played.
	qualityOption string
	audioKbps     atomic.Int32
	downloads     *audioDownloads
	audioNow      string
	audioNowList  string
	audioProgress bool
	audioStats    map[string]audioStat
	broken        map[string]bool
	// licenseOpen holds the licence rows of the settings left open, by the
	// work they name; licenseOpenDialog shows the dialog the app's own
	// licence opens, licenseTitle its heading and licenseBody its text.
	licenseOpen       map[string]bool
	licenseOpenDialog bool
	licenseTitle      string
	licenseBody       string
	page              *MylistPage
	nextPage          int
	hasMore           bool
	loading           bool
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
	// thumbSeen is the frame build that last drew each picture, and
	// thumbFrame the build being drawn: the memory cache trims the pictures
	// no frame is drawing and keeps the ones a screen shows.
	thumbSeen  map[string]uint64
	thumbFrame uint64
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
	router.Transition = ui.TransitionFade
	// The card's theme is the app's, its background see-through: the root
	// of a window fills all of it, and the card's rounded corners have to
	// show what is behind them rather than a square of colour.
	cardTheme := *t
	cardTheme.Background = ui.RGBA(0, 0, 0, 0)
	app := &nativeApp{
		mylist:       mylist,
		nico:         NewNico(),
		discord:      newDiscordRPC(discordClientID),
		session:      sessionStore{mylist: mylist},
		router:       router,
		theme:        t,
		cardTheme:    &cardTheme,
		sidebarOpen:  true,
		player:       playerState{Volume: 1},
		thumbs:       map[string]*ui.Bitmap{},
		thumbBusy:    map[string]bool{},
		thumbFail:    map[string]bool{},
		thumbSeen:    map[string]uint64{},
		fill:         newSavedFill(),
		downloads:    newAudioDownloads(),
		audioStats:   map[string]audioStat{},
		broken:       map[string]bool{},
		queueMenuRow: -1,
		queueFollow:  -2,
		queueChoice:  -1,
		licenseOpen:  map[string]bool{},
	}
	// Until the settings load, the downloads fetch at the highest preset.
	app.setAudioQuality(audioQualityPresets[0])
	if ids, err := mylist.LoadBroken(); err != nil {
		log.Printf("downloads: %v", err)
	} else {
		for _, id := range ids {
			app.broken[id] = true
		}
	}
	// Saving runs in the background: the bodies one mylist at a time, and the
	// audio of the download-enabled mylists one track at a time.
	go app.fillWorker()
	go app.downloadWorker()
	return app
}

// The motions the app's screens share: a panel rises in and sinks out,
// a list row rises in, a menu pops in (the attach step gives the panel
// its place every frame, so it moves by no offset), and the sidebar
// slides in from and out to the rail's side. While the desktop asks for
// less motion, the fades alone remain.
var (
	motionIn      = &ui.Motion{Opacity: 0, Y: 10}
	motionOut     = &ui.Motion{Opacity: 0, Y: -10}
	motionMenu    = &ui.Motion{Opacity: 0, Scale: 0.95}
	motionFade    = &ui.Motion{Opacity: 0}
	motionRow     = &ui.Motion{Opacity: 0, Y: 8}
	motionSidebar = &ui.Motion{Opacity: 0, X: -262}
)

// panelIn is the transition a screen appearing inside the content panel
// takes: it rises in, and sinks out as it leaves.
var panelIn = ui.ElementTransition{Enter: motionIn, Exit: motionOut}

// menuIn is the transition a popover's panel takes popping in.
var menuIn = ui.ElementTransition{Enter: motionMenu}

// dialogFade is the transition a dialog's backdrop takes: the whole
// dialog, dimmed window and panel alike, fades in at once. Closing drops
// the dialog from the view at once, so it has no way out to animate.
var dialogFade = ui.ElementTransition{Enter: motionFade}

// modalDialog shows fn's dialog the way ui.Modal does — the dimmed window
// and the centered panel — and fades the whole of it in at once, as a
// dialog is a new part of the interface that its transition does not move.
func (app *nativeApp) modalDialog(c *ui.Context, open *bool, fn func()) {
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.4)).Transition(dialogFade)
		panel.Padding(app.theme.Space(5)).Gap(app.theme.Space(3)).
			Radius(app.theme.Space(2.5)).Background(app.theme.Background).
			Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3))
		fn()
	})
}

func (app *nativeApp) view(c *ui.Context) {
	// The window's thread takes the work the background handed over; without
	// one this frame build is that thread.
	app.runHeld()
	// One count for this build: the pictures it draws are what the memory
	// cache keeps when it trims.
	app.thumbFrame++
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
	app.modalDialog(c, &app.importing, func() { app.importDialog(c) })
	app.modalDialog(c, &app.combining, func() { app.combineDialog(c) })
	app.modalDialog(c, &app.confirmLogout, func() { app.logoutDialog(c) })
	app.modalDialog(c, &app.confirmDelete, func() { app.deleteDialog(c) })
	app.modalDialog(c, &app.licenseOpenDialog, func() { app.licenseDialog(c) })
	// A menu whose mylist left the screen, as after a refresh, does not stay
	// open on the element that took its place.
	if app.menuOpen && !app.menuBuilt {
		app.menuOpen = false
	}
	app.menuBuilt = false
	// The overlay layer is here every frame, so a popover or dialog opening
	// later finds a parent that was there before it, and enters through its
	// transition rather than showing at once.
	ui.Overlay(c, func() {})
}

func (app *nativeApp) sidebar(c *ui.Context) {
	// The rail takes the sidebar's place as the sidebar folds its width
	// away, and yields it back as the sidebar grows out of the rail's side.
	ui.Column(c).Key("sidebar").Transition(ui.ElementTransition{
		Enter: &ui.Motion{Collapse: true, Opacity: 0},
		Exit:  &ui.Motion{Collapse: true, Opacity: 0},
	}).Width(250).FillHeight().Padding(12, 20, 20, 12).Gap(16).
		Background(app.theme.Surface).Border(1, app.theme.Border).Radius(12).
		Children(func() {
			ui.Row(c).FillWidth().Height(48).AlignItems(ui.Center).Gap(12).Children(func() {
				app.sidebarToggle(c, false)
				lockup := ui.ButtonBase(c).Label(locales.NavSettings()).Tooltip(locales.NavSettings()).
					Grow(1).Justify(ui.Center).Gap(12)
				if lockup.Hovered() {
					lockup.Background(app.theme.SurfaceHover).Radius(10)
				}
				lockup.Children(func() {
					app.brandMark(c)
					ui.Column(c).Gap(2).Children(func() {
						ui.Text(c, "VocaBox").FontSize(25).Bold()
						ui.Text(c, "NICONICO MUSIC").FontSize(10).FontWeight(700).LetterSpacing(1.2).TextColor(app.theme.TextMuted)
					})
				})
				if lockup.Clicked() {
					app.navigate(settingsPath)
				}
			})

			ui.Divider(c)
			page := app.router.Path()
			// Choosing a destination leaves the queue screen for it, even the
			// mylist already showing, whose click changes no route of its own.
			// The sidebar lists the same destinations as the home: the
			// mylists, and the combined ones under their own heading.
			pinned, own, combined, read := app.mylistGroups()
			entries := append(append(pinned, own...), read...)
			homeClicked, mylistClicked := false, false
			sidebar := ui.Sidebar(c, &page, func() {
				item := ui.SidebarItem(c, homePath, nil, locales.NavHome())
				homeClicked = item.Clicked()
				ui.SidebarSection(c, locales.NavMylists(), nil, func() {
					for _, entry := range entries {
						if app.sidebarMylistItem(c, entry) {
							mylistClicked = true
						}
					}
				})
				if len(combined) > 0 {
					ui.SidebarSection(c, locales.NavCombined(), nil, func() {
						for _, entry := range combined {
							if app.sidebarMylistItem(c, entry) {
								mylistClicked = true
							}
						}
					})
				}
			}).Grow(1)
			if homeClicked || mylistClicked {
				app.queueOpen = false
			}
			if sidebar.Changed() {
				app.router.Push(page)
			}
			if len(entries) == 0 && len(combined) == 0 {
				ui.Text(c, locales.SidebarEmpty()).FontSize(12).TextColor(app.theme.TextMuted)
			}
			app.accountRow(c)
		})
}

// sidebarMylistItem is one mylist of the sidebar: the way to it, its saving
// mark and pin badge as the home's card carries them, and its right-click
// menu. It reports a click.
func (app *nativeApp) sidebarMylistItem(c *ui.Context, entry mylistEntry) bool {
	item := ui.SidebarItem(c, mylistPath(entry.ID), nil, entry.Name)
	if entry.Download {
		// The sidebar is narrow: the chip's mark alone, without its count.
		item.Children(func() { app.audioBadge(c, app.audioMylistState(entry.ID), 20, 11) })
	}
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
		return true
	}
	return false
}

// accountRow shows the sign-in state at the foot of the sidebar: the way
// in while signed out, and the way out while signed in, with the account's
// picture and name once they are fetched.
func (app *nativeApp) accountRow(c *ui.Context) {
	ui.Divider(c)
	if app.loggedIn {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			app.accountAvatar(c)
			label := locales.SignedIn()
			if app.accountName != "" {
				label = locales.SignedInAs(app.accountName)
			}
			ui.Text(c, label).FontSize(12).TextColor(app.theme.TextMuted).
				SingleLine().Ellipsis("…").Grow(1).Basis(0).MinWidth(0)
			if button := ui.Button(c, locales.ActionLogout()).Padding(6, 9); button.Clicked() {
				app.confirmLogout = true
			}
		})
		return
	}
	label := locales.LoginWithNiconico()
	if app.loginWindow != nil {
		label = locales.LoginOpening()
	}
	if button := ui.Button(c, label).FillWidth().Disabled(app.loginWindow != nil); button.Clicked() {
		app.openLogin()
	}
}

// logoutDialog asks before the session is forgotten, so one click on the
// sidebar's way out does not sign the user out.
func (app *nativeApp) logoutDialog(c *ui.Context) {
	ui.Column(c).Width(320).Gap(14).Children(func() {
		ui.Text(c, locales.LogoutTitle()).FontSize(20).Bold()
		ui.Text(c, locales.LogoutBody()).FontSize(12).TextColor(app.theme.TextMuted)
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if button := ui.Button(c, locales.ActionCancel()); button.Clicked() {
				app.confirmLogout = false
			}
			if button := ui.PrimaryButton(c, locales.ActionLogoutConfirm()); button.Clicked() {
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
	ui.Column(c).Key("rail").Transition(ui.ElementTransition{
		Enter: motionIn, Exit: motionIn,
	}).Width(64).FillHeight().PaddingY(12).Gap(12).
		Background(app.theme.Surface).Border(1, app.theme.Border).Radius(12).
		Children(func() {
			ui.Row(c).FillWidth().Height(48).Justify(ui.Start).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(12).Shrink(0)
				app.sidebarToggle(c, true)
			})
			ui.Row(c).FillWidth().Justify(ui.Start).Children(func() {
				ui.Box(c).Width(12).Shrink(0)
				mark := ui.ButtonBase(c).Label(locales.NavSettings()).Tooltip(locales.NavSettings()).Size(36, 36)
				if mark.Hovered() {
					mark.Background(app.theme.SurfaceHover).Radius(10)
				}
				mark.Children(func() { app.brandMark(c) })
				if mark.Clicked() {
					app.navigate(settingsPath)
				}
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
	item := ui.Button(c, "").Label(locales.NavHome()).Tooltip(locales.NavHome()).
		Size(36, 36).Padding(0).BorderWidth(0).Radius(10)
	if app.router.Path() == homePath {
		item.Background(app.theme.SurfacePressed)
	}
	item.Children(func() { ui.Icon(c, iconHome).FontSize(20) })
	if item.Clicked() {
		app.navigate(homePath)
	}
}

// railMylist shows one pinned mylist as its first track's picture, the way
// to it and its menu; the tooltip names it, and the label marks it as the
// pinned shortcut, apart from the same mylist's name on the home.
func (app *nativeApp) railMylist(c *ui.Context, entry mylistEntry) {
	item := ui.Button(c, "").Label(locales.PinnedName(entry.Name)).Tooltip(entry.Name).
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
		app.navigate(mylistPath(entry.ID))
	}
}

// railTile shows a mylist's first track's picture, and a list mark until
// the picture arrives, or where the mylist has no track to show.
func (app *nativeApp) railTile(c *ui.Context, items []MylistItem) {
	if len(items) > 0 {
		item := items[0]
		bitmap := app.thumbAt(item.WatchID)
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

// navigate goes to a destination chosen by the logo, the rail's home or a
// rail mylist, leaving the queue screen for it even the destination already
// showing.
func (app *nativeApp) navigate(path string) {
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
		label := locales.LoginWithNiconico()
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
		tip = locales.Account()
	}
	button := ui.Button(c, "").Label(locales.Account()).Tooltip(tip).
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
			Role(ui.RoleMenu).Label(locales.AccountMenu()).Transition(menuIn)
		name := app.accountName
		if name == "" {
			name = locales.SignedIn()
		}
		ui.Text(c, name).FontSize(12).TextColor(app.theme.TextMuted).
			Padding(6, 8).MaxWidth(200).SingleLine().Ellipsis("…")
		item := app.menuItem(c, iconLogOut, locales.ActionLogout())
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
	label := locales.SidebarClose()
	if opening {
		label = locales.SidebarOpen()
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
	ui.Text(c, app.status).FontSize(12).TextColor(color).
		Transition(ui.ElementTransition{Enter: motionIn, Exit: motionIn})
}

// currentSaved returns the index line of the mylist shown, when there is one.
func (app *nativeApp) currentSaved() *SavedIndexEntry {
	if app.page == nil {
		return nil
	}
	return app.savedEntry(app.page.ID)
}

// savedEntry returns the saved index line with the ID, when there is one.
func (app *nativeApp) savedEntry(id string) *SavedIndexEntry {
	for i := range app.saved {
		if app.saved[i].ID == id {
			return &app.saved[i]
		}
	}
	return nil
}

// unpin takes a mylist's pin off, keeping its saved body.
func (app *nativeApp) unpin(id string) { app.setPinned(id, false) }

// pin pins a saved mylist, writing only the index; one whose body is not
// saved yet is fetched and saved first. A combined list has no body to fetch
// and is pinned as it stands.
func (app *nativeApp) pin(id string) {
	if app.saving {
		return
	}
	if entry := app.savedEntry(id); entry != nil && (entry.IsCombined() || app.mylist.HasSavedBody(id)) {
		app.setPinned(id, true)
		return
	}
	app.saveMylist(id)
}

// setPinned writes a saved mylist's pin, touching only the index.
func (app *nativeApp) setPinned(id string, pinned bool) {
	saved, err := app.mylist.SetPinned(id, pinned)
	if err != nil {
		app.setError(err)
		return
	}
	app.saved = saved
	if pinned {
		app.setStatus(locales.StatusPinned())
	} else {
		app.setStatus(locales.StatusUnpinned())
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
	// A combined list is built from its members' saved copies: there is no
	// body and no niconico mylist behind it.
	if entry := app.savedEntry(id); entry != nil && entry.IsCombined() {
		app.openCombined(*entry)
		return
	}
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

// adoptSavedPage loads a saved body into the shown mylist page and hands its
// entries to the player.
func (app *nativeApp) adoptSavedPage(entry SavedMylist) {
	app.resetTrackPlace(entry.ID)
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

// resetTrackPlace starts the track list at the top of the mylist id names,
// when that is not the one already shown: the list keeps the place of the
// mylist shown before otherwise, so another one would open mid-way down.
func (app *nativeApp) resetTrackPlace(id string) {
	if app.page != nil && app.page.ID == id {
		return
	}
	app.trackList.ScrollTo(0, ui.Start)
}

// adoptShownBody hands the saved body of the mylist to the shown page and
// the player when that is the mylist on screen.
func (app *nativeApp) adoptShownBody(id string) {
	if app.page == nil || app.page.ID != id {
		return
	}
	body, err := app.mylist.LoadSavedBody(id)
	if err != nil {
		return
	}
	app.adoptSavedPage(body)
}

// openSaved shows the saved body of the mylist id names, reporting whether
// there is one; the route is already the page shown.
func (app *nativeApp) openSaved(id string) bool {
	if app.savedEntry(id) == nil {
		return false
	}
	body, err := app.mylist.LoadSavedBody(id)
	if err != nil {
		// No body yet — the saving has not reached this mylist — or one
		// that cannot be read: the API answers instead.
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("mylists: %v", err)
		}
		return false
	}
	app.loadToken++
	app.loading = false
	app.pendingAdvance = false
	app.setStatus(locales.StatusFromSaved())
	app.playerError = ""
	app.adoptSavedPage(body)
	app.startPendingMylist(body.ID, body.Items)
	return true
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
	app.setStatus(locales.StatusLoadingMylist())
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
			app.resetTrackPlace(page.ID)
			app.page = &page
			app.nextPage = 2
			app.hasMore = page.HasNext
			app.resetThumbFailures()
			app.resetThumbnailFill(page.Items)
			app.setStatus("")
			if route := mylistPath(page.ID); app.router.Path() != route {
				app.router.Push(route)
			}
			// The opened mylist belongs on the home from here on, and its
			// body is saved in the background, so opening it later needs no
			// network.
			if saved, err := app.mylist.TrackMylist(page); err != nil {
				log.Printf("mylists: %v", err)
			} else {
				app.saved = saved
			}
			app.saveBody(page.ID)
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
	app.setStatus(locales.StatusLoadingMore())
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

// canNext reports whether a next track can be started: the queue has one
// loaded, the playing mylist has a page to load, or repeat goes back to the
// queue's start. The player bar's button and the tray's 次へ judge alike.
func (app *nativeApp) canNext() bool {
	return app.player.WatchID != "" &&
		(app.player.CanNext || app.canLoadNext() || app.player.Repeat)
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

// saveCurrent pins the shown mylist, fetching and saving it when its body is
// not saved yet.
func (app *nativeApp) saveCurrent() {
	if app.page == nil || app.saving {
		return
	}
	app.saveMylist(app.page.ID)
}

// saveMylist fetches every page of the mylist, saves its body, and pins it;
// the shown page adopts the saved body as soon as it is written.
func (app *nativeApp) saveMylist(id string) {
	if app.saving {
		return
	}
	app.saving = true
	app.setStatus(locales.StatusSavingMylist())
	go func() {
		saved, err := app.mylist.SaveMylist(id)
		app.update(func() {
			app.saving = false
			if err != nil {
				app.setError(err)
				return
			}
			app.saved = saved
			app.adoptShownBody(id)
			app.setStatus(locales.StatusPinned())
		})
	}()
}

// refreshSaved replaces the saved body of the mylist from the API.
func (app *nativeApp) refreshSaved(entry SavedIndexEntry) {
	if app.saving {
		return
	}
	app.saving = true
	app.setStatus(locales.StatusRefreshingMylist())
	go func() {
		saved, err := app.mylist.RefreshMylist(entry.ID)
		app.update(func() {
			app.saving = false
			if err != nil {
				app.setError(err)
				return
			}
			app.saved = saved
			app.adoptShownBody(entry.ID)
			app.setStatus(locales.StatusRefreshed())
			// A refreshed body can hold new tracks: a download-enabled
			// mylist fetches them without waiting for the next start.
			app.enqueueSavedAudio(entry.ID)
		})
	}()
}

// reloadSaved reloads the saved index into the view; callers are in a UI
// update, so the view ends up with the disk's latest.
func (app *nativeApp) reloadSaved() {
	saved, err := app.mylist.ListSaved()
	if err != nil {
		log.Printf("mylists: %v", err)
		return
	}
	app.saved = saved
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
