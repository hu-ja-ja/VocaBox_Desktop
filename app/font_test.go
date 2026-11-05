package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestAppFontsAreRegistered checks the bundled fonts reach the text system, and
// that the app's theme and its card's ask for them: a font that fails to
// register would otherwise show as the system's without a word.
func TestAppFontsAreRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, f := range ui.FontFamilies() {
		if f.Registered {
			registered[f.Name] = true
		}
	}
	for _, name := range []string{appFontFamily, appFontHans, appFontHant} {
		if !registered[name] {
			t.Errorf("%s is not registered", name)
		}
	}
	app := newNativeApp(NewMylist())
	// TestMain pins the language the app shows to ja.
	if want := appFontFor("ja"); app.theme.Font != want || app.cardTheme.Font != want {
		t.Fatalf("the theme asks for %q/%q; want %q", app.theme.Font, app.cardTheme.Font, want)
	}
}

// TestAppFontFor checks a Chinese language draws in its own font first, so its
// text takes Chinese glyph forms, and that the other Chinese font comes before
// any system one, so a lyric of the other script does not fall to a font the
// app knows nothing about.
func TestAppFontFor(t *testing.T) {
	cases := []struct{ locale, want string }{
		{"", "M PLUS 1, Noto Sans SC, Noto Sans TC"},
		{"ja", "M PLUS 1, Noto Sans SC, Noto Sans TC"},
		{"en", "M PLUS 1, Noto Sans SC, Noto Sans TC"},
		{"zh", "Noto Sans SC, M PLUS 1, Noto Sans TC"},
		{"zh-CN", "Noto Sans SC, M PLUS 1, Noto Sans TC"},
		{"zh-Hans", "Noto Sans SC, M PLUS 1, Noto Sans TC"},
		{"zh-TW", "Noto Sans TC, M PLUS 1, Noto Sans SC"},
		{"zh-HK", "Noto Sans TC, M PLUS 1, Noto Sans SC"},
		{"zh-Hant", "Noto Sans TC, M PLUS 1, Noto Sans SC"},
	}
	for _, c := range cases {
		if got := appFontFor(c.locale); got != c.want {
			t.Errorf("appFontFor(%q) = %q; want %q", c.locale, got, c.want)
		}
	}
}
