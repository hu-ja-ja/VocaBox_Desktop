package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// fillIdle waits for the background saving to finish everything queued, and
// runs what the worker handed over, so a test can read the view without
// racing the worker.
func fillIdle(t *testing.T, app *nativeApp) {
	t.Helper()
	waitForApp(t, app, "the background saving to go idle", func() bool {
		app.fill.mu.Lock()
		defer app.fill.mu.Unlock()
		return len(app.fill.pending) == 0 && len(app.fill.queued) == 0
	})
}

// ownFillFixture is the account's mylists as the sign-in sync sees them: a
// public one with a sample entry and a private empty one.
const ownFillFixture = `{"meta":{"status":200},"data":{"mylists":[` +
	`{"id":200,"isPublic":true,"name":"自分のA","itemsCount":1,` +
	`"sampleItems":[{"watchId":"sm1","video":{"id":"sm1","title":"曲A","duration":125}}]},` +
	`{"id":300,"isPublic":false,"name":"自分のB","itemsCount":0,"sampleItems":[]}` +
	`]}}`

// fillBody is the one-page body the fill test's server serves for a mylist.
func fillBody(name string) string {
	return fmt.Sprintf(`{"meta":{"status":200},"data":{"mylist":{"name":%q,"isPublic":true,`+
		`"items":[{"watchId":"sm1","video":{"id":"sm1","title":"曲A","duration":125}}],`+
		`"totalItemCount":1,"hasNext":false}}}`, name)
}

// TestOwnMylistsFillBodiesInBackground checks the sign-in flow: the account's
// mylists land in the index and one worker saves the missing bodies.
func TestOwnMylistsFillBodiesInBackground(t *testing.T) {
	var pages int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/users/me/mylists", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ownFillFixture))
	})
	mux.HandleFunc("/v2/mylists/200", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pages, 1)
		_, _ = w.Write([]byte(fillBody("自分のA")))
	})
	mux.HandleFunc("/v2/mylists/300", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pages, 1)
		_, _ = w.Write([]byte(fillBody("自分のB")))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	m := &Mylist{client: srv.Client(), baseURL: srv.URL, configDir: t.TempDir()}
	app := newNativeApp(m)
	app.loggedIn = true

	app.loadOwnMylists()
	waitForApp(t, app, "both own mylists' bodies", func() bool {
		return m.HasSavedBody("200") && m.HasSavedBody("300")
	})
	fillIdle(t, app)

	index, err := m.ListSaved()
	if err != nil || len(index) != 2 {
		t.Fatalf("ListSaved() = %+v, %v; want both own mylists", index, err)
	}
	if !index[0].Mine || index[0].Name != "自分のA" || len(index[0].Sample) != 1 {
		t.Errorf("merged own line = %+v; want live metadata and the sample", index[0])
	}
	if !index[1].Mine || !index[1].Private || index[1].Name != "自分のB" {
		t.Errorf("private own line = %+v; want Mine and Private", index[1])
	}
	if body, err := m.LoadSavedBody("200"); err != nil || len(body.Items) != 1 {
		t.Errorf("LoadSavedBody(200) = %+v, %v; want the saved body", body, err)
	}
	if len(app.saved) != 2 {
		t.Errorf("the view holds %d saved mylists; want 2", len(app.saved))
	}

	// A saved body is never fetched again.
	before := atomic.LoadInt32(&pages)
	app.fillSavedBody("200")
	if got := atomic.LoadInt32(&pages); got != before {
		t.Errorf("filling a saved body made %d requests; want none", got-before)
	}
}

// TestOpeningAMylistSavesItsBody checks that opening a mylist records it and
// saves its body in the background, so opening it again needs no network.
func TestOpeningAMylistSavesItsBody(t *testing.T) {
	m, hits := newSavedService(t, savedFixture)
	app := newNativeApp(m)

	app.fetchMylist("27411728")
	waitForApp(t, app, "the opened mylist's body", func() bool { return m.HasSavedBody("27411728") })
	fillIdle(t, app)

	index, err := m.ListSaved()
	if err != nil || len(index) != 1 {
		t.Fatalf("ListSaved() = %+v, %v; want the opened mylist recorded", index, err)
	}
	if index[0].Pinned || index[0].Name != "保存マイリスト" || len(index[0].Sample) != 2 {
		t.Fatalf("index line = %+v; want the opened mylist unpinned with its sample", index[0])
	}
	if len(app.saved) != 1 || app.saved[0].ID != "27411728" {
		t.Fatalf("the view does not show the opened mylist: %+v", app.saved)
	}
	if body, err := m.LoadSavedBody("27411728"); err != nil || len(body.Items) != 2 {
		t.Fatalf("LoadSavedBody = %+v, %v; want the saved body", body, err)
	}
	if n := atomic.LoadInt32(hits); n != 2 {
		t.Errorf("requests = %d; want the page fetch and the body fetch", n)
	}
}

// TestPinSavedBodyFetchesNothing checks the app's pin: a mylist whose body is
// already saved is pinned by the index alone.
func TestPinSavedBodyFetchesNothing(t *testing.T) {
	m, hits := newSavedService(t, savedFixture)
	if _, err := m.RefreshMylist("27411728"); err != nil {
		t.Fatal(err)
	}
	app := newNativeApp(m)
	saved, err := m.ListSaved()
	if err != nil {
		t.Fatal(err)
	}
	app.saved = saved

	before := atomic.LoadInt32(hits)
	app.pin("27411728")
	if !app.saved[0].Pinned {
		t.Fatalf("the saved mylist was not pinned: %+v", app.saved)
	}
	if got := atomic.LoadInt32(hits); got != before {
		t.Errorf("pinning a saved mylist made %d requests; want none", got-before)
	}
}
