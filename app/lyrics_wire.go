package main

import (
	"log"
	"net/http"
	"time"

	"github.com/hu-ja-ja/VocaBox_Desktop/app/lyrics"
)

// lyricsTimeout bounds one source's answer, so a slow source does not hold
// the next one up for long.
const lyricsTimeout = 5 * time.Second

// newLyricsService builds the app's lyrics lookup: the official niconico
// source, then the fallback databases, sharing the app's cache directory.
// Another source is added to this provider list beside them; the service
// tries them in order.
func newLyricsService(m *Mylist, n *Nico) *lyrics.Service {
	dir := ""
	if d, err := m.configPath("lyrics"); err != nil {
		log.Printf("lyrics: %v", err)
	} else {
		dir = d
	}
	// The official source speaks with the session's cookies; the fallback
	// databases are strangers and get a client without them.
	official := &http.Client{Timeout: lyricsTimeout}
	if n.client != nil {
		official.Jar = n.client.Jar
	}
	external := &http.Client{Timeout: lyricsTimeout}
	return lyrics.NewService(dir,
		lyrics.NewNicoProvider(official, n.apiBase, n.userAgent, n.LoggedIn),
		lyrics.NewKugouProvider(external, "", "", n.userAgent),
		lyrics.NewNeteaseProvider(external, "", "", n.userAgent),
	)
}
