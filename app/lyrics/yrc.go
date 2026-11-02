package lyrics

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// parseYRC reads NetEase's word-timed YRC: JSON credit lines around timed
// lines whose words carry absolute millisecond positions. Unlike KRC's word
// offsets, a YRC word's start is already the track's position.
func parseYRC(text string) []Line {
	text = strings.TrimPrefix(text, "\uFEFF")
	var lines []Line
	for _, raw := range strings.Split(text, "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, "{") {
			continue
		}
		m := timedLineRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		start, _ := strconv.ParseFloat(m[1], 64)
		duration, _ := strconv.ParseFloat(m[2], 64)
		words, lineText := parseYRCWords(m[3])
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

// parseYRCWords splits one YRC line body into its words. A "(" followed by a
// digit starts a word timestamp; any other "(" is the lyric's own text.
// Full-width parentheses and corner brackets carry ruby and translation
// blocks some uploaders add: they are dropped with their contents, so the
// words that remain are the plain lyric and still join into the line.
func parseYRCWords(body string) ([]Word, string) {
	var words []Word
	var text, current strings.Builder
	var wordStart, wordDuration float64
	var ruby, translation int
	flush := func() {
		if current.Len() == 0 {
			return
		}
		value := current.String()
		words = append(words, Word{
			Text:    value,
			StartMS: wordStart,
			EndMS:   wordStart + wordDuration,
		})
		text.WriteString(value)
		current.Reset()
	}
	for i := 0; i < len(body); {
		if body[i] == '(' && i+1 < len(body) && body[i+1] >= '0' && body[i+1] <= '9' {
			close := strings.IndexByte(body[i:], ')')
			if close >= 0 {
				start, duration, ok := parseYRCTime(body[i+1 : i+close])
				if ok {
					flush()
					wordStart, wordDuration = start, duration
					i += close + 1
					continue
				}
			}
		}
		r, size := utf8.DecodeRuneInString(body[i:])
		i += size
		switch r {
		case '（':
			ruby++
			continue
		case '）':
			if ruby > 0 {
				ruby--
				continue
			}
		case '【':
			translation++
			continue
		case '】':
			if translation > 0 {
				translation--
				continue
			}
		}
		if ruby > 0 || translation > 0 {
			continue
		}
		current.WriteRune(r)
	}
	flush()
	return words, text.String()
}

// parseYRCTime reads a "(start,duration,0)" timestamp's first two numbers.
func parseYRCTime(token string) (start, duration float64, ok bool) {
	parts := strings.Split(token, ",")
	if len(parts) < 2 {
		return 0, 0, false
	}
	start, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0, 0, false
	}
	duration, err = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return 0, 0, false
	}
	return start, duration, true
}
