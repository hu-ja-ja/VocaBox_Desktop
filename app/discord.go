package main

import (
	"fmt"
	"time"

	"github.com/hu-ja-ja/VocaBox_Desktop/app/discord"
)

// The Discord Rich Presence: the track playing, shown on the user's Discord
// profile the way WSA_RPC_Bridge shows it. The client lives in app/discord;
// the app decides what the card says and when it changes.

// discordClientID is VocaBox's Discord application, which names and pictures
// the card. Discord asks for one on every connection.
const discordClientID = "1560608108722524272"

// discordActivityListening is the activity type the card shows, as
// WSA_RPC_Bridge shows it: the user listening to the track.
const discordActivityListening = 2

// discordResendTick is the step the resend loop takes, and the intervals are
// whole multiples of it, so one loop serves both. discordResendInterval is how
// often a playing presence is sent again unchanged: a Discord client that
// starts after the app hears of the track playing only on the next send.
// discordPausedResendInterval is how often a paused card is re-pinned: Discord
// counts its elapsed time up itself and cannot be told to freeze, so only a
// fresh start of now minus the position keeps the paused position on show.
const (
	discordResendTick           = time.Second
	discordResendInterval       = 5 * time.Second
	discordPausedResendInterval = 3 * time.Second
	discordResendEvery          = int(discordResendInterval / discordResendTick)
	discordPausedResendEvery    = int(discordPausedResendInterval / discordResendTick)
)

// discordActivity is the card Discord shows, as the client names it.
type discordActivity = discord.Activity

type discordAssets = discord.Assets

type discordTimestamps = discord.Timestamps

// discordPresence is the card the track playing shows, the shape of
// WSA_RPC_Bridge's: the track names the card and reads under state, the app
// reads under details, the artwork and its uploader fill the large image.
// The timer runs from the position reached whether the track plays or is
// paused, so a paused card shows that position rather than the count Discord
// makes up from the activity's start.
func discordPresence(st playerState) discordActivity {
	activity := discordActivity{
		Name:    st.Title,
		Type:    discordActivityListening,
		Details: "VocaBox",
		State:   st.Title,
	}
	if st.ThumbURL != "" {
		activity.Assets = &discordAssets{LargeImage: st.ThumbURL, LargeText: st.Uploader}
	}
	start := time.Now().Unix() - int64(st.PositionMS/1000)
	activity.Timestamps = &discordTimestamps{Start: start}
	if st.DurationMS > 0 {
		activity.Timestamps.End = start + int64(st.DurationMS/1000)
	}
	return activity
}

// syncDiscord hands the track playing to Discord: at once when it changed,
// and on force from the periodic resend and the settings toggle.
func (p *playback) syncDiscord(force bool) {
	if !p.app.discordOn || p.app.discord == nil {
		return
	}
	st := p.app.player
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%v\x00%d\x00%d",
		st.Title, st.Uploader, st.ThumbURL, p.wantPlaying, int64(st.PositionMS/1000), int64(st.DurationMS/1000))
	if !force && key == p.discordKey {
		return
	}
	p.discordKey = key
	if st.Title == "" {
		// Nothing plays: the card comes off with the connection.
		p.app.discord.Disconnect()
		return
	}
	p.app.discord.Update(discordPresence(st))
}

// resendDiscord keeps Discord in step with the app: a client that starts or
// restarts while a track plays hears of it within discordResendInterval even
// though the app only sends on a change, and a paused card is re-pinned on
// its own interval, since Discord keeps counting its elapsed time up.
func (p *playback) resendDiscord() {
	for tick := 1; ; tick++ {
		time.Sleep(discordResendTick)
		n := tick
		p.post(func() {
			if p.app.discordOn && p.app.player.Title != "" && discordResendDue(n, p.wantPlaying) {
				p.syncDiscord(true)
			}
		})
	}
}

// discordResendDue reports whether the tick numbered n is due a resend: every
// discordPausedResendEvery ticks while paused, so the paused position is
// re-pinned, and every discordResendEvery ticks while playing, which needs
// only the keepalive.
func discordResendDue(n int, playing bool) bool {
	if playing {
		return n%discordResendEvery == 0
	}
	return n%discordPausedResendEvery == 0
}

// setDiscordRPC turns the presence on or off from the settings: on shows the
// track playing at once, off takes the card off the profile.
func (app *nativeApp) setDiscordRPC(enabled bool) {
	app.discordOn = enabled
	if !enabled {
		if app.discord != nil {
			app.discord.Disconnect()
		}
		return
	}
	if app.playback != nil {
		app.playback.syncDiscord(true)
	}
}

// discordConn is the app's presence client, which newNativeApp wires and a
// test stands in for.
type discordConn interface {
	Update(discordActivity)
	Disconnect()
}
