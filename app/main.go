package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

func main() {
	// A second launch hands its command line to the instance already up,
	// which shows its window: another tray icon and another player would
	// otherwise start beside the one still playing.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	// The update window, in native UI as the rest of the app is: it checks in
	// the background once a day, and the settings ask it to check now.
	mygo.Use(native.Plugin)
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
		if err := app.nico.SetSession(cookie); err != nil {
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
		app.setAudioQuality(audioQualityKbps(settings.DownloadQuality))
		app.discordOn = settings.DiscordRPC
	}
	// The mylists the user keeps offline start fetching in the background,
	// one track at a time, at the quality the settings named.
	app.startAudioDownloads()

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
			Volume:          app.player.Volume,
			Shuffle:         app.player.Shuffle,
			Repeat:          app.player.Repeat,
			DownloadQuality: audioQualityValue(int(app.audioKbps.Load())),
			DiscordRPC:      app.discordOn,
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
		// The work the background handed over before the window existed runs
		// here, on the UI thread, ahead of anything the window posts.
		app.runHeld()
		// The mylists the user keeps offline start fetching in the background,
		// one track at a time, at the quality the settings named. The window
		// exists first, so the progress the worker reports reaches the UI
		// thread rather than writing the view's state from its own.
		app.startAudioDownloads()
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
