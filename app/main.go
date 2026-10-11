package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mylist := NewMylist()
	app := newNativeApp(mylist)
	if saved, err := mylist.ListSaved(); err != nil {
		app.setError(err)
	} else {
		app.saved = saved
	}
	mygo.Bind(mylist, &PlayerBridge{app: app})

	mygo.App.WhenReady(func() {
		app.window = mygo.NewWindow(mygo.WindowOptions{
			Title:           "VocaBox",
			Width:           1120,
			Height:          760,
			MinWidth:        760,
			MinHeight:       560,
			BackgroundColor: "#121217",
			Content:         ui.View(app.view),
		})
		player, err := app.window.NewWebView(mygo.WebViewOptions{URL: "/player.html", BackgroundColor: "#121217"})
		if err != nil {
			log.Printf("player webview: %v", err)
			app.playerError = "プレイヤーを初期化できませんでした。"
			app.window.Invalidate()
			return
		}
		app.playerView = player
		app.window.Invalidate()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
