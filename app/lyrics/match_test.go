package lyrics

import "testing"

func TestCoreTitle(t *testing.T) {
	for _, c := range []struct {
		in, want string
	}{
		{"サンプル曲", "サンプル曲"},
		{"サンプル曲 (Game Version)", "サンプル曲"},
		{"サンプル曲（样本曲）", "サンプル曲"},
		{"作曲者P - テスト曲 feat. 歌手", "作曲者P - テスト曲"},
		{"【歌手】サンプル曲【オリジナル曲】", "サンプル曲"},
		{"テスト曲 ft. 歌手", "テスト曲"},
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
		{"作曲者P - テスト曲 feat. 歌手", "テスト曲"},
		{"サンプル曲 / 作者P", "サンプル曲"},
		{"サンプル曲", "サンプル曲"},
		{"歌手－サンプル曲", "サンプル曲"},
	} {
		if got := searchTitle(c.in); got != c.want {
			t.Errorf("searchTitle(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestRankRejectsTheWrongCut(t *testing.T) {
	q := Query{Title: "サンプル曲", Artist: "作者P", DurationMS: 242000}
	if _, ok := rank(q, "サンプル曲", "歌手", 242000); !ok {
		t.Fatal("the full version was rejected")
	}
	if _, ok := rank(q, "サンプル曲 (Game Version)", "歌手", 218000); ok {
		t.Fatal("another cut passed the duration check")
	}
	if _, ok := rank(q, "完全に別の曲", "歌手", 242000); ok {
		t.Fatal("another song passed the title check")
	}
}

// TestRankAcceptsAnotherPressingWithTheArtist checks the wider 15-second
// window: another pressing of the same song is allowed when the artist
// confirms it, and a cover that is only close in length is not.
func TestRankAcceptsAnotherPressingWithTheArtist(t *testing.T) {
	q := Query{Title: "サンプル曲", Artist: "作者P", DurationMS: 242000}
	if _, ok := rank(q, "サンプル曲", "作者P 歌手", 252000); !ok {
		t.Fatal("another pressing with the artist was rejected")
	}
	if _, ok := rank(q, "サンプル曲", "別の歌手", 252000); ok {
		t.Fatal("a cover ten seconds off without the artist was accepted")
	}
}

func TestRankRejectsKaraokeTitles(t *testing.T) {
	q := Query{Title: "サンプル曲", Artist: "作者P", DurationMS: 242000}
	for _, title := range []string{
		"サンプル曲 (オリジナルカラオケ)",
		"サンプル曲 (Instrumental)",
		"サンプル曲 (纯音乐)",
	} {
		if _, ok := rank(q, title, "作者P", 242000); ok {
			t.Errorf("%q passed as the song's lyrics", title)
		}
	}
}

func TestRankWithoutDurationNeedsAnExactTitle(t *testing.T) {
	q := Query{Title: "サンプル曲", DurationMS: 0}
	if _, ok := rank(q, "サンプル曲", "", 0); !ok {
		t.Fatal("an exact title without a duration was rejected")
	}
	if _, ok := rank(q, "サンプル曲 別バージョン", "", 0); ok {
		t.Fatal("a partial title without a duration was accepted")
	}
}

func TestRankPrefersTheMatchingArtist(t *testing.T) {
	q := Query{Title: "作曲者P - テスト曲 feat. 歌手", Artist: "作曲者P", DurationMS: 202000}
	same, ok := rank(q, "テスト曲", "作曲者P", 202000)
	if !ok {
		t.Fatal("the matching candidate was rejected")
	}
	cover, ok := rank(q, "テスト曲", "別の歌手", 202000)
	if !ok {
		t.Fatal("a same-length candidate with another artist was rejected")
	}
	if same <= cover {
		t.Fatalf("scores = %d and %d; want the matching artist first", same, cover)
	}
}
