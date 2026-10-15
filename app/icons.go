package main

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

// App icons come from Lucide (ISC, Feather-derived ones MIT). See
// THIRD_PARTY_NOTICES.md. Parsed once at startup, not per frame.
//
//go:embed icons/play.svg
var playSVG []byte

//go:embed icons/pause.svg
var pauseSVG []byte

//go:embed icons/skip-back.svg
var skipBackSVG []byte

//go:embed icons/skip-forward.svg
var skipForwardSVG []byte

//go:embed icons/repeat.svg
var repeatSVG []byte

//go:embed icons/shuffle.svg
var shuffleSVG []byte

//go:embed icons/volume-2.svg
var volumeSVG []byte

//go:embed icons/volume-x.svg
var volumeMutedSVG []byte

//go:embed icons/menu.svg
var menuSVG []byte

//go:embed icons/plus.svg
var plusSVG []byte

//go:embed icons/queue.svg
var queueSVG []byte

//go:embed icons/ellipsis-vertical.svg
var ellipsisSVG []byte

//go:embed icons/pin.svg
var pinSVG []byte

//go:embed icons/lock.svg
var lockSVG []byte

//go:embed icons/trash-2.svg
var trashSVG []byte

//go:embed icons/house.svg
var houseSVG []byte

//go:embed icons/log-in.svg
var logInSVG []byte

//go:embed icons/log-out.svg
var logOutSVG []byte

//go:embed icons/layers-2.svg
var layersSVG []byte

var (
	iconPlay        = ui.MustParseSVG(playSVG)
	iconPause       = ui.MustParseSVG(pauseSVG)
	iconSkipBack    = ui.MustParseSVG(skipBackSVG)
	iconSkipForward = ui.MustParseSVG(skipForwardSVG)
	iconRepeat      = ui.MustParseSVG(repeatSVG)
	iconShuffle     = ui.MustParseSVG(shuffleSVG)
	iconVolume      = ui.MustParseSVG(volumeSVG)
	iconVolumeMuted = ui.MustParseSVG(volumeMutedSVG)
	iconMenu        = ui.MustParseSVG(menuSVG)
	iconPlus        = ui.MustParseSVG(plusSVG)
	iconQueue       = ui.MustParseSVG(queueSVG)
	iconMore        = ui.MustParseSVG(ellipsisSVG)
	iconPin         = ui.MustParseSVG(pinSVG)
	iconLock        = ui.MustParseSVG(lockSVG)
	iconTrash       = ui.MustParseSVG(trashSVG)
	iconHome        = ui.MustParseSVG(houseSVG)
	iconLogIn       = ui.MustParseSVG(logInSVG)
	iconLogOut      = ui.MustParseSVG(logOutSVG)
	iconLayers      = ui.MustParseSVG(layersSVG)
)
