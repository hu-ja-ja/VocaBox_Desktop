package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// TestNativeAppLoadsAccount drives the sign-in decoration: with a session
// loaded the account API and its picture are fetched, and logging out
// forgets the account the sidebar showed.
func TestNativeAppLoadsAccount(t *testing.T) {
	var icon bytes.Buffer
	if err := png.Encode(&icon, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	host := server.Listener.Addr().String()
	mux.HandleFunc("/v1/users/me", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": map[string]any{"status": 200},
			"data": map[string]any{"user": map[string]any{
				"nickname": "テスト",
				"icons":    map[string]any{"large": "https://" + host + "/icon.png"},
			}},
		})
	})
	mux.HandleFunc("/icon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(icon.Bytes())
	})

	n := NewNico()
	n.apiBase = server.URL
	// The client keeps its jar and timeout; the test server's certificate
	// is what it must trust.
	n.client.Transport = server.Client().Transport
	if err := n.SetSession("user_session=abc"); err != nil {
		t.Fatal(err)
	}
	mylist := &Mylist{client: server.Client(), configDir: t.TempDir(), thumbHost: host}
	app := newNativeApp(mylist)
	app.playback = &playback{nico: n}
	app.loggedIn = true

	user, body, err := app.fetchAccount()
	if err != nil {
		t.Fatal(err)
	}
	if user.Nickname != "テスト" || len(body) == 0 {
		t.Fatalf("fetchAccount() = %#v, %d bytes", user, len(body))
	}
	if _, err := ui.DecodeBitmap(body); err != nil {
		t.Fatalf("the fetched picture does not decode: %v", err)
	}

	// Logging out forgets what the sidebar showed.
	app.accountName = user.Nickname
	app.clearSession()
	if app.loggedIn || app.accountName != "" || app.accountIcon != nil {
		t.Fatalf("logout kept the account: %q, %v", app.accountName, app.accountIcon)
	}
}

// TestSessionCookieHeader drives the session picking with what the cookie
// store returns after a sign-in: the session cookies are taken, whatever
// host nicovideo set them for, and everything else is left behind.
func TestSessionCookieHeader(t *testing.T) {
	cookies := []mygo.Cookie{
		{Name: "nicosid", Value: "1", Domain: ".nicovideo.jp"},
		{Name: "user_session", Value: "user_session_1_abc", Domain: ".nicovideo.jp"},
		{Name: "user_session_secure", Value: "def", Domain: "account.nicovideo.jp"},
		{Name: "user_session", Value: "host-only", Domain: "www.nicovideo.jp"},
	}
	if got, want := sessionCookieHeader(cookies), "user_session=user_session_1_abc; user_session_secure=def"; got != want {
		t.Fatalf("header = %q; want %q", got, want)
	}
	for _, bad := range [][]mygo.Cookie{
		nil,
		{{Name: "nicosid", Value: "1", Domain: ".nicovideo.jp"}},
		{{Name: "user_session", Value: "", Domain: ".nicovideo.jp"}},
	} {
		if got := sessionCookieHeader(bad); got != "" {
			t.Errorf("header of %+v = %q; want \"\"", bad, got)
		}
	}
}
