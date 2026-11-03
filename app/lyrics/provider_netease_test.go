package lyrics

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// neteaseSearchHandler answers the song search with one matching song.
func neteaseSearchHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("search method = %s; want POST", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		if got := r.PostForm.Get("s"); got != "作者P サンプル曲" {
			t.Errorf("s = %q; want the uploader and title", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"result": map[string]any{"songs": []map[string]any{{
				"id": 26096272, "name": "サンプル曲", "dt": 242000,
				"ar": []map[string]any{{"name": "作者P"}, {"name": "歌手"}},
			}}},
		})
	}
}

func TestNeteaseProviderReadsYRC(t *testing.T) {
	const yrc = `{"t":0,"c":[{"tx":"作词: "},{"tx":"誰か"}]}
[420,4440](420,1320,0)Lately(1740,0,0), (1740,570,0)I've (2310,600,0)been
{"t":5000,"c":[{"tx":"作曲: "},{"tx":"誰か"}]}`
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/api/cloudsearch/pc", neteaseSearchHandler(t))
	mux.HandleFunc("/eapi/song/lyric/v1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("lyric method = %s; want POST", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		if r.PostForm.Get("params") == "" {
			t.Error("the eapi request carries no params")
		}
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "os=android") {
			t.Errorf("Cookie = %q; want the eapi header", got)
		}
		if got := r.Header.Get("User-Agent"); !strings.Contains(got, "HuaweiBrowser") {
			t.Errorf("User-Agent = %q; want the eapi client", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"yrc":  map[string]any{"lyric": yrc},
		})
	})
	mux.HandleFunc("/api/song/lyric", func(w http.ResponseWriter, r *http.Request) {
		t.Error("the LRC fallback ran after a YRC answer")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "lrc": map[string]any{"lyric": ""}})
	})

	provider := NewNeteaseProvider(server.Client(), server.URL, server.URL, "VocaBox/test")
	doc, err := provider.Fetch(Query{VideoID: "sm1", Title: "サンプル曲", Artist: "作者P", DurationMS: 242000})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Timed || len(doc.Lines) != 1 {
		t.Fatalf("doc = %#v; want one timed line", doc)
	}
	line := doc.Lines[0]
	if line.Text != "Lately, I've been" || line.StartMS != 420 || line.EndMS != 4860 {
		t.Fatalf("line = %#v", line)
	}
	if len(line.Words) != 4 {
		t.Fatalf("words = %#v; want four", line.Words)
	}
	if line.Words[0].StartMS != 420 || line.Words[0].EndMS != 1740 {
		t.Fatalf("first word = %#v", line.Words[0])
	}
	if line.Words[1].Text != ", " || line.Words[1].StartMS != 1740 || line.Words[1].EndMS != 1740 {
		t.Fatalf("second word = %#v", line.Words[1])
	}
	if line.Words[2].StartMS != 1740 || line.Words[3].StartMS != 2310 {
		t.Fatalf("word starts = %#v", line.Words)
	}
}

func TestNeteaseProviderFallsBackToTheWebLRC(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/api/cloudsearch/pc", neteaseSearchHandler(t))
	mux.HandleFunc("/eapi/song/lyric/v1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/api/song/lyric", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("id"); got != "26096272" {
			t.Errorf("lyric id = %q; want the chosen song", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"lrc": map[string]any{"lyric": "[00:00.000] 作词 : 作者P\n" +
				"[00:00.000] 作曲 : 作者P\n" +
				"[00:01.00] 一行目\n" +
				"[00:03.50] 二行目\n"},
		})
	})

	provider := NewNeteaseProvider(server.Client(), server.URL, server.URL, "VocaBox/test")
	doc, err := provider.Fetch(Query{VideoID: "sm1", Title: "サンプル曲", Artist: "作者P", DurationMS: 242000})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Timed || len(doc.Lines) != 2 {
		t.Fatalf("doc = %#v; want two timed lines", doc)
	}
	if doc.Lines[0].Text != "一行目" || doc.Lines[0].StartMS != 1000 || doc.Lines[0].EndMS != 3500 {
		t.Fatalf("first line = %#v", doc.Lines[0])
	}
	if doc.Lines[1].Text != "二行目" || doc.Lines[1].StartMS != 3500 {
		t.Fatalf("second line = %#v", doc.Lines[1])
	}
}

func TestNeteaseProviderReadsTheEAPILRC(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/api/cloudsearch/pc", neteaseSearchHandler(t))
	mux.HandleFunc("/eapi/song/lyric/v1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"yrc":  map[string]any{"lyric": ""},
			"lrc": map[string]any{"lyric": "[00:01.00] 一行目\n" +
				"[00:03.00] 二行目\n"},
		})
	})
	mux.HandleFunc("/api/song/lyric", func(w http.ResponseWriter, r *http.Request) {
		t.Error("the web LRC fallback ran though eapi answered")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200})
	})
	provider := NewNeteaseProvider(server.Client(), server.URL, server.URL, "")
	doc, err := provider.Fetch(Query{Title: "サンプル曲", Artist: "作者P", DurationMS: 242000})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Lines) != 2 || doc.Lines[0].Text != "一行目" || doc.Lines[1].StartMS != 3000 {
		t.Fatalf("doc = %#v; want the LRC eapi carried", doc)
	}
}

// TestNeteaseProviderPrefersTheCleanYRC checks that a ruby or translation
// copy is kept only until a clean word-timed answer turns up.
func TestNeteaseProviderPrefersTheCleanYRC(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/api/cloudsearch/pc", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"result": map[string]any{"songs": []map[string]any{
				{"id": 1, "name": "サンプル曲", "dt": 242000, "ar": []map[string]any{{"name": "作者P"}}},
				{"id": 2, "name": "サンプル曲", "dt": 243000, "ar": []map[string]any{{"name": "作者P"}}},
			}},
		})
	})
	mux.HandleFunc("/eapi/song/lyric/v1", func(w http.ResponseWriter, r *http.Request) {
		yrc := "[1000,1000](1000,500,0)歌(1500,500,0)詞（かし）【 翻訳 】"
		if calls.Add(1) == 2 {
			yrc = "[1000,1000](1000,500,0)本(1500,500,0)文"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"yrc":  map[string]any{"lyric": yrc},
		})
	})
	mux.HandleFunc("/api/song/lyric", func(w http.ResponseWriter, r *http.Request) {
		t.Error("the web LRC fallback ran")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200})
	})
	provider := NewNeteaseProvider(server.Client(), server.URL, server.URL, "")
	doc, err := provider.Fetch(Query{Title: "サンプル曲", Artist: "作者P", DurationMS: 242000})
	if err != nil {
		t.Fatal(err)
	}
	if doc.annotated {
		t.Fatal("the annotated copy was returned over the clean one")
	}
	if len(doc.Lines) != 1 || doc.Lines[0].Text != "本文" {
		t.Fatalf("doc = %#v; want the clean YRC", doc)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("eapi calls = %d; want both candidates tried", got)
	}
}

func TestNeteaseProviderReadsTheOldSongFields(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/api/cloudsearch/pc", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"result": map[string]any{"songs": []map[string]any{{
				"id": 1, "name": "サンプル曲", "duration": 242000,
				"artists": []map[string]any{{"name": "歌手"}},
			}}},
		})
	})
	mux.HandleFunc("/eapi/song/lyric/v1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/api/song/lyric", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"lrc":  map[string]any{"lyric": "[00:01.00] 一行目\n"},
		})
	})
	provider := NewNeteaseProvider(server.Client(), server.URL, server.URL, "")
	doc, err := provider.Fetch(Query{Title: "サンプル曲", DurationMS: 242000})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Lines) != 1 || doc.Lines[0].Text != "一行目" {
		t.Fatalf("doc = %#v", doc)
	}
}

func TestNeteaseProviderNoLyrics(t *testing.T) {
	for _, c := range []struct {
		name       string
		eapiStatus int
		webBody    map[string]any
	}{
		{name: "eapi empty", eapiStatus: http.StatusOK},
		{name: "web flagged", eapiStatus: http.StatusInternalServerError, webBody: map[string]any{"code": 200, "nolyric": true}},
		{name: "web credits only", eapiStatus: http.StatusInternalServerError, webBody: map[string]any{
			"code": 200, "lrc": map[string]any{"lyric": "[00:00.00] 作词 : 誰か\n"},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			mux := http.NewServeMux()
			server := httptest.NewServer(mux)
			defer server.Close()
			mux.HandleFunc("/api/cloudsearch/pc", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 200,
					"result": map[string]any{"songs": []map[string]any{{
						"id": 1, "name": "サンプル曲", "dt": 242000,
						"ar": []map[string]any{{"name": "歌手"}},
					}}},
				})
			})
			mux.HandleFunc("/eapi/song/lyric/v1", func(w http.ResponseWriter, r *http.Request) {
				if c.eapiStatus != http.StatusOK {
					w.WriteHeader(c.eapiStatus)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 200,
					"yrc":  map[string]any{"lyric": ""},
					"lrc":  map[string]any{"lyric": ""},
				})
			})
			mux.HandleFunc("/api/song/lyric", func(w http.ResponseWriter, r *http.Request) {
				if c.eapiStatus == http.StatusOK {
					t.Error("the web LRC fallback ran after eapi answered with no lyrics")
				}
				_ = json.NewEncoder(w).Encode(c.webBody)
			})
			provider := NewNeteaseProvider(server.Client(), server.URL, server.URL, "")
			_, err := provider.Fetch(Query{Title: "サンプル曲", DurationMS: 242000})
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Fetch error = %v; want ErrNotFound", err)
			}
		})
	}
}
