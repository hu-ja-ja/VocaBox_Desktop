package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
