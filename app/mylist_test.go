package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
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
	`{"watchId":"sm1","video":{"id":"sm1","title":"曲A","duration":125,"owner":{"name":"作者A"}}},` +
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
