package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// The offline saving of a track and of a mylist shows as a round mark: the
// arrow down to the line once the track is saved — the circle filled in the
// app's accent with the arrow knocked out in its text color, as the logo is —
// the same arrow outlined while it is only planned, a spinner while it is
// being fetched, and the warning file outlined once the saved audio turned
// out broken. The same mark titles the saving of a whole mylist, with how
// many of its tracks are saved beside it.

// audioIcon is what a state draws and says: the icon (nil for the spinner),
// the label of the state, the color it reads in, and whether the mark is the
// filled one.
func (app *nativeApp) audioIcon(state audioState) (icon *ui.SVG, tip string, color ui.Color, filled bool) {
	switch state {
	case audioSaved:
		return iconArrowDown, locales.AudioSavedLabel(), app.theme.AccentText, true
	case audioDownloading:
		return nil, locales.AudioSavingLabel(), app.theme.Accent, false
	case audioBroken:
		return iconFileWarning, locales.AudioBrokenLabel(), app.theme.Danger, false
	}
	return iconArrowDown, locales.AudioPlannedLabel(), app.theme.Accent, false
}

// audioBadge is the round mark of one state at the size given: filled in the
// accent once saved, outlined otherwise.
func (app *nativeApp) audioBadge(c *ui.Context, state audioState, size, iconSize float32) ui.Element {
	icon, tip, color, filled := app.audioIcon(state)
	badge := ui.Box(c).Size(size, size).Radius(size / 2).Center().Shrink(0)
	if filled {
		badge.Background(app.theme.Accent)
	} else {
		badge.Background(app.theme.Background).Border(1, app.theme.Border)
	}
	badge.Children(func() {
		if icon == nil {
			ui.Spinner(c).Label(tip).Tooltip(tip).TextColor(color)
			return
		}
		ui.Icon(c, icon).FontSize(iconSize).TextColor(color).Label(tip).Tooltip(tip)
	})
	return badge
}

// trackAudioState is how one row's mark is decided: a video niconico no
// longer serves is no save target, so it carries no mark whatever this PC
// still holds for it.
func (app *nativeApp) trackAudioState(item MylistItem, planned bool) audioState {
	if !isPlayable(item) {
		return audioNone
	}
	return app.audioStateOf(item.WatchID, planned)
}

// trackAudioMark is the mark of one track of a mylist: planned says the list
// holding it saves for offline playing, so a track nothing is saved for and
// nothing is planned for shows no mark at all.
func (app *nativeApp) trackAudioMark(c *ui.Context, item MylistItem, planned bool) ui.Element {
	state := app.trackAudioState(item, planned)
	if state == audioNone {
		return ui.Box(c).Width(24).Shrink(0)
	}
	return app.audioBadge(c, state, 24, 13)
}

// audioChip is the mark of a whole mylist: its state, and the count of the
// tracks it holds that are saved.
func (app *nativeApp) audioChip(c *ui.Context, id string) ui.Element {
	state := app.audioMylistState(id)
	_, tip, _, _ := app.audioIcon(state)
	stat := app.audioStats[id]
	if stat.broken > 0 {
		tip += "・" + locales.AudioBrokenCount(stat.broken)
	}
	chip := ui.Box(c).Radius(10).Padding(3, 7).Shrink(0).Label(tip).
		Background(app.theme.Background).Border(1, app.theme.Border)
	chip.Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			app.audioBadge(c, state, 20, 11)
			if stat.total > 0 {
				ui.Text(c, fmt.Sprintf("%d/%d", stat.saved, stat.total)).FontSize(11).TextColor(app.theme.Text)
			}
		})
	})
	return chip
}
