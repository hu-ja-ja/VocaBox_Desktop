package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
}

func newAudioTestServer(t *testing.T) *audioTestServer {
	t.Helper()
	init := []byte("\x00\x00\x00\x10ftyp")
	bands := []string{"192", "64"}
	plain := map[string][][]byte{}
	s := &audioTestServer{want: map[string][]byte{}}
	for _, band := range bands {
		first, second := audioTestSegment(0x11), audioTestSegment(0x22)
		plain[band] = [][]byte{first, second}
		s.want[band] = bytes.Join([][]byte{init, first, second}, nil)
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
				if s.broken {
					http.Error(w, "gone", http.StatusInternalServerError)
					return
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
