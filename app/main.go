package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mylist := NewMylist()
	// Keep the window's state in VocaBox's own config directory, beside
	// settings.json, rather than mygo's default store.
	mygo.App.SetWindowStateStore(windowStateStore{mylist: mylist})
	app := newNativeApp(mylist)
	app.playback = newPlayback(app)
	if saved, err := mylist.ListSaved(); err != nil {
		app.setError(err)
	} else {
		app.saved = saved
	}
	if settings, err := mylist.LoadSettings(); err != nil {
		log.Printf("settings: %v", err)
	} else {
		// The engine owns the queue's modes: hand the saved preferences over
		// before the first list arrives.
		app.playback.queue.setShuffle(settings.Shuffle)
		app.playback.queue.repeat = settings.Repeat
		app.player.Volume = settings.Volume
		app.player.Shuffle = settings.Shuffle
		app.player.Repeat = settings.Repeat
	}

	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		app.playback.shutdown()
		if err := mylist.SaveSettings(Settings{
			Volume:  app.player.Volume,
			Shuffle: app.player.Shuffle,
			Repeat:  app.player.Repeat,
		}); err != nil {
			log.Printf("settings: %v", err)
		}
	})

	mygo.App.WhenReady(func() {
		app.window = mygo.NewWindow(mygo.WindowOptions{
			Title:           "VocaBox",
			StateKey:        "main",
			Width:           1120,
			Height:          760,
			MinWidth:        760,
			MinHeight:       560,
			BackgroundColor: "#121217",
			Content:         ui.View(app.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
