package lyrics

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLineAtAndProgress(t *testing.T) {
	doc := Doc{Timed: true, Lines: []Line{
		{Text: "one", StartMS: 1000, EndMS: 2000},
		{Text: "two", StartMS: 3000, EndMS: 4000},
	}}
	at := []struct {
		ms   float64
		want int
	}{
		{0, -1},
		{999, -1},
		{1000, 0},
		{1500, 0},
		{2000, 0},
		{2500, 0},
		{3000, 1},
		{4000, 1},
		{9000, 1},
	}
	for _, c := range at {
		if got := doc.LineAt(c.ms); got != c.want {
			t.Errorf("LineAt(%v) = %d; want %d", c.ms, got, c.want)
		}
	}
	line := doc.Lines[0]
	for _, c := range []struct {
		ms   float64
		want float64
	}{{0, 0}, {1000, 0}, {1500, 0.5}, {2000, 1}, {2500, 1}} {
		if got := line.Progress(c.ms); got != c.want {
			t.Errorf("Progress(%v) = %v; want %v", c.ms, got, c.want)
		}
	}
	// A line with no duration is complete as soon as it starts.
	point := Line{StartMS: 1000, EndMS: 1000}
	if got := point.Progress(999); got != 0 {
		t.Errorf("point progress before start = %v; want 0", got)
	}
	if got := point.Progress(1000); got != 1 {
		t.Errorf("point progress at start = %v; want 1", got)
	}
}

func TestDocWordTimed(t *testing.T) {
	if (Doc{Lines: []Line{{Text: "line"}}}).WordTimed() {
		t.Fatal("a line-timed document reads as word timed")
	}
	worded := Doc{Lines: []Line{{Text: "word", Words: []Word{{Text: "word"}}}}}
	if !worded.WordTimed() {
		t.Fatal("a word-timed document does not read as word timed")
	}
}

// fakeProvider is one source a service test controls.
type fakeProvider struct {
	name  string
	doc   Doc
	err   error
	calls int
}

func (f *fakeProvider) ID() string { return f.name }

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Fetch(Query) (Doc, error) {
	f.calls++
	return f.doc, f.err
}

// TestCachePathNamesTheFormatVersion checks the format version and the
// source are part of the cache key, so bumping it leaves old answers behind
// and two sources do not overwrite each other.
func TestCachePathNamesTheFormatVersion(t *testing.T) {
	c := &cache{dir: "cache"}
	path, ok := c.path("sm1", SourceNiconico)
	if !ok || filepath.Base(path) != cacheVersion+"-sm1-"+SourceNiconico+".json" {
		t.Fatalf("cache path = %q, %v; want the format version and source in the name", path, ok)
	}
}

// TestDocIDFollowsTheTiming checks the name given to a document: the same
// lyrics keep it, a corrected word keeps it, and re-timed or other data gets
// a new one.
func TestDocIDFollowsTheTiming(t *testing.T) {
	timed := func() Doc {
		return Doc{Source: SourceNiconico, Timed: true, Lines: []Line{
			{Text: "one", StartMS: 0, EndMS: 1000},
			{Text: "two", StartMS: 2000, EndMS: 3000},
		}}
	}
	id := timed().ID()
	if id == "" || timed().ID() != id {
		t.Fatalf("the same document gave %q then %q", id, timed().ID())
	}
	// Correcting the words keeps the shift: the timing is the same.
	text := timed()
	text.Lines[0].Text = "uno"
	if text.ID() != id {
		t.Fatal("a text-only edit changed the lyrics id")
	}
	// Re-timing a line, another source, an untimed document, and word
	// timing each make another id.
	moved := timed()
	moved.Lines[1].StartMS = 2100
	if moved.ID() == id {
		t.Fatal("a re-timed line kept the lyrics id")
	}
	other := timed()
	other.Source = SourceKugou
	if other.ID() == id {
		t.Fatal("another source kept the lyrics id")
	}
	untimed := timed()
	untimed.Timed = false
	if untimed.ID() == id {
		t.Fatal("an untimed document kept the lyrics id")
	}
	worded := timed()
	worded.Lines[0].Words = []Word{{Text: "one", StartMS: 0, EndMS: 500}}
	later := timed()
	later.Lines[0].Words = []Word{{Text: "one", StartMS: 0, EndMS: 600}}
	if worded.ID() == id || worded.ID() == later.ID() {
		t.Fatal("word timing does not make an id of its own")
	}
}

// TestServiceKeepsTheOffsets checks the timing shifts live in one file,
// survive a new service, and belong to the lyrics they were set on.
func TestServiceKeepsTheOffsets(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, &fakeProvider{name: SourceNiconico})
	if err := s.SetOffset("sm1", SourceNiconico, "lyrics-a", -120); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOffset("sm1", SourceKugou, "lyrics-b", 50); err != nil {
		t.Fatal(err)
	}
	// One file holds both, keyed by video and source.
	path := filepath.Join(dir, "offsets.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the offsets file is missing: %v", err)
	}
	for _, key := range []string{`"sm1-niconico"`, `"sm1-kugou"`} {
		if !strings.Contains(string(body), key) {
			t.Fatalf("the offsets file has no %s: %s", key, body)
		}
	}
	again := NewService(dir, &fakeProvider{name: SourceNiconico})
	if got := again.Offset("sm1", SourceNiconico, "lyrics-a"); got != -120 {
		t.Fatalf("offset = %d; want -120", got)
	}
	// A re-timed document is another lyrics: its shift starts over.
	if got := again.Offset("sm1", SourceNiconico, "lyrics-z"); got != 0 {
		t.Fatalf("another lyrics of the track offset = %d; want 0", got)
	}
	if got := again.Offset("sm1", SourceKugou, "lyrics-b"); got != 50 {
		t.Fatalf("another source's offset = %d; want 50", got)
	}
	if got := again.Offset("sm2", SourceNiconico, "lyrics-a"); got != 0 {
		t.Fatalf("another video's offset = %d; want 0", got)
	}
	// A shift set on the re-timed document replaces the old one: one entry
	// per track and source.
	if err := again.SetOffset("sm1", SourceNiconico, "lyrics-z", 30); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil || strings.Contains(string(body), "lyrics-a") {
		t.Fatalf("the old lyrics' entry stayed: %v %s", err, body)
	}
	if got := again.Offset("sm1", SourceNiconico, "lyrics-z"); got != 30 {
		t.Fatalf("offset after re-timing = %d; want 30", got)
	}
	if got := again.Offset("sm1", SourceNiconico, "lyrics-a"); got != 0 {
		t.Fatalf("the old lyrics' offset = %d; want 0", got)
	}
	// A shift back to 0 drops its entry and keeps the others.
	if err := again.SetOffset("sm1", SourceNiconico, "lyrics-z", 0); err != nil {
		t.Fatal(err)
	}
	if got := again.Offset("sm1", SourceNiconico, "lyrics-z"); got != 0 {
		t.Fatalf("offset after clearing = %d; want 0", got)
	}
	if got := again.Offset("sm1", SourceKugou, "lyrics-b"); got != 50 {
		t.Fatalf("clearing one shift dropped another: %d", got)
	}
	// No shifts left no file: the default says nothing.
	if err := again.SetOffset("sm1", SourceKugou, "lyrics-b", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("clearing every shift left a file: %v", err)
	}
	// A cache that saves no lyrics has nowhere to keep a shift.
	disabled := NewService("", &fakeProvider{name: SourceNiconico})
	if err := disabled.SetOffset("sm1", SourceNiconico, "lyrics-a", 50); err != nil ||
		disabled.Offset("sm1", SourceNiconico, "lyrics-a") != 0 {
		t.Fatalf("a disabled cache kept an offset: err=%v", err)
	}
}

// TestServiceSetsAsideTheUnreadableOffsets checks a file the app cannot read
// is kept for the user rather than overwritten by the first new shift.
func TestServiceSetsAsideTheUnreadableOffsets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "offsets.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewService(dir, &fakeProvider{name: SourceNiconico})
	if got := s.Offset("sm1", SourceNiconico, "lyrics-a"); got != 0 {
		t.Fatalf("a broken file gave offset %d; want 0", got)
	}
	body, err := os.ReadFile(path + ".bak")
	if err != nil || string(body) != "{broken" {
		t.Fatalf("the broken file was not kept: %v %q", err, body)
	}
	if err := s.SetOffset("sm1", SourceNiconico, "lyrics-a", 50); err != nil {
		t.Fatal(err)
	}
	again := NewService(dir, &fakeProvider{name: SourceNiconico})
	if got := again.Offset("sm1", SourceNiconico, "lyrics-a"); got != 50 {
		t.Fatalf("offset after the broken file = %d; want 50", got)
	}
}

func TestServiceFallsBackAndCreditsTheSource(t *testing.T) {
	failing := &fakeProvider{name: "first", err: ErrLimit}
	answering := &fakeProvider{name: "second", doc: Doc{Lines: []Line{{Text: "歌詞"}}}}
	s := NewService("", failing, answering)
	doc, err := s.Load(Query{VideoID: "sm1"})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Source != "second" || len(doc.Lines) != 1 {
		t.Fatalf("doc = %#v; want the second source's answer credited to it", doc)
	}
	if failing.calls != 1 || answering.calls != 1 {
		t.Fatalf("calls = %d, %d; want one each", failing.calls, answering.calls)
	}
}

func TestServiceReturnsTheFirstErrorWhenAllFail(t *testing.T) {
	first := &fakeProvider{name: "first", err: ErrLogin}
	second := &fakeProvider{name: "second", err: ErrFailed}
	s := NewService("", first, second)
	if _, err := s.Load(Query{VideoID: "sm1"}); !errors.Is(err, ErrLogin) {
		t.Fatalf("Load error = %v; want the first source's error", err)
	}
}

// TestQueryBudget checks the time a lookup is given: none is never spent, and
// a spent one reads as spent at once.
func TestQueryBudget(t *testing.T) {
	if (Query{}).expired() {
		t.Fatal("a lookup without a budget reads as spent")
	}
	if !(Query{}).withDeadline(-time.Second).expired() {
		t.Fatal("a lookup whose budget is spent does not read as spent")
	}
	if (Query{}).withDeadline(time.Minute).expired() {
		t.Fatal("a fresh lookup reads as spent")
	}
}

// TestServiceGivesUpWhenTheBudgetIsSpent checks that a lookup out of time asks
// no source, and reports a failure rather than that the track has no lyrics.
func TestServiceGivesUpWhenTheBudgetIsSpent(t *testing.T) {
	provider := &fakeProvider{name: "source", doc: Doc{Lines: []Line{{Text: "歌詞"}}}}
	s := NewService("", provider)
	s.timeout = -time.Second
	if _, err := s.Load(Query{VideoID: "sm1"}); !errors.Is(err, ErrFailed) {
		t.Fatalf("Load error = %v; want ErrFailed", err)
	}
	if provider.calls != 0 {
		t.Fatalf("Load called the source %d times; want none once the budget is spent", provider.calls)
	}
	if _, err := s.LoadSource("source", Query{VideoID: "sm1"}); !errors.Is(err, ErrFailed) {
		t.Fatalf("LoadSource error = %v; want ErrFailed", err)
	}
	if provider.calls != 0 {
		t.Fatalf("LoadSource called the source %d times; want none once the budget is spent", provider.calls)
	}
}

func TestServiceCachesTheAnswer(t *testing.T) {
	provider := &fakeProvider{name: "niconico", doc: Doc{
		Timed: true,
		Lines: []Line{{Text: "歌詞", StartMS: 1000, EndMS: 2000}},
	}}
	s := NewService(t.TempDir(), provider)
	first, err := s.Load(Query{VideoID: "sm1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Load(Query{VideoID: "sm1"})
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider called %d times; want the cache to answer the second load", provider.calls)
	}
	if second.Source != first.Source || second.Timed != first.Timed || len(second.Lines) != 1 {
		t.Fatalf("cached doc = %#v; want %#v", second, first)
	}
	if second.Lines[0].EndMS != 2000 {
		t.Fatalf("cached line = %#v; want its timing kept", second.Lines[0])
	}
}

func TestServiceSkipsAnEmptyAnswer(t *testing.T) {
	empty := &fakeProvider{name: "empty", doc: Doc{}}
	answering := &fakeProvider{name: "second", doc: Doc{Lines: []Line{{Text: "歌詞"}}}}
	s := NewService("", empty, answering)
	doc, err := s.Load(Query{VideoID: "sm1"})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Source != "second" {
		t.Fatalf("doc = %#v; want the empty answer passed over", doc)
	}
}

func TestServiceSourcesAreInPriorityOrder(t *testing.T) {
	s := NewService("",
		&fakeProvider{name: SourceNiconico},
		&fakeProvider{name: SourceKugou},
		&fakeProvider{name: SourceNetease},
	)
	sources := s.Sources()
	if len(sources) != 3 {
		t.Fatalf("sources = %#v; want three", sources)
	}
	for i, want := range []string{SourceNiconico, SourceKugou, SourceNetease} {
		if sources[i].ID != want {
			t.Fatalf("source %d = %q; want %q", i, sources[i].ID, want)
		}
	}
}

func TestServiceLoadSourceFetchesOnlyThatSource(t *testing.T) {
	nico := &fakeProvider{name: SourceNiconico, doc: Doc{Lines: []Line{{Text: "公式"}}}}
	kugou := &fakeProvider{name: SourceKugou, err: ErrNotFound}
	s := NewService("", nico, kugou)
	if _, err := s.LoadSource(SourceKugou, Query{VideoID: "sm1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadSource error = %v; want the chosen source's error", err)
	}
	if nico.calls != 0 {
		t.Fatalf("the official source was called %d times; want the chosen source alone", nico.calls)
	}
	if kugou.calls != 1 {
		t.Fatalf("the chosen source was called %d times; want once", kugou.calls)
	}
}

func TestServiceCachesEachSourceSeparately(t *testing.T) {
	nico := &fakeProvider{name: SourceNiconico, err: ErrNotFound}
	kugou := &fakeProvider{name: SourceKugou, doc: Doc{Lines: []Line{{Text: "歌詞", StartMS: 1000}}}}
	s := NewService(t.TempDir(), nico, kugou)
	if _, err := s.LoadSource(SourceKugou, Query{VideoID: "sm1"}); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Load(Query{VideoID: "sm1"})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Source != SourceKugou {
		t.Fatalf("doc = %#v; want the cached fallback answer", doc)
	}
	if kugou.calls != 1 {
		t.Fatalf("the fallback source was called %d times; want the cache to answer", kugou.calls)
	}
}
