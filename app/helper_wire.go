package main

import "github.com/hu-ja-ja/VocaBox_Desktop/app/helper"

// The playback engine speaks the helper's protocol by these names, so the
// engine reads as it did while the protocol lives in its own package.
type helperEvent = helper.Event
type helperCommand = helper.Command

// startHelper launches the helper at path, as the engine's spawn does.
func startHelper(path, dir string, onEvent func(helperEvent), onExit func(error)) (*helper.Process, error) {
	return helper.Start(path, dir, onEvent, onExit)
}
