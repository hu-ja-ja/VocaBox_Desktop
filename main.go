package main

import (
	"log"

	"github.com/egoist/mygo"
)

func main() {
	mygo.Bind(NewMylist())

	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:           "VocaBox",
			Width:           960,
			Height:          640,
			MinWidth:        480,
			MinHeight:       360,
			BackgroundColor: "light-dark(#f6f7f9, #0f1115)",
			// The frontend: devUrl during `mygo dev`, frontendDist embedded by `mygo build`.
			URL: "/",
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
