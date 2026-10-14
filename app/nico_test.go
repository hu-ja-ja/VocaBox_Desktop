package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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
