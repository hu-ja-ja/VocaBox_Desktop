package lyrics

import "testing"

func TestParseYRC(t *testing.T) {
	const text = `{"t":0,"c":[{"tx":"作词: "},{"tx":"誰か"}]}
[420,4440](420,1320,0)Lately(1740,0,0), (1740,570,0)I've (2310,600,0)been
{"t":5000,"c":[{"tx":"作曲: "},{"tx":"誰か"}]}`
	lines := parseYRC(text)
	if len(lines) != 1 {
		t.Fatalf("lines = %#v; want the credit JSON lines dropped", lines)
	}
	line := lines[0]
	if line.Text != "Lately, I've been" || line.StartMS != 420 || line.EndMS != 4860 {
		t.Fatalf("line = %#v", line)
	}
	if len(line.Words) != 4 {
		t.Fatalf("words = %#v; want four", line.Words)
	}
	if line.Words[0].Text != "Lately" || line.Words[0].StartMS != 420 || line.Words[0].EndMS != 1740 {
		t.Fatalf("first word = %#v", line.Words[0])
	}
	if line.Words[1].Text != ", " || line.Words[1].StartMS != 1740 || line.Words[1].EndMS != 1740 {
		t.Fatalf("second word = %#v", line.Words[1])
	}
	if line.Words[2].Text != "I've " || line.Words[2].StartMS != 1740 || line.Words[2].EndMS != 2310 {
		t.Fatalf("third word = %#v", line.Words[2])
	}
	if line.Words[3].Text != "been" || line.Words[3].StartMS != 2310 || line.Words[3].EndMS != 2910 {
		t.Fatalf("fourth word = %#v", line.Words[3])
	}
}

func TestParseYRCStripsRubyAndTranslation(t *testing.T) {
	const text = "[32210,2250](32210,90,0)大(32300,110,0)胆(32410,170,0)不(32580,370,0)敌" +
		"(32950,0,0)（(32950,0,0)だ(32950,0,0)い(32950,0,0)た(32950,0,0)ん(32950,0,0)ふ(32950,0,0)て(32950,0,0)き(32950,0,0)）" +
		"(32950,210,0)に(33160,230,0)ハ(33390,170,0)イ(33560,300,0)カ(33860,130,0)ラ(33990,380,0)革(34370,90,0)命" +
		"(34460,0,0)（(34460,0,0)か(34460,0,0)く(34460,0,0)め(34460,0,0)い(34460,0,0)）【(34460,0,0)英(34460,0,0)勇(34460,0,0)】"
	lines := parseYRC(text)
	if len(lines) != 1 {
		t.Fatalf("lines = %#v", lines)
	}
	line := lines[0]
	if line.Text != "大胆不敌にハイカラ革命" {
		t.Fatalf("text = %q; want ruby and translation dropped", line.Text)
	}
	if len(line.Words) != 11 {
		t.Fatalf("words = %#v; want the plain lyric's words", line.Words)
	}
	if line.Words[3].Text != "敌" || line.Words[3].StartMS != 32580 || line.Words[3].EndMS != 32950 {
		t.Fatalf("word before the ruby = %#v", line.Words[3])
	}
	if line.Words[4].Text != "に" || line.Words[4].StartMS != 32950 {
		t.Fatalf("word after the ruby = %#v", line.Words[4])
	}
}

func TestParseYRCKeepsLiteralParentheses(t *testing.T) {
	lines := parseYRC("[1000,1000](1000,500,0)Hello (world)")
	if len(lines) != 1 {
		t.Fatalf("lines = %#v", lines)
	}
	line := lines[0]
	if line.Text != "Hello (world)" {
		t.Fatalf("text = %q", line.Text)
	}
	if len(line.Words) != 1 || line.Words[0].Text != "Hello (world)" {
		t.Fatalf("words = %#v", line.Words)
	}
	if line.Words[0].StartMS != 1000 || line.Words[0].EndMS != 1500 {
		t.Fatalf("word timing = %#v", line.Words[0])
	}
}
