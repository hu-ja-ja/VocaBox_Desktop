package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// audioTestKey and audioTestIV are what the fixture's playlist names: the
// delivery edge serves AES-128-CBC segments under one fixed IV.
var (
	audioTestKey = []byte("0123456789abcdef")
	audioTestIV  = bytes.Repeat([]byte{0x2a}, aes.BlockSize)
)

// audioTestSegment is one plaintext segment: a CMAF box the downloader's
// check accepts.
func audioTestSegment(fill byte) []byte {
	return append([]byte("\x00\x00\x00\x10moof"), bytes.Repeat([]byte{fill}, 96)...)
}

// mvhdInit is an initialization section holding one movie header of the given
// version and timescale, its duration unset, as niconico's does.
func mvhdInit(version byte, timescale uint32) []byte {
	fields := 8
	if version == 1 {
		fields = 16
	}
	mvhd := make([]byte, 16+fields+4)
	binary.BigEndian.PutUint32(mvhd[0:4], uint32(len(mvhd)))
	copy(mvhd[4:8], "mvhd")
	mvhd[8] = version
	binary.BigEndian.PutUint32(mvhd[12+fields:], timescale)
	moov := make([]byte, 8+len(mvhd))
	binary.BigEndian.PutUint32(moov[0:4], uint32(len(moov)))
	copy(moov[4:8], "moov")
	copy(moov[8:], mvhd)
	return append([]byte("\x00\x00\x00\x10ftyp"), moov...)
}

// encryptAudioSegment encrypts one segment the way the edge serves it.
func encryptAudioSegment(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(audioTestKey)
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(append([]byte(nil), plaintext...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, audioTestIV).CryptBlocks(out, padded)
	return out
}

// audioTestServer serves one track of two bands the way niconico does: the
// watch API, the access-rights call with its cookie, the master playlist, and
// each band's AES-128 encrypted media playlist. want is what each band's
// saved file must hold.
type audioTestServer struct {
	nico *Nico
	want map[string][]byte
	// broken makes the segments answer 500, the way an asset that is gone does.
	broken bool
	// segmentDelay slows each segment answer, so a test can watch the fetch
	// window overlap the requests.
	segmentDelay time.Duration
	// mu guards the in-flight count of the segment handlers.
	mu          sync.Mutex
	inFlight    int
	maxInFlight int
}

// segmentBegin marks one segment fetch as in flight and hands back the mark
// that ends it, so a test can read how many ran at once.
func (s *audioTestServer) segmentBegin() func() {
	s.mu.Lock()
	s.inFlight++
	if s.inFlight > s.maxInFlight {
		s.maxInFlight = s.inFlight
	}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.inFlight--
		s.mu.Unlock()
	}
}

func (s *audioTestServer) maxConcurrentSegments() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxInFlight
}

func newAudioTestServer(t *testing.T) *audioTestServer {
	t.Helper()
	init := mvhdInit(0, 1000)
	// The saved file's movie header carries the playlist's total (12.012s).
	wantInit := append([]byte(nil), init...)
	if at := bytes.Index(wantInit, []byte("mvhd")); at >= 0 {
		binary.BigEndian.PutUint32(wantInit[at+20:at+24], 12_012)
	}
	bands := []string{"192", "64"}
	plain := map[string][][]byte{}
	s := &audioTestServer{want: map[string][]byte{}}
	for _, band := range bands {
		first, second := audioTestSegment(0x11), audioTestSegment(0x22)
		plain[band] = [][]byte{first, second}
		s.want[band] = bytes.Join([][]byte{wantInit, first, second}, nil)
	}

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	mux.HandleFunc("/api/watch/v3_guest/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": map[string]any{"status": 200},
			"data": map[string]any{
				"client": map[string]any{"watchTrackId": "track-1"},
				"media": map[string]any{"domand": map[string]any{
					"accessRightKey": "jwt",
					"videos":         []map[string]any{{"id": "video-h264-360p", "isAvailable": true}},
					"audios": []map[string]any{
						{"id": "audio-aac-192kbps", "isAvailable": true, "bitRate": 222751, "samplingRate": 48000},
						{"id": "audio-aac-64kbps", "isAvailable": true, "bitRate": 69474, "samplingRate": 48000},
					},
				}},
			},
		})
	})
	mux.HandleFunc("/v1/watch/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "domand_bid=bid-1; Path=/")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": map[string]any{"status": 201},
			"data": map[string]any{"contentUrl": server.URL + "/master.m3u8"},
		})
	})
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "#EXTM3U\n"+
			`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-aac-64kbps",NAME="低音質",URI="`+server.URL+`/64.m3u8"`+"\n"+
			`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-aac-192kbps",NAME="標準",URI="`+server.URL+`/192.m3u8"`+"\n")
	})
	mux.HandleFunc("/key.bin", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(audioTestKey)
	})
	mux.HandleFunc("/init.bin", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(init)
	})
	for _, band := range bands {
		mux.HandleFunc("/"+band+".m3u8", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "#EXTM3U\n"+
				"#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:1\n"+
				`#EXT-X-MAP:URI="init.bin"`+"\n"+
				`#EXT-X-KEY:METHOD=AES-128,URI="key.bin",IV=0x`+hex.EncodeToString(audioTestIV)+"\n"+
				"#EXTINF:6.006,\n"+band+"-1.m4s\n"+
				"#EXTINF:6.006,\n"+band+"-2.m4s\n#EXT-X-ENDLIST\n")
		})
		for i, segment := range plain[band] {
			segment := segment
			mux.HandleFunc(fmt.Sprintf("/%s-%d.m4s", band, i+1), func(w http.ResponseWriter, r *http.Request) {
				done := s.segmentBegin()
				defer done()
				if s.broken {
					http.Error(w, "gone", http.StatusInternalServerError)
					return
				}
				if s.segmentDelay > 0 {
					time.Sleep(s.segmentDelay)
				}
				_, _ = w.Write(encryptAudioSegment(t, segment))
			})
		}
	}

	nico := NewNico()
	nico.baseURL = server.URL
	nico.apiBase = server.URL
	s.nico = nico
	return s
}

func TestPickAudioBand(t *testing.T) {
	cases := []struct {
		name     string
		bitrates []int
		target   int
		want     int
	}{
		{"the target itself", []int{320, 192, 64}, 320, 0},
		{"a middle preset", []int{320, 192, 64}, 192, 1},
		{"the lowest preset", []int{320, 192, 64}, 64, 2},
		{"a missing band gives the best below it", []int{192, 64}, 128, 1},
		{"a target above every band gives the top one", []int{192, 64}, 320, 0},
		{"a target below every band gives the lowest", []int{192, 64}, 32, 1},
		{"an unnamed bitrate is skipped", []int{0, 0}, 320, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickAudioBand(tc.bitrates, tc.target); got != tc.want {
				t.Fatalf("pickAudioBand(%v, %d) = %d; want %d", tc.bitrates, tc.target, got, tc.want)
			}
		})
	}
}

// TestWithMovieDuration checks the length the save writes into the movie
// header: the version 0 and version 1 durations, and an initialization section
// without a movie header left as it is.
func TestWithMovieDuration(t *testing.T) {
	cases := []struct {
		name      string
		version   byte
		timescale uint32
		want      uint64
	}{
		{"version 0", 0, 1000, 12_012},
		{"version 1", 1, 600, 7_207},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			init := mvhdInit(tc.version, tc.timescale)
			got := withMovieDuration(init, 12_012)
			at := bytes.Index(got, []byte("mvhd"))
			// The duration sits after the timescale, 4 bytes wide in version
			// 0 and 8 in version 1.
			var duration uint64
			if tc.version == 0 {
				duration = uint64(binary.BigEndian.Uint32(got[at+20 : at+24]))
			} else {
				duration = binary.BigEndian.Uint64(got[at+28 : at+36])
			}
			if duration != tc.want {
				t.Fatalf("duration = %d; want %d", duration, tc.want)
			}
			if tc.version == 0 {
				if d := binary.BigEndian.Uint32(init[at+20 : at+24]); d != 0 {
					t.Fatalf("the input section was modified: %d", d)
				}
			} else if d := binary.BigEndian.Uint64(init[at+28 : at+36]); d != 0 {
				t.Fatalf("the input section was modified: %d", d)
			}
		})
	}
	plain := []byte("\x00\x00\x00\x10ftyp")
	if got := withMovieDuration(plain, 12_012); !bytes.Equal(got, plain) {
		t.Fatalf("a section without a movie header was changed: %v", got)
	}
}

// TestNicoDownloadAudioSavesTheChosenBand drives the whole save against a
// local server: the band the preset asks for is the one fetched, and the file
// holds its initialization section and decrypted segments in order.
func TestNicoDownloadAudioSavesTheChosenBand(t *testing.T) {
	server := newAudioTestServer(t)
	cases := []struct {
		target int
		band   string
	}{
		{320, "192"}, // no 320kbps band is served: the best band below it is
		{192, "192"},
		{128, "64"},
		{64, "64"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%dkbps", tc.target), func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "sm123.m4a")
			if err := server.nico.DownloadAudio("sm123", tc.target, dest); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, server.want[tc.band]) {
				t.Fatalf("saved %d bytes; want the %skbps band's %d", len(got), tc.band, len(server.want[tc.band]))
			}
			if _, err := os.Stat(dest + audioPartExtension); !os.IsNotExist(err) {
				t.Fatalf("the part file is still there: %v", err)
			}
		})
	}
}

// TestNicoDownloadAudioLeavesNoFileOnFailure checks that a failed save leaves
// neither a file that looks whole nor a part file behind.
func TestNicoDownloadAudioLeavesNoFileOnFailure(t *testing.T) {
	server := newAudioTestServer(t)
	server.broken = true
	dest := filepath.Join(t.TempDir(), "sm123.m4a")
	if err := server.nico.DownloadAudio("sm123", 192, dest); err == nil {
		t.Fatal("a segment that cannot be fetched must be an error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("a failed save left a file: %v", err)
	}
	if _, err := os.Stat(dest + audioPartExtension); !os.IsNotExist(err) {
		t.Fatalf("a failed save left a part file: %v", err)
	}
}

// TestNicoDownloadAudioFetchesSegmentsPaced checks how one track's segment
// requests are made: with the pauses off a slow segment's fetch overlaps the
// next request — the window, not one after the other — and with the pauses on
// the request starts stay spaced, so a save keeps the edge's view of the
// track player-like either way.
func TestNicoDownloadAudioFetchesSegmentsPaced(t *testing.T) {
	interval := audioSegmentInterval
	t.Cleanup(func() { audioSegmentInterval = interval })
	server := newAudioTestServer(t)
	dest := func() string { return filepath.Join(t.TempDir(), "sm123.m4a") }

	t.Run("the window overlaps the fetches", func(t *testing.T) {
		audioSegmentInterval = 0
		server.segmentDelay = 100 * time.Millisecond
		if err := server.nico.DownloadAudio("sm123", 192, dest()); err != nil {
			t.Fatal(err)
		}
		if got := server.maxConcurrentSegments(); got < 2 {
			t.Fatalf("the segment fetches ran one after the other: %d in flight", got)
		}
	})

	t.Run("the starts stay spaced", func(t *testing.T) {
		server.segmentDelay = 0
		audioSegmentInterval = 100 * time.Millisecond
		start := time.Now()
		if err := server.nico.DownloadAudio("sm123", 192, dest()); err != nil {
			t.Fatal(err)
		}
		if got := time.Since(start); got < 90*time.Millisecond {
			t.Fatalf("the segment requests of a track were not spaced: %v", got)
		}
	})
}
