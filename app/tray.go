package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// VocaBox lives on in the notification area once its window closes: playback
// goes on in the AmsHelper child process, and the tray icon is both the way
// back in and the transport while the window is away.

// trayIconSize is the icon's edge in pixels: 32 for a 16-point icon on a
// high-resolution display.
const trayIconSize = 32

// trayCard sizes the card the icon's right click opens: trayCardThumb is the
// height of its picture, 16:9 wide, and trayCardButton the width one
// transport button takes, which keeps the row steady as labels change. The
// card keeps one height whether a track plays or not: its idle state fills
// the same room as the track's picture and lines.
const (
	trayCardWidth  = 360
	trayCardHeight = 192
	trayCardThumb  = 54
	trayCardButton = 104
)

// setupTray adds the icon to the notification area, the card its right
// click opens, and hands the transport to the media keys. A tray that cannot
// be created leaves the app as it was: a close then quits rather than
// leaving no way back.
func (app *nativeApp) setupTray() {
	tray, err := mygo.NewTray(mygo.TrayOptions{Icon: trayIconPNG(false)})
	if err != nil {
		log.Printf("tray: %v", err)
		return
	}
	app.tray = tray
	// A left click is the transport, as on any player. The card is the
	// right click's, so the tooltip is left empty: the shell's plain one
	// would come up over it.
	tray.OnClick(app.trayPlayPause)
	tray.OnRightClick(app.toggleTrayCard)
	app.trayCard = app.newTrayCard(tray)
	app.registerMediaKeys()
	app.refreshTray()
}

// trayPlayPause is the icon's left click: the transport, and the card takes
// the keyboard back, which the taskbar's activation just took. A card left
// without it cannot hear the user going to another window, and would stay
// up for good.
func (app *nativeApp) trayPlayPause() {
	app.togglePlay()
	if app.trayCard != nil && app.trayCard.IsVisible() {
		app.trayCard.Focus()
	}
}

// newTrayCard creates the now-playing card: a flyout beside the icon that
// takes the keyboard while it shows, as a menu does, so a click outside it
// closes it. The card is made once and hidden and shown; a close is a hide,
// or a dismissal would tear down the window the next open needs.
//
// It draws its own rounded corners and edge: the window is transparent, and
// the system's shadow, cast around the window's square rectangle, would show
// behind them (the system does not round a transparent window).
func (app *nativeApp) newTrayCard(tray *mygo.Tray) *mygo.Window {
	card := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:      tray,
		Placement: mygo.PlacementBottom,
		Gap:       8,
		Width:     trayCardWidth,
		Height:    trayCardHeight,
		Focusable: true,
		Hidden:    true,
		Content:   ui.View(app.trayCardView),
	})
	card.OnClose(func(e *mygo.CloseEvent) {
		if app.quitting {
			return
		}
		e.PreventDefault()
		card.Hide()
	})
	return card
}

// hideOnClose reports whether a close request only hides the window: there
// is a tray to come back from, and the app is not on its way out.
func (app *nativeApp) hideOnClose() bool {
	return app.tray != nil && !app.quitting
}

// refreshTray keeps the icon on what the player does, its colour saying
// whether a track plays, and shows a card that is up the new state: one
// opened from the icon is not rebuilt by a click of its own.
func (app *nativeApp) refreshTray() {
	if app.tray == nil {
		return
	}
	if app.trayPlaying != app.player.Playing {
		app.trayPlaying = app.player.Playing
		_ = app.tray.SetIcon(trayIconPNG(app.trayPlaying), false)
	}
	if app.trayCard != nil && app.trayCard.IsVisible() {
		app.trayCard.Invalidate()
	}
}

// toggleTrayCard shows the card or takes it away again: the icon's right
// click opens it, where the track, the transport, and the way out are.
func (app *nativeApp) toggleTrayCard() {
	if app.tray == nil {
		return
	}
	if app.trayCard == nil || app.trayCard.IsDestroyed() {
		// Nothing should destroy the card while the app runs, but one
		// gone leaves the tray with no way to the transport: it is made
		// again rather than never showing.
		app.trayCard = app.newTrayCard(app.tray)
	}
	if app.trayCard.IsVisible() {
		app.trayCard.Hide()
		return
	}
	// The icon moves as the taskbar does and as the overflow panel opens:
	// placing the card again from the icon puts it where the icon is now.
	app.trayCard.SetAnchor(mygo.Rectangle{})
	app.trayCard.Show()
}

// trayCardView is the card the tray icon's right click opens: what plays,
// how far it has come, and everything the tray offers while the window is
// away.
func (app *nativeApp) trayCardView(c *ui.Context) {
	c.SetTheme(app.cardTheme)
	ui.Column(c).Fill().Background(app.theme.Surface).Radius(12).
		Border(1, app.theme.Border).Padding(12).Gap(10).Children(func() {
		app.trayCardTrack(c)
		ui.Divider(c)
		app.trayCardTransport(c)
		app.trayCardActions(c)
	})
}

// trayCardTrack is the card's leading lines: the track playing, its picture,
// and how far it has come, or the idle state where the track would be.
func (app *nativeApp) trayCardTrack(c *ui.Context) {
	if app.player.WatchID == "" {
		app.trayCardIdle(c)
		ui.Progress(c, 0)
		return
	}
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		app.thumbnail(c, app.playingTrack(), trayCardThumb)
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, app.player.Title).FontSize(14).Bold().SingleLine().Ellipsis("…")
			ui.Text(c, app.player.Uploader).FontSize(12).TextColor(app.theme.TextMuted).SingleLine().Ellipsis("…")
			ui.Text(c, app.playTime()).FontSize(11).TextColor(app.theme.TextMuted)
		})
	})
	ui.Progress(c, app.playRatio())
}

// trayCardIdle is the card with nothing playing: the room the track's
// picture and lines take, holding the list mark and what to do instead of
// one muted line on its own.
func (app *nativeApp) trayCardIdle(c *ui.Context) {
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(thumbSlotWidth(trayCardThumb, 0), trayCardThumb).Shrink(0).
			Radius(4).Center().Background(app.theme.SurfaceHover).Children(func() {
			ui.Icon(c, iconQueue).FontSize(20).TextColor(app.theme.TextMuted)
		})
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, "再生していません").FontSize(13)
			ui.Text(c, "マイリストから曲を選んで再生してください").FontSize(11).
				TextColor(app.theme.TextMuted).SingleLine().Ellipsis("…")
		})
	})
}

// trayCardTransport is the card's transport, in the middle: 前へ / 再生・
// 一時停止 / 次へ, each showing its icon and its name, so the row reads at a
// glance rather than by its symbols.
func (app *nativeApp) trayCardTransport(c *ui.Context) {
	playLabel, playIcon := "再生", iconPlay
	if app.player.Playing {
		playLabel, playIcon = "一時停止", iconPause
	}
	ui.Row(c).FillWidth().Gap(8).Justify(ui.Center).Children(func() {
		app.trayCardTransportButton(c, false, "前へ", iconSkipBack, app.player.WatchID == "", func() {
			app.sendPlayer(PlayerCommand{Action: "previous"})
		})
		app.trayCardTransportButton(c, true, playLabel, playIcon, !app.player.Ready, app.togglePlay)
		app.trayCardTransportButton(c, false, "次へ", iconSkipForward, !app.canNext(), app.advance)
	})
}

// trayCardTransportButton is one button of the card's transport: its icon
// and its name, in the width that keeps the row from moving when the play
// button's name changes.
func (app *nativeApp) trayCardTransportButton(c *ui.Context, primary bool, label string, icon *ui.SVG, off bool, run func()) {
	var button ui.Element
	if primary {
		button = ui.PrimaryButton(c, "")
	} else {
		button = ui.Button(c, "")
	}
	button = button.Label(label).Tooltip(label).Width(trayCardButton).Gap(6).
		Justify(ui.Center).Disabled(off).Children(func() {
		ui.Icon(c, icon).FontSize(17)
		ui.Text(c, label).FontSize(12)
	})
	if button.Clicked() {
		run()
	}
}

// trayCardActions is the card's last row: silence on the left, the window
// and the way out on the right.
func (app *nativeApp) trayCardActions(c *ui.Context) {
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		muteLabel := app.muteLabel()
		muteIcon := iconVolume
		if app.silent() {
			muteIcon = iconVolumeMuted
		}
		mute := ui.Button(c, "").Label(muteLabel).Tooltip(muteLabel).Gap(6).Padding(6, 10).Children(func() {
			ui.Icon(c, muteIcon).FontSize(15)
			ui.Text(c, muteLabel).FontSize(12)
		})
		if mute.Clicked() {
			app.toggleMute()
		}
		ui.Spacer(c).Grow(1)
		app.trayCardWindow(c)
		app.trayCardQuit(c)
	})
}

// trayCardWindow brings the window back, which a card opened from the tray
// is often in front of.
func (app *nativeApp) trayCardWindow(c *ui.Context) {
	button := ui.Button(c, "ウィンドウを表示").Label("ウィンドウを表示").Padding(6, 10)
	if button.Clicked() {
		app.showWindow()
		app.trayCard.Hide()
	}
}

// trayCardQuit ends the app: with the window only hiding, the card is the
// tray's only way out.
func (app *nativeApp) trayCardQuit(c *ui.Context) {
	quit := ui.Button(c, "終了").Label("終了").Tooltip("VocaBox を終了").Padding(6, 10)
	if quit.Clicked() {
		app.trayCard.Hide()
		mygo.App.Quit()
	}
}

// playTime is the track's position, its length and the audio band playing,
// as the card shows them.
func (app *nativeApp) playTime() string {
	line := formatMillis(app.player.PositionMS) + " / " + formatMillis(app.player.DurationMS)
	if app.player.Quality != "" {
		line += "  " + app.player.Quality
	}
	return line
}

// playRatio is how far the track playing has come, 0 to 1.
func (app *nativeApp) playRatio() float64 {
	duration := app.player.DurationMS
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0
	}
	return min(max(app.player.PositionMS/duration, 0), 1)
}

// muteLabel names what the mute button does, as the player bar labels it.
func (app *nativeApp) muteLabel() string {
	if app.silent() {
		return "ミュート解除"
	}
	return "ミュート"
}

// togglePlay plays or pauses the track, from the player bar's button, the
// tray icon's click and the media keys alike.
func (app *nativeApp) togglePlay() {
	if app.player.WatchID == "" {
		return
	}
	action := "play"
	if app.player.Playing {
		action = "pause"
	}
	app.sendPlayer(PlayerCommand{Action: action})
}

// showWindow brings the window back from the tray, restoring it from the
// taskbar when it was minimized, and gives it the keyboard.
func (app *nativeApp) showWindow() {
	if app.window == nil {
		return
	}
	if app.window.IsMinimized() {
		app.window.Restore()
	}
	app.window.Show()
	app.window.Focus()
}

// registerMediaKeys hands the transport to the keyboard's media keys. A key
// another player already holds stays with it: the registration fails and the
// app logs it and lives without that key.
func (app *nativeApp) registerMediaKeys() {
	keys := []struct {
		accelerator string
		run         func()
	}{
		{"MediaPlayPause", app.togglePlay},
		{"MediaNextTrack", app.advance},
		{"MediaPreviousTrack", func() { app.sendPlayer(PlayerCommand{Action: "previous"}) }},
	}
	for _, key := range keys {
		if err := mygo.GlobalShortcut.Register(key.accelerator, key.run); err != nil {
			log.Printf("media key %s: %v", key.accelerator, err)
		}
	}
}

// trayIconPNG draws the tray icon: the VocaBox V, in the accent while a
// track plays and in the muted grey otherwise. It is drawn rather than
// shipped, so no asset has to be kept in step with the theme.
func trayIconPNG(playing bool) []byte {
	ink := color.NRGBA{R: 0xa6, G: 0xa0, B: 0xad, A: 0xff}
	if playing {
		ink = color.NRGBA{R: 0xe4, G: 0x5b, B: 0x9b, A: 0xff}
	}
	img := image.NewNRGBA(image.Rect(0, 0, trayIconSize, trayIconSize))
	const pen, top, bottom = 2.6, 7.0, 25.0
	for y := top; y <= bottom; y++ {
		t := (y - top) / (bottom - top)
		disc(img, 7+t*9, y, pen, ink)
		disc(img, 24-t*9, y, pen, ink)
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}

// disc fills one round dot, the pen the tray icon is drawn with.
func disc(img *image.NRGBA, cx, cy, radius float64, ink color.NRGBA) {
	for y := int(cy - radius); y <= int(cy+radius)+1; y++ {
		for x := int(cx - radius); x <= int(cx+radius)+1; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if dx*dx+dy*dy <= radius*radius {
				img.SetNRGBA(x, y, ink)
			}
		}
	}
}
