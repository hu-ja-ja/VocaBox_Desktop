package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Eyevinn/hls-m3u8/m3u8"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// The audio of a track a download-enabled mylist holds is kept on this PC as
// one file per track: the media initialization section and the decrypted
// segments of one audio rendition, which the OS media stack plays as it
// plays a file. The folder lives beside the other state files, so a user
// finds everything the app keeps for them in one place.

const (
	// audioDirName names the folder inside the config directory holding the
	// saved audio.
	audioDirName = "audio"
	// audioExtension is the suffix of a saved track.
	audioExtension = ".m4a"
	// audioPartExtension is the suffix of a track still being written: the
	// file is renamed when it is whole, so a crash never leaves a torn track
	// to be played in place of the real one.
	audioPartExtension = ".part"
)

// errAudioTrack is returned for an ID that cannot name a saved track: the
// saved mylists are hand-editable files, and only a watch ID names one.
var errAudioTrack = errors.New(locales.ErrAudioSave())

// audioQualityPresets are the bitrates the settings screen offers, highest
// first: the target a download picks its band by.
var audioQualityPresets = []int{320, 192, 64}

// audioQualityKbps is a stored quality as its bitrate: the highest for an
// empty or unknown one.
func audioQualityKbps(quality string) int {
	if kbps, err := strconv.Atoi(quality); err == nil {
		for _, preset := range audioQualityPresets {
			if preset == kbps {
				return kbps
			}
		}
	}
	return audioQualityPresets[0]
}

// audioQualityValue is a bitrate as the settings store it.
func audioQualityValue(kbps int) string { return strconv.Itoa(kbps) }

// audioQualityLabel is the option the settings dropdown shows for a bitrate,
// the estimate of what five minutes of audio costs at it included.
func audioQualityLabel(kbps int) string {
	switch kbps {
	case 192:
		return locales.SettingsQualityStandard()
	case 64:
		return locales.SettingsQualityLow()
	}
	return locales.SettingsQualityHigh()
}

// audioQualityOfLabel returns the bitrate of the option the dropdown shows,
// the highest for an unknown one.
func audioQualityOfLabel(label string) int {
	for _, preset := range audioQualityPresets {
		if label == audioQualityLabel(preset) {
			return preset
		}
	}
	return audioQualityPresets[0]
}

// pickAudioBand is the band of bitrates a download saves: the highest at or
// below target, the lowest band when every one is above it, and the
// playlist's last band when none of them names its bitrate. bitrates are the
// bands highest first, 0 where the master playlist does not name one.
func pickAudioBand(bitrates []int, target int) int {
	lowest := -1
	for i, kbps := range bitrates {
		if kbps <= 0 {
			continue
		}
		if kbps <= target {
			return i
		}
		lowest = i
	}
	if lowest >= 0 {
		return lowest
	}
	return len(bitrates) - 1
}

// audioDir is the directory holding the saved audio.
func (m *Mylist) audioDir() (string, error) {
	return m.configPath(audioDirName)
}

// audioPath is where the track's saved audio lives. Only a watch ID names a
// file, so a hand-edited mylist can never point outside the directory.
func (m *Mylist) audioPath(watchID string) (string, error) {
	if !isWatchID(watchID) {
		return "", errAudioTrack
	}
	dir, err := m.audioDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, watchID+audioExtension), nil
}

// isWatchID reports whether a string looks like a watch ID: letters and
// digits only, as every nicovideo ID is.
func isWatchID(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			continue
		}
		return false
	}
	return true
}

// savedAudioURI is a saved track as the helper's play command names it.
func savedAudioURI(path string) string {
	return "file:///" + filepath.ToSlash(path)
}

// DownloadAudio saves the audio of one track at dest: the band the target
// bitrate asks for, fetched as the delivery edge serves it and written out as
// one fragmented MP4.
func (n *Nico) DownloadAudio(videoID string, targetKbps int, dest string) error {
	sources, err := n.Prepare(videoID)
	if err != nil {
		return err
	}
	if len(sources.URIs) == 0 {
		return errors.New(locales.ErrNoAudio())
	}
	// The segment requests stay paced, a small window of them in flight at
	// once: the edge sees a track fetched the way a player fetches one.
	playlistURL := sources.URIs[pickAudioBand(sources.BitRates, targetKbps)]
	body, err := n.get(playlistURL)
	if err != nil {
		return err
	}
	media, err := decodeAudioPlaylist(body)
	if err != nil {
		return err
	}
	return n.writeAudio(media, playlistURL, dest)
}

// decodeAudioPlaylist reads one audio media playlist. The library keeps the
// HLS grammar; the app only reads what niconico serves.
func decodeAudioPlaylist(body []byte) (*m3u8.MediaPlaylist, error) {
	playlist, listType, err := m3u8.DecodeFrom(bytes.NewReader(body), false)
	if err != nil || listType != m3u8.MEDIA {
		return nil, errors.New(locales.ErrAudioUnsupported())
	}
	media, ok := playlist.(*m3u8.MediaPlaylist)
	if !ok {
		return nil, errors.New(locales.ErrAudioUnsupported())
	}
	return media, nil
}

// writeAudio streams the playlist into dest through a part file, so a
// failure never leaves a file that looks whole.
func (n *Nico) writeAudio(media *m3u8.MediaPlaylist, playlistURL, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return errors.New(locales.ErrConfigDirCreate())
	}
	part := dest + audioPartExtension
	out, err := os.Create(part)
	if err != nil {
		return errors.New(locales.ErrAudioSave())
	}
	err = n.copyAudio(media, playlistURL, out)
	if closeErr := out.Close(); err == nil && closeErr != nil {
		err = errors.New(locales.ErrAudioSave())
	}
	if err != nil {
		os.Remove(part)
		return err
	}
	if err := os.Rename(part, dest); err != nil {
		os.Remove(part)
		return errors.New(locales.ErrAudioSave())
	}
	return nil
}

// audioKey is the AES-128 key one stretch of a playlist is encrypted with,
// kept while the playlist keeps naming the same key.
type audioKey struct {
	uri string
	key []byte
}

// audioWindow is how many segment fetches may be in flight at once. The paced
// request starts keep the edge's view player-like; the window lets one
// segment's answer arrive while the next request is already out, so a slow
// answer adds to the pass only once the window is full.
const audioWindow = 3

// audioFetch is the outcome of one segment fetch, handed to the writer in
// playlist order.
type audioFetch struct {
	body []byte
	err  error
}

// fetchSegments starts the segment fetches of one playlist: the request
// starts are spaced by audioSegmentInterval and at most audioWindow are in
// flight, so the edge sees a paced, small look-ahead as it sees a player's
// rather than a crawler's sweep. Each segment's outcome is handed over on its
// own channel, in playlist order. A caller done early closes stop, which
// abandons the fetches not started yet; one in flight ends on its own, its
// outcome waiting in the buffered channel. The returned channel closes once
// no further request will be started.
func (n *Nico) fetchSegments(segments []*m3u8.MediaSegment, playlistURL string, stop <-chan struct{}) ([]chan audioFetch, <-chan struct{}) {
	fetched := make([]chan audioFetch, len(segments))
	for i := range fetched {
		fetched[i] = make(chan audioFetch, 1)
	}
	inFlight := make(chan struct{}, audioWindow)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i, segment := range segments {
			if i > 0 && audioSegmentInterval > 0 {
				select {
				case <-time.After(audioSegmentInterval):
				case <-stop:
					return
				}
			}
			// shortcut: niconico's audio renditions use no byte ranges; the
			// writer rejects a playlist that names one before it would read
			// this fetch, so nothing is fetched for it here.
			if segment.Limit > 0 {
				continue
			}
			select {
			case inFlight <- struct{}{}:
			case <-stop:
				return
			}
			go func(fetched chan audioFetch, uri string) {
				defer func() { <-inFlight }()
				body, err := n.get(uri)
				fetched <- audioFetch{body: body, err: err}
			}(fetched[i], audioResourceURL(playlistURL, segment.URI))
		}
	}()
	return fetched, done
}

// copyAudio writes the media initialization section and the decrypted
// segments of the playlist, in order, from fetches that run up to audioWindow
// ahead of the writing. The segments come from GetAllSegments: the playlist
// holds them in a ring buffer padded with nils.
func (n *Nico) copyAudio(media *m3u8.MediaPlaylist, playlistURL string, out io.Writer) error {
	segments := media.GetAllSegments()
	if len(segments) == 0 {
		return errors.New(locales.ErrAudioSourceMissing())
	}
	// The saved file carries its own length: the media stack reads the movie
	// header's duration, and a fragmented MP4 written without one leaves it no
	// length to report.
	durationMS := 0.0
	for _, segment := range segments {
		durationMS += segment.Duration
	}
	durationMS *= 1000
	stop := make(chan struct{})
	fetched, done := n.fetchSegments(segments, playlistURL, stop)
	defer func() {
		// The writer is done: no further request is started, and the
		// fetcher has ended by the time this returns.
		close(stop)
		<-done
	}()
	var key audioKey
	initURI := ""
	for i, segment := range segments {
		// shortcut: niconico's audio renditions use no byte ranges; a
		// playlist that does needs range requests rather than whole files.
		if segment.Limit > 0 {
			return errors.New(locales.ErrAudioUnsupported())
		}
		init := media.Map
		if segment.Map != nil {
			init = segment.Map
		}
		if init != nil && init.URI != initURI {
			body, err := n.get(audioResourceURL(playlistURL, init.URI))
			if err != nil {
				return err
			}
			if _, err := out.Write(withMovieDuration(body, durationMS)); err != nil {
				return errors.New(locales.ErrAudioSave())
			}
			initURI = init.URI
		}
		fetch := <-fetched[i]
		if fetch.err != nil {
			return fetch.err
		}
		body := fetch.body
		segmentKey, ok := audioSegmentKey(segment, media)
		if ok && segmentKey.Method != "" && segmentKey.Method != "NONE" {
			if segmentKey.Method != "AES-128" {
				return errors.New(locales.ErrAudioUnsupported())
			}
			if segmentKey.URI != key.uri {
				keyBody, err := n.get(audioResourceURL(playlistURL, segmentKey.URI))
				if err != nil {
					return err
				}
				if len(keyBody) != aes.BlockSize {
					return errors.New(locales.ErrAudioUnsupported())
				}
				key = audioKey{uri: segmentKey.URI, key: keyBody}
			}
			decrypted, err := decryptAudioSegment(key.key, audioIV(segmentKey, segment), body)
			if err != nil {
				return err
			}
			body = decrypted
		}
		if _, err := out.Write(body); err != nil {
			return errors.New(locales.ErrAudioSave())
		}
	}
	return nil
}

// withMovieDuration returns the initialization section with its movie header's
// duration set to the playlist's total, so the saved file names its own length.
// A section without an mvhd, or one whose timescale is unusable, is returned as
// it is.
func withMovieDuration(init []byte, durationMS float64) []byte {
	mvhd := findBox(init, "mvhd")
	if mvhd < 0 {
		return init
	}
	out := append([]byte(nil), init...)
	switch init[mvhd+8] {
	case 0:
		timescale := binary.BigEndian.Uint32(init[mvhd+20 : mvhd+24])
		if timescale == 0 {
			return init
		}
		binary.BigEndian.PutUint32(out[mvhd+24:mvhd+28], uint32(durationMS*float64(timescale)/1000))
	case 1:
		timescale := binary.BigEndian.Uint32(init[mvhd+28 : mvhd+32])
		if timescale == 0 {
			return init
		}
		binary.BigEndian.PutUint64(out[mvhd+32:mvhd+40], uint64(durationMS*float64(timescale)/1000))
	}
	return out
}

// findBox returns the offset of the first box of the given type in b, -1 when
// there is none. It walks box boundaries and enters a moov to look at its
// children, which is where an mvhd sits.
func findBox(b []byte, kind string) int {
	for off := 0; off+8 <= len(b); {
		size := int(binary.BigEndian.Uint32(b[off : off+4]))
		if size < 8 || off+size > len(b) {
			return -1
		}
		switch string(b[off+4 : off+8]) {
		case kind:
			return off
		case "moov":
			if at := findBox(b[off+8:off+size], kind); at >= 0 {
				return off + 8 + at
			}
		}
		off += size
	}
	return -1
}

// audioSegmentKey is the key one segment is encrypted with: the last key its
// own tag names, or the playlist's default, which the decoder keeps for the
// first key of the playlist. The decoder attaches a key to the segment that
// follows its tag, so the key is remembered for the segments after it.
func audioSegmentKey(segment *m3u8.MediaSegment, media *m3u8.MediaPlaylist) (m3u8.Key, bool) {
	keys := segment.Keys
	if len(keys) == 0 {
		keys = media.Keys
	}
	if len(keys) == 0 {
		return m3u8.Key{}, false
	}
	return keys[len(keys)-1], true
}

// audioIV is the IV of one segment: the one its key names, or the segment's
// media sequence number when it names none, as RFC 8216 defines.
func audioIV(key m3u8.Key, segment *m3u8.MediaSegment) []byte {
	if iv, err := hex.DecodeString(strings.TrimPrefix(key.IV, "0x")); err == nil && len(iv) == aes.BlockSize {
		return iv
	}
	iv := make([]byte, aes.BlockSize)
	binary.BigEndian.PutUint64(iv[aes.BlockSize-8:], segment.SeqId)
	return iv
}

// decryptAudioSegment decrypts one AES-128-CBC segment and strips its PKCS7
// padding. The first box of the plaintext is checked, so a wrong key or IV
// reads as an error rather than as a corrupt saved file.
func decryptAudioSegment(key, iv, data []byte) ([]byte, error) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New(locales.ErrAudioUnsupported())
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New(locales.ErrAudioUnsupported())
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	pad := int(out[len(out)-1])
	if pad < 1 || pad > aes.BlockSize || pad > len(out) {
		return nil, errors.New(locales.ErrAudioUnsupported())
	}
	out = out[:len(out)-pad]
	if !looksLikeAudioBox(out) {
		return nil, errors.New(locales.ErrAudioUnsupported())
	}
	return out, nil
}

// audioBoxTypes are the CMAF box types a decrypted segment may begin with.
var audioBoxTypes = map[string]bool{
	"styp": true, "moof": true, "sidx": true, "free": true, "skip": true,
	"emsg": true, "prft": true, "mdat": true, "ftyp": true,
}

func looksLikeAudioBox(b []byte) bool {
	return len(b) >= 8 && audioBoxTypes[string(b[4:8])]
}

// audioResourceURL resolves one of a playlist's entries against the
// playlist's own URL, as the entries may be relative.
func audioResourceURL(playlistURL, resource string) string {
	base, err := url.Parse(playlistURL)
	if err != nil {
		return resource
	}
	ref, err := url.Parse(resource)
	if err != nil {
		return resource
	}
	return base.ResolveReference(ref).String()
}
