// Package lyrics fetches the lyrics of a track. It knows nothing about
// playback or the UI: a Provider answers a Query with a Doc, and the Service
// tries the providers in order, the local cache first. Adding a source is a
// Provider implementation and one entry in the service's provider list.
package lyrics

import "log"

// SourceNiconico names niconico's official lyrics, the source the app ships.
const SourceNiconico = "niconico"

// Word is one run of a line with timing of its own, when the source has word
// timing. Sources that only time whole lines leave it empty.
type Word struct {
	Text    string  `json:"text"`
	StartMS float64 `json:"startMs,omitempty"`
	EndMS   float64 `json:"endMs,omitempty"`
}

// Line is one block of lyrics: one line, or a block of lines the source timed
// as one unit. Text keeps the block's newlines. StartMS and EndMS are zero
// when the source has no timing.
type Line struct {
	Text    string  `json:"text"`
	StartMS float64 `json:"startMs,omitempty"`
	EndMS   float64 `json:"endMs,omitempty"`
	Words   []Word  `json:"words,omitempty"`
}

// Progress is how far the position is through the line: 0 before the line,
// 1 once it has ended, clamped in between. A line with no duration is
// complete as soon as it starts.
func (l Line) Progress(ms float64) float64 {
	if l.EndMS <= l.StartMS {
		if ms >= l.StartMS {
			return 1
		}
		return 0
	}
	return max(0, min(1, (ms-l.StartMS)/(l.EndMS-l.StartMS)))
}

// Doc is one track's lyrics, as a source answered them.
type Doc struct {
	Source string `json:"source,omitempty"`
	// Timed says the lines carry playback positions: the UI may follow them.
	Timed bool   `json:"timed,omitempty"`
	Lines []Line `json:"lines"`
}

// WordTimed reports whether the document carries word timing. A source with
// word timing shows the character view; one with line timing only shows the
// line view, and an untimed one the whole sheet.
func (d Doc) WordTimed() bool {
	for _, line := range d.Lines {
		if len(line.Words) > 0 {
			return true
		}
	}
	return false
}

// LineAt returns the index of the line playing at ms: the last line whose
// start is at or before it, -1 before the first line. A position in a gap
// between lines keeps the line that started last, so a lyric stays read
// during an instrumental break.
func (d Doc) LineAt(ms float64) int {
	at := -1
	for i, line := range d.Lines {
		if line.StartMS > ms {
			continue
		}
		if at < 0 || line.StartMS >= d.Lines[at].StartMS {
			at = i
		}
	}
	return at
}

// Query is what a source needs to look up one track. VideoID is the source's
// own ID for the video; the metadata lets a source that searches by title
// and artist match the track. HasLyrics and HasLyricsKnown are the watch
// API's answer, Known false when the track played from a saved file and the
// API was never asked.
type Query struct {
	VideoID        string
	Title          string
	Artist         string
	DurationMS     float64
	HasLyrics      bool
	HasLyricsKnown bool
}

// Provider fetches one track's lyrics from one source. Name is how the
// source is credited. ErrNotFound asks the service to try the next source;
// every other error does too, but the first one is what the user sees when
// no source answers.
type Provider interface {
	Name() string
	Fetch(Query) (Doc, error)
}

// Service is the app's one lyrics lookup: the local cache, then the
// providers in order.
type Service struct {
	providers []Provider
	cache     *cache
}

// NewService returns a service over providers, remembering answers in
// cacheDir. An empty cacheDir disables the cache.
func NewService(cacheDir string, providers ...Provider) *Service {
	s := &Service{providers: providers}
	if cacheDir != "" {
		s.cache = &cache{dir: cacheDir}
	}
	return s
}

// Load returns the track's lyrics: the cached answer when there is one, else
// the first provider that answers. A source's answer is cached, credited to
// it, and returned; errors try the next source, and the first error is
// returned when none answers.
func (s *Service) Load(q Query) (Doc, error) {
	if s == nil {
		return Doc{}, ErrFailed
	}
	if s.cache != nil {
		if doc, ok := s.cache.load(q.VideoID); ok {
			return doc, nil
		}
	}
	var first error
	for _, provider := range s.providers {
		doc, err := provider.Fetch(q)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if len(doc.Lines) == 0 {
			if first == nil {
				first = ErrNotFound
			}
			continue
		}
		doc.Source = provider.Name()
		if s.cache != nil {
			if err := s.cache.save(q.VideoID, doc); err != nil {
				log.Printf("lyrics: cache %s: %v", q.VideoID, err)
			}
		}
		return doc, nil
	}
	if first == nil {
		first = ErrNotFound
	}
	return Doc{}, first
}
