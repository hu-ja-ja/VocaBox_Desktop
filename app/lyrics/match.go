package lyrics

import (
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// featRe cuts the guest singer off a title: the part that names who sings
// it, not the song. It needs a space before the word, so a title that merely
// starts with "feat" keeps it.
var featRe = regexp.MustCompile(`(?i)\s+(?:feat\.?|ft\.?)\s*`)

// titleSeparators folds the dashes and slashes either site writes a title
// with, so the artist can be told from the song.
var titleSeparators = strings.NewReplacer("／", " / ", "－", " - ", "–", " - ", "—", " - ")

// karaokeRe names a pressing whose lyrics are not the song's: an off-vocal
// or instrumental copy often carries only a credit line, and would otherwise
// win on title and duration.
var karaokeRe = regexp.MustCompile(`(?i)カラオケ|オフボーカル|オフヴォーカル|off\s*vocal|instrumental|纯音乐|純音楽|伴奏|インスト`)

// normalize folds a title or artist to the characters that identify it:
// NFKC, lower case, and letters and digits only. Punctuation and spacing of
// either site's styling then cannot decide a match.
func normalize(s string) string {
	s = norm.NFKC.String(strings.ToLower(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// coreTitle drops the decorations a source's title carries: a parenthesized
// or bracketed part, and the feat. clause. A title that is only decoration
// keeps its original text.
func coreTitle(s string) string {
	cut := s
	if m := featRe.FindStringIndex(cut); m != nil {
		cut = cut[:m[0]]
	}
	var b strings.Builder
	depth := 0
	for _, r := range cut {
		switch r {
		case '(', '（', '[', '【':
			depth++
			continue
		case ')', '）', ']', '】':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 {
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return strings.TrimSpace(s)
	}
	return out
}

// searchTitle is the title to search by when the full title finds nothing:
// the decorations and the artist prefix are dropped.
func searchTitle(title string) string {
	core := titleSeparators.Replace(coreTitle(title))
	if i := strings.LastIndex(core, " - "); i >= 0 {
		if tail := strings.TrimSpace(core[i+3:]); tail != "" {
			return tail
		}
	}
	if i := strings.LastIndex(core, " / "); i >= 0 {
		if head := strings.TrimSpace(core[:i]); head != "" {
			return head
		}
	}
	return strings.TrimSpace(core)
}

// searchQueries returns the keyword rounds a source searches: the track's
// uploader and title, then the title freed of decorations.
func searchQueries(q Query) []string {
	title := strings.TrimSpace(q.Title)
	if title == "" {
		return nil
	}
	var out []string
	if artist := strings.TrimSpace(q.Artist); artist != "" {
		out = append(out, artist+" "+title)
	} else {
		out = append(out, title)
	}
	if cleaned := searchTitle(title); cleaned != "" && cleaned != title && !slices.Contains(out, cleaned) {
		out = append(out, cleaned)
	}
	return out
}

// rank scores a candidate against the track, reporting false for one that
// may not be shown at all: its title must match, and a known duration must
// be within five seconds. Title exactness, duration closeness and a matching
// artist only order the candidates that pass.
func rank(q Query, title, artist string, durationMS float64) (int, bool) {
	if karaokeRe.MatchString(title) {
		return 0, false
	}
	want, got := normalize(coreTitle(q.Title)), normalize(coreTitle(title))
	if want == "" || got == "" {
		return 0, false
	}
	score := 0
	switch {
	case want == got:
		score += 5
	case strings.Contains(want, got):
		score += 3
	case strings.Contains(got, want):
		score += 2
	default:
		return 0, false
	}
	durationDiff := -1.0
	if q.DurationMS > 0 && durationMS > 0 {
		durationDiff = math.Abs(q.DurationMS-durationMS) / 1000
		switch {
		case durationDiff <= 2:
			score += 2
		case durationDiff <= 5:
			score += 1
		case durationDiff <= 15:
			// Another pressing can run a little longer without being
			// another song. It earns no points and needs the artist.
		default:
			return 0, false
		}
	} else if score < 5 {
		// Without a duration to check, only an exact title is enough to
		// trust the answer.
		return 0, false
	}
	artistScore := 0
	if q.Artist != "" && artist != "" {
		hay := normalize(coreTitle(q.Title) + " " + q.Artist)
		na := normalize(coreTitle(artist))
		nq := normalize(q.Artist)
		switch {
		case na == "":
		case strings.Contains(hay, na):
			artistScore = 3
		case nq != "" && len([]rune(nq)) >= 2 && strings.Contains(na, nq):
			artistScore = 1
		}
	}
	if durationDiff > 5 && artistScore == 0 {
		// A longer gap without the artist to confirm it is more likely
		// another cut or a cover than the same song.
		return 0, false
	}
	return score + artistScore, true
}

// msFromDuration reads a duration some APIs answer in milliseconds and
// others in seconds.
func msFromDuration(v float64) float64 {
	if v > 0 && v < 1000 {
		return v * 1000
	}
	return v
}
