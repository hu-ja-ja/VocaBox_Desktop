package lyrics

import (
	"errors"
	"path/filepath"
	"testing"
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

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Fetch(Query) (Doc, error) {
	f.calls++
	return f.doc, f.err
}

// TestCachePathNamesTheFormatVersion checks the format version is part of the
// cache key, so bumping it leaves old answers behind.
func TestCachePathNamesTheFormatVersion(t *testing.T) {
	c := &cache{dir: "cache"}
	path, ok := c.path("sm1")
	if !ok || filepath.Base(path) != cacheVersion+"-sm1.json" {
		t.Fatalf("cache path = %q, %v; want the format version in the name", path, ok)
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
