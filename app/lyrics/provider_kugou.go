package lyrics

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const (
	kugouSearchBaseURL = "https://mobiles.kugou.com"
	kugouLyricsBaseURL = "https://lyrics.kugou.com"
	// kugouCandidateLimit is how many ranked songs and lyric candidates one
	// lookup tries before giving up.
	kugouCandidateLimit = 3
)

// kugouProvider is the KuGou lyrics database, the first fallback when
// niconico has no official lyrics. Its search answers a song's hash, and the
// lyrics service answers the candidates and then the KRC file itself.
type kugouProvider struct {
	client     *http.Client
	searchBase string
	lyricsBase string
	userAgent  string
}

// NewKugouProvider returns the KuGou source. Empty bases mean the real
// service; tests point them at their own server.
func NewKugouProvider(client *http.Client, searchBase, lyricsBase, userAgent string) Provider {
	if searchBase == "" {
		searchBase = kugouSearchBaseURL
	}
	if lyricsBase == "" {
		lyricsBase = kugouLyricsBaseURL
	}
	return &kugouProvider{
		client:     client,
		searchBase: strings.TrimRight(searchBase, "/"),
		lyricsBase: strings.TrimRight(lyricsBase, "/"),
		userAgent:  userAgent,
	}
}

func (p *kugouProvider) ID() string { return SourceKugou }

func (p *kugouProvider) Name() string { return "KuGou" }

// kugouSong is one search hit, as the matcher ranks it.
type kugouSong struct {
	Hash       string
	SongName   string
	Singer     string
	DurationMS float64
	score      int
}

// kugouLyricCandidate is one lyrics file the lyrics search offers.
type kugouLyricCandidate struct {
	ID         string  `json:"id"`
	AccessKey  string  `json:"accesskey"`
	Song       string  `json:"song"`
	Singer     string  `json:"singer"`
	DurationMS float64 `json:"duration"`
	Format     int     `json:"content_format"`
	score      int
}

func (p *kugouProvider) Fetch(q Query) (Doc, error) {
	if p == nil || p.client == nil || strings.TrimSpace(q.Title) == "" {
		return Doc{}, ErrNotFound
	}
	var firstErr error
	for _, keyword := range searchQueries(q) {
		songs, err := p.search(keyword)
		if err != nil {
			return Doc{}, err
		}
		var accepted []kugouSong
		for _, song := range songs {
			score, ok := rank(q, song.SongName, song.Singer, song.DurationMS)
			if !ok {
				continue
			}
			song.score = score
			accepted = append(accepted, song)
		}
		sort.SliceStable(accepted, func(i, j int) bool { return accepted[i].score > accepted[j].score })
		if len(accepted) > kugouCandidateLimit {
			accepted = accepted[:kugouCandidateLimit]
		}
		for _, song := range accepted {
			doc, err := p.lyrics(q, song)
			if err == nil {
				return doc, nil
			}
			if !errors.Is(err, ErrNotFound) && firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		return Doc{}, firstErr
	}
	return Doc{}, ErrNotFound
}

// search looks a keyword up and returns the songs that may be the track.
func (p *kugouProvider) search(keyword string) ([]kugouSong, error) {
	query := url.Values{}
	query.Set("version", "10000")
	query.Set("plat", "0")
	query.Set("correct", "1")
	query.Set("pagesize", "10")
	query.Set("keyword", keyword)
	var parsed struct {
		Status int `json:"status"`
		Data   struct {
			Info []struct {
				Hash     string `json:"hash"`
				SongName string `json:"songname"`
				Singer   string `json:"singername"`
				Duration int    `json:"duration"`
			} `json:"info"`
		} `json:"data"`
	}
	if err := p.get(p.searchBase+"/api/v3/search/song?"+query.Encode(), &parsed); err != nil {
		return nil, err
	}
	if parsed.Status != 1 {
		return nil, nil
	}
	songs := make([]kugouSong, 0, len(parsed.Data.Info))
	for _, song := range parsed.Data.Info {
		if song.Hash == "" || song.SongName == "" {
			continue
		}
		songs = append(songs, kugouSong{
			Hash:       song.Hash,
			SongName:   song.SongName,
			Singer:     song.Singer,
			DurationMS: float64(song.Duration) * 1000,
		})
	}
	return songs, nil
}

// lyrics finds the best lyric candidates for a song and downloads the first
// one that parses.
func (p *kugouProvider) lyrics(q Query, song kugouSong) (Doc, error) {
	query := url.Values{}
	query.Set("ver", "1")
	query.Set("man", "yes")
	query.Set("client", "mobi")
	query.Set("hash", song.Hash)
	var parsed struct {
		Candidates []kugouLyricCandidate `json:"candidates"`
	}
	if err := p.get(p.lyricsBase+"/search?"+query.Encode(), &parsed); err != nil {
		return Doc{}, err
	}
	var accepted []kugouLyricCandidate
	for _, candidate := range parsed.Candidates {
		if candidate.ID == "" || candidate.AccessKey == "" {
			continue
		}
		score, ok := rank(q, candidate.Song, candidate.Singer, candidate.DurationMS)
		if !ok {
			continue
		}
		candidate.score = score
		accepted = append(accepted, candidate)
	}
	sort.SliceStable(accepted, func(i, j int) bool {
		if accepted[i].score != accepted[j].score {
			return accepted[i].score > accepted[j].score
		}
		return accepted[i].Format == 4 && accepted[j].Format != 4
	})
	if len(accepted) > kugouCandidateLimit {
		accepted = accepted[:kugouCandidateLimit]
	}
	var firstErr error
	for _, candidate := range accepted {
		doc, err := p.download(candidate)
		if err == nil {
			return doc, nil
		}
		if !errors.Is(err, ErrNotFound) && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return Doc{}, firstErr
	}
	return Doc{}, ErrNotFound
}

// download fetches one lyric file and parses it: KRC when the body is one,
// else the LRC the same request may answer.
func (p *kugouProvider) download(candidate kugouLyricCandidate) (Doc, error) {
	query := url.Values{}
	query.Set("ver", "1")
	query.Set("client", "pc")
	query.Set("fmt", "krc")
	query.Set("charset", "utf8")
	query.Set("id", candidate.ID)
	query.Set("accesskey", candidate.AccessKey)
	var parsed struct {
		Content string `json:"content"`
	}
	if err := p.get(p.lyricsBase+"/download?"+query.Encode(), &parsed); err != nil {
		return Doc{}, err
	}
	if strings.TrimSpace(parsed.Content) == "" {
		return Doc{}, ErrNotFound
	}
	text, err := decodeLyricsBody(parsed.Content)
	if err != nil {
		return Doc{}, fmt.Errorf("%w: %v", ErrFailed, err)
	}
	lines := parseKRC(text)
	if len(lines) > 0 {
		lines = dropKRCHeader(lines, candidate.Song, candidate.Singer)
	} else {
		lines = parseLRC(text)
	}
	if len(lines) == 0 {
		return Doc{}, ErrNotFound
	}
	return Doc{Timed: true, Lines: lines}, nil
}

// get sends one GET and decodes its JSON answer.
func (p *kugouProvider) get(rawURL string, v any) error {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrFailed, err)
	}
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}
	req.Header.Set("Accept", "application/json")
	return sourceJSON(p.client, req, v)
}
