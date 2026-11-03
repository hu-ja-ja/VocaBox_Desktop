// Package lyrics fetches the lyrics of a track. It knows nothing about
// playback or the UI: a Provider answers a Query with a Doc, and the Service
// tries the providers in order, the local cache first. Adding a source is a
// Provider implementation and one entry in the service's provider list.
package lyrics

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"time"
)

// The IDs the app's sources answer to. They are stable values: the cache and
// the UI's tabs ask by them, while the provider's Name is what the user reads.
const (
	// SourceNiconico names niconico's official lyrics.
	SourceNiconico = "niconico"
	// SourceKugou names the KuGou lyrics database.
	SourceKugou = "kugou"
	// SourceNetease names the NetEase Cloud Music lyrics database.
	SourceNetease = "netease"
)

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
	// Source is the ID of the provider that answered, not its display name.
	Source string `json:"source,omitempty"`
	// Timed says the lines carry playback positions: the UI may follow them.
	Timed bool   `json:"timed,omitempty"`
	Lines []Line `json:"lines"`
	// annotated says the source's text carried ruby or translation blocks
	// the parser stripped. The NetEase lookup prefers a cleaner answer and
	// the cache does not keep this marker.
	annotated bool
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

// ID names the document by its source and timing, not its words: an edit
// that only corrects the text keeps the timing shift, while re-timed lyrics
// get a new ID and start over. It is a short hex digest, stable across runs.
func (d Doc) ID() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%v|", d.Source, d.Timed)
	for _, line := range d.Lines {
		fmt.Fprintf(h, "%v,%v|", line.StartMS, line.EndMS)
		for _, word := range line.Words {
			fmt.Fprintf(h, "%v,%v|", word.StartMS, word.EndMS)
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
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
	// deadline is when the lookup asking gives up on the sources still to
	// try; the zero time never gives up.
	deadline time.Time
}

// lookupTimeout bounds one lookup across the sources, so a source that keeps
// timing out cannot hold the card on "loading" for minutes. A request already
// in flight may outlast it by its own client's timeout.
const lookupTimeout = 15 * time.Second

// errTimeout reports a lookup that spent its budget before a source answered.
// It says the lookup failed, not that the track has no lyrics.
var errTimeout = fmt.Errorf("%w: the lookup ran out of time", ErrFailed)

// withDeadline starts the lookup's budget of d.
func (q Query) withDeadline(d time.Duration) Query {
	q.deadline = time.Now().Add(d)
	return q
}

// expired reports whether the lookup has spent its budget.
func (q Query) expired() bool {
	return !q.deadline.IsZero() && time.Now().After(q.deadline)
}

// Provider fetches one track's lyrics from one source. ID is the stable name
// the cache and the UI ask by, and Name is how the source is credited and
// listed. ErrNotFound asks the service to try the next source; every other
// error does too, but the first one is what the user sees when no source
// answers.
type Provider interface {
	ID() string
	Name() string
	Fetch(Query) (Doc, error)
}

// Source is one provider as the UI lists it: ID asks for it, Name labels it
// and credits it.
type Source struct {
	ID   string
	Name string
}

// Service is the app's one lyrics lookup: the local cache, then the
// providers in order.
type Service struct {
	providers []Provider
	cache     *cache
	// timeout is how long one lookup may take; tests shorten it.
	timeout time.Duration
}

// NewService returns a service over providers, remembering answers in
// cacheDir. An empty cacheDir disables the cache.
func NewService(cacheDir string, providers ...Provider) *Service {
	s := &Service{providers: providers, timeout: lookupTimeout}
	if cacheDir != "" {
		s.cache = &cache{dir: cacheDir}
		s.cache.offsets = s.cache.loadOffsets()
	}
	return s
}

// Sources returns the providers in priority order: the order the automatic
// lookup tries and the UI lists them in.
func (s *Service) Sources() []Source {
	if s == nil {
		return nil
	}
	sources := make([]Source, 0, len(s.providers))
	for _, provider := range s.providers {
		sources = append(sources, Source{ID: provider.ID(), Name: provider.Name()})
	}
	return sources
}

// Load returns the track's lyrics: the first provider in priority order that
// has a cached answer or answers. A source's answer is cached, credited to
// it, and returned; errors try the next source, and the first error is
// returned when none answers. The sources share one budget of time, so a slow
// one leaves the next the rest.
func (s *Service) Load(q Query) (Doc, error) {
	if s == nil {
		return Doc{}, ErrFailed
	}
	q = q.withDeadline(s.timeout)
	var first error
	for _, provider := range s.providers {
		// A cached answer costs no request: it is worth reading even when
		// the budget is gone.
		if doc, ok := s.cached(provider, q.VideoID); ok {
			return doc, nil
		}
		if q.expired() {
			break
		}
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
		return s.finish(provider, q.VideoID, doc), nil
	}
	if q.expired() {
		// No source answered in the time given, which says nothing about
		// whether the track has lyrics.
		return Doc{}, errTimeout
	}
	if first == nil {
		first = ErrNotFound
	}
	return Doc{}, first
}

// LoadSource returns one provider's lyrics: its cached answer, else its
// Fetch. No other provider is tried, so the UI's tab shows exactly the source
// the user chose.
func (s *Service) LoadSource(id string, q Query) (Doc, error) {
	if s == nil {
		return Doc{}, ErrFailed
	}
	q = q.withDeadline(s.timeout)
	for _, provider := range s.providers {
		if provider.ID() != id {
			continue
		}
		if doc, ok := s.cached(provider, q.VideoID); ok {
			return doc, nil
		}
		if q.expired() {
			return Doc{}, errTimeout
		}
		doc, err := provider.Fetch(q)
		if err != nil {
			if q.expired() {
				return Doc{}, errTimeout
			}
			return Doc{}, err
		}
		if len(doc.Lines) == 0 {
			return Doc{}, ErrNotFound
		}
		return s.finish(provider, q.VideoID, doc), nil
	}
	return Doc{}, ErrFailed
}

// Offset returns the timing shift the user saved for the source's lyrics of
// the video: milliseconds added to every line, + meaning the lines start
// later. A shift set on other lyrics of the track, as a re-timed document,
// does not apply, so it returns 0 then.
func (s *Service) Offset(videoID, sourceID, lyricsID string) int {
	if s == nil || s.cache == nil {
		return 0
	}
	entry, ok := s.cache.offsets[offsetKey(videoID, sourceID)]
	if !ok || entry.Lyrics != lyricsID {
		return 0
	}
	return entry.Offset
}

// SetOffset saves the timing shift for the source's lyrics of the video,
// replacing the shift of another lyrics of the same track. A shift of 0 drops
// the entry, as the default says nothing.
func (s *Service) SetOffset(videoID, sourceID, lyricsID string, ms int) error {
	if s == nil || s.cache == nil {
		return nil
	}
	offsets := s.cache.offsets
	if offsets == nil {
		offsets = map[string]offsetEntry{}
		s.cache.offsets = offsets
	}
	key := offsetKey(videoID, sourceID)
	if ms == 0 {
		if _, ok := offsets[key]; !ok {
			return nil
		}
		delete(offsets, key)
	} else {
		entry := offsetEntry{Lyrics: lyricsID, Offset: ms}
		if offsets[key] == entry {
			return nil
		}
		offsets[key] = entry
	}
	return s.cache.saveOffsets(offsets)
}

// cached returns the provider's saved answer for the track.
func (s *Service) cached(provider Provider, videoID string) (Doc, bool) {
	if s.cache == nil {
		return Doc{}, false
	}
	doc, ok := s.cache.load(videoID, provider.ID())
	if !ok {
		return Doc{}, false
	}
	doc.Source = provider.ID()
	return doc, true
}

// finish credits the answer to its provider and saves it.
func (s *Service) finish(provider Provider, videoID string, doc Doc) Doc {
	doc.Source = provider.ID()
	if s.cache != nil {
		if err := s.cache.save(videoID, provider.ID(), doc); err != nil {
			log.Printf("lyrics: cache %s: %v", videoID, err)
		}
	}
	return doc
}
