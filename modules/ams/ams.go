// Package ams plays VocaBox's tracks through Windows' own media stack:
// AdaptiveMediaSource for the signed HLS playlists of niconico DOMAND, and
// MediaPlayer for the sound, as the former AmsHelper process did through a
// pipe (see 要件/再生バックエンド調査-決定.md).
package ams

import (
	"io"
	"time"
)

// Player plays one track at a time.
//
// Every method returns once the work is queued, never when the media stack is
// done with it: the UI thread calls them, and the implementation must keep
// Windows off that thread. Close stops the track and releases the player.
//
//mygo:native
type Player interface {
	// Play loads a track and starts it, or installs it paused when
	// Load.Start is false. A load replaces the track playing.
	Play(load Load) error
	// Pause pauses the track playing, keeping its position.
	Pause() error
	// Resume plays a paused track.
	Resume() error
	// Seek moves the track playing to ms.
	Seek(ms float64) error
	// SetVolume sets the gain, from 0 to 1.
	SetVolume(v float64) error
	// Stop drops the track.
	Stop() error
	// SetPositionRate says how often the player reports its position, about
	// every every; zero stops the reports. The reports come as "position"
	// events.
	SetPositionRate(every time.Duration) error
	// OnEvent calls f for each event of the player until the subscription
	// is closed. f runs on the main thread.
	OnEvent(f func(Event)) io.Closer
	// Close stops the track and ends the player.
	Close() error
}

// Load is one track to play: a signed audio media playlist, or a saved file.
type Load struct {
	// URL is the signed audio media playlist, and File a saved track as a
	// file:// URI. File wins when both are set.
	URL  string
	File string
	// Cookie goes on every request of the media stack, as
	// "name=value; ..."; domand_bid is what the manifest and segments need.
	Cookie string
	// UserAgent is the app's, which niconico signs for.
	UserAgent string
	// Generation names this load: its events carry it back, so the app
	// drops what a replaced track reports late.
	Generation int
	// Start plays the load; false installs it paused.
	Start bool
}

// Event is what the player reports: opened, ended, failed or position.
type Event struct {
	// Event is "opened", "ended", "failed" or "position".
	Event string
	// DurationMS is the track's length, when the media stack knows it.
	DurationMS float64
	// MS is where the track is heard, a "position" event's.
	MS float64
	// Code, Ext and Msg describe a failure: the media stack's error name,
	// its HResult for the log, and its message for the user.
	Code string
	Ext  int
	Msg  string
	// Generation is the load the event belongs to.
	Generation int
}
