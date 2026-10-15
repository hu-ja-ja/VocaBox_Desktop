package main

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

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
			app.transportButtons(c)
			repeat := ui.Toggle(c, &app.player.Repeat, "").Label("ループ").Tooltip("ループ").Children(func() {
				ui.Icon(c, iconRepeat).FontSize(18)
			})
			if repeat.Changed() {
				app.sendPlayer(PlayerCommand{Action: "repeat", Enabled: app.player.Repeat})
			}

			ui.Row(c).Grow(1).Basis(0).MinWidth(0).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
				// The bar shows the player's level, except while the user
				// drags it: then it keeps its own level, so the reports of
				// the track playing never tug the thumb from under the
				// pointer.
				app.player.Volume = clampVolume(app.player.Volume)
				if !app.volumeHeld {
					app.volumeValue, app.volumeSent = app.player.Volume, app.player.Volume
				}
				// The icon silences the player and comes back to the level
				// the mute was taken at. It sits on the bar's left, sized a
				// touch above the bar's own height, and shows the silence of
				// either way: the mute it took, or a level pulled to zero.
				muteLabel := app.muteLabel()
				muteIcon := iconVolume
				if app.silent() {
					muteIcon = iconVolumeMuted
				}
				mute := ui.Button(c, "").Label(muteLabel).Tooltip(muteLabel).Padding(4, 6).BorderWidth(0)
				mute.Children(func() {
					ui.Icon(c, muteIcon).FontSize(16).TextColor(app.theme.TextMuted)
				})
				if mute.Clicked() {
					app.toggleMute()
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
				// A level the user raises by hand while muted is them wanting
				// sound back: the icon follows rather than keeping the
				// silence on. Only their own drag counts, so a report of the
				// level before the mute never unmutes it.
				if (held || volumeSlider.Changed()) && app.volumeValue > 0 {
					app.muted = false
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

// transportButtons lays out 前へ / 再生・一時停止 / 次へ: the player bar and
// the tray's card both carry them, and both judge them the same way.
func (app *nativeApp) transportButtons(c *ui.Context) {
	// The player restarts the track first while it is past its opening, so
	// the button needs no can-previous state.
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
		app.togglePlay()
	}
	next := ui.Button(c, "").Label("次へ").Tooltip("次へ").Children(func() {
		ui.Icon(c, iconSkipForward).FontSize(18)
	}).Disabled(!app.canNext())
	if next.Clicked() {
		app.advance()
	}
}

// playingTrack is the track the report names, built without looking at the
// loaded mylist: the playing mylist is not always the one on screen.
func (app *nativeApp) playingTrack() MylistItem {
	return MylistItem{
		WatchID:   app.player.WatchID,
		Available: true,
		Title:     app.player.Title,
		Uploader:  app.player.Uploader,
		ThumbURL:  app.player.ThumbURL,
	}
}

// silent reports the player at silence: muted by the icon, or a level the
// user pulled down to zero. The icon shows the mute mark either way. The
// reported level is the player's, so a card opened with the window hidden
// reads it without a frame of the bar's own.
func (app *nativeApp) silent() bool {
	return app.muted || app.player.Volume <= 0
}

// toggleMute silences the player by the icon and comes back to the level the
// mute was taken at; a level dragged down to zero comes back to half, since
// no mute remembers a level for it.
func (app *nativeApp) toggleMute() {
	level := 0.5
	switch {
	case app.muted:
		app.muted = false
		if app.volumeBeforeMute > 0 {
			level = app.volumeBeforeMute
		}
	case app.player.Volume > 0:
		app.muted = true
		app.volumeBeforeMute = app.player.Volume
		level = 0
	}
	app.player.Volume, app.volumeValue, app.volumeSent = level, level, level
	app.volumeSentAt = time.Now()
	app.sendPlayer(PlayerCommand{Action: "volume", Volume: level})
}

func clampVolume(volume float64) float64 {
	if math.IsNaN(volume) || math.IsInf(volume, 0) {
		return 1
	}
	return min(max(volume, 0), 1)
}

func formatMillis(ms float64) string {
	if math.IsNaN(ms) || math.IsInf(ms, 0) || ms < 0 {
		return "--:--"
	}
	seconds := int(ms / 1000)
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}
