package main

import (
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/app/lyrics"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// lyricsScrollAnimation is the glide of the line view to the line playing: a
// spring keeps its velocity when the next line arrives before it settles, and
// settles without overshooting the line it arrives at.
var lyricsScrollAnimation = ui.Animation{Spring: ui.SpringOptions{Duration: 320 * time.Millisecond}}

// lyricsFirstLineLeadMS is how far the official data's first line starts
// after its vocal. The lines after it are timed to the vocal, so only the
// first line moves.
const lyricsFirstLineLeadMS = 1000

// lyricsSeekLeadMS is how far before a clicked line the player lands, so the
// line's opening is heard rather than clipped.
const lyricsSeekLeadMS = 100

// leadFirstLyricLine moves the first line of an official document earlier by
// lyricsFirstLineLeadMS, so it turns white with the vocal. The lines after it
// keep their timings, and other sources are left alone.
func leadFirstLyricLine(doc *lyrics.Doc) {
	if doc == nil || !doc.Timed || doc.Source != lyrics.SourceNiconico || len(doc.Lines) == 0 {
		return
	}
	line := &doc.Lines[0]
	line.StartMS = max(0, line.StartMS-lyricsFirstLineLeadMS)
	for i := range line.Words {
		line.Words[i].StartMS = max(0, line.Words[i].StartMS-lyricsFirstLineLeadMS)
		line.Words[i].EndMS = max(0, line.Words[i].EndMS-lyricsFirstLineLeadMS)
	}
}

// lyricsPanel is the card floating over the queue's right: the track's
// lyrics, credited to the source that answered. The queue stays visible and
// usable on its left, and the player bar below is untouched.
func (app *nativeApp) lyricsPanel(c *ui.Context) {
	app.syncLyrics()
	ui.Column(c).Absolute().Top(12).Right(12).Bottom(12).WidthPercent(52).MaxWidth(620).
		Background(app.theme.Surface).Border(1, app.theme.Border).Radius(12).
		Shadow(-6, 0, 24, 0, ui.RGBA(0, 0, 0, 0.35)).
		Transition(ui.ElementTransition{Enter: motionRise, Exit: motionFade}).
		Padding(18, 24, 12).Gap(12).Children(func() {
		app.lyricsHeader(c)
		app.lyricsSources(c)
		app.lyricsBody(c)
		app.lyricsFooter(c)
	})
}

// lyricsSources is the provider tabs: the source that answered, or the one
// the user picked. The card starts in the automatic priority order; a tab
// asks for one provider alone, and a new track returns to the automatic one.
func (app *nativeApp) lyricsSources(c *ui.Context) {
	if app.lyrics == nil {
		return
	}
	sources := app.lyrics.Sources()
	if len(sources) == 0 {
		return
	}
	labels := make([]string, len(sources))
	for i, source := range sources {
		labels[i] = source.Name
	}
	tabs := ui.Segmented(c, &app.lyricsTab, labels...).Label(locales.LyricsSourceLabel())
	if !tabs.Changed() {
		return
	}
	app.beginLyricsSource(app.lyricsTrack(), sources[app.lyricsTab].ID)
}

// lyricsHeader names the panel and the track. The view below it follows from
// the document: timed lyrics are one list whose current line colors its
// words when the source timed them, and an untimed document the whole sheet,
// so there is no unit to choose.
func (app *nativeApp) lyricsHeader(c *ui.Context) {
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, locales.LyricsTitle()).FontSize(21).Bold().SingleLine().Ellipsis("…")
			if meta := app.lyricsMeta(); meta != "" {
				ui.Text(c, meta).FontSize(12).SingleLine().Ellipsis("…").TextColor(app.theme.TextMuted)
			}
		})
		close := ui.ButtonBase(c).Label(locales.LyricsClose()).Tooltip(locales.LyricsClose()).
			Size(28, 28).Padding(0).BorderWidth(0).Radius(6)
		if close.Hovered() {
			close.Background(app.theme.SurfaceHover)
		}
		close.Children(func() {
			ui.Text(c, "×").FontSize(20).TextColor(app.theme.TextMuted)
		})
		if close.Clicked() {
			app.lyricsOpen = false
		}
	})
}

// lyricsMeta is the track line: its title and uploader; empty when no track
// plays, which the body's prompt says instead.
func (app *nativeApp) lyricsMeta() string {
	if app.player.Title == "" {
		return ""
	}
	if app.player.Uploader == "" {
		return app.player.Title
	}
	return app.player.Title + "  ·  " + app.player.Uploader
}

// lyricsBody shows the state of the lookup, or the lyrics in the view the
// document's timing picks: timed documents are one list, and an untimed one
// the whole sheet. The list colors the current line's words only when the
// source timed them.
func (app *nativeApp) lyricsBody(c *ui.Context) {
	if app.lyricsTrack() == "" {
		app.lyricsMessage(c, locales.PlayerPickTrack(), false)
		return
	}
	switch {
	case app.lyricsLoading:
		app.lyricsMessage(c, locales.LyricsLoading(), false)
	case app.lyricsErr != nil:
		app.lyricsError(c)
	case app.lyricsDoc == nil:
		app.lyricsMessage(c, locales.LyricsLoading(), false)
	case len(app.lyricsDoc.Lines) == 0:
		app.lyricsMessage(c, locales.LyricsNotFound(), true)
	case !app.lyricsDoc.Timed:
		app.lyricsPlain(c, app.lyricsDoc)
	default:
		app.lyricsLines(c, app.lyricsDoc)
	}
}

// lyricsMessage shows one line in the middle of the panel, with a retry
// where trying again may help.
func (app *nativeApp) lyricsMessage(c *ui.Context, message string, retry bool) {
	ui.Column(c).Grow(1).FillWidth().Center().Gap(10).Children(func() {
		ui.Text(c, message).FontSize(13).TextAlign(ui.Center).MaxWidth(300).TextColor(app.theme.TextMuted)
		if retry {
			if button := ui.Button(c, locales.LyricsRetry()); button.Clicked() {
				app.retryLyrics()
			}
		}
	})
}

// lyricsError shows the lookup's failure as the message it deserves: the
// monthly limit, a track without lyrics, a channel video, a missing login,
// or a session niconico no longer accepts.
func (app *nativeApp) lyricsError(c *ui.Context) {
	message := locales.LyricsFailed()
	switch {
	case errors.Is(app.lyricsErr, lyrics.ErrNotFound):
		// The official source knows the work has none; the fallback
		// databases only know they did not find it.
		if app.lyricsSource == lyrics.SourceNiconico {
			message = locales.LyricsNotFound()
		} else {
			message = locales.LyricsNotFoundSource()
		}
	case errors.Is(app.lyricsErr, lyrics.ErrLogin):
		message = locales.LyricsLogin()
	case errors.Is(app.lyricsErr, lyrics.ErrLimit):
		message = locales.LyricsLimit()
	case errors.Is(app.lyricsErr, lyrics.ErrForbidden):
		message = locales.LyricsForbidden()
	case errors.Is(app.lyricsErr, lyrics.ErrUnauthorized):
		message = locales.ErrSessionExpired()
	}
	ui.Column(c).Grow(1).FillWidth().Center().Gap(12).Children(func() {
		ui.Text(c, message).FontSize(13).TextAlign(ui.Center).MaxWidth(320).TextColor(app.theme.TextMuted)
		ui.Row(c).Gap(10).Children(func() {
			if app.lyricsNeedLogin {
				if button := ui.PrimaryButton(c, locales.LoginWithNiconico()); button.Clicked() {
					app.openLogin()
				}
			}
			if button := ui.Button(c, locales.LyricsRetry()); button.Clicked() {
				app.retryLyrics()
			}
		})
	})
}

// lyricsPlain shows an untimed document: every line, as a sheet to read.
func (app *nativeApp) lyricsPlain(c *ui.Context, doc *lyrics.Doc) {
	ui.Column(c).Grow(1).FillWidth().Gap(8).Children(func() {
		ui.Text(c, locales.LyricsUntimed()).FontSize(11).TextColor(app.theme.TextMuted)
		app.lyricsWhole(c, doc)
	})
}

// lyricsPosition is the document's position: the player's moved by the
// card's offset, so a positive one makes the lines wait.
func (app *nativeApp) lyricsPosition() float64 {
	return app.player.PositionMS - app.lyricsOffsetMS
}

// lyricSpans colors one line to the position by the words its source timed.
// A line the source timed only as a whole keeps one color: the app never
// guesses a character-level split.
func (app *nativeApp) lyricSpans(line lyrics.Line) []ui.Span {
	done, rest := app.theme.Text, app.theme.TextMuted
	position := app.lyricsPosition()
	if len(line.Words) == 0 || wordsText(line.Words) != line.Text {
		return []ui.Span{{Text: line.Text, Color: done}}
	}
	spans := make([]ui.Span, 0, len(line.Words))
	for _, word := range line.Words {
		color := rest
		if position >= word.StartMS {
			color = done
		}
		spans = append(spans, ui.Span{Text: word.Text, Color: color})
	}
	return spans
}

// wordsText joins a source's words back into the line, to trust them only
// when they cover exactly its text.
func wordsText(words []lyrics.Word) string {
	var b strings.Builder
	for _, word := range words {
		b.WriteString(word.Text)
	}
	return b.String()
}

// lyricsLines is the line view: the document as a list, every line played so
// far white and the current one bold, the rest faint. The current line colors
// its words to the position when the source timed them; a line-timed
// document keeps the whole line white rather than guessing a split. The list
// glides to the current line as the track moves on.
func (app *nativeApp) lyricsLines(c *ui.Context, doc *lyrics.Doc) {
	// The rows' bounds and the viewport are the frame before's, drawn with
	// this offset: measure against it, not the offset this frame's glide sets.
	wasY := app.lyricsScroll.Y
	current := doc.LineAt(app.lyricsPosition())
	app.followLyrics(current)
	if cap(app.lyricsLineCenter) < len(doc.Lines) {
		app.lyricsLineCenter = make([]float32, len(doc.Lines))
	}
	app.lyricsLineCenter = app.lyricsLineCenter[:len(doc.Lines)]
	scroll := ui.Scroll(c).TrackScroll(&app.lyricsScroll).Grow(1)
	// The viewport's place is the frame before's, as the rows' are.
	if bounds := scroll.Bounds(); bounds.H > 0 {
		app.lyricsViewY, app.lyricsViewH = bounds.Y, bounds.H
	}
	scroll.Children(func() {
		ui.Column(c).FillWidth().Children(func() {
			for i := range doc.Lines {
				line := doc.Lines[i]
				row := ui.Button(c, "").Label(line.Text).FillWidth().Padding(10, 16).
					BorderWidth(0).Radius(8).Justify(ui.Start).AlignItems(ui.Start).
					Disabled(!app.player.Ready)
				if row.Hovered() {
					row.Background(app.theme.SurfaceHover)
				} else {
					row.Background(app.theme.Surface)
				}
				row.Children(func() {
					if i == current && len(line.Words) > 0 && wordsText(line.Words) == line.Text {
						ui.RichText(c, app.lyricSpans(line)...).FillWidth().FontSize(21).Bold()
						return
					}
					text := ui.Text(c, line.Text).FillWidth().FontSize(21)
					switch {
					case i == current:
						text.Bold().TextColor(app.theme.Text)
					case current >= 0 && i < current:
						text.TextColor(app.theme.Text)
					default:
						text.TextColor(app.theme.TextMuted)
					}
				})
				if row.Clicked() {
					app.seekLyrics(line)
				}
				app.measureLyricsLine(i, row, wasY)
			}
		})
	})
}

// measureLyricsLine records where a line sits in the scroll's content, so the
// follow can glide to it before the next frame lays the list out again. scrollY
// is the offset the bounds were drawn with.
func (app *nativeApp) measureLyricsLine(i int, row ui.Element, scrollY float32) {
	bounds := row.Bounds()
	if bounds.H == 0 || app.lyricsViewH == 0 {
		return
	}
	app.lyricsLineCenter[i] = bounds.Y + bounds.H/2 - app.lyricsViewY + scrollY
}

// lyricsWhole is the whole view: every line as a plain white sheet, with the
// scroll where the user leaves it rather than following the track.
func (app *nativeApp) lyricsWhole(c *ui.Context, doc *lyrics.Doc) {
	ui.Scroll(c).TrackScroll(&app.lyricsScroll).Grow(1).Children(func() {
		ui.Column(c).FillWidth().Children(func() {
			for i := range doc.Lines {
				ui.Box(c).FillWidth().Padding(9, 16).Children(func() {
					ui.Text(c, doc.Lines[i].Text).FontSize(19).TextColor(app.theme.Text)
				})
			}
		})
	})
}

// seekLyrics jumps the player to the line, a moment before its start so the
// opening is heard rather than clipped. A player still opening takes no seek,
// and an untimed line has nowhere to go.
func (app *nativeApp) seekLyrics(line lyrics.Line) {
	if !app.player.Ready {
		return
	}
	ms := max(0, line.StartMS+app.lyricsOffsetMS-lyricsSeekLeadMS)
	app.seekValue, app.seekSettling = ms, true
	app.sendPlayer(PlayerCommand{Action: "seek", PositionMS: ms})
}

// followLyrics glides the line playing to the middle of the line view, once
// per line, so a list the user is reading stays where they left it until the
// next line takes over.
func (app *nativeApp) followLyrics(current int) {
	if current < 0 || current >= len(app.lyricsLineCenter) || app.lyricsViewH <= 0 {
		return
	}
	track := app.lyricsTrack()
	if app.lyricsFollow == current && app.lyricsFollowVideo == track {
		return
	}
	center := app.lyricsLineCenter[current]
	if center <= 0 {
		// The line was not laid out yet: the next frame knows where it is.
		return
	}
	app.lyricsFollow, app.lyricsFollowVideo = current, track
	target := center - app.lyricsViewH/2
	target = max(0, min(target, app.lyricsScroll.MaxY))
	app.lyricsScroll.ScrollTo(0, target, lyricsScrollAnimation)
}

// lyricsOffsetStepMS is one press of the offset field's arrows or keys, and
// lyricsOffsetLimitMS how far the shift may go either way.
const (
	lyricsOffsetStepMS  = 100
	lyricsOffsetLimitMS = 10000
)

// lyricsFooter credits the source that answered and carries the timing
// shift: stepped in milliseconds, + moving every line later, saved in the
// lyrics cache so the same track and source come back in sync.
func (app *nativeApp) lyricsFooter(c *ui.Context) {
	doc := app.lyricsDoc
	if doc == nil {
		return
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
		if doc.Source != "" {
			ui.Text(c, locales.LyricsSource(app.lyricsSourceName(doc.Source))).FontSize(11).
				SingleLine().Ellipsis("…").Grow(1).TextColor(app.theme.TextMuted)
		} else {
			ui.Spacer(c)
		}
		if !doc.Timed {
			return
		}
		app.lyricsOffsetField(c)
		ui.Text(c, "ms").FontSize(11).TextColor(app.theme.TextMuted)
	})
}

// lyricsOffsetField is the shift as a number beside mygo's stepper: the
// arrows and the up and down keys move it, the stepper keeps the step, the
// limit, and the spin button's reading, and a click on the number takes the
// keyboard for it.
func (app *nativeApp) lyricsOffsetField(c *ui.Context) {
	ui.Row(c).Shrink(0).AlignItems(ui.Center).Background(app.theme.Surface).
		Border(1, app.theme.Border).Radius(6).Clip().Children(func() {
		value := ui.Text(c, strconv.Itoa(int(app.lyricsOffsetMS))).FontSize(14).
			Width(64).Padding(5, 10).TextAlign(ui.End).TextColor(app.theme.Text).Cursor(ui.CursorText)
		ui.Box(c).Width(1).Height(28).Background(app.theme.Border)
		stepper := ui.Stepper(c, &app.lyricsOffsetMS, -lyricsOffsetLimitMS, lyricsOffsetLimitMS, lyricsOffsetStepMS).
			Width(24).BorderWidth(0).Radius(0).Background(ui.RGBA(0, 0, 0, 0)).
			Label(locales.LyricsOffset()).Tooltip(locales.LyricsOffset())
		// The value is the field's reading, so a click on it takes the
		// keyboard as a field's click does.
		if value.Clicked() {
			stepper.Focus()
		}
		if stepper.Changed() {
			app.saveLyricsOffset()
		}
	})
}

// saveLyricsOffset keeps the card's shift in the lyrics cache, tied to the
// track, source, and lyrics it belongs to.
func (app *nativeApp) saveLyricsOffset() {
	doc := app.lyricsDoc
	if doc == nil || app.lyrics == nil {
		return
	}
	if err := app.lyrics.SetOffset(app.lyricsVideoID, doc.Source, app.lyricsID, int(app.lyricsOffsetMS)); err != nil {
		log.Printf("lyrics: saving the offset: %v", err)
	}
}

// lyricsSourceName is the display name of a source ID, the ID itself for a
// source the service does not list.
func (app *nativeApp) lyricsSourceName(id string) string {
	if app.lyrics != nil {
		for _, source := range app.lyrics.Sources() {
			if source.ID == id {
				return source.Name
			}
		}
	}
	return id
}

// lyricsTrack is the video the lyrics lookup asks by: its niconico video ID
// when playback knows it, else the mylist's watch ID.
func (app *nativeApp) lyricsTrack() string {
	if app.player.VideoID != "" {
		return app.player.VideoID
	}
	return app.player.WatchID
}

// syncLyrics starts the lookup for the track playing when the panel shows a
// document of another track, and tries again once a login the failure asked
// for has completed. A new track starts over from the automatic order.
func (app *nativeApp) syncLyrics() {
	if !app.lyricsOpen {
		return
	}
	track := app.lyricsTrack()
	if track == "" {
		return
	}
	if app.lyricsVideoID != track {
		app.lyricsSource = ""
		app.beginLyrics(track)
		return
	}
	if app.lyricsNeedLogin && app.loggedIn && !app.lyricsLoginWhenFailed {
		app.beginLyrics(track)
	}
}

// beginLyrics looks the track's lyrics up in the mode the card is in.
func (app *nativeApp) beginLyrics(track string) {
	app.beginLyricsSource(track, app.lyricsSource)
}

// beginLyricsSource looks the track's lyrics up in the background, under a
// token the next lookup invalidates, and shows the answer, the failure, or
// the login it asks for. An empty source follows the automatic priority
// order; any other asks that provider alone.
func (app *nativeApp) beginLyricsSource(track, source string) {
	if track == "" || app.lyrics == nil {
		return
	}
	app.lyricsSource = source
	app.lyricsVideoID = track
	app.lyricsDoc = nil
	app.lyricsID = ""
	app.lyricsOffsetMS = 0
	app.lyricsErr = nil
	app.lyricsNeedLogin = false
	app.lyricsLoginWhenFailed = false
	app.lyricsLoading = true
	if source == "" {
		app.lyricsTab = 0
	}
	app.lyricsFollow, app.lyricsFollowVideo = -1, ""
	// A new document starts at its top, with no line measured yet.
	app.lyricsScroll = ui.ScrollState{}
	app.lyricsLineCenter = nil
	app.lyricsToken++
	token := app.lyricsToken
	query := lyrics.Query{
		VideoID:        track,
		Title:          app.player.Title,
		Artist:         app.player.Uploader,
		DurationMS:     app.player.DurationMS,
		HasLyrics:      app.player.HasLyrics,
		HasLyricsKnown: app.player.LyricsKnown,
	}
	service := app.lyrics
	go func() {
		var doc lyrics.Doc
		var err error
		if source == "" {
			doc, err = service.Load(query)
		} else {
			doc, err = service.LoadSource(source, query)
		}
		if err == nil {
			leadFirstLyricLine(&doc)
		}
		app.update(func() {
			if token != app.lyricsToken {
				return
			}
			app.lyricsLoading = false
			if err != nil {
				app.lyricsErr = err
				switch {
				case errors.Is(err, lyrics.ErrUnauthorized):
					app.sessionExpired()
					app.lyricsNeedLogin = true
					app.lyricsLoginWhenFailed = app.loggedIn
				case errors.Is(err, lyrics.ErrLogin):
					app.lyricsNeedLogin = true
					app.lyricsLoginWhenFailed = app.loggedIn
				}
				return
			}
			app.lyricsDoc = &doc
			app.lyricsID = doc.ID()
			app.lyricsOffsetMS = float64(app.lyrics.Offset(track, doc.Source, app.lyricsID))
			if source == "" {
				app.lyricsTab = app.lyricsTabOf(doc.Source)
			}
		})
	}()
}

// lyricsTabOf is the tab of the provider that answered.
func (app *nativeApp) lyricsTabOf(id string) int {
	if app.lyrics != nil {
		for i, source := range app.lyrics.Sources() {
			if source.ID == id {
				return i
			}
		}
	}
	return app.lyricsTab
}

// retryLyrics asks again for the track playing, after a failure or a login.
func (app *nativeApp) retryLyrics() {
	if track := app.lyricsTrack(); track != "" {
		app.beginLyrics(track)
	}
}

// toggleLyrics opens or closes the lyrics card. Opening it ensures the
// queue is there: the card floats over it.
func (app *nativeApp) toggleLyrics() {
	app.lyricsOpen = !app.lyricsOpen
	if app.lyricsOpen {
		app.queueOpen = true
	}
}
