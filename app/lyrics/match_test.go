package lyrics

import "testing"

func TestCoreTitle(t *testing.T) {
	for _, c := range []struct {
		in, want string
	}{
		{"千本桜", "千本桜"},
		{"千本桜 (Game Version)", "千本桜"},
		{"千本桜（千本樱）", "千本桜"},
		{"DECO*27 - モニタリング feat. 初音ミク", "DECO*27 - モニタリング"},
		{"【初音ミク】千本桜【オリジナル曲】", "千本桜"},
		{"モニタリング ft. 初音ミク", "モニタリング"},
		{"【歌詞】", "【歌詞】"},
	} {
		if got := coreTitle(c.in); got != c.want {
			t.Errorf("coreTitle(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestSearchTitle(t *testing.T) {
	for _, c := range []struct {
		in, want string
	}{
		{"DECO*27 - モニタリング feat. 初音ミク", "モニタリング"},
		{"千本桜 / 黒うさP", "千本桜"},
		{"千本桜", "千本桜"},
		{"初音ミク－千本桜", "千本桜"},
	} {
		if got := searchTitle(c.in); got != c.want {
			t.Errorf("searchTitle(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestRankRejectsTheWrongCut(t *testing.T) {
	q := Query{Title: "千本桜", Artist: "黒うさP", DurationMS: 242000}
	if _, ok := rank(q, "千本桜", "初音ミク", 242000); !ok {
		t.Fatal("the full version was rejected")
	}
	if _, ok := rank(q, "千本桜 (Game Version)", "初音ミク", 218000); ok {
		t.Fatal("another cut passed the duration check")
	}
	if _, ok := rank(q, "完全に別の曲", "初音ミク", 242000); ok {
		t.Fatal("another song passed the title check")
	}
}

// TestRankAcceptsAnotherPressingWithTheArtist checks the wider 15-second
// window: another pressing of the same song is allowed when the artist
// confirms it, and a cover that is only close in length is not.
func TestRankAcceptsAnotherPressingWithTheArtist(t *testing.T) {
	q := Query{Title: "千本桜", Artist: "黒うさP", DurationMS: 242000}
	if _, ok := rank(q, "千本桜", "黒うさP 初音ミク", 252000); !ok {
		t.Fatal("another pressing with the artist was rejected")
	}
	if _, ok := rank(q, "千本桜", "Ado", 252000); ok {
		t.Fatal("a cover ten seconds off without the artist was accepted")
	}
}

func TestRankRejectsKaraokeTitles(t *testing.T) {
	q := Query{Title: "千本桜", Artist: "黒うさP", DurationMS: 242000}
	for _, title := range []string{
		"千本桜 (オリジナルカラオケ)",
		"千本桜 (Instrumental)",
		"千本桜 (纯音乐)",
	} {
		if _, ok := rank(q, title, "黒うさP", 242000); ok {
			t.Errorf("%q passed as the song's lyrics", title)
		}
	}
}

func TestRankWithoutDurationNeedsAnExactTitle(t *testing.T) {
	q := Query{Title: "千本桜", DurationMS: 0}
	if _, ok := rank(q, "千本桜", "", 0); !ok {
		t.Fatal("an exact title without a duration was rejected")
	}
	if _, ok := rank(q, "千本桜 夜ニ紛レ", "", 0); ok {
		t.Fatal("a partial title without a duration was accepted")
	}
}

func TestRankPrefersTheMatchingArtist(t *testing.T) {
	q := Query{Title: "DECO*27 - モニタリング feat. 初音ミク", Artist: "DECO*27", DurationMS: 202000}
	same, ok := rank(q, "モニタリング", "DECO*27", 202000)
	if !ok {
		t.Fatal("the matching candidate was rejected")
	}
	cover, ok := rank(q, "モニタリング", "Ado", 202000)
	if !ok {
		t.Fatal("a same-length candidate with another artist was rejected")
	}
	if same <= cover {
		t.Fatalf("scores = %d and %d; want the matching artist first", same, cover)
	}
}
