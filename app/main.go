package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	// A second launch hands its command line to the instance already up,
	// which shows its window: another tray icon and another player would
	// otherwise start beside the one still playing.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	mylist := NewMylist()
	// Keep the window's state in VocaBox's own config directory, beside
	// settings.json, rather than mygo's default store.
	mygo.App.SetWindowStateStore(windowStateStore{mylist: mylist})
	app := newNativeApp(mylist)
	app.playback = newPlayback(app)
	// A session kept from an earlier run signs the player in; an expired
	// one is noticed and forgotten when a track plays.
	if cookie, err := app.session.Load(); err != nil {
		log.Printf("session: %v", err)
	} else if cookie != "" {
		if err := app.playback.nico.SetSession(cookie); err != nil {
			log.Printf("session: %v", err)
		} else {
			app.loggedIn = true
			app.loadAccount()
			app.loadOwnMylists()
		}
	}
	if saved, err := mylist.ListSaved(); err != nil {
		app.setError(err)
	} else {
		app.saved = saved
	}
	if settings, err := mylist.LoadSettings(); err != nil {
		log.Printf("settings: %v", err)
	} else {
		// The engine owns the queue's modes: hand the saved preferences over
		// before the first mylist arrives.
		app.playback.queue.setShuffle(settings.Shuffle)
		app.playback.queue.repeat = settings.Repeat
		app.player.Volume = settings.Volume
		app.player.Shuffle = settings.Shuffle
		app.player.Repeat = settings.Repeat
	}

	mygo.App.OnSecondInstance(func([]string, string) { app.showWindow() })
	// A quit, asked for by the tray or by a signal, closes the window the
	// close listener would otherwise only hide.
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { app.quitting = true })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		// The icon goes with the app: the shell is not left showing one of
		// a process that is gone.
		if app.tray != nil {
			app.tray.Destroy()
		}
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
		// Closing the window leaves VocaBox playing in the tray; without a
		// tray to come back from it quits as it always did.
		app.window.OnClose(func(e *mygo.CloseEvent) {
			if !app.hideOnClose() {
				return
			}
			e.PreventDefault()
			app.window.Hide()
		})
		app.setupTray()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
