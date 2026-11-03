package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestAppFontIsRegistered checks the bundled font reaches the text system, and
// that the app's theme and its card's ask for it: a font that fails to
// register would otherwise show as the system's without a word.
func TestAppFontIsRegistered(t *testing.T) {
	registered := false
	for _, f := range ui.FontFamilies() {
		if f.Name == appFontFamily {
			registered = f.Registered
		}
	}
	if !registered {
		t.Fatalf("%s is not registered", appFontFamily)
	}
	app := newNativeApp(NewMylist())
	if app.theme.Font != appFontFamily || app.cardTheme.Font != appFontFamily {
		t.Fatalf("the theme asks for %q/%q; want %q", app.theme.Font, app.cardTheme.Font, appFontFamily)
	}
}
