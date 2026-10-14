package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	neturl "net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Nico fetches what playing one video needs from niconico's own API: the
// watch metadata, the HLS content URL, and the audio-only media playlists
// inside the master playlist. The shapes follow yt-dlp's niconico extractor;
// the flow is the one the playback spikes verified (see 要件/).
type Nico struct {
	client *http.Client
	// baseURL and apiBase are overridable so tests point them at their own
	// server; the app always talks to nicovideo.
	baseURL   string
	apiBase   string
	userAgent string
}

const (
	nicoBaseURL    = "https://www.nicovideo.jp"
	nicoAPIBaseURL = "https://nvapi.nicovideo.jp"
	// playbackUserAgent identifies the app to niconico and to the delivery
	// edge. The browser-like prefix keeps niconico from redirecting API
	// calls to its SPA page.
	playbackUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) VocaBox/0.1.0"
	// maxNicoBody bounds every playback API and playlist response.
	maxNicoBody = 8 << 20
)

// NewNico returns the playback acquisition service, with a cookie jar: the
// access-rights response sets domand_bid, and the delivery edge serves
// manifests and segments only to a client carrying it.
func NewNico() *Nico {
	jar, _ := cookiejar.New(nil)
	return &Nico{
		client:    &http.Client{Timeout: 30 * time.Second, Jar: jar},
		baseURL:   nicoBaseURL,
		apiBase:   nicoAPIBaseURL,
		userAgent: playbackUserAgent,
	}
}

// playbackSources is everything the helper needs for one track: the audio
// media playlist URLs, the highest band first (the Go side falls back to the
// next one when playback of a band fails), and the Cookie and User-Agent the
// delivery hosts expect.
type playbackSources struct {
	URIs      []string
	Cookie    string
	UserAgent string
}

// nicoAudio is one audio rendition of the watch API. SamplingRate keeps the
// bands Windows' AAC decoder cannot play (88.2/96kHz) out of reach.
type nicoAudio struct {
	ID           string `json:"id"`
	IsAvailable  bool   `json:"isAvailable"`
	BitRate      int    `json:"bitRate"`
	SamplingRate int    `json:"samplingRate"`
}

type nicoVideo struct {
	ID          string `json:"id"`
	IsAvailable bool   `json:"isAvailable"`
}

// nicoWatch is what playback needs out of GET /api/watch/v3_guest/{id}.
type nicoWatch struct {
	Videos         []nicoVideo
	Audios         []nicoAudio
	AccessRightKey string
	TrackID        string
}

// Prepare fetches a track's playable sources: the watch metadata, the
// access-rights content URL, and the audio media playlists of the master.
func (n *Nico) Prepare(videoID string) (playbackSources, error) {
	watch, err := n.fetchWatch(videoID)
	if err != nil {
		return playbackSources{}, err
	}
	audios := bestAudioFormats(watch.Audios)
	videos := availableVideoIDs(watch.Videos)
	if len(audios) == 0 || len(videos) == 0 {
		return playbackSources{}, errors.New("この動画には再生できる音源がありません")
	}
	master, err := n.contentURL(videoID, watch, videos, audios)
	if err != nil {
		return playbackSources{}, err
	}
	body, err := n.get(master)
	if err != nil {
		return playbackSources{}, err
	}
	variants := audioVariants(string(body))
	if len(variants) == 0 {
		return playbackSources{}, errors.New("この動画の音源の取得先が見つかりませんでした")
	}
	uris := make([]string, 0, len(variants))
	for _, v := range variants {
		uris = append(uris, v.URI)
	}
	return playbackSources{
		URIs:      uris,
		Cookie:    n.cookieHeader(master),
		UserAgent: n.userAgent,
	}, nil
}

// bestAudioFormats returns the audio renditions worth playing, the highest
// bit rate first: available bands only, and none above 48kHz, which
// Windows' AAC decoder cannot play (576kbps is 88.2/96kHz only). All of them
// are requested at once, so the Go side can fall back a band when playback
// of one fails. With no premium login the top band is 128/192kbps.
func bestAudioFormats(audios []nicoAudio) []nicoAudio {
	best := make([]nicoAudio, 0, len(audios))
	for _, a := range audios {
		if a.IsAvailable && a.SamplingRate <= 48000 {
			best = append(best, a)
		}
	}
	sort.SliceStable(best, func(i, j int) bool { return best[i].BitRate > best[j].BitRate })
	return best
}

// availableVideoIDs returns the video renditions the access-rights request
// pairs with the audio ones; playback never downloads them, but the request
// takes video+audio pairs only.
func availableVideoIDs(videos []nicoVideo) []string {
	ids := make([]string, 0, len(videos))
	for _, v := range videos {
		if v.IsAvailable {
			ids = append(ids, v.ID)
		}
	}
	return ids
}

// fetchWatch loads the watch metadata as a guest; public videos need no
// cookies. The X-Frontend-ID/-Version pair and actionTrackId are required
// headers — without them the API answers 400 INVALID_PARAMETER.
func (n *Nico) fetchWatch(videoID string) (nicoWatch, error) {
	trackID := fmt.Sprintf("AAAAAAAAAA_%d", time.Now().UnixMilli())
	req, err := n.request(http.MethodGet, n.baseURL+"/api/watch/v3_guest/"+videoID+"?actionTrackId="+trackID, nil)
	if err != nil {
		return nicoWatch{}, errors.New("動画の情報を取得できませんでした")
	}
	var resp struct {
		Meta struct {
			Status    int    `json:"status"`
			ErrorCode string `json:"errorCode"`
		} `json:"meta"`
		Data struct {
			Client struct {
				WatchTrackID string `json:"watchTrackId"`
			} `json:"client"`
			Media struct {
				Domand struct {
					Videos         []nicoVideo `json:"videos"`
					Audios         []nicoAudio `json:"audios"`
					AccessRightKey string      `json:"accessRightKey"`
				} `json:"domand"`
			} `json:"media"`
		} `json:"data"`
	}
	if err := n.doJSON(req, &resp); err != nil {
		return nicoWatch{}, err
	}
	if resp.Meta.Status != http.StatusOK {
		return nicoWatch{}, fmt.Errorf("動画の情報を取得できませんでした（status %d %s）", resp.Meta.Status, resp.Meta.ErrorCode)
	}
	return nicoWatch{
		Videos:         resp.Data.Media.Domand.Videos,
		Audios:         resp.Data.Media.Domand.Audios,
		AccessRightKey: resp.Data.Media.Domand.AccessRightKey,
		TrackID:        resp.Data.Client.WatchTrackID,
	}, nil
}

// contentURL trades the access-right key for the HLS master playlist URL.
// The key is a short-lived JWT (about 10 minutes) bound to this watch
// session, so it is fetched per play.
func (n *Nico) contentURL(videoID string, watch nicoWatch, videos []string, audios []nicoAudio) (string, error) {
	var outputs [][2]string
	for _, video := range videos {
		for _, audio := range audios {
			outputs = append(outputs, [2]string{video, audio.ID})
		}
	}
	body, err := json.Marshal(map[string][][2]string{"outputs": outputs})
	if err != nil {
		return "", errors.New("この動画の音源を取得できませんでした")
	}
	req, err := n.request(http.MethodPost,
		n.apiBase+"/v1/watch/"+videoID+"/access-rights/hls?actionTrackId="+watch.TrackID, bytes.NewReader(body))
	if err != nil {
		return "", errors.New("この動画の音源を取得できませんでした")
	}
	req.Header.Set("Accept", "application/json;charset=utf-8")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Access-Right-Key", watch.AccessRightKey)
	req.Header.Set("X-Request-With", n.baseURL)
	var resp struct {
		Meta struct {
			Status    int    `json:"status"`
			ErrorCode string `json:"errorCode"`
		} `json:"meta"`
		Data struct {
			ContentURL string `json:"contentUrl"`
		} `json:"data"`
	}
	if err := n.doJSON(req, &resp); err != nil {
		return "", err
	}
	if resp.Data.ContentURL == "" {
		return "", fmt.Errorf("この動画の音源を取得できませんでした（status %d %s）", resp.Meta.Status, resp.Meta.ErrorCode)
	}
	return resp.Data.ContentURL, nil
}

// audioVariant is one audio rendition of the HLS master playlist.
type audioVariant struct {
	URI     string
	BitRate int // from the GROUP-ID like audio-aac-192kbps, 0 if unnamed
}

var (
	audioGroupRe = regexp.MustCompile(`TYPE=AUDIO.*?GROUP-ID="([^"]*)".*?URI="([^"]*)"`)
	kbpsRe       = regexp.MustCompile(`(\d+)kbps`)
)

// audioVariants returns the audio-only renditions of a master playlist
// (#EXT-X-MEDIA TYPE=AUDIO, which is how niconico lists them — the same
// video media playlist is shared by several stream variants), highest
// bitrate first. The URI is the absolute signed URL of the audio media
// playlist, which is what the helper must be fed: handed the master, AMS
// would download video too.
func audioVariants(master string) []audioVariant {
	var audio []audioVariant
	for _, m := range audioGroupRe.FindAllStringSubmatch(master, -1) {
		v := audioVariant{URI: m[2]}
		if k := kbpsRe.FindStringSubmatch(m[1]); k != nil {
			fmt.Sscanf(k[1], "%d", &v.BitRate)
		}
		audio = append(audio, v)
	}
	sort.SliceStable(audio, func(i, j int) bool { return audio[i].BitRate > audio[j].BitRate })
	return audio
}

// cookieHeader is the Cookie header value the jar would send to rawURL. The
// helper hands it to its own HTTP stack, which has no jar of the app's.
func (n *Nico) cookieHeader(rawURL string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return ""
	}
	var parts []string
	for _, c := range n.client.Jar.Cookies(u) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// request prepares a request with the frontend headers every niconico API
// call wants.
func (n *Nico) request(method, rawURL string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Frontend-ID", "6")
	req.Header.Set("X-Frontend-Version", "0")
	req.Header.Set("User-Agent", n.userAgent)
	return req, nil
}

// doJSON sends req and decodes the JSON body, mapping transport and HTTP
// failures to messages the player can show.
func (n *Nico) doJSON(req *http.Request, out any) error {
	resp, err := n.client.Do(req)
	if err != nil {
		return errors.New("ニコニコに接続できませんでした（通信エラー）")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxNicoBody+1))
	if err != nil || len(body) > maxNicoBody {
		return errors.New("ニコニコの応答を読み取れませんでした")
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ニコニコのAPIが失敗しました（HTTP %d）", resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("ニコニコの応答を読み取れませんでした")
	}
	return nil
}

// get fetches a playlist with the frontend headers and cookies.
func (n *Nico) get(rawURL string) ([]byte, error) {
	req, err := n.request(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.New("音源の取得に失敗しました")
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, errors.New("音源の取得に失敗しました（通信エラー）")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxNicoBody+1))
	if err != nil || len(body) > maxNicoBody {
		return nil, errors.New("音源の取得に失敗しました")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("音源の取得に失敗しました（HTTP %d）", resp.StatusCode)
	}
	return body, nil
}
