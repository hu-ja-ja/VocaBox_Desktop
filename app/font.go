package main

import (
	_ "embed"
	"log"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// The families the app's bundled fonts are registered under. appFontFamily is
// the one the theme asks for apart from a language's own Chinese font.
const (
	appFontFamily = "M PLUS 1"
	appFontHans   = "Noto Sans SC"
	appFontHant   = "Noto Sans TC"
)

// The interface fonts: M PLUS 1, the variable weight axis included, so the
// theme's bold text takes the real bold rather than a synthetic one, and the
// Chinese fonts the languages written in Chinese draw in. See
// THIRD_PARTY_NOTICES.md.
//
//go:embed fonts/MPLUS1-Variable.ttf
var appFont []byte

//go:embed fonts/NotoSansSC-Variable.ttf
var appFontHansData []byte

//go:embed fonts/NotoSansTC-Variable.ttf
var appFontHantData []byte

func init() {
	// A font the platform cannot add leaves the system's, which the theme
	// falls back to on its own.
	registerFont(appFont, appFontFamily)
	registerFont(appFontHansData, appFontHans)
	registerFont(appFontHantData, appFontHant)
}

func registerFont(data []byte, family string) {
	if err := ui.RegisterFont(data, family); err != nil {
		log.Printf("font: %v", err)
	}
}

// appFontFor is the family list the app draws in for the language it shows:
// that language's font first, so Chinese text takes Chinese glyph forms rather
// than Japanese ones, then the others, so a song title or a lyric in another
// script never falls to a system font.
func appFontFor(locale string) string {
	switch i18n.Match([]string{locale}, []string{"zh-Hans", "zh-Hant"}) {
	case "zh-Hans":
		return appFontHans + ", " + appFontFamily + ", " + appFontHant
	case "zh-Hant":
		return appFontHant + ", " + appFontFamily + ", " + appFontHans
	}
	return appFontFamily + ", " + appFontHans + ", " + appFontHant
}
