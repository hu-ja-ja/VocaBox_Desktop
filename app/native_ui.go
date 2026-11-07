package main

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/app/discord"
	"github.com/hu-ja-ja/VocaBox_Desktop/app/lyrics"
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
	// VideoID is the video's ID at niconico, which the lyrics lookup asks by.
	// WatchID is the mylist item's own; the two usually match.
	VideoID string
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
	// HasLyrics is the watch API's answer for the track; LyricsKnown says the
	// API was asked. A saved file plays without asking, so its answer is
	// unknown and the lyrics lookup may still try.
	HasLyrics   bool
	LyricsKnown bool
	Ready       bool
	Playing     bool
	CanNext     bool
	Shuffle     bool
	Repeat      bool
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
	nico *Nico
	// lyrics is the app's one lyrics lookup: the cache and the sources, the
	// official niconico one and the fallback databases.
	lyrics *lyrics.Service
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
	// queueWasOpen and sidebarBeforeQueue fold the sidebar while the queue
	// shows, and bring back the sidebar the queue was opened in.
	queueWasOpen       bool
	sidebarBeforeQueue bool
	// lyricsOpen shows the lyrics card floating over the queue.
	// lyricsVideoID is the track the document or error belongs to,
	// lyricsDoc the document, and lyricsErr the failure to show.
	// lyricsSource is the provider the user picked, "" while the card
	// follows the automatic priority order; lyricsTab is the tab it shows.
	// lyricsFollow is the line the line view last glided to, per
	// lyricsFollowVideo. lyricsToken drops the answer of a lookup the next
	// track replaced.
	lyricsOpen    bool
	lyricsVideoID string
	lyricsDoc     *lyrics.Doc
	// lyricsOffsetMS shifts the document's timing: every line starts that
	// many milliseconds later, + meaning the user waits longer for it.
	// lyricsID names the document whose timing that shift belongs to, so a
	// re-timed document starts over.
	lyricsOffsetMS  float64
	lyricsID        string
	lyricsErr       error
	lyricsLoading   bool
	lyricsNeedLogin bool
	lyricsSource    string
	lyricsTab       int
	// lyricsLoginWhenFailed is the login state when the failure was shown:
	// the lookup retries by itself only once a login has happened since.
	lyricsLoginWhenFailed bool
	lyricsFollow          int
	lyricsFollowVideo     string
	lyricsToken           uint64
	// lyricsSync mirrors whether the card shows a timed document, for the
	// position pull: it runs off the UI thread, so it cannot read the state
	// above.
	lyricsSync atomic.Bool
	// lyricsScroll keeps the lyrics view's place; the viewport bounds are
	// retained to verify the active line stays centered in UI tests.
	lyricsScroll ui.ScrollState
	lyricsViewY  float32
	lyricsViewH  float32
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
	t.Font = appFontFor(i18n.Locale())
	t.Radius = 8
	t.Spacing = 4
	router := ui.NewRouter(homePath)
	router.Transition = ui.TransitionFade
	// The card's theme is the app's, its background see-through: the root
	// of a window fills all of it, and the card's rounded corners have to
	// show what is behind them rather than a square of colour.
	cardTheme := *t
	cardTheme.Background = ui.RGBA(0, 0, 0, 0)
	nico := NewNico()
	app := &nativeApp{
		mylist:       mylist,
		nico:         nico,
		discord:      discord.New(discordClientID),
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
		lyricsFollow: -1,
	}
	app.lyrics = newLyricsService(mylist, nico)
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
// its place every frame, so it moves by no offset). While the desktop
// asks for less motion, the fades alone remain.
var (
	motionIn   = &ui.Motion{Opacity: 0, Y: 10}
	motionOut  = &ui.Motion{Opacity: 0, Y: -10}
	motionRise = &ui.Motion{Opacity: 0, Y: 48}
	motionMenu = &ui.Motion{Opacity: 0, Scale: 0.95}
	motionFade = &ui.Motion{Opacity: 0}
	motionRow  = &ui.Motion{Opacity: 0, Y: 8}
)

// queueShift is the curve of the queue sliding in from under the player
// bar, and back out over the page as it closes.
var queueShift = ui.Animation{Duration: 240 * time.Millisecond, Ease: ui.EaseInOut}

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
	// The position pull runs off this thread: tell it whether a timed
	// document follows the track, so it asks at the fast rate only then.
	app.lyricsSync.Store(app.lyricsOpen && app.lyricsDoc != nil && app.lyricsDoc.Timed)
	// The queue takes the window: opening it folds the sidebar away, and
	// closing it brings back the sidebar the queue was opened in.
	if app.queueOpen != app.queueWasOpen {
		if app.queueOpen {
			app.sidebarBeforeQueue = app.sidebarOpen
			app.sidebarOpen = false
		} else {
			app.sidebarOpen = app.sidebarBeforeQueue
		}
		app.queueWasOpen = app.queueOpen
	}
	ui.Column(c).Fill().Background(app.theme.Background).Padding(12).Gap(12).Children(func() {
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(12).Children(func() {
			if app.sidebarOpen {
				// The rail's account menu goes with the rail; one left open
				// would show again when the sidebar closes.
				app.railAccountOpen = false
			}
			app.sidebar(c)
			// The panel takes the width the sidebar yields as it folds, and
			// clips the queue's sheet to its own rounded corner.
			panel := ui.Column(c).Grow(1).FillHeight().MinWidth(0).Radius(12).
				Clip().Background(app.theme.Surface).Border(1, app.theme.Border)
			// The panel keeps the queue's slide, so the sheet starts and
			// ends in a frame of its own rather than the screen taking its
			// place every time the queue opens or closes.
			target := float32(0)
			if app.queueOpen {
				target = 1
			}
			slide := panel.AnimateTo("queue-slide", target, queueShift)
			panel.Children(func() {
				// The page stays behind the sheet while it covers or
				// uncovers it, and goes while the queue alone shows.
				if !app.queueOpen || slide < 1 {
					app.content(c)
				}
				if app.queueOpen || slide > 0 {
					app.queueView(c, slide)
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

// toggleQueue opens or closes the queue sheet. Closing it also closes the
// lyrics beside it: the two are one screen.
func (app *nativeApp) toggleQueue() {
	app.queueOpen = !app.queueOpen
	if !app.queueOpen {
		app.lyricsOpen = false
	}
}

// navigate goes to a destination chosen by the logo, the rail's home or a
// rail mylist, leaving the queue screen for it even the destination already
// showing.
func (app *nativeApp) navigate(path string) {
	app.menuOpen = false
	app.queueOpen = false
	app.lyricsOpen = false
	app.loadingID = ""
	if app.router.Path() != path {
		app.router.Push(path)
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
