package main

import (
	"os"
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// TestMain pins the language the views say their messages in: before the app
// runs, i18n.Locale would otherwise be the machine's or MYGO_LOCALE's, and the
// tests below assert the Japanese labels. Other languages are tested through
// locales.In.
func TestMain(m *testing.M) {
	i18n.SetLocaleFunc(func() string { return "ja" })
	os.Exit(m.Run())
}

// TestCatalogsSaySongCounts pins the plural forms the UI asks for every
// frame, and the fallback of a language the app does not ship.
func TestCatalogsSaySongCounts(t *testing.T) {
	cases := []struct {
		name  string
		got   string
		want  string
	}{
		{"ja 3", locales.In("ja").QueueSongCount(3), "3曲"},
		{"en 1", locales.In("en").QueueSongCount(1), "1 song"},
		{"en 3", locales.In("en").QueueSongCount(3), "3 songs"},
		{"de falls back to the source", locales.In("de").NavHome(), "ホーム"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q; want %q", tc.name, tc.got, tc.want)
		}
	}
}
