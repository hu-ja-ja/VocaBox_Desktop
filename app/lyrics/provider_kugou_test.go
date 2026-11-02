package lyrics

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// krcFixture encodes text the way KuGou's KRC download answers it: zlib,
// the KRC XOR, the krc1 marker, and base64.
func krcFixture(t *testing.T, text string) string {
	t.Helper()
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	raw := compressed.Bytes()
	for i := range raw {
		raw[i] ^= krcKey[i%len(krcKey)]
	}
	payload := append([]byte("krc1"), raw...)
	return base64.StdEncoding.EncodeToString(payload)
}

func TestKugouProviderReadsKRC(t *testing.T) {
	content := krcFixture(t, "[ti:千本桜]\n"+
		"[0,1000]千本桜 - 初音ミク\n"+
		"[1000,100]词：黒うさP\n"+
		"[2000,2000]<0,500,0>千<500,500,0>本<1000,1000,0>桜\n"+
		"[4000,1500]<0,750,0>夜<750,750,0>ニ")
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/api/v3/search/song", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("keyword"); got != "黒うさP 千本桜" {
			t.Errorf("keyword = %q; want the uploader and title", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 1,
			"data": map[string]any{"info": []map[string]any{{
				"hash": "h1", "songname": "千本桜", "singername": "初音ミク", "duration": 242,
			}}},
		})
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("hash"); got != "h1" {
			t.Errorf("hash = %q; want the song's hash", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 200,
			"candidates": []map[string]any{{
				"id": "c1", "accesskey": "k1", "song": "千本桜", "singer": "初音ミク",
				"duration": 242000, "content_format": 4,
			}},
		})
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("id"); got != "c1" {
			t.Errorf("id = %q", got)
		}
		if got := r.URL.Query().Get("accesskey"); got != "k1" {
			t.Errorf("accesskey = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "content": content})
	})

	provider := NewKugouProvider(server.Client(), server.URL, server.URL, "VocaBox/test")
	doc, err := provider.Fetch(Query{VideoID: "sm1", Title: "千本桜", Artist: "黒うさP", DurationMS: 242000})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Timed || len(doc.Lines) != 2 {
		t.Fatalf("doc = %#v; want two timed lines", doc)
	}
	first := doc.Lines[0]
	if first.Text != "千本桜" || first.StartMS != 2000 || first.EndMS != 4000 {
		t.Fatalf("first line = %#v; want the title header and credit line dropped", first)
	}
	if len(first.Words) != 3 || first.Words[1].StartMS != 2500 || first.Words[2].EndMS != 4000 {
		t.Fatalf("words = %#v", first.Words)
	}
	if doc.Lines[1].StartMS != 4000 {
		t.Fatalf("second line = %#v", doc.Lines[1])
	}
}

func TestKugouProviderReadsLRC(t *testing.T) {
	content := base64.StdEncoding.EncodeToString([]byte("[00:01.00] 一行目\n[00:03.50] 二行目\n"))
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/api/v3/search/song", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 1,
			"data": map[string]any{"info": []map[string]any{{
				"hash": "h1", "songname": "千本桜", "singername": "初音ミク", "duration": 242,
			}}},
		})
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 200,
			"candidates": []map[string]any{{
				"id": "c1", "accesskey": "k1", "song": "千本桜", "singer": "初音ミク",
				"duration": 242000, "content_format": 1,
			}},
		})
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "content": content})
	})

	provider := NewKugouProvider(server.Client(), server.URL, server.URL, "")
	doc, err := provider.Fetch(Query{Title: "千本桜", DurationMS: 242000})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Timed || len(doc.Lines) != 2 {
		t.Fatalf("doc = %#v; want two timed lines", doc)
	}
	if doc.Lines[0].StartMS != 1000 || doc.Lines[1].StartMS != 3500 {
		t.Fatalf("timings = %v, %v", doc.Lines[0].StartMS, doc.Lines[1].StartMS)
	}
}

func TestKugouProviderRejectsAnotherCut(t *testing.T) {
	lyricsCalls := 0
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/api/v3/search/song", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 1,
			"data": map[string]any{"info": []map[string]any{{
				"hash": "h1", "songname": "千本桜 (Game Version)", "singername": "初音ミク", "duration": 218,
			}}},
		})
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		lyricsCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "candidates": []map[string]any{}})
	})
	provider := NewKugouProvider(server.Client(), server.URL, server.URL, "")
	_, err := provider.Fetch(Query{Title: "千本桜", DurationMS: 242000})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Fetch error = %v; want ErrNotFound", err)
	}
	if lyricsCalls != 0 {
		t.Fatalf("the lyrics search ran %d times for a rejected cut", lyricsCalls)
	}
}
