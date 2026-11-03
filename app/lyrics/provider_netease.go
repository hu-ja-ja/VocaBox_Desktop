package lyrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	neteaseAPIBaseURL  = "https://music.163.com"
	neteaseEAPIBaseURL = "https://interface3.music.163.com"
	neteaseEAPILyric   = "/eapi/song/lyric/v1"
	// neteaseEAPIUserAgent is what the eapi endpoint expects: the Android
	// app's browser-like client.
	neteaseEAPIUserAgent = "Mozilla/5.0 (Linux; Android 9; PCT-AL10) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.64 HuaweiBrowser/10.0.3.311 Mobile Safari/537.36"
	// neteaseCandidateLimit is how many ranked songs one lookup tries before
	// giving up. It is wider than KuGou's because a song's word timing often
	// sits on a different pressing, so the first candidates may only have
	// LRC while a later one has YRC.
	neteaseCandidateLimit = 6
)

// neteaseProvider is the NetEase Cloud Music lyrics database, the last
// fallback. Word-timed YRC comes from the eapi endpoint; a track without it
// falls back to the plain LRC the web API answers.
type neteaseProvider struct {
	client    *http.Client
	apiBase   string
	eapiBase  string
	userAgent string
}

// NewNeteaseProvider returns the NetEase source. Empty bases mean the real
// services; tests point them at their own server.
func NewNeteaseProvider(client *http.Client, apiBase, eapiBase, userAgent string) Provider {
	if apiBase == "" {
		apiBase = neteaseAPIBaseURL
	}
	if eapiBase == "" {
		eapiBase = neteaseEAPIBaseURL
	}
	return &neteaseProvider{
		client:    client,
		apiBase:   strings.TrimRight(apiBase, "/"),
		eapiBase:  strings.TrimRight(eapiBase, "/"),
		userAgent: userAgent,
	}
}

func (p *neteaseProvider) ID() string { return SourceNetease }

func (p *neteaseProvider) Name() string { return "NetEase" }

// neteaseSong is one search hit, as the matcher ranks it.
type neteaseSong struct {
	ID         string
	Title      string
	Artist     string
	DurationMS float64
	score      int
}

func (p *neteaseProvider) Fetch(q Query) (Doc, error) {
	if p == nil || p.client == nil || strings.TrimSpace(q.Title) == "" {
		return Doc{}, ErrNotFound
	}
	var fallback Doc
	hasFallback := false
	var annotatedYRC Doc
	hasAnnotatedYRC := false
	var firstErr error
	seen := map[string]bool{}
	for _, keyword := range searchQueries(q) {
		if q.expired() {
			break
		}
		songs, err := p.search(keyword)
		if err != nil {
			return Doc{}, err
		}
		var accepted []neteaseSong
		for _, song := range songs {
			score, ok := rank(q, song.Title, song.Artist, song.DurationMS)
			if !ok {
				continue
			}
			song.score = score
			accepted = append(accepted, song)
		}
		sort.SliceStable(accepted, func(i, j int) bool { return accepted[i].score > accepted[j].score })
		if len(accepted) > neteaseCandidateLimit {
			accepted = accepted[:neteaseCandidateLimit]
		}
		for _, song := range accepted {
			if q.expired() {
				break
			}
			if seen[song.ID] {
				continue
			}
			seen[song.ID] = true
			doc, err := p.lyrics(song.ID)
			if err == nil {
				// A word-timed answer wins over a line-timed one: the
				// user asked for the finest timing the source has. A
				// clean one wins over a ruby or translation copy, and
				// both win over LRC.
				if doc.WordTimed() {
					if doc.annotated {
						if !hasAnnotatedYRC {
							annotatedYRC, hasAnnotatedYRC = doc, true
						}
						continue
					}
					return doc, nil
				}
				if !hasFallback {
					fallback, hasFallback = doc, true
				}
				continue
			}
			if !errors.Is(err, ErrNotFound) && firstErr == nil {
				firstErr = err
			}
		}
	}
	if hasAnnotatedYRC {
		return annotatedYRC, nil
	}
	if hasFallback {
		return fallback, nil
	}
	if firstErr != nil {
		return Doc{}, firstErr
	}
	return Doc{}, ErrNotFound
}

// search runs one keyword through the cloud search.
func (p *neteaseProvider) search(keyword string) ([]neteaseSong, error) {
	form := url.Values{}
	form.Set("s", keyword)
	form.Set("type", "1")
	form.Set("offset", "0")
	form.Set("limit", "20")
	req, err := http.NewRequest(http.MethodPost, p.apiBase+"/api/cloudsearch/pc", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFailed, err)
	}
	p.headers(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var parsed struct {
		Code   int `json:"code"`
		Result struct {
			Songs []struct {
				ID       int64   `json:"id"`
				Name     string  `json:"name"`
				Duration float64 `json:"duration"`
				DT       float64 `json:"dt"`
				Artists  []struct {
					Name string `json:"name"`
				} `json:"artists"`
				AR []struct {
					Name string `json:"name"`
				} `json:"ar"`
			} `json:"songs"`
		} `json:"result"`
	}
	if err := sourceJSON(p.client, req, &parsed); err != nil {
		return nil, err
	}
	if parsed.Code != 200 {
		return nil, fmt.Errorf("%w: code %d", ErrFailed, parsed.Code)
	}
	songs := make([]neteaseSong, 0, len(parsed.Result.Songs))
	for _, song := range parsed.Result.Songs {
		if song.ID == 0 || song.Name == "" {
			continue
		}
		names := song.AR
		if len(names) == 0 {
			names = song.Artists
		}
		artists := make([]string, 0, len(names))
		for _, artist := range names {
			if artist.Name != "" {
				artists = append(artists, artist.Name)
			}
		}
		duration := song.DT
		if duration == 0 {
			duration = msFromDuration(song.Duration)
		}
		songs = append(songs, neteaseSong{
			ID:         strconv.FormatInt(song.ID, 10),
			Title:      song.Name,
			Artist:     strings.Join(artists, " "),
			DurationMS: duration,
		})
	}
	return songs, nil
}

// lyrics fetches one song's lyrics: the eapi answer (word-timed YRC when
// the song has one, else the line-timed LRC it carries), falling back to the
// older web API when eapi fails.
func (p *neteaseProvider) lyrics(id string) (Doc, error) {
	doc, err := p.eapiLyrics(id)
	if err == nil {
		return doc, nil
	}
	if errors.Is(err, ErrNotFound) {
		// Eapi answered and the song has neither YRC nor LRC; the older
		// endpoint has nothing more to add.
		return Doc{}, ErrNotFound
	}
	lrc, lrcErr := p.lrc(id)
	if lrcErr == nil {
		return lrc, nil
	}
	if errors.Is(lrcErr, ErrNotFound) {
		// The older API answered; eapi failing changes nothing about
		// whether the song has lyrics.
		return Doc{}, ErrNotFound
	}
	return Doc{}, err
}

// eapiLyrics fetches the eapi answer, which carries both the word-timed YRC
// and, for songs without one, the line-timed LRC.
func (p *neteaseProvider) eapiLyrics(id string) (Doc, error) {
	header := eapiHeader()
	headerJSON, err := json.Marshal(headerMap(header))
	if err != nil {
		return Doc{}, fmt.Errorf("%w: %v", ErrFailed, err)
	}
	data := map[string]any{
		"id":         id,
		"cp":         "false",
		"lv":         "0",
		"kv":         "0",
		"tv":         "0",
		"rv":         "0",
		"yv":         "0",
		"ytv":        "0",
		"yrv":        "0",
		"csrf_token": "",
		"header":     string(headerJSON),
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return Doc{}, fmt.Errorf("%w: %v", ErrFailed, err)
	}
	endpoint := p.eapiBase + neteaseEAPILyric
	params, err := encryptEAPI(endpoint, string(dataJSON))
	if err != nil {
		return Doc{}, fmt.Errorf("%w: %v", ErrFailed, err)
	}
	form := url.Values{}
	form.Set("params", params)
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Doc{}, fmt.Errorf("%w: %v", ErrFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", neteaseEAPIUserAgent)
	req.Header.Set("Referer", "https://music.163.com/")
	req.Header.Set("Cookie", eapiCookie(header))
	var parsed struct {
		Code int `json:"code"`
		YRC  struct {
			Lyric string `json:"lyric"`
		} `json:"yrc"`
		LRC struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
	}
	if err := sourceJSON(p.client, req, &parsed); err != nil {
		return Doc{}, err
	}
	if parsed.Code != 0 && parsed.Code != 200 {
		return Doc{}, fmt.Errorf("%w: code %d", ErrFailed, parsed.Code)
	}
	if yrc := strings.TrimSpace(parsed.YRC.Lyric); yrc != "" {
		if lines := parseYRC(yrc); len(lines) > 0 {
			annotated := strings.Contains(yrc, "（") || strings.Contains(yrc, "【")
			return Doc{Timed: true, Lines: lines, annotated: annotated}, nil
		}
	}
	if lrc := strings.TrimSpace(parsed.LRC.Lyric); lrc != "" {
		if lines := parseLRC(lrc); len(lines) > 0 {
			return Doc{Timed: true, Lines: lines}, nil
		}
	}
	return Doc{}, ErrNotFound
}

// lrc fetches the line-timed lyrics of the web API.
func (p *neteaseProvider) lrc(id string) (Doc, error) {
	query := url.Values{}
	query.Set("id", id)
	query.Set("lv", "1")
	query.Set("kv", "1")
	query.Set("tv", "-1")
	req, err := http.NewRequest(http.MethodGet, p.apiBase+"/api/song/lyric?"+query.Encode(), nil)
	if err != nil {
		return Doc{}, fmt.Errorf("%w: %v", ErrFailed, err)
	}
	p.headers(req)
	var parsed struct {
		Code int `json:"code"`
		LRC  struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
		NoLyric     bool `json:"nolyric"`
		Uncollected bool `json:"uncollected"`
	}
	if err := sourceJSON(p.client, req, &parsed); err != nil {
		return Doc{}, err
	}
	if parsed.Code != 0 && parsed.Code != 200 {
		return Doc{}, fmt.Errorf("%w: code %d", ErrFailed, parsed.Code)
	}
	if parsed.NoLyric || parsed.Uncollected || strings.TrimSpace(parsed.LRC.Lyric) == "" {
		return Doc{}, ErrNotFound
	}
	lines := parseLRC(parsed.LRC.Lyric)
	if len(lines) == 0 {
		return Doc{}, ErrNotFound
	}
	return Doc{Timed: true, Lines: lines}, nil
}

// eapiHeader is the anonymous client header the eapi endpoint expects, as
// key/value pairs in the order the cookie joins them.
func eapiHeader() [][2]string {
	now := time.Now()
	return [][2]string{
		{"__csrf", ""},
		{"appver", "8.0.0"},
		{"buildver", strconv.FormatInt(now.Unix(), 10)},
		{"channel", ""},
		{"deviceId", ""},
		{"mobilename", ""},
		{"resolution", "1920x1080"},
		{"os", "android"},
		{"osver", ""},
		{"requestId", fmt.Sprintf("%d_%04d", now.UnixMilli(), now.UnixNano()%1000)},
		{"versioncode", "140"},
		{"MUSIC_U", ""},
	}
}

// headerMap turns the eapi header pairs into the JSON object the body
// carries as a string.
func headerMap(header [][2]string) map[string]string {
	out := make(map[string]string, len(header))
	for _, pair := range header {
		out[pair[0]] = pair[1]
	}
	return out
}

// eapiCookie joins the eapi header pairs the way the endpoint's cookie
// carries them.
func eapiCookie(header [][2]string) string {
	parts := make([]string, 0, len(header))
	for _, pair := range header {
		parts = append(parts, pair[0]+"="+pair[1])
	}
	return strings.Join(parts, "; ")
}

// headers are what the web API expects of a browser answer.
func (p *neteaseProvider) headers(req *http.Request) {
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}
	req.Header.Set("Referer", "https://music.163.com/")
}
