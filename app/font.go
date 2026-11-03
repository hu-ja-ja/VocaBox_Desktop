package main

import (
	_ "embed"
	"log"

	"github.com/egoist/mygo/ui"
)

// appFontFamily is the family the app's bundled font is registered under, and
// the one the theme asks for.
const appFontFamily = "M PLUS 1"

// The interface font: M PLUS 1, the variable weight axis included, so the
// theme's bold text takes the real bold rather than a synthetic one. See
// THIRD_PARTY_NOTICES.md.
//
//go:embed fonts/MPLUS1-Variable.ttf
var appFont []byte

func init() {
	// A font the platform cannot add leaves the system's, which the theme
	// falls back to on its own.
	if err := ui.RegisterFont(appFont, appFontFamily); err != nil {
		log.Printf("font: %v", err)
	}
}
