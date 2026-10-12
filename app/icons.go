package main

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

// Player icons come from Lucide (ISC, Feather-derived ones MIT). See
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

//go:embed icons/menu.svg
var menuSVG []byte

//go:embed icons/plus.svg
var plusSVG []byte

var (
	iconPlay        = ui.MustParseSVG(playSVG)
	iconPause       = ui.MustParseSVG(pauseSVG)
	iconSkipBack    = ui.MustParseSVG(skipBackSVG)
	iconSkipForward = ui.MustParseSVG(skipForwardSVG)
	iconRepeat      = ui.MustParseSVG(repeatSVG)
	iconShuffle     = ui.MustParseSVG(shuffleSVG)
	iconVolume      = ui.MustParseSVG(volumeSVG)
	iconMenu        = ui.MustParseSVG(menuSVG)
	iconPlus        = ui.MustParseSVG(plusSVG)
)
