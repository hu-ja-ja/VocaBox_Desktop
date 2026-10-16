package main

import (
	"errors"
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
	`"name":"テストマイリスト","description":"説明文","isPublic":true,` +
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
	if gotReq.URL.Path != "/v2/mylists/27411728" || gotReq.URL.Query().Get("page") != "2" || gotReq.URL.Query().Get("pageSize") != "100" {
		t.Errorf("unexpected request URL: %s", gotReq.URL)
	}
	if gotReq.Header.Get("X-Frontend-ID") != "6" || gotReq.Header.Get("X-Frontend-Version") != "0" {
		t.Errorf("missing frontend headers: %v", gotReq.Header)
	}
	if ua := gotReq.Header.Get("User-Agent"); ua == "" {
		t.Error("missing identifying User-Agent")
	}
	if page.ID != "27411728" || page.Name != "テストマイリスト" || page.Description != "説明文" || page.Owner != "投稿者" {
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

// TestGetPagePrivateFallsBackToOwn checks the private path: the public
// endpoint refuses the mylist, and a signed-in request retries the account's
// own endpoint, carrying the session cookie.
func TestGetPagePrivateFallsBackToOwn(t *testing.T) {
	private := strings.Replace(mylistFixture, `"isPublic":true`, `"isPublic":false`, 1)
	var publicHits, ownHits int32
	var ownReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v2/") {
			atomic.AddInt32(&publicHits, 1)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt32(&ownHits, 1)
		ownReq = r
		_, _ = w.Write([]byte(private))
	}))
	t.Cleanup(srv.Close)

	m := &Mylist{
		client:  srv.Client(),
		baseURL: srv.URL,
		session: func(string) string { return "user_session=abc" },
	}
	page, err := m.GetPage("27411728", 1)
	if err != nil {
		t.Fatalf("GetPage returned an error: %v", err)
	}
	if publicHits != 1 || ownHits != 1 {
		t.Errorf("requests: public=%d own=%d; want one each", publicHits, ownHits)
	}
	if ownReq.URL.Path != "/v1/users/me/mylists/27411728" {
		t.Errorf("own request path = %q", ownReq.URL.Path)
	}
	if q := ownReq.URL.Query(); q.Get("pageSize") != "100" || q.Get("page") != "1" {
		t.Errorf("own request query = %q", ownReq.URL.RawQuery)
	}
	if got := ownReq.Header.Get("Cookie"); got != "user_session=abc" {
		t.Errorf("own request Cookie = %q; want the session", got)
	}
	// The private mylist maps like any other.
	if page.Name != "テストマイリスト" || len(page.Items) != 3 || page.TotalItemCount != 3 {
		t.Errorf("own page = %+v; want the private mylist's contents", page)
	}
}

// TestGetPageGuestSkipsOwnEndpoint checks that a guest never probes the
// account endpoint, and hears that login is what a private mylist needs.
func TestGetPageGuestSkipsOwnEndpoint(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	m := &Mylist{client: srv.Client(), baseURL: srv.URL}
	_, err := m.GetPage("27411728", 1)
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "ログイン") {
		t.Errorf("error = %v; want a login hint", err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("requests = %d; want only the public one", n)
	}
}

// TestGetPagePublicDoesNotFallBack checks that a public mylist never costs
// the own probe, even signed in.
func TestGetPagePublicDoesNotFallBack(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(mylistFixture))
	}))
	t.Cleanup(srv.Close)

	m := &Mylist{
		client:  srv.Client(),
		baseURL: srv.URL,
		session: func(string) string { return "user_session=abc" },
	}
	if _, err := m.GetPage("27411728", 1); err != nil {
		t.Fatalf("GetPage returned an error: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("requests = %d; want only the public one", n)
	}
}

// ownMylistsFixture is the account's mylists for the ListOwn tests: one private
// mylist with a sample entry, one public with none.
const ownMylistsFixture = `{"meta":{"status":200},"data":{"mylists":[` +
	`{"id":71043760,"isPublic":false,"name":"非公開マイリスト","itemsCount":2,` +
	`"sampleItems":[{"watchId":"sm1","video":{"id":"sm1","title":"曲A","duration":125,"thumbnail":{"url":"https://nicovideo.cdn.nimg.jp/thumbnails/1/1"}}}]},` +
	`{"id":71043761,"isPublic":true,"name":"公開マイリスト","itemsCount":0,"sampleItems":[]}` +
	`]}}`

func TestListOwn(t *testing.T) {
	var gotReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r
		_, _ = w.Write([]byte(ownMylistsFixture))
	}))
	t.Cleanup(srv.Close)

	m := &Mylist{
		client:  srv.Client(),
		baseURL: srv.URL,
		session: func(string) string { return "user_session=abc" },
	}
	own, err := m.ListOwn()
	if err != nil {
		t.Fatalf("ListOwn returned an error: %v", err)
	}
	if gotReq.URL.Path != "/v1/users/me/mylists" || gotReq.URL.Query().Get("sampleItemCount") != "4" {
		t.Errorf("request URL = %s", gotReq.URL)
	}
	if got := gotReq.Header.Get("Cookie"); got != "user_session=abc" {
		t.Errorf("Cookie = %q; want the session", got)
	}
	if len(own) != 2 {
		t.Fatalf("got %d mylists, want 2", len(own))
	}
	if own[0].ID != "71043760" || own[0].Name != "非公開マイリスト" || own[0].IsPublic || own[0].ItemCount != 2 {
		t.Errorf("private mylist = %+v", own[0])
	}
	if len(own[0].Items) != 1 || own[0].Items[0].Title != "曲A" || own[0].Items[0].ThumbURL == "" {
		t.Errorf("sample entries = %+v", own[0].Items)
	}
	if own[1].ID != "71043761" || !own[1].IsPublic || own[1].ItemCount != 0 || len(own[1].Items) != 0 {
		t.Errorf("public mylist = %+v", own[1])
	}
}

// TestListOwnRequiresLogin checks a guest's account mylists are refused with a
// login hint rather than looking like an empty account.
func TestListOwnRequiresLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	m := &Mylist{client: srv.Client(), baseURL: srv.URL}
	_, err := m.ListOwn()
	if err == nil {
		t.Fatal("ListOwn as a guest = nil error; want a refusal")
	}
	if !strings.Contains(err.Error(), "ログイン") {
		t.Errorf("error = %v; want a login hint", err)
	}
}

// savedFixture is a one-page mylist used by the saved-body tests.
const savedFixture = `{"meta":{"status":200},"data":{"mylist":{` +
	`"name":"保存マイリスト","description":"保存説明","isPublic":true,` +
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
	m, hits := newSavedService(t, savedFixture)

	// Missing index means none saved yet.
	got, err := m.ListSaved()
	if err != nil || len(got) != 0 {
		t.Fatalf("ListSaved() = %v, %v; want [], nil", got, err)
	}

	// Invalid IDs are rejected before any request.
	if _, err := m.SaveMylist("not a mylist"); err == nil {
		t.Error("SaveMylist(bad input) = nil error; want an error")
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Errorf("invalid input made %d requests; want none", n)
	}

	// Save two, then upsert one by URL without duplicating; a save pins.
	if _, err := m.SaveMylist("27411728"); err != nil {
		t.Fatalf("SaveMylist = %v; want nil", err)
	}
	if _, err := m.SaveMylist("https://www.nicovideo.jp/mylist/100"); err != nil {
		t.Fatalf("SaveMylist = %v; want nil", err)
	}
	got, err = m.SaveMylist("https://nico.ms/mylist/27411728")
	if err != nil {
		t.Fatalf("SaveMylist upsert = %v; want nil", err)
	}
	if len(got) != 2 || got[0].ID != "27411728" || got[1].ID != "100" {
		t.Fatalf("upserted index = %+v; want 27411728 then 100, no duplicates", got)
	}
	if !got[0].Pinned || !got[1].Pinned {
		t.Errorf("a save must pin: %+v", got)
	}

	// The index carries what a card draws; the body holds the list.
	entry := got[0]
	if entry.Name != "保存マイリスト" || entry.Owner != "保存者" || entry.TotalItemCount != 2 || len(entry.Sample) != 2 {
		t.Fatalf("index line = %+v; want the fetched header and sample", entry)
	}
	body, err := m.LoadSavedBody("27411728")
	if err != nil {
		t.Fatalf("LoadSavedBody = %v; want nil", err)
	}
	if body.Name != "保存マイリスト" || body.Description != "保存説明" || body.TotalItemCount != 2 || len(body.Items) != 2 {
		t.Fatalf("stored body = %+v; want fetched contents", body)
	}
	if body.Items[0].Title != "曲A" || body.Items[1].Available {
		t.Errorf("stored items = %+v; want fetched contents", body.Items)
	}

	// A fresh service over the same dir reads the same index and bodies; a
	// mylist without one reports os.ErrNotExist.
	same := &Mylist{configDir: m.configDir}
	got, err = same.ListSaved()
	if err != nil || len(got) != 2 || len(got[0].Sample) != 2 {
		t.Fatalf("ListSaved() = %+v, %v; want 2 records with samples, nil", got, err)
	}
	if body, err := same.LoadSavedBody("100"); err != nil || len(body.Items) != 2 {
		t.Fatalf("LoadSavedBody(100) = %+v, %v; want the body", body, err)
	}
	if _, err := same.LoadSavedBody("999"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("LoadSavedBody(unknown) = %v; want os.ErrNotExist", err)
	}

	// Pinning and unpinning write the index only: no request, body kept.
	before := atomic.LoadInt32(hits)
	got, err = m.SetPinned("100", false)
	if err != nil || got[1].Pinned {
		t.Fatalf("SetPinned(false) = %+v, %v; want the pin off", got, err)
	}
	if n := atomic.LoadInt32(hits); n != before {
		t.Errorf("SetPinned made %d requests; want none", n-before)
	}
	if !m.HasSavedBody("100") {
		t.Error("unpinning must keep the saved body")
	}
	if got, err = m.SetPinned("100", true); err != nil || !got[1].Pinned {
		t.Fatalf("SetPinned(true) = %+v, %v; want the pin on", got, err)
	}

	// Refresh replaces the body and keeps the pin.
	got, err = m.RefreshMylist("100")
	if err != nil || !got[1].Pinned {
		t.Fatalf("RefreshMylist = %+v, %v; want the pin kept", got, err)
	}

	// TrackMylist records an opened mylist without fetching it: a new one
	// is added unpinned, an existing one keeps its pin.
	got, err = m.TrackMylist(MylistPage{ID: "200", Name: "読み込んだ", TotalItemCount: 1,
		Items: []MylistItem{{WatchID: "sm9", Title: "読み込み曲"}}})
	if err != nil {
		t.Fatalf("TrackMylist = %v; want nil", err)
	}
	if len(got) != 3 || got[2].ID != "200" || got[2].Pinned || got[2].Name != "読み込んだ" {
		t.Fatalf("tracked index = %+v; want 200 added unpinned", got)
	}
	got, _ = m.TrackMylist(MylistPage{ID: "100", Name: "更新された名前", TotalItemCount: 3})
	if !got[1].Pinned || got[1].Name != "更新された名前" {
		t.Errorf("TrackMylist must keep the pin and take the new name: %+v", got[1])
	}

	// Remove deletes the index line and the body; unknown IDs are idempotent.
	got, err = m.RemoveSaved("100")
	if err != nil || len(got) != 2 || got[0].ID != "27411728" {
		t.Fatalf("RemoveSaved() = %v, %v; want [27411728 200], nil", got, err)
	}
	if m.HasSavedBody("100") {
		t.Error("RemoveSaved must delete the body")
	}
	got, err = m.RemoveSaved("999")
	if err != nil || len(got) != 2 {
		t.Fatalf("RemoveSaved(unknown) = %v, %v; want unchanged, nil", got, err)
	}

	// Concurrent saves on one instance keep both entries.
	var wg sync.WaitGroup
	for _, id := range []string{"300", "400"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.SaveMylist(id); err != nil {
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
	if !ids["300"] || !ids["400"] {
		t.Errorf("after concurrent saves, ids = %v; want both 300 and 400", ids)
	}

	// Malformed index JSON is reported, never silently overwritten.
	bad := &Mylist{configDir: t.TempDir()}
	badPath := filepath.Join(bad.configDir, "mylists", "index.json")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
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

// TestListSavedReadsOnlyTheIndex checks that listing needs no body: the
// bodies are read when their mylist opens.
func TestListSavedReadsOnlyTheIndex(t *testing.T) {
	m, _ := newSavedService(t, savedFixture)
	if _, err := m.SaveMylist("27411728"); err != nil {
		t.Fatal(err)
	}
	path, err := m.bodyPath("27411728")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListSaved()
	if err != nil || len(got) != 1 {
		t.Fatalf("ListSaved() with the body gone = %+v, %v; want the index line", got, err)
	}
	if _, err := m.LoadSavedBody("27411728"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("LoadSavedBody with the body gone = %v; want os.ErrNotExist", err)
	}
}

// TestSyncOwnMergesAndPrunes checks the merge of the account's mylists into
// the index: live metadata lands, a list the account dropped loses its cache
// unless it is pinned.
func TestSyncOwnMergesAndPrunes(t *testing.T) {
	m, _ := newSavedService(t, savedFixture)
	own := []OwnMylist{
		{ID: "111", Name: "自分の公開", IsPublic: true, ItemCount: 2,
			Items: []MylistItem{{WatchID: "sma", Title: "自分の曲"}}},
		{ID: "222", Name: "自分の非公開", IsPublic: false, ItemCount: 1},
	}
	got, err := m.SyncOwn(own)
	if err != nil || len(got) != 2 {
		t.Fatalf("SyncOwn(own) = %+v, %v; want both lists", got, err)
	}
	if !got[0].Mine || got[0].Private || got[0].Name != "自分の公開" || got[0].TotalItemCount != 2 {
		t.Errorf("merged own line = %+v; want live name, count, and privacy", got[0])
	}
	if !got[1].Mine || !got[1].Private {
		t.Errorf("private own line = %+v; want Mine and Private", got[1])
	}
	if len(got[0].Sample) != 1 || got[0].Sample[0].WatchID != "sma" {
		t.Errorf("own sample = %+v; want the account's sample entries", got[0].Sample)
	}

	// Save and pin one body; the other stays a cache without a body. Then
	// the account drops both and gains a new list.
	if _, err := m.SaveMylist("111"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RefreshMylist("222"); err != nil {
		t.Fatal(err)
	}
	got, err = m.SyncOwn([]OwnMylist{{ID: "333", Name: "新しいリスト", IsPublic: true, ItemCount: 5}})
	if err != nil {
		t.Fatalf("SyncOwn(dropped) = %v; want nil", err)
	}
	byID := map[string]SavedIndexEntry{}
	for _, entry := range got {
		byID[entry.ID] = entry
	}
	if entry, ok := byID["111"]; !ok || !entry.Pinned || !entry.Mine {
		t.Errorf("pinned own mylist must stay: %+v", byID["111"])
	}
	if !m.HasSavedBody("111") {
		t.Error("the pinned mylist's body must stay")
	}
	if _, ok := byID["222"]; ok {
		t.Errorf("an unpinned mylist the account dropped must go: %+v", byID["222"])
	}
	if m.HasSavedBody("222") {
		t.Error("a dropped cache must lose its body")
	}
	if entry, ok := byID["333"]; !ok || !entry.Mine || entry.Pinned {
		t.Errorf("the new own mylist must be added unpinned: %+v", byID["333"])
	}
}

func TestSaveMylistFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	m := &Mylist{client: srv.Client(), baseURL: srv.URL, configDir: t.TempDir()}
	if _, err := m.SaveMylist("27411728"); err == nil {
		t.Error("SaveMylist(bad fetch) = nil error; want an error")
	}
	if got, err := m.ListSaved(); err != nil || len(got) != 0 {
		t.Errorf("a failed save must not persist: %v, %v", got, err)
	}
	if m.HasSavedBody("27411728") {
		t.Error("a failed save must not leave a body")
	}
}

func TestFetchAllMergesPages(t *testing.T) {
	final := `{"meta":{"status":200},"data":{"mylist":{"name":"テストマイリスト","isPublic":true,` +
		`"items":[{"watchId":"sm4","video":{"id":"sm4","title":"曲D","duration":125}}],` +
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
	if got.Name != "テストマイリスト" {
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

// TestMapMylistItemSkipsDeletedVideos checks the stub the mylist API answers
// with for a deleted or private video — a placeholder picture, a hidden
// owner, no duration — which the list shows by the stub's own title without
// playing it, while a video niconico still serves keeps its metadata. A
// hidden owner alone is not the stub's mark: the duration decides, so a video
// the API hides the owner of but would still play is not dropped.
func TestMapMylistItemSkipsDeletedVideos(t *testing.T) {
	body := `{"meta":{"status":200},"data":{"mylist":{"name":"t","isPublic":true,"items":[` +
		`{"watchId":"sm1","video":{"id":"sm1","title":"曲A","duration":125,"owner":{"name":"作者","visibility":"visible"}}},` +
		`{"watchId":"sm2","video":{"id":"sm2","title":"削除された動画","duration":0,"owner":{"ownerType":"hidden","type":"unknown","visibility":"hidden","name":""}}},` +
		`{"watchId":"sm3","video":{"id":"sm3","title":"曲C","owner":{"name":"作者"}}},` +
		`{"watchId":"sm4","video":null},` +
		`{"watchId":"sm5","video":{"id":"sm5","title":"自分の非公開曲","duration":125,"owner":{"visibility":"hidden","name":""}}}` +
		`],"totalItemCount":5}}}`
	page, err := mapMylistPage("1", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 5 {
		t.Fatalf("items = %d; want the whole page listed", len(page.Items))
	}
	served := page.Items[0]
	if !served.Available || served.Title != "曲A" || served.Duration != 125 {
		t.Fatalf("a served video lost its metadata: %+v", served)
	}
	stub := page.Items[1]
	if stub.Available || stub.Duration != 0 || stub.Title == "" {
		t.Fatalf("a deleted video's stub read as a track: %+v", stub)
	}
	if isPlayable(stub) {
		t.Fatalf("a deleted video's stub reads as playable: %+v", stub)
	}
	if page.Items[3].Available {
		t.Fatalf("a video the API did not answer for read as a track: %+v", page.Items[3])
	}
	private := page.Items[4]
	if !private.Available || private.Duration != 125 || !isPlayable(private) {
		t.Fatalf("a video with a hidden owner and a duration reads as deleted: %+v", private)
	}
}

func TestMapMylistPageThumbnailURLs(t *testing.T) {
	// url wins; middleUrl alone falls back to the same shape without .M;
	// videos without a thumbnail keep an empty URL.
	body := `{"meta":{"status":200},"data":{"mylist":{"name":"t","isPublic":true,"items":[` +
		`{"watchId":"sm1","video":{"id":"sm1","title":"a","duration":125,"thumbnail":{"url":"https://nicovideo.cdn.nimg.jp/thumbnails/1/1","middleUrl":"https://nicovideo.cdn.nimg.jp/thumbnails/1/1.M"}}},` +
		`{"watchId":"sm2","video":{"id":"sm2","title":"b","duration":125,"thumbnail":{"middleUrl":"https://nicovideo.cdn.nimg.jp/thumbnails/2/2.M"}}},` +
		`{"watchId":"sm3","video":{"id":"sm3","title":"c","duration":125}}` +
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
	// A derived URL takes the .M size.
	derived := "https://nicovideo.cdn.nimg.jp/thumbnails/27718606/27718606"
	want := []string{derived + ".M", derived}
	if got := m.thumbnailCandidates("sm27718606", ""); !slices.Equal(got, want) {
		t.Errorf("derived candidates = %q; want %q", got, want)
	}
	// So does the API's own URL, version hash and all.
	hashed := "https://nicovideo.cdn.nimg.jp/thumbnails/42448221/42448221.86845938"
	want = []string{hashed + ".M", hashed}
	if got := m.thumbnailCandidates("sm42448221", hashed); !slices.Equal(got, want) {
		t.Errorf("hashed candidates = %q; want %q", got, want)
	}
	// A URL of another shape is used as it is.
	deleted := "https://resource.video.nimg.jp/web/img/common/video_deleted.jpg?_t=20181018"
	if got := m.thumbnailCandidates("sm1", deleted); !slices.Equal(got, []string{deleted}) {
		t.Errorf("deleted candidates = %q; want just the URL", got)
	}
	foreign := "https://example.com/a/b"
	if got := m.thumbnailCandidates("sm1", foreign); !slices.Equal(got, []string{foreign}) {
		t.Errorf("foreign candidates = %q; want just the URL", got)
	}
	if got := m.thumbnailCandidates("lv", ""); got != nil {
		t.Errorf("candidates without a number = %q; want none", got)
	}
}

func TestThumbHostAllowed(t *testing.T) {
	m := &Mylist{}
	for _, host := range []string{"nicovideo.cdn.nimg.jp", "resource.video.nimg.jp", "img.cdn.nimg.jp", "nimg.jp", "NICOVIDEO.CDN.NIMG.JP"} {
		if !m.thumbHostAllowed(host) {
			t.Errorf("thumbHostAllowed(%q) = false; want true", host)
		}
	}
	for _, host := range []string{"example.com", "nimg.jp.evil.example", "evil-nimg.jp", "xnimg.jp", "", "127.0.0.1:8443"} {
		if m.thumbHostAllowed(host) {
			t.Errorf("thumbHostAllowed(%q) = true; want false", host)
		}
	}
	// A test override allows its own server only.
	override := &Mylist{thumbHost: "127.0.0.1:8443"}
	if !override.thumbHostAllowed("127.0.0.1:8443") {
		t.Error("the test host must be allowed")
	}
	if override.thumbHostAllowed("resource.video.nimg.jp") {
		t.Error("a test override must not allow the live CDN")
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

func TestThumbnailPrefersMiddleSizeForHashedURL(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if !strings.HasSuffix(r.URL.Path, ".M") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("middle-picture"))
	}))
	t.Cleanup(srv.Close)
	host := srv.Listener.Addr().String()
	m := &Mylist{client: srv.Client(), configDir: t.TempDir(), thumbHost: host}

	// The API names the small picture under a version hash; the .M size is
	// that same URL with .M appended.
	base := "https://" + host + "/thumbnails/42448221/42448221.86845938"
	got, err := m.Thumbnail("sm42448221", base)
	if err != nil || string(got) != "middle-picture" {
		t.Fatalf("Thumbnail() = %q, %v; want the .M picture", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "/thumbnails/42448221/42448221.86845938.M" {
		t.Errorf("requested %v; want the hashed .M picture first", paths)
	}
}

func TestThumbnailFallsBackOnlyWhenMissing(t *testing.T) {
	t.Run("a 404 falls back to the small picture and caches it", func(t *testing.T) {
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

		got, err := m.Thumbnail("sm9", "https://"+host+"/thumbnails/9/9")
		if err != nil || string(got) != "small-picture" {
			t.Fatalf("Thumbnail() = %q, %v; want the small picture", got, err)
		}
		if !m.HasThumbnail("sm9") {
			t.Error("the fallback picture must be cached")
		}
	})

	t.Run("a server error keeps the lesser picture out of the cache", func(t *testing.T) {
		var mu sync.Mutex
		var paths []string
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.URL.Path)
			mu.Unlock()
			if strings.HasSuffix(r.URL.Path, ".M") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("small-picture"))
		}))
		t.Cleanup(srv.Close)
		host := srv.Listener.Addr().String()
		m := &Mylist{client: srv.Client(), configDir: t.TempDir(), thumbHost: host}

		if _, err := m.Thumbnail("sm9", "https://"+host+"/thumbnails/9/9"); err == nil {
			t.Fatal("Thumbnail() = nil error; want the server error")
		}
		mu.Lock()
		defer mu.Unlock()
		if len(paths) != 1 {
			t.Errorf("requested %v; want only the .M attempt", paths)
		}
		if m.HasThumbnail("sm9") {
			t.Error("a temporary failure must not cache a lesser picture")
		}
	})
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

func TestThumbnailFetchesFileNamedURL(t *testing.T) {
	var hits int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("deleted-picture"))
	}))
	t.Cleanup(srv.Close)
	host := srv.Listener.Addr().String()
	m := &Mylist{client: srv.Client(), configDir: t.TempDir(), thumbHost: host}

	// A URL that names a jpg with a query, as the API's deleted-video
	// placeholder does, is fetched once and as it is.
	picture := "https://" + host + "/web/img/common/video_deleted.jpg?_t=20181018"
	got, err := m.Thumbnail("sm1", picture)
	if err != nil || string(got) != "deleted-picture" {
		t.Fatalf("Thumbnail() = %q, %v; want the placeholder picture", got, err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("requests = %d; want the file-named URL fetched as it is", n)
	}
}

func TestHasThumbnail(t *testing.T) {
	dir := t.TempDir()
	m := &Mylist{configDir: dir}
	if m.HasThumbnail("sm1") {
		t.Error("HasThumbnail before a fetch = true; want false")
	}
	if m.HasThumbnail("bad/../id") {
		t.Error("HasThumbnail of an unsafe ID = true; want false")
	}
	if err := os.MkdirAll(filepath.Join(dir, "thumbs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "thumbs", "sm1.jpg"), []byte("picture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !m.HasThumbnail("sm1") {
		t.Error("HasThumbnail with the cache file in place = false; want true")
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
	if err != nil || page.Name != "テストマイリスト" {
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

func TestSetPinnedJoinsPinnedEnd(t *testing.T) {
	m := &Mylist{configDir: t.TempDir()}
	if err := m.writeIndex([]SavedIndexEntry{{ID: "100"}, {ID: "200"}, {ID: "300"}}); err != nil {
		t.Fatal(err)
	}
	// Pinned in an order unlike the index order: each joins the end of the
	// pinned group rather than its front.
	for _, id := range []string{"300", "100", "200"} {
		if _, err := m.SetPinned(id, true); err != nil {
			t.Fatal(err)
		}
	}
	pinned := func(entries []SavedIndexEntry) []string {
		var ids []string
		for _, e := range entries {
			if e.Pinned {
				ids = append(ids, e.ID)
			}
		}
		return ids
	}
	saved, err := m.ListSaved()
	if err != nil {
		t.Fatal(err)
	}
	if got := pinned(saved); !slices.Equal(got, []string{"300", "100", "200"}) {
		t.Fatalf("pinned order = %v; want 300 100 200", got)
	}
	// Unpinning the first leaves the rest in the order they were pinned.
	if saved, err = m.SetPinned("300", false); err != nil {
		t.Fatal(err)
	}
	if got := pinned(saved); !slices.Equal(got, []string{"100", "200"}) {
		t.Fatalf("pinned order after unpin = %v; want 100 200", got)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	m := &Mylist{configDir: t.TempDir()}
	// A missing file yields the defaults.
	got, err := m.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() = %v", err)
	}
	if got != defaultSettings() {
		t.Fatalf("defaults = %#v; want %#v", got, defaultSettings())
	}

	want := Settings{Volume: 0.35, Shuffle: true, Repeat: true}
	if err := m.SaveSettings(want); err != nil {
		t.Fatalf("SaveSettings() = %v", err)
	}
	if got, err = m.LoadSettings(); err != nil || got != want {
		t.Fatalf("round trip = %#v, %v; want %#v", got, err, want)
	}

	// A file an older version wrote, missing a field, keeps that default.
	path, err := m.settingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"repeat":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err = m.LoadSettings(); err != nil || got.Volume != 1 || got.Shuffle || !got.Repeat {
		t.Fatalf("partial file = %#v, %v; want volume 1, shuffle off, repeat on", got, err)
	}
}
