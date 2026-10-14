package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBestAudioFormatsKeepsPlayableBands(t *testing.T) {
	audios := []nicoAudio{
		{ID: "audio-aac-320kbps", IsAvailable: false, BitRate: 325175, SamplingRate: 48000},
		{ID: "audio-aac-192kbps", IsAvailable: true, BitRate: 222751, SamplingRate: 48000},
		{ID: "audio-aac-64kbps", IsAvailable: true, BitRate: 69474, SamplingRate: 48000},
		// Windows' AAC decoder cannot play above 48kHz, so 576kbps (which
		// only exists at 88.2/96kHz) must stay out of reach.
		{ID: "audio-aac-576kbps", IsAvailable: true, BitRate: 576000, SamplingRate: 96000},
		{ID: "audio-aac-128kbps", IsAvailable: true, BitRate: 128000},
	}
	got := bestAudioFormats(audios)
	ids := make([]string, len(got))
	for i, a := range got {
		ids[i] = a.ID
	}
	want := []string{"audio-aac-192kbps", "audio-aac-128kbps", "audio-aac-64kbps"}
	if !slices.Equal(ids, want) {
		t.Fatalf("bestAudioFormats = %v; want %v", ids, want)
	}
}

func TestAudioVariantsSortsBands(t *testing.T) {
	master := "#EXTM3U\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-aac-64kbps",NAME="低音質",URI="https://delivery.example/64.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=VIDEO,GROUP-ID="video-h264-360p",URI="https://delivery.example/video.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-aac-192kbps",URI="https://delivery.example/192.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",URI="https://delivery.example/unknown.m3u8"` + "\n"
	got := audioVariants(master)
	uris := make([]string, len(got))
	for i, v := range got {
		uris[i] = v.URI
	}
	want := []string{
		"https://delivery.example/192.m3u8",
		"https://delivery.example/64.m3u8",
		"https://delivery.example/unknown.m3u8",
	}
	if !slices.Equal(uris, want) {
		t.Fatalf("audioVariants = %v; want %v", uris, want)
	}
}

// TestNicoPrepareFollowsThePlaybackFlow drives the whole acquisition against
// a local server: watch API, access-rights (with its domand_bid cookie), and
// the master playlist's audio variants.
func TestNicoPrepareFollowsThePlaybackFlow(t *testing.T) {
	var outputs [][2]string
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc("/api/watch/v3_guest/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Frontend-ID") != "6" || r.Header.Get("X-Frontend-Version") != "0" {
			t.Errorf("watch request lost the frontend headers: %v", r.Header)
		}
		if r.URL.Query().Get("actionTrackId") == "" {
			t.Error("watch request lost the actionTrackId")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": map[string]any{"status": 200},
			"data": map[string]any{
				"client": map[string]any{"watchTrackId": "track-1"},
				"media": map[string]any{"domand": map[string]any{
					"accessRightKey": "jwt",
					"videos": []map[string]any{
						{"id": "video-h264-360p", "isAvailable": true},
						{"id": "video-h264-720p", "isAvailable": false},
					},
					"audios": []map[string]any{
						{"id": "audio-aac-320kbps", "isAvailable": false, "bitRate": 325175, "samplingRate": 48000},
						{"id": "audio-aac-192kbps", "isAvailable": true, "bitRate": 222751, "samplingRate": 48000},
						{"id": "audio-aac-64kbps", "isAvailable": true, "bitRate": 69474, "samplingRate": 48000},
						{"id": "audio-aac-576kbps", "isAvailable": true, "bitRate": 576000, "samplingRate": 96000},
					},
				}},
			},
		})
	})
	mux.HandleFunc("/v1/watch/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Access-Right-Key") != "jwt" {
			t.Error("access-rights request lost the right key")
		}
		if err := json.NewDecoder(r.Body).Decode(&struct {
			Outputs *[][2]string `json:"outputs"`
		}{Outputs: &outputs}); err != nil {
			t.Errorf("access-rights body: %v", err)
		}
		// The delivery edge needs this cookie on every manifest and segment
		// fetch, including the helper's own.
		w.Header().Set("Set-Cookie", "domand_bid=bid-1; Path=/")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": map[string]any{"status": 201},
			"data": map[string]any{"contentUrl": server.URL + "/master.m3u8"},
		})
	})
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "domand_bid=bid-1") {
			t.Errorf("master fetch lost the domand_bid cookie: %q", r.Header.Get("Cookie"))
		}
		_, _ = w.Write([]byte("#EXTM3U\n" +
			`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-aac-64kbps",URI="` + server.URL + `/64.m3u8"` + "\n" +
			`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-aac-192kbps",URI="` + server.URL + `/192.m3u8"` + "\n"))
	})

	n := NewNico()
	n.baseURL = server.URL
	n.apiBase = server.URL
	srcs, err := n.Prepare("sm123")
	if err != nil {
		t.Fatal(err)
	}
	wantURIs := []string{server.URL + "/192.m3u8", server.URL + "/64.m3u8"}
	if !slices.Equal(srcs.URIs, wantURIs) {
		t.Fatalf("sources = %v; want %v", srcs.URIs, wantURIs)
	}
	if !strings.Contains(srcs.Cookie, "domand_bid=bid-1") {
		t.Fatalf("cookie = %q", srcs.Cookie)
	}
	if srcs.UserAgent == "" {
		t.Fatal("sources carry no user agent")
	}
	// Only the playable and permitted bands are requested: no 320 (guest),
	// no 576 (96kHz), no unavailable video, and video and audio are paired.
	wantOutputs := [][2]string{
		{"video-h264-360p", "audio-aac-192kbps"},
		{"video-h264-360p", "audio-aac-64kbps"},
	}
	if !slices.Equal(outputs, wantOutputs) {
		t.Fatalf("access-rights outputs = %v; want %v", outputs, wantOutputs)
	}
}

func TestNicoPrepareReportsWatchFailure(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/api/watch/v3_guest/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"meta":{"status":400,"errorCode":"INVALID_PARAMETER"},"data":null}`))
	})
	n := NewNico()
	n.baseURL = server.URL
	n.apiBase = server.URL
	if _, err := n.Prepare("sm123"); err == nil {
		t.Fatal("a failed watch call must be reported")
	}
}

// TestNicoUserReadsTheAccount drives users/me: the account's name and its
// large picture, falling back to the small one, and a call made without a
// session.
func TestNicoUserReadsTheAccount(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	var noLarge atomic.Bool
	mux.HandleFunc("/v1/users/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Frontend-ID") != "6" || r.Header.Get("X-Frontend-Version") != "0" {
			t.Errorf("users/me lost the frontend headers: %v", r.Header)
		}
		icons := map[string]any{"small": "https://example.invalid/icon-small.jpg"}
		if !noLarge.Load() {
			icons["large"] = "https://example.invalid/icon-large.jpg"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": map[string]any{"status": 200},
			"data": map[string]any{"user": map[string]any{
				"nickname": "テスト",
				"icons":    icons,
			}},
		})
	})
	n := NewNico()
	n.apiBase = server.URL
	if _, err := n.User(); err == nil {
		t.Fatal("a call without a session must be reported")
	}
	if err := n.SetSession("user_session=abc"); err != nil {
		t.Fatal(err)
	}
	user, err := n.User()
	if err != nil {
		t.Fatal(err)
	}
	if user.Nickname != "テスト" || user.IconURL != "https://example.invalid/icon-large.jpg" {
		t.Fatalf("User() = %#v; want the nickname and the large picture", user)
	}

	// An account without a large picture shows its small one.
	noLarge.Store(true)
	if user, err = n.User(); err != nil || user.IconURL != "https://example.invalid/icon-small.jpg" {
		t.Fatalf("User() = %#v, %v; want the small picture", user, err)
	}
}

// TestNicoSessionCookiesStayOnNiconico checks where a loaded session is
// sent: nicovideo's own hosts carry it, any other host never.
func TestNicoSessionCookiesStayOnNiconico(t *testing.T) {
	n := NewNico()
	if err := n.SetSession("user_session=abc; user_session_secure=def; nicosid=1"); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
	if !n.LoggedIn() {
		t.Fatal("a loaded session does not report itself")
	}
	sent := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		var parts []string
		for _, c := range n.client.Jar.Cookies(u) {
			parts = append(parts, c.Name+"="+c.Value)
		}
		sort.Strings(parts)
		return strings.Join(parts, "; ")
	}
	want := "user_session=abc; user_session_secure=def"
	for _, host := range []string{
		"https://www.nicovideo.jp/watch/sm9",
		"https://nvapi.nicovideo.jp/v1/watch/sm9/access-rights/hls",
		"https://delivery.domand.nicovideo.jp/hlsbid/x/playlists/media/audio.m3u8",
	} {
		if got := sent(host); got != want {
			t.Errorf("cookies for %s = %q; want %q", host, got, want)
		}
	}
	if got := sent("https://example.com/"); got != "" {
		t.Fatalf("the session leaks to another host: %q", got)
	}
	n.ClearSession()
	if n.LoggedIn() || sent("https://www.nicovideo.jp/") != "" {
		t.Fatal("ClearSession kept the session")
	}
}

func TestNicoSetSessionRejectsNonSessionCookies(t *testing.T) {
	n := NewNico()
	for _, input := range []string{"", "   ", "nicosid=1", "user_session="} {
		if err := n.SetSession(input); err == nil {
			t.Errorf("SetSession(%q) was accepted", input)
		}
	}
	if n.LoggedIn() {
		t.Fatal("a rejected session was loaded")
	}
}

// TestNicoWatchPathFollowsTheSession checks which watch API a track is
// acquired from: the guest one, or the signed-in one that answers the
// premium bands.
func TestNicoWatchPathFollowsTheSession(t *testing.T) {
	var paths []string
	var noSideEffects []string
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/api/watch/", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		noSideEffects = append(noSideEffects, r.URL.Query().Get("noSideEffect"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": map[string]any{"status": 200},
			"data": map[string]any{
				"client": map[string]any{"watchTrackId": "track"},
				"media":  map[string]any{"domand": map[string]any{"accessRightKey": "jwt"}},
			},
		})
	})

	n := NewNico()
	n.baseURL = server.URL
	if _, err := n.fetchWatch("sm9", false); err != nil {
		t.Fatalf("guest watch: %v", err)
	}
	if err := n.SetSession("user_session=abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := n.fetchWatch("sm9", true); err != nil {
		t.Fatalf("signed-in watch: %v", err)
	}
	if want := []string{"/api/watch/v3_guest/sm9", "/api/watch/v3/sm9"}; !slices.Equal(paths, want) {
		t.Fatalf("paths = %v; want %v", paths, want)
	}
	if want := []string{"", "true"}; !slices.Equal(noSideEffects, want) {
		t.Fatalf("noSideEffect = %v; want %v", noSideEffects, want)
	}
}

// TestNicoExpiredSession checks that UNAUTHORIZED asks for a new login only
// when a session was loaded.
func TestNicoExpiredSession(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/api/watch/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"meta":{"status":400,"errorCode":"UNAUTHORIZED"},"data":null}`))
	})

	n := NewNico()
	n.baseURL = server.URL
	if _, err := n.fetchWatch("sm9", false); err == nil || errors.Is(err, errSessionExpired) {
		t.Fatalf("guest error = %v", err)
	}
	if err := n.SetSession("user_session=abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := n.fetchWatch("sm9", false); !errors.Is(err, errSessionExpired) {
		t.Fatalf("expired session error = %v", err)
	}
}
