package main

import (
	"testing"

	"github.com/egoist/mygo"
)

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
