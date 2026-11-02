package lyrics

import "testing"

func TestParseLRCReadsTimestampsAndDropsCredits(t *testing.T) {
	lines := parseLRC("\uFEFF[00:00.000] 作词 : 黒うさP\n" +
		"[00:00.000] 作曲 : 黒うさP\n" +
		"[00:01.00] 一行目\n" +
		"[00:03.500][00:10.250] 二行目\n" +
		"[00:05.00]\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %#v; want three cues", lines)
	}
	if lines[0].Text != "一行目" || lines[0].StartMS != 1000 || lines[0].EndMS != 3500 {
		t.Fatalf("first line = %#v", lines[0])
	}
	if lines[1].Text != "二行目" || lines[1].StartMS != 3500 || lines[1].EndMS != 10250 {
		t.Fatalf("second line = %#v", lines[1])
	}
	if lines[2].Text != "二行目" || lines[2].StartMS != 10250 {
		t.Fatalf("third line = %#v", lines[2])
	}
}

func TestParseKRCReadsWordsAndSkipsMetadata(t *testing.T) {
	lines := parseKRC("[ti:千本桜]\n" +
		"[0,2000]<0,500,0>千<500,500,0>本<1000,1000,0>桜\n" +
		"[100,100]词：黒うさP\n" +
		"[2000,1500]<0,750,0>夜<750,750,0>ニ")
	if len(lines) != 2 {
		t.Fatalf("lines = %#v; want two", lines)
	}
	first := lines[0]
	if first.Text != "千本桜" || first.StartMS != 0 || first.EndMS != 2000 {
		t.Fatalf("first line = %#v", first)
	}
	if len(first.Words) != 3 {
		t.Fatalf("words = %#v; want three", first.Words)
	}
	if first.Words[0].StartMS != 0 || first.Words[0].EndMS != 500 ||
		first.Words[1].StartMS != 500 || first.Words[2].EndMS != 2000 {
		t.Fatalf("word timing = %#v", first.Words)
	}
	if lines[1].Text != "夜ニ" || lines[1].StartMS != 2000 || lines[1].Words[1].StartMS != 2750 {
		t.Fatalf("second line = %#v", lines[1])
	}
}

func TestDropKRCHeader(t *testing.T) {
	lines := []Line{
		{Text: "千本桜 (千本樱) - 初音ミク (初音未来)", StartMS: 0},
		{Text: "大胆不敵に", StartMS: 32119},
	}
	got := dropKRCHeader(lines, "千本桜 (千本樱)", "初音ミク (初音未来)")
	if len(got) != 1 || got[0].Text != "大胆不敵に" {
		t.Fatalf("lines = %#v; want the title header dropped", got)
	}
	// A first lyric that only repeats the title is kept.
	repeated := []Line{{Text: "千本桜", StartMS: 0}}
	if got := dropKRCHeader(repeated, "千本桜", "初音ミク"); len(got) != 1 {
		t.Fatalf("a title-only first lyric was dropped: %#v", got)
	}
}
