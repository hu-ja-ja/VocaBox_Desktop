package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseMylistInput(t *testing.T) {
	valid := map[string]string{
		"27411728":     "27411728",
		"  27411728  ": "27411728",
		"https://www.nicovideo.jp/mylist/27411728":            "27411728",
		"http://nicovideo.jp/mylist/27411728":                 "27411728",
		"https://sp.nicovideo.jp/mylist/27411728":             "27411728",
		"https://www.nicovideo.jp/mylist/27411728/":           "27411728",
		"https://www.nicovideo.jp/user/12345/mylist/27411728": "27411728",
		"https://www.nicovideo.jp/my/mylist/#/27411728":       "27411728",
		"https://www.nicovideo.jp/mylist/#/27411728":          "27411728",
		"https://nico.ms/mylist/27411728":                     "27411728",
		"www.nicovideo.jp/mylist/27411728":                    "27411728",
		"https://WWW.NICOVIDEO.JP/mylist/27411728":            "27411728",
		"https://www.nicovideo.jp/mylist/27411728?ref=abc":    "27411728",
	}
	for in, want := range valid {
		if got, err := ParseMylistInput(in); err != nil || got != want {
			t.Errorf("ParseMylistInput(%q) = %q, %v; want %q, nil", in, got, err, want)
		}
	}

	invalid := []string{
		"",
		"   ",
		"abc",
		"sm12345",
		"12a34",
		"https://example.com/mylist/27411728",
		"https://evlnicovideo.jp/mylist/27411728",
		"https://nicovideo.jp.evil.example/mylist/27411728",
		"https://www.nicovideo.jp/watch/sm12345",
		"https://www.nicovideo.jp/mylist/abc",
		"https://www.nicovideo.jp/mylist/",
		"https://www.nicovideo.jp/user/abc/mylist/27411728",
		"https://www.nicovideo.jp/my/mylist/",
		"https://nico.ms/watch/sm12345",
		"javascript://nicovideo.jp/mylist/123",
		"ftp://www.nicovideo.jp/mylist/27411728",
		"https://www.nicovideo.jp/mylist/#/",
		"///bad",
	}
	for _, in := range invalid {
		if got, err := ParseMylistInput(in); err == nil {
			t.Errorf("ParseMylistInput(%q) = %q, nil; want an error", in, got)
		}
	}
}

const mylistFixture = `{"meta":{"status":200},"data":{"mylist":{` +
	`"name":"テストリスト","description":"説明文","isPublic":true,` +
	`"owner":{"name":"投稿者"},` +
	`"items":[` +
	`{"watchId":"sm1","video":{"id":"sm1","title":"曲A","duration":125,"owner":{"name":"作者A"},` +
	`"thumbnail":{"url":"https://nicovideo.cdn.nimg.jp/thumbnails/1/1","middleUrl":"https://nicovideo.cdn.nimg.jp/thumbnails/1/1.M"}}},` +
	`{"watchId":"sm2","video":null},` +
	`{"watchId":"sm3"}` +
	`],` +
	`"totalItemCount":3,"hasNext":true}}}`

func TestGetPageMapsResponse(t *testing.T) {
	var gotReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r
		_, _ = w.Write([]byte(mylistFixture))
	}))
	t.Cleanup(srv.Close)

	m := &Mylist{client: srv.Client(), baseURL: srv.URL}
	page, err := m.GetPage("27411728", 2)
	if err != nil {
		t.Fatalf("GetPage returned an error: %v", err)
	}
	if gotReq.URL.Path != "/mylists/27411728" || gotReq.URL.Query().Get("page") != "2" || gotReq.URL.Query().Get("pageSize") != "100" {
		t.Errorf("unexpected request URL: %s", gotReq.URL)
	}
	if gotReq.Header.Get("X-Frontend-ID") != "6" || gotReq.Header.Get("X-Frontend-Version") != "0" {
		t.Errorf("missing frontend headers: %v", gotReq.Header)
	}
	if ua := gotReq.Header.Get("User-Agent"); ua == "" {
		t.Error("missing identifying User-Agent")
	}
	if page.ID != "27411728" || page.Name != "テストリスト" || page.Description != "説明文" || page.Owner != "投稿者" {
		t.Errorf("wrong mylist header: %+v", page)
	}
	if page.TotalItemCount != 3 || !page.HasNext {
		t.Errorf("wrong paging: %+v", page)
	}
	if len(page.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(page.Items))
	}
	first := page.Items[0]
	if !first.Available || first.Title != "曲A" || first.Uploader != "作者A" || first.Duration != 125 || first.VideoID != "sm1" {
		t.Errorf("wrong first item: %+v", first)
	}
	if first.ThumbURL != "https://nicovideo.cdn.nimg.jp/thumbnails/1/1" {
		t.Errorf("wrong thumbnail URL: %+v", first)
	}
	// Items without video metadata stay listed as unavailable.
	for _, item := range page.Items[1:] {
		if item.Available || item.Title != "" {
			t.Errorf("unavailable item must keep only its watchId: %+v", item)
		}
	}
	if page.Items[1].WatchID != "sm2" || page.Items[2].WatchID != "sm3" {
		t.Errorf("wrong watchIds: %+v", page.Items[1:])
	}
}

func TestGetPageErrors(t *testing.T) {
	private := strings.Replace(mylistFixture, `"isPublic":true`, `"isPublic":false`, 1)
	badStatus := strings.Replace(mylistFixture, `"status":200`, `"status":404`, 1)

	cases := []struct {
		name   string
		input  string
		status int
		body   string
	}{
		{"invalid input", "not a mylist", http.StatusOK, mylistFixture},
		{"http 404", "27411728", http.StatusNotFound, "not found"},
		{"http 403", "27411728", http.StatusForbidden, "forbidden"},
		{"http 500", "27411728", http.StatusInternalServerError, "error"},
		{"http 404 with html body", "27411728", http.StatusNotFound, "<html>nope</html>"},
		{"api status", "27411728", http.StatusOK, badStatus},
		{"private list", "27411728", http.StatusOK, private},
		{"bad json", "27411728", http.StatusOK, "{oops"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			m := &Mylist{client: srv.Client(), baseURL: srv.URL}
			if _, err := m.GetPage(tc.input, 1); err == nil {
				t.Error("want an error, got nil")
			}
		})
	}

	t.Run("html body is not leaked", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("<html>secret</html>"))
		}))
		t.Cleanup(srv.Close)
		m := &Mylist{client: srv.Client(), baseURL: srv.URL}
		_, err := m.GetPage("27411728", 1)
		if err == nil {
			t.Fatal("want an error, got nil")
		}
		if strings.Contains(err.Error(), "secret") {
			t.Errorf("error must not include the response body: %v", err)
		}
	})

	t.Run("page below 1 requests page 1", func(t *testing.T) {
		var path string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.String()
			_, _ = w.Write([]byte(mylistFixture))
		}))
		t.Cleanup(srv.Close)
		m := &Mylist{client: srv.Client(), baseURL: srv.URL}
		if _, err := m.GetPage("27411728", 0); err != nil {
			t.Fatalf("GetPage returned an error: %v", err)
		}
		if !strings.Contains(path, "page=1") {
			t.Errorf("want page=1 in %q", path)
		}
	})
}

// savedFixture is a one-page mylist used by the saved-snapshot tests.
const savedFixture = `{"meta":{"status":200},"data":{"mylist":{` +
	`"name":"保存リスト","description":"保存説明","isPublic":true,` +
	`"owner":{"name":"保存者"},` +
	`"items":[` +
	`{"watchId":"sm1","video":{"id":"sm1","title":"曲A","duration":125,"owner":{"name":"作者A"}}},` +
	`{"watchId":"sm2","video":null}` +
	`],` +
	`"totalItemCount":2,"hasNext":false}}}`

// newSavedService returns a Mylist over a temp config dir and a test server
// serving body for every page, plus a request counter.
func newSavedService(t *testing.T, body string) (*Mylist, *int32) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Mylist{client: srv.Client(), baseURL: srv.URL, configDir: t.TempDir()}, &hits
}

func TestSavedMylists(t *testing.T) {
	m, _ := newSavedService(t, savedFixture)

	// Missing file means none saved yet.
	got, err := m.ListSaved()
	if err != nil || len(got) != 0 {
		t.Fatalf("ListSaved() = %v, %v; want [], nil", got, err)
	}

	// Invalid IDs and blank names are rejected before any request.
	if _, err := m.SaveMylist("not a mylist", "x"); err == nil {
		t.Error("SaveMylist(bad input) = nil error; want an error")
	}
	if _, err := m.SaveMylist("27411728", "  "); err == nil {
		t.Error("SaveMylist(blank name) = nil error; want an error")
	}

	// Save two, then upsert one by URL without duplicating.
	if _, err := m.SaveMylist("27411728", "リストA"); err != nil {
		t.Fatalf("SaveMylist = %v; want nil", err)
	}
	if _, err := m.SaveMylist("https://www.nicovideo.jp/mylist/100", "リストB"); err != nil {
		t.Fatalf("SaveMylist = %v; want nil", err)
	}
	got, err = m.SaveMylist("https://nico.ms/mylist/27411728", "リストA更新")
	if err != nil {
		t.Fatalf("SaveMylist upsert = %v; want nil", err)
	}
	if len(got) != 2 || got[0].ID != "27411728" || got[0].Name != "リストA更新" ||
		got[1].ID != "100" || got[1].Name != "リストB" {
		t.Fatalf("upserted list = %+v; want 2 records, updated name, no duplicates", got)
	}

	// The stored record holds the fetched contents, not just the reference.
	snap := got[0]
	if snap.Owner != "保存者" || snap.Description != "保存説明" || snap.TotalItemCount != 2 || len(snap.Items) != 2 {
		t.Fatalf("stored snapshot = %+v; want fetched contents", snap)
	}
	if snap.Items[0].Title != "曲A" || snap.Items[1].Available {
		t.Errorf("stored items = %+v; want fetched contents", snap.Items)
	}

	// A fresh service over the same dir lists the same records, contents intact.
	same := &Mylist{configDir: m.configDir}
	got, err = same.ListSaved()
	if err != nil || len(got) != 2 || len(got[0].Items) != 2 {
		t.Fatalf("ListSaved() = %+v, %v; want 2 records with 2 items, nil", got, err)
	}

	// Remove drops only the local snapshot; unknown IDs are idempotent.
	got, err = m.RemoveSaved("100")
	if err != nil || len(got) != 1 || got[0].ID != "27411728" {
		t.Fatalf("RemoveSaved() = %v, %v; want [27411728], nil", got, err)
	}
	got, err = m.RemoveSaved("999")
	if err != nil || len(got) != 1 {
		t.Fatalf("RemoveSaved(unknown) = %v, %v; want unchanged, nil", got, err)
	}

	// Concurrent saves on one instance keep both entries.
	var wg sync.WaitGroup
	for _, id := range []string{"200", "300"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.SaveMylist(id, "並行"+id); err != nil {
				t.Errorf("concurrent SaveMylist(%s) = %v; want nil", id, err)
			}
		}()
	}
	wg.Wait()
	got, err = m.ListSaved()
	if err != nil {
		t.Fatalf("ListSaved() = %v; want nil", err)
	}
	ids := map[string]bool{}
	for _, s := range got {
		ids[s.ID] = true
	}
	if !ids["200"] || !ids["300"] {
		t.Errorf("after concurrent saves, ids = %v; want both 200 and 300", ids)
	}

	// Malformed JSON is reported, never silently overwritten.
	bad := &Mylist{configDir: t.TempDir()}
	badPath := filepath.Join(bad.configDir, "mylists.json")
	if err := os.MkdirAll(bad.configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badPath, []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := bad.ListSaved(); err == nil {
		t.Error("ListSaved(corrupt) = nil error; want an error")
	}
	raw, _ := os.ReadFile(badPath)
	if string(raw) != "{oops" {
		t.Errorf("corrupt file was overwritten: %q", raw)
	}
}

func TestSaveMylistFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	m := &Mylist{client: srv.Client(), baseURL: srv.URL, configDir: t.TempDir()}
	if _, err := m.SaveMylist("27411728", "リスト"); err == nil {
		t.Error("SaveMylist(bad fetch) = nil error; want an error")
	}
	if got, err := m.ListSaved(); err != nil || len(got) != 0 {
		t.Errorf("a failed save must not persist: %v, %v", got, err)
	}
}

func TestFetchAllMergesPages(t *testing.T) {
	final := `{"meta":{"status":200},"data":{"mylist":{"name":"テストリスト","isPublic":true,` +
		`"items":[{"watchId":"sm4","video":{"id":"sm4","title":"曲D"}}],` +
		`"totalItemCount":4,"hasNext":false}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(mylistFixture)) // 3 items, hasNext true
			return
		}
		_, _ = w.Write([]byte(final))
	}))
	t.Cleanup(srv.Close)
	m := &Mylist{client: srv.Client(), baseURL: srv.URL}

	got, err := m.fetchAll("27411728")
	if err != nil {
		t.Fatalf("fetchAll returned an error: %v", err)
	}
	if len(got.Items) != 4 || got.Items[3].Title != "曲D" {
		t.Errorf("merged items = %+v; want 4 items incl. 曲D", got.Items)
	}
	if got.Name != "テストリスト" {
		t.Errorf("header must come from page 1: %+v", got)
	}
	if got.HasNext {
		t.Error("a snapshot must clear HasNext")
	}
}

func TestFetchAllStopsAtPageCap(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(mylistFixture)) // always hasNext true
	}))
	t.Cleanup(srv.Close)
	m := &Mylist{client: srv.Client(), baseURL: srv.URL}

	if _, err := m.fetchAll("27411728"); err != nil {
		t.Fatalf("fetchAll returned an error: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != maxMylistPages {
		t.Errorf("requests = %d; want the cap %d", n, maxMylistPages)
	}
}

func TestMapMylistPageThumbnailURLs(t *testing.T) {
	// url wins; middleUrl alone falls back to the same shape without .M;
	// videos without a thumbnail keep an empty URL.
	body := `{"meta":{"status":200},"data":{"mylist":{"name":"t","isPublic":true,"items":[` +
		`{"watchId":"sm1","video":{"id":"sm1","title":"a","thumbnail":{"url":"https://nicovideo.cdn.nimg.jp/thumbnails/1/1","middleUrl":"https://nicovideo.cdn.nimg.jp/thumbnails/1/1.M"}}},` +
		`{"watchId":"sm2","video":{"id":"sm2","title":"b","thumbnail":{"middleUrl":"https://nicovideo.cdn.nimg.jp/thumbnails/2/2.M"}}},` +
		`{"watchId":"sm3","video":{"id":"sm3","title":"c"}}` +
		`],"totalItemCount":3}}}`
	page, err := mapMylistPage("1", []byte(body))
	if err != nil {
		t.Fatalf("mapMylistPage returned an error: %v", err)
	}
	want := []string{
		"https://nicovideo.cdn.nimg.jp/thumbnails/1/1",
		"https://nicovideo.cdn.nimg.jp/thumbnails/2/2",
		"",
	}
	for i, w := range want {
		if got := page.Items[i].ThumbURL; got != w {
			t.Errorf("item %d ThumbURL = %q; want %q", i, got, w)
		}
	}
}

func TestThumbnailCandidates(t *testing.T) {
	m := &Mylist{}
	want := []string{
		"https://nicovideo.cdn.nimg.jp/thumbnails/27718606/27718606.M",
		"https://nicovideo.cdn.nimg.jp/thumbnails/27718606/27718606",
	}
	if got := m.thumbnailCandidates("sm27718606", ""); !slices.Equal(got, want) {
		t.Errorf("derived candidates = %q; want %q", got, want)
	}
	want = []string{"https://example.com/a/b.M", "https://example.com/a/b"}
	if got := m.thumbnailCandidates("sm1", "https://example.com/a/b"); !slices.Equal(got, want) {
		t.Errorf("stored candidates = %q; want %q", got, want)
	}
	if got := m.thumbnailCandidates("lv", ""); got != nil {
		t.Errorf("candidates without a number = %q; want none", got)
	}
}

func TestVideoNumberAndSafeID(t *testing.T) {
	for in, want := range map[string]string{"sm27718606": "27718606", "nm9": "9", "123": "123", "lv": "", "": ""} {
		if got := videoNumber(in); got != want {
			t.Errorf("videoNumber(%q) = %q; want %q", in, got, want)
		}
	}
	for _, id := range []string{"sm123", "sm-1_a", "ABC"} {
		if !isSafeID(id) {
			t.Errorf("isSafeID(%q) = false; want true", id)
		}
	}
	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "a b", strings.Repeat("a", 65)} {
		if isSafeID(id) {
			t.Errorf("isSafeID(%q) = true; want false", id)
		}
	}
}

func TestThumbnailCachesToDisk(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/thumbnails/1/1.M" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("middle-picture"))
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	host := srv.Listener.Addr().String()
	m := &Mylist{client: srv.Client(), configDir: dir, thumbHost: host}
	base := "https://" + host + "/thumbnails/1/1"

	got, err := m.Thumbnail("sm1", base)
	if err != nil || string(got) != "middle-picture" {
		t.Fatalf("Thumbnail() = %q, %v; want the .M picture", got, err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("requests = %d; want 1", n)
	}
	// The second call comes from the disk cache, and a fresh service over
	// the same directory reads the same cache file.
	if got, err = m.Thumbnail("sm1", base); err != nil || string(got) != "middle-picture" {
		t.Fatalf("cached Thumbnail() = %q, %v; want the .M picture", got, err)
	}
	fresh := &Mylist{client: srv.Client(), configDir: dir, thumbHost: host}
	if got, err = fresh.Thumbnail("sm1", base); err != nil || string(got) != "middle-picture" {
		t.Fatalf("fresh-service Thumbnail() = %q, %v; want the disk-cached picture", got, err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("requests = %d; want the picture fetched once", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "thumbs", "sm1.jpg")); err != nil {
		t.Errorf("cache file is missing: %v", err)
	}
}

func TestThumbnailFallsBackToSmallPicture(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".M") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("small-picture"))
	}))
	t.Cleanup(srv.Close)
	host := srv.Listener.Addr().String()
	m := &Mylist{client: srv.Client(), configDir: t.TempDir(), thumbHost: host}
	base := "https://" + host + "/thumbnails/9/9"

	got, err := m.Thumbnail("sm9", base)
	if err != nil || string(got) != "small-picture" {
		t.Fatalf("Thumbnail() = %q, %v; want the small fallback picture", got, err)
	}
	// The fallback picture is cached like any other.
	if got, err = m.Thumbnail("sm9", base); err != nil || string(got) != "small-picture" {
		t.Fatalf("cached Thumbnail() = %q, %v; want the small fallback picture", got, err)
	}
}

func TestThumbnailDerivesURLForOldSnapshots(t *testing.T) {
	var gotPath string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("derived-picture"))
	}))
	t.Cleanup(srv.Close)
	host := srv.Listener.Addr().String()
	m := &Mylist{client: srv.Client(), configDir: t.TempDir(), thumbHost: host}

	// An entry saved before ThumbURL existed has none: the CDN's URL shape
	// is derived from the video number.
	got, err := m.Thumbnail("sm27718606", "")
	if err != nil || string(got) != "derived-picture" {
		t.Fatalf("Thumbnail() = %q, %v; want the derived picture", got, err)
	}
	if gotPath != "/thumbnails/27718606/27718606.M" {
		t.Errorf("fetched %q; want the derived .M path", gotPath)
	}
	// An ID with no number has no URL to derive.
	if _, err := m.Thumbnail("lv", ""); err == nil {
		t.Error("Thumbnail(no number) = nil error; want an error")
	}
}

func TestThumbnailRejectsUntrustedResponses(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/big"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(make([]byte, maxThumbBody+1))
		case strings.HasPrefix(r.URL.Path, "/text"):
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>no</html>"))
		default:
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("picture"))
		}
	}))
	t.Cleanup(srv.Close)
	host := srv.Listener.Addr().String()
	m := &Mylist{client: srv.Client(), configDir: t.TempDir(), thumbHost: host}

	// A URL outside the thumbnail CDN is never fetched.
	if _, err := m.Thumbnail("sm1", "https://example.com/thumbnails/1/1"); err == nil {
		t.Error("a foreign host must be rejected")
	}
	if _, err := m.Thumbnail("sm1", "https://"+host+"/text"); err == nil {
		t.Error("a non-image response must be rejected")
	}
	if _, err := m.Thumbnail("sm1", "https://"+host+"/big"); err == nil {
		t.Error("an oversized response must be rejected")
	}
}

func TestNewMylistPolitenessDefaults(t *testing.T) {
	m := NewMylist()
	if m.thumbLimiter == nil {
		t.Fatal("NewMylist must set the thumbnail limiter")
	}
	if m.thumbLimiter.interval != thumbInterval || m.thumbLimiter.cooldown != thumbCooldown || cap(m.thumbLimiter.slots) != thumbConcurrency {
		t.Errorf("limiter = %+v; want interval %v, cooldown %v, %d slots", m.thumbLimiter, thumbInterval, thumbCooldown, thumbConcurrency)
	}
	if m.pageInterval != mylistPageInterval {
		t.Errorf("pageInterval = %v; want %v", m.pageInterval, mylistPageInterval)
	}
	if m.client.Timeout <= 0 {
		t.Error("client must keep a timeout")
	}
}

func TestThumbnailLimiterBoundsConcurrencyAndSpacing(t *testing.T) {
	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0
	var starts []time.Time
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		starts = append(starts, time.Now())
		mu.Unlock()
		// Hold the request long enough that a second fetch overlaps it unless
		// the limiter bounds them.
		time.Sleep(75 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("picture"))
	}))
	t.Cleanup(srv.Close)
	host := srv.Listener.Addr().String()
	m := &Mylist{
		client:       srv.Client(),
		configDir:    t.TempDir(),
		thumbHost:    host,
		thumbLimiter: newThumbnailLimiter(2, 50*time.Millisecond, time.Second),
	}

	const n = 5
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("sm%d", i)
			if _, err := m.Thumbnail(id, "https://"+host+"/thumbnails/1/1"); err != nil {
				t.Errorf("Thumbnail(%s) = %v; want a picture", id, err)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if maxInFlight > 2 {
		t.Errorf("max concurrent fetches = %d; want at most 2", maxInFlight)
	}
	if len(starts) != n {
		t.Fatalf("requests = %d; want %d", len(starts), n)
	}
	if spread := starts[len(starts)-1].Sub(starts[0]); spread < 150*time.Millisecond {
		t.Errorf("requests started %v apart; want the interval to space them", spread)
	}
}

func TestThumbnailPausesAfter429(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var times []time.Time
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		times = append(times, time.Now())
		first := len(paths) == 1
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("picture"))
	}))
	t.Cleanup(srv.Close)
	host := srv.Listener.Addr().String()
	m := &Mylist{
		client:       srv.Client(),
		configDir:    t.TempDir(),
		thumbHost:    host,
		thumbLimiter: newThumbnailLimiter(1, time.Millisecond, 80*time.Millisecond),
	}

	// The 429 stops the whole lookup: no fallback URL is tried.
	if _, err := m.Thumbnail("sm1", "https://"+host+"/thumbnails/1/1"); err == nil {
		t.Error("Thumbnail(429) = nil error; want a busy error")
	}
	// The next fetch waits out the cooldown before asking again.
	if _, err := m.Thumbnail("sm2", "https://"+host+"/thumbnails/2/2"); err != nil {
		t.Fatalf("Thumbnail after cooldown = %v; want a picture", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != "/thumbnails/1/1.M" || paths[1] != "/thumbnails/2/2.M" {
		t.Fatalf("requested %v; want the first .M and then the second .M only", paths)
	}
	if gap := times[1].Sub(times[0]); gap < 60*time.Millisecond {
		t.Errorf("second fetch came %v later; want the cooldown to hold it", gap)
	}
}

func TestRetryAfter(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	cases := []struct {
		header string
		want   time.Duration
	}{
		{"3", 3 * time.Second},
		{"0", 0},
		{"999", retryAfterMax},
		{"garbage", retryAfterDefault},
		{"", retryAfterDefault},
		{past, 0},
	}
	for _, tc := range cases {
		resp := &http.Response{Header: http.Header{}}
		if tc.header != "" {
			resp.Header.Set("Retry-After", tc.header)
		}
		if got := retryAfter(resp); got != tc.want {
			t.Errorf("retryAfter(%q) = %v; want %v", tc.header, got, tc.want)
		}
	}
}

func TestFetchPageWaitsOutRetryAfter(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(mylistFixture))
	}))
	t.Cleanup(srv.Close)
	m := &Mylist{client: srv.Client(), baseURL: srv.URL}

	page, err := m.GetPage("27411728", 1)
	if err != nil || page.Name != "テストリスト" {
		t.Fatalf("GetPage after one 429 = %+v, %v; want the page", page, err)
	}
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Errorf("requests = %d; want the 429 honored with one retry", n)
	}
}

func TestFetchPageGivesUpAfterSecondRefusal(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	m := &Mylist{client: srv.Client(), baseURL: srv.URL}

	_, err := m.GetPage("27411728", 1)
	if err == nil || !strings.Contains(err.Error(), "混み合っています") {
		t.Fatalf("GetPage with a busy API = %v; want a busy message", err)
	}
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Errorf("requests = %d; want one retry only", n)
	}
}

func TestFetchAllSpacesPages(t *testing.T) {
	var mu sync.Mutex
	var starts []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"meta":{"status":200},"data":{"mylist":{"name":"t","isPublic":true,"items":[],"totalItemCount":100,"hasNext":false}}}`))
			return
		}
		_, _ = w.Write([]byte(mylistFixture)) // hasNext true
	}))
	t.Cleanup(srv.Close)
	m := &Mylist{client: srv.Client(), baseURL: srv.URL, pageInterval: 40 * time.Millisecond}

	if _, err := m.fetchAll("27411728"); err != nil {
		t.Fatalf("fetchAll returned an error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 2 {
		t.Fatalf("requests = %d; want 2", len(starts))
	}
	if gap := starts[1].Sub(starts[0]); gap < 30*time.Millisecond {
		t.Errorf("pages fetched %v apart; want the page interval to space them", gap)
	}
}
