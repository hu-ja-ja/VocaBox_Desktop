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
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
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
	// mu guards loggedIn, which the sign-in flow sets while playback may
	// be acquiring a track.
	mu sync.Mutex
	// loggedIn says a session is loaded: the watch API is asked as
	// /api/watch/v3, which answers the signed-in audio bands (up to
	// 320kbps with premium), instead of /api/watch/v3_guest.
	loggedIn bool
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

// sessionCookieNames are the cookies a session is made of: what the
// sign-in flow keeps out of the web view's cookie store, and what
// ClearSession expires.
var sessionCookieNames = []string{"user_session", "user_session_secure"}

// errSessionExpired reports that niconico no longer accepts the stored
// session: the caller forgets it and asks for a new login.
var errSessionExpired = errors.New(locales.ErrSessionExpired())

// sessionSeedURL is the URL the session cookies are stored for; their
// domain is .nicovideo.jp, which covers watch, nvapi and delivery.
var sessionSeedURL = &neturl.URL{Scheme: "https", Host: "www.nicovideo.jp", Path: "/"}

// SetSession loads a session's cookies, a Cookie header like
// "user_session=…; user_session_secure=…", into the jar: the watch,
// access-rights and delivery requests then carry them. Only the session
// cookies are kept, and only for nicovideo.
func (n *Nico) SetSession(cookieHeader string) error {
	cookies := sessionCookies(cookieHeader)
	if len(cookies) == 0 {
		return errors.New(locales.ErrSessionInvalid())
	}
	n.client.Jar.SetCookies(sessionSeedURL, cookies)
	n.mu.Lock()
	n.loggedIn = true
	n.mu.Unlock()
	return nil
}

// ClearSession forgets the session cookies, returning the client to guest
// watch calls. The jar stays, so a track already acquiring keeps the
// delivery cookie it was given.
func (n *Nico) ClearSession() {
	expired := make([]*http.Cookie, 0, len(sessionCookieNames))
	for _, name := range sessionCookieNames {
		expired = append(expired, &http.Cookie{
			Name: name, Value: "", Path: "/", Domain: ".nicovideo.jp",
			MaxAge: -1, Secure: true,
		})
	}
	n.mu.Lock()
	n.loggedIn = false
	n.mu.Unlock()
	n.client.Jar.SetCookies(sessionSeedURL, expired)
}

// LoggedIn reports whether a session is loaded.
func (n *Nico) LoggedIn() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.loggedIn
}

// sessionCookies picks the session cookies out of a Cookie header. Any
// other pair is dropped, so a corrupted file cannot seed the jar with
// cookies of its own.
func sessionCookies(header string) []*http.Cookie {
	var out []*http.Cookie
	for _, part := range strings.Split(header, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !slices.Contains(sessionCookieNames, name) || value == "" {
			continue
		}
		out = append(out, &http.Cookie{
			Name: name, Value: value,
			Path: "/", Domain: ".nicovideo.jp", Secure: true,
		})
	}
	return out
}

// verifyVideoID is the video a loaded session is checked against: the
// first niconico video, which stays public.
const verifyVideoID = "sm9"

// VerifySession checks a loaded session against the watch API, which
// answers /api/watch/v3 with 200 for a session niconico accepts and
// UNAUTHORIZED for one it does not. The check asks for no side effects,
// so it writes no watch history.
func (n *Nico) VerifySession() error {
	if !n.LoggedIn() {
		return errors.New(locales.ErrNotLoggedIn())
	}
	_, err := n.fetchWatch(verifyVideoID, true)
	return err
}

// nicoUser is the signed-in account as far as the sidebar shows it: the
// display name and the URL of the account's picture.
type nicoUser struct {
	Nickname string
	IconURL  string
}

// User fetches the signed-in account's name and picture URL from the
// users/me API. A session niconico no longer accepts is an ordinary error
// here: only the avatar would show, and playback reports the expiry.
func (n *Nico) User() (nicoUser, error) {
	if !n.LoggedIn() {
		return nicoUser{}, errors.New(locales.ErrNotLoggedIn())
	}
	req, err := n.request(http.MethodGet, n.apiBase+"/v1/users/me", nil)
	if err != nil {
		return nicoUser{}, errors.New(locales.ErrAccountFetch())
	}
	var resp struct {
		Meta struct {
			Status int `json:"status"`
		} `json:"meta"`
		Data struct {
			User struct {
				Nickname string `json:"nickname"`
				Icons    struct {
					Small string `json:"small"`
					Large string `json:"large"`
				} `json:"icons"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := n.doJSON(req, &resp); err != nil {
		return nicoUser{}, err
	}
	if resp.Meta.Status != http.StatusOK {
		return nicoUser{}, errors.New(locales.ErrAccountFetchStatus(resp.Meta.Status))
	}
	icon := resp.Data.User.Icons.Large
	if icon == "" {
		icon = resp.Data.User.Icons.Small
	}
	return nicoUser{Nickname: resp.Data.User.Nickname, IconURL: icon}, nil
}

// playbackSources is everything the helper needs for one track: the audio
// media playlist URLs, the highest band first (the Go side falls back to the
// next one when playback of a band fails), and the Cookie and User-Agent the
// delivery hosts expect. File is the track's saved copy instead: it plays
// without the network, and the bands stay empty.
type playbackSources struct {
	URIs []string
	// BitRates is the kbps of each band of URIs, for the quality the
	// player bar shows; 0 where the master playlist does not name one.
	BitRates  []int
	Cookie    string
	UserAgent string
	File      string
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
	watch, err := n.fetchWatch(videoID, false)
	if err != nil {
		return playbackSources{}, err
	}
	audios := bestAudioFormats(watch.Audios)
	videos := availableVideoIDs(watch.Videos)
	if len(audios) == 0 || len(videos) == 0 {
		return playbackSources{}, errors.New(locales.ErrNoAudio())
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
		return playbackSources{}, errors.New(locales.ErrAudioSourceMissing())
	}
	uris := make([]string, 0, len(variants))
	bitrates := make([]int, 0, len(variants))
	for _, v := range variants {
		uris = append(uris, v.URI)
		bitrates = append(bitrates, v.BitRate)
	}
	return playbackSources{
		URIs:      uris,
		BitRates:  bitrates,
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

// fetchWatch loads the watch metadata: as a guest when no session is
// loaded, as /api/watch/v3 once one is, which answers the signed-in audio
// bands. noSideEffect asks the API to write no watch history, for the
// checks that are not playback. The X-Frontend-ID/-Version pair and
// actionTrackId are required headers — without them the API answers 400
// INVALID_PARAMETER.
func (n *Nico) fetchWatch(videoID string, noSideEffect bool) (nicoWatch, error) {
	trackID := fmt.Sprintf("AAAAAAAAAA_%d", time.Now().UnixMilli())
	path := "v3_guest"
	n.mu.Lock()
	loggedIn := n.loggedIn
	n.mu.Unlock()
	if loggedIn {
		path = "v3"
	}
	rawURL := n.baseURL + "/api/watch/" + path + "/" + videoID + "?actionTrackId=" + trackID
	if noSideEffect {
		rawURL += "&noSideEffect=true"
	}
	req, err := n.request(http.MethodGet, rawURL, nil)
	if err != nil {
		return nicoWatch{}, errors.New(locales.ErrWatchFetch())
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
		// A session niconico no longer accepts is what asks for a new
		// login; any other failure is an ordinary API error.
		if loggedIn && resp.Meta.ErrorCode == "UNAUTHORIZED" {
			return nicoWatch{}, errSessionExpired
		}
		return nicoWatch{}, errors.New(locales.ErrWatchFetchStatus(resp.Meta.Status, resp.Meta.ErrorCode))
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
	outputs := make([][2]string, 0, len(audios))
	for _, audio := range audios {
		outputs = append(outputs, [2]string{videos[0], audio.ID})
	}
	body, err := json.Marshal(map[string][][2]string{"outputs": outputs})
	if err != nil {
		return "", errors.New(locales.ErrAudioFetch())
	}
	req, err := n.request(http.MethodPost,
		n.apiBase+"/v1/watch/"+videoID+"/access-rights/hls?actionTrackId="+watch.TrackID, bytes.NewReader(body))
	if err != nil {
		return "", errors.New(locales.ErrAudioFetch())
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
		return "", errors.New(locales.ErrAudioFetchStatus(resp.Meta.Status, resp.Meta.ErrorCode))
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

// SessionCookie returns the Cookie header the jar would send to rawURL, ""
// as a guest: the mylist service sends the same session to the metadata
// API, so the account's own private mylists answer.
func (n *Nico) SessionCookie(rawURL string) string { return n.cookieHeader(rawURL) }

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
		return errors.New(locales.ErrNicoNetwork())
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxNicoBody+1))
	if err != nil || len(body) > maxNicoBody {
		return errors.New(locales.ErrNicoParse())
	}
	if err := json.Unmarshal(body, out); err != nil {
		if resp.StatusCode/100 != 2 {
			return errors.New(locales.ErrNicoAPIHTTP(resp.StatusCode))
		}
		return errors.New(locales.ErrNicoParse())
	}
	if resp.StatusCode/100 != 2 && !hasMetaStatus(body) {
		return errors.New(locales.ErrNicoAPIHTTP(resp.StatusCode))
	}
	return nil
}

func hasMetaStatus(body []byte) bool {
	var env struct {
		Meta struct {
			Status int `json:"status"`
		} `json:"meta"`
	}
	return json.Unmarshal(body, &env) == nil && env.Meta.Status > 0
}

// get fetches a playlist with the frontend headers and cookies.
func (n *Nico) get(rawURL string) ([]byte, error) {
	req, err := n.request(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.New(locales.ErrAudioSourceFetch())
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, errors.New(locales.ErrAudioSourceNetwork())
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxNicoBody+1))
	if err != nil || len(body) > maxNicoBody {
		return nil, errors.New(locales.ErrAudioSourceFetch())
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(locales.ErrAudioSourceHTTP(resp.StatusCode))
	}
	return body, nil
}
