package main

import (
	"log"

	"github.com/hu-ja-ja/VocaBox_Desktop/app/lyrics"
)

// newLyricsService builds the app's lyrics lookup: the official niconico
// source, sharing the client whose cookie jar carries the session, and the
// app's own cache directory. Another source is added to this provider list
// beside the official one; the service tries them in order.
func newLyricsService(m *Mylist, n *Nico) *lyrics.Service {
	dir := ""
	if d, err := m.configPath("lyrics"); err != nil {
		log.Printf("lyrics: %v", err)
	} else {
		dir = d
	}
	return lyrics.NewService(dir,
		lyrics.NewNicoProvider(n.client, n.apiBase, n.userAgent, n.LoggedIn),
	)
}
