package lyrics

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// krcKey is the 16-byte key KuGou's KRC files are XORed with.
var krcKey = []byte{
	0x40, 0x47, 0x61, 0x77, 0x5E, 0x32, 0x74, 0x47,
	0x51, 0x36, 0x31, 0x2D, 0xCE, 0xD2, 0x6E, 0x69,
}

var (
	// timedLineRe is the header both KRC and YRC lines share: a start and a
	// duration in milliseconds, then the timed text.
	timedLineRe = regexp.MustCompile(`^\[(\d+),(\d+)\](.*)$`)
	krcWordRe   = regexp.MustCompile(`<(\d+),(\d+)(?:,-?\d+)?>([^<]*)`)
	krcTagRe    = regexp.MustCompile(`<[^>]*>`)
)

// decodeLyricsBody returns the text of a downloaded KuGou body: the decrypted
// KRC file when the content is one, else the LRC text it already is.
func decodeLyricsBody(content string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(content))
	if err != nil {
		return content, nil
	}
	if len(raw) < 4 || string(raw[:4]) != "krc1" {
		return string(raw), nil
	}
	raw = raw[4:]
	for i := range raw {
		raw[i] ^= krcKey[i%len(krcKey)]
	}
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	defer zr.Close()
	text, err := io.ReadAll(io.LimitReader(zr, maxSourceBody+1))
	if err != nil {
		return "", err
	}
	if len(text) > maxSourceBody {
		return "", ErrFailed
	}
	return string(text), nil
}

// parseKRC reads a decrypted KuGou KRC file: timed lines whose words carry
// their own offsets from the line's start.
func parseKRC(text string) []Line {
	text = strings.TrimPrefix(text, "\uFEFF")
	var lines []Line
	for _, raw := range strings.Split(text, "\n") {
		raw = strings.TrimSpace(raw)
		m := timedLineRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		start, _ := strconv.ParseFloat(m[1], 64)
		duration, _ := strconv.ParseFloat(m[2], 64)
		body := m[3]
		var words []Word
		for _, wm := range krcWordRe.FindAllStringSubmatch(body, -1) {
			offset, _ := strconv.ParseFloat(wm[1], 64)
			wordDuration, _ := strconv.ParseFloat(wm[2], 64)
			if wm[3] == "" {
				continue
			}
			words = append(words, Word{
				Text:    wm[3],
				StartMS: start + offset,
				EndMS:   start + offset + wordDuration,
			})
		}
		lineText := strings.TrimSpace(krcTagRe.ReplaceAllString(body, ""))
		if len(words) > 0 {
			var b strings.Builder
			for _, word := range words {
				b.WriteString(word.Text)
			}
			lineText = b.String()
		}
		if lineText == "" || lrcCreditRe.MatchString(lineText) || instrumentalRe.MatchString(lineText) {
			continue
		}
		lines = append(lines, Line{
			Text:    lineText,
			StartMS: start,
			EndMS:   start + duration,
			Words:   words,
		})
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].StartMS < lines[j].StartMS })
	return lines
}

// dropKRCHeader removes the timed title-and-singer line some KRC files start
// with: it names the same song and singer the search already matched, and is
// no lyric. A first line that only repeats the title is kept.
func dropKRCHeader(lines []Line, song, singer string) []Line {
	if len(lines) == 0 {
		return lines
	}
	title := normalize(coreTitle(song))
	if title == "" || !strings.Contains(normalize(coreTitle(lines[0].Text)), title) {
		return lines
	}
	if singerName := normalize(coreTitle(singer)); singerName != "" &&
		strings.Contains(normalize(coreTitle(lines[0].Text)), singerName) {
		return lines[1:]
	}
	if strings.Contains(lines[0].Text, " - ") || strings.Contains(lines[0].Text, "／") {
		return lines[1:]
	}
	return lines
}

var (
	lrcTagRe = regexp.MustCompile(`\[(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?\]`)
	// lrcCreditRe matches a credit line, which is no lyric.
	lrcCreditRe = regexp.MustCompile(`(?i)^(作词|作曲|编曲|编词|作詞|編曲|編詞|制作人|监制|混音|母带|和声|录音|配唱|出品|发行|词|曲|Lyricist|Composer|Arranger|Producer|Mixing|Mastering)\s*[:：]`)
	// instrumentalRe matches the line an instrumental pressing carries
	// instead of lyrics.
	instrumentalRe = regexp.MustCompile(`(?i)^(纯音乐|純音楽|instrumental|off\s*vocal|カラオケ|オフボーカル)`)
)

// parseLRC reads an LRC file: one or more timestamps a line, and the text
// after them. Credit lines and empty cues are dropped.
func parseLRC(text string) []Line {
	text = strings.TrimPrefix(text, "\uFEFF")
	var lines []Line
	for _, raw := range strings.Split(text, "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		matches := lrcTagRe.FindAllStringSubmatchIndex(raw, -1)
		if len(matches) == 0 {
			continue
		}
		body := strings.TrimSpace(raw[matches[len(matches)-1][1]:])
		if body == "" || lrcCreditRe.MatchString(body) || instrumentalRe.MatchString(body) {
			continue
		}
		for _, m := range matches {
			lines = append(lines, Line{Text: body, StartMS: lrcStartMS(raw, m)})
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].StartMS < lines[j].StartMS })
	for i := range lines {
		if i+1 < len(lines) && lines[i+1].StartMS > lines[i].StartMS {
			lines[i].EndMS = lines[i+1].StartMS
		}
	}
	return lines
}

// lrcStartMS reads one timestamp: minutes, seconds, and a fraction of a
// second written with one to three digits.
func lrcStartMS(s string, m []int) float64 {
	minutes, _ := strconv.ParseFloat(s[m[2]:m[3]], 64)
	seconds, _ := strconv.ParseFloat(s[m[4]:m[5]], 64)
	var millis float64
	if m[6] >= 0 {
		frac := s[m[6]:m[7]]
		value, _ := strconv.ParseFloat(frac, 64)
		switch len(frac) {
		case 1:
			millis = value * 100
		case 2:
			millis = value * 10
		default:
			millis = value
		}
	}
	return minutes*60000 + seconds*1000 + millis
}
