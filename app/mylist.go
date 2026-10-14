package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// nvapiBaseURL is the only host this app fetches mylist metadata from.
	nvapiBaseURL = "https://nvapi.nicovideo.jp"
	// mylistPageInterval spaces the pages of a full snapshot fetch, so saving
	// a large mylist does not hammer the metadata API.
	mylistPageInterval = 200 * time.Millisecond
	// retryAfterMax bounds how long a Retry-After header can pause a request;
	// a missing header waits retryAfterDefault.
	retryAfterMax     = 30 * time.Second
	retryAfterDefault = 5 * time.Second
	// mylistPageSize is the number of entries requested per page.
	mylistPageSize = 100
	// maxMylistBody bounds a metadata response body.
	maxMylistBody = 8 << 20
	// maxMylistPages bounds a snapshot fetch so a server that keeps reporting
	// hasNext cannot loop forever (200 pages * 100 items).
	maxMylistPages = 200
	// mylistUserAgent identifies this client, as the community docs ask.
	mylistUserAgent = "VocaBox/0.1.0"
)

// errMylistInput is returned when the input is neither a numeric mylist ID
// nor a supported mylist URL.
var errMylistInput = errors.New("マイリストのIDまたはURLの形式が正しくありません")

// MylistItem is one entry of a mylist. Video metadata can be absent
// (deleted/private video); such items stay listed as unavailable rather than
// being dropped or filled in.
type MylistItem struct {
	WatchID   string `json:"watchId"`
	Available bool   `json:"available"`
	VideoID   string `json:"videoId,omitempty"`
	Title     string `json:"title,omitempty"`
	Uploader  string `json:"uploader,omitempty"`
	Duration  int    `json:"duration,omitempty"`
	// ThumbURL is the extension-less picture URL the mylist API gives, kept
	// with the item so saved snapshots show pictures without a refresh.
	// Videos have one only when the API knew them; entries saved before
	// this field existed fall back to a URL derived from the watch ID.
	ThumbURL string `json:"thumbnailUrl,omitempty"`
}

// MylistPage is one page of mylist metadata.
type MylistPage struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Description    string       `json:"description"`
	Owner          string       `json:"owner"`
	TotalItemCount int          `json:"totalItemCount"`
	HasNext        bool         `json:"hasNext"`
	Items          []MylistItem `json:"items"`
}

// Mylist looks up NicoNico mylist metadata: public mylists as a guest, and
// the signed-in account's own mylists — private ones included — through the
// session's cookies. It also keeps locally held snapshots of saved mylists.
// The lookups use the community-documented unofficial NVAPI endpoints.
type Mylist struct {
	client  *http.Client
	baseURL string
	// session returns the Cookie header to send to an API URL, "" as a
	// guest. Nil means no session source, as in tests that build a Mylist
	// directly and in the guest-only stages.
	session func(rawURL string) string
	// configDir overrides the user config directory holding mylists.json.
	// Empty means os.UserConfigDir()/VocaBox. Tests set a temp dir.
	configDir string
	// thumbHost overrides the host thumbnail pictures are fetched from.
	// Tests point it at their server; empty means the real CDN.
	thumbHost string
	// thumbLimiter spaces and bounds thumbnail fetches. Nil means no limit,
	// as in tests that build a Mylist directly.
	thumbLimiter *thumbnailLimiter
	// pageInterval spaces the pages of a full snapshot fetch. Zero means
	// none, as in tests.
	pageInterval time.Duration
	// savedMu serializes file operations on the saved mylists.
	// HTTP fetches never take it.
	savedMu sync.Mutex
}

// NewMylist returns the bound Mylist service.
func NewMylist() *Mylist {
	return &Mylist{
		client:       &http.Client{Timeout: 15 * time.Second},
		baseURL:      nvapiBaseURL,
		thumbLimiter: newThumbnailLimiter(thumbConcurrency, thumbInterval, thumbCooldown),
		pageInterval: mylistPageInterval,
	}
}

// GetPage returns page (1-based) of the mylist identified by input, which
// is a numeric ID or a supported mylist URL: a public mylist as a guest, and
// the account's own private mylist once signed in.
func (m *Mylist) GetPage(input string, page int) (MylistPage, error) {
	id, err := ParseMylistInput(input)
	if err != nil {
		return MylistPage{}, err
	}
	if page < 1 {
		page = 1
	}
	return m.fetchPage(id, page)
}

// ownSampleCount asks each of the account's mylists for a few entries:
// enough for a card's 2×2 pictures, few enough to stay one request.
const ownSampleCount = 4

// OwnMylist is one mylist of the signed-in account, private ones included:
// what the home shows as a card.
type OwnMylist struct {
	ID        string
	Name      string
	IsPublic  bool
	ItemCount int
	// Items are the first few entries, for the card's pictures.
	Items []MylistItem
}

// ListOwn returns the signed-in account's mylists, private ones included.
// Without a session the API refuses, which the caller only logs: the home
// simply shows no own mylists.
func (m *Mylist) ListOwn() ([]OwnMylist, error) {
	rawURL := fmt.Sprintf("%s/v1/users/me/mylists?sampleItemCount=%d", m.baseURL, ownSampleCount)
	body, err := m.fetchBody(rawURL)
	if err != nil {
		return nil, err
	}
	return mapOwnMylists(body)
}

// fetchPage returns page (1-based) of the mylist: the public endpoint, and
// — when a session is loaded and the public one refuses — the account's own
// endpoint, which is where private mylists are served from.
//
//	shortcut: a private mylist costs one refused probe per page; remember
//	the route if that ever shows up in practice.
func (m *Mylist) fetchPage(id string, page int) (MylistPage, error) {
	rawURL := fmt.Sprintf("%s/v2/mylists/%s?pageSize=%d&page=%d", m.baseURL, id, mylistPageSize, page)
	body, err := m.fetchBody(rawURL)
	if err != nil && m.sessionCookie(rawURL) != "" && isMylistDenied(err) {
		rawURL = fmt.Sprintf("%s/v1/users/me/mylists/%s?pageSize=%d&page=%d", m.baseURL, id, mylistPageSize, page)
		body, err = m.fetchBody(rawURL)
	}
	if err != nil {
		return MylistPage{}, err
	}
	return mapMylistPage(id, body)
}

// fetchBody sends one API request and returns its body, mapping the
// answer's failures to messages the player can show.
func (m *Mylist) fetchBody(rawURL string) ([]byte, error) {
	resp, err := m.mylistRequest(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, mylistHTTPError(resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMylistBody+1))
	if err != nil {
		return nil, errors.New("マイリストの取得に失敗しました")
	}
	if len(body) > maxMylistBody {
		return nil, errors.New("マイリストの応答が大きすぎます")
	}
	return body, nil
}

// mylistRequest sends one request, waiting out one Retry-After when the
// API asks to slow down. A second refusal returns, so a busy API does not
// hold the caller any longer. The session's cookies ride along: the
// account's own mylists are answered only with them.
func (m *Mylist) mylistRequest(rawURL string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, errors.New("マイリストの取得に失敗しました")
		}
		req.Header.Set("X-Frontend-ID", "6")
		req.Header.Set("X-Frontend-Version", "0")
		req.Header.Set("User-Agent", mylistUserAgent)
		if cookie := m.sessionCookie(rawURL); cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		resp, err := m.client.Do(req)
		if err != nil {
			return nil, errors.New("マイリストの取得に失敗しました（通信エラー）")
		}
		busy := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable
		if !busy || attempt > 0 {
			return resp, nil
		}
		wait := retryAfter(resp)
		resp.Body.Close()
		time.Sleep(wait)
	}
}

// sessionCookie is the Cookie header the session provides for an API URL,
// "" as a guest or with no session source.
func (m *Mylist) sessionCookie(rawURL string) string {
	if m.session == nil {
		return ""
	}
	return m.session(rawURL)
}

// retryAfter reads a Retry-After header, in seconds or as an HTTP date, up
// to retryAfterMax; a missing or unreadable header waits retryAfterDefault.
func retryAfter(resp *http.Response) time.Duration {
	value := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if secs, err := strconv.Atoi(value); err == nil && secs >= 0 {
		return min(time.Duration(secs)*time.Second, retryAfterMax)
	}
	if when, err := http.ParseTime(value); err == nil {
		return min(max(time.Until(when), 0), retryAfterMax)
	}
	return retryAfterDefault
}

// fetchAll fetches every page of the mylist and returns one snapshot: header
// fields from page 1, all items merged, HasNext cleared. maxMylistPages bounds
// the loop, and pageInterval spaces the requests.
func (m *Mylist) fetchAll(id string) (MylistPage, error) {
	snapshot := MylistPage{ID: id, Items: []MylistItem{}}
	for page := 1; page <= maxMylistPages; page++ {
		p, err := m.fetchPage(id, page)
		if err != nil {
			return MylistPage{}, err
		}
		if page == 1 {
			snapshot = p
			snapshot.Items = []MylistItem{}
		}
		snapshot.Items = append(snapshot.Items, p.Items...)
		if !p.HasNext {
			break
		}
		if m.pageInterval > 0 {
			time.Sleep(m.pageInterval)
		}
	}
	snapshot.HasNext = false
	return snapshot, nil
}

// mylistStatusError is an API answer that failed with an HTTP status: the
// caller can tell a refusal — which the account's own endpoint may still
// answer — from a busy or broken API.
type mylistStatusError struct{ status int }

func (e mylistStatusError) Error() string {
	switch e.status {
	case http.StatusNotFound:
		return "マイリストが見つかりません（非公開マイリストはログインすると読み込めます）"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "マイリストを表示できません（非公開マイリストはログインすると読み込めます）"
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return "ニコニコが混み合っています。しばらくしてからもう一度お試しください"
	}
	return fmt.Sprintf("マイリストの取得に失敗しました（HTTP %d）", e.status)
}

func mylistHTTPError(status int) error { return mylistStatusError{status: status} }

// isMylistDenied reports whether err is the API refusing this mylist — not
// found or not allowed — the cases the account's own endpoint may still
// answer for.
func isMylistDenied(err error) bool {
	var status mylistStatusError
	if !errors.As(err, &status) {
		return false
	}
	switch status.status {
	case http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden:
		return true
	}
	return false
}

type nvapiMylistResponse struct {
	Meta struct {
		Status int `json:"status"`
	} `json:"meta"`
	Data struct {
		Mylist nvapiMylist `json:"mylist"`
	} `json:"data"`
}

type nvapiMylist struct {
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	IsPublic       bool        `json:"isPublic"`
	Owner          *nvapiOwner `json:"owner"`
	Items          []nvapiItem `json:"items"`
	TotalItemCount int         `json:"totalItemCount"`
	HasNext        bool        `json:"hasNext"`
}

type nvapiOwner struct {
	Name string `json:"name"`
}

type nvapiItem struct {
	WatchID string      `json:"watchId"`
	Video   *nvapiVideo `json:"video"`
}

type nvapiVideo struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Duration  int             `json:"duration"`
	Owner     *nvapiOwner     `json:"owner"`
	Thumbnail *nvapiThumbnail `json:"thumbnail"`
}

type nvapiThumbnail struct {
	URL       string `json:"url"`
	MiddleURL string `json:"middleUrl"`
}

// nvapiThumbBase picks the extension-less thumbnail URL of an entry: the
// .M and .L sized pictures are asked for by suffix. Older entries carry
// only a middleUrl.
func nvapiThumbBase(t *nvapiThumbnail) string {
	if t == nil {
		return ""
	}
	if t.URL != "" {
		return t.URL
	}
	return strings.TrimSuffix(t.MiddleURL, ".M")
}

func mapMylistPage(id string, body []byte) (MylistPage, error) {
	var res nvapiMylistResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return MylistPage{}, errors.New("マイリストの応答を読み取れませんでした")
	}
	if res.Meta.Status != http.StatusOK {
		return MylistPage{}, fmt.Errorf("マイリストの取得に失敗しました（status %d）", res.Meta.Status)
	}
	ml := res.Data.Mylist
	out := MylistPage{
		ID:             id,
		Name:           ml.Name,
		Description:    ml.Description,
		TotalItemCount: ml.TotalItemCount,
		HasNext:        ml.HasNext,
		Items:          []MylistItem{},
	}
	if ml.Owner != nil {
		out.Owner = ml.Owner.Name
	}
	for _, it := range ml.Items {
		out.Items = append(out.Items, mapMylistItem(it))
	}
	return out, nil
}

// mapMylistItem maps one entry: metadata only when the API knew the video.
func mapMylistItem(it nvapiItem) MylistItem {
	item := MylistItem{WatchID: it.WatchID}
	if it.Video != nil {
		item.Available = true
		item.VideoID = it.Video.ID
		item.Title = it.Video.Title
		item.Duration = it.Video.Duration
		item.ThumbURL = nvapiThumbBase(it.Video.Thumbnail)
		if it.Video.Owner != nil {
			item.Uploader = it.Video.Owner.Name
		}
	}
	return item
}

// nvapiOwnMylistsResponse is GET /v1/users/me/mylists: the signed-in
// account's mylists, sampleItems carrying a few entries of each.
type nvapiOwnMylistsResponse struct {
	Meta struct {
		Status int `json:"status"`
	} `json:"meta"`
	Data struct {
		Mylists []nvapiOwnMylist `json:"mylists"`
	} `json:"data"`
}

type nvapiOwnMylist struct {
	ID          int         `json:"id"`
	IsPublic    bool        `json:"isPublic"`
	Name        string      `json:"name"`
	ItemsCount  int         `json:"itemsCount"`
	SampleItems []nvapiItem `json:"sampleItems"`
}

func mapOwnMylists(body []byte) ([]OwnMylist, error) {
	var res nvapiOwnMylistsResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, errors.New("マイリストの応答を読み取れませんでした")
	}
	if res.Meta.Status != http.StatusOK {
		return nil, fmt.Errorf("マイリストの取得に失敗しました（status %d）", res.Meta.Status)
	}
	out := make([]OwnMylist, 0, len(res.Data.Mylists))
	for _, ml := range res.Data.Mylists {
		own := OwnMylist{
			ID:        strconv.Itoa(ml.ID),
			Name:      ml.Name,
			IsPublic:  ml.IsPublic,
			ItemCount: ml.ItemsCount,
		}
		for _, it := range ml.SampleItems {
			own.Items = append(own.Items, mapMylistItem(it))
		}
		out = append(out, own)
	}
	return out, nil
}

// ParseMylistInput extracts a numeric mylist ID from a bare ID or a
// nicovideo.jp / nico.ms URL. Only the ID is returned: the app always calls
// the fixed NVAPI host and never fetches an arbitrary user-supplied URL.
func ParseMylistInput(input string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", errors.New("マイリストのIDまたはURLを入力してください")
	}
	if isDigits(s) {
		return s, nil
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errMylistInput
	}
	switch strings.ToLower(u.Hostname()) {
	case "nicovideo.jp", "www.nicovideo.jp", "sp.nicovideo.jp":
		return mylistIDFromNicoPath(strings.TrimSuffix(u.Path, "/"), strings.TrimPrefix(u.Fragment, "/"))
	case "nico.ms":
		return mylistIDFromSegments(strings.TrimSuffix(u.Path, "/"), "mylist")
	default:
		return "", errors.New("対応していないURLです（nicovideo.jp / nico.ms のマイリストのみ）")
	}
}

// mylistIDFromNicoPath handles nicovideo.jp paths: /mylist/{id},
// /user/{userID}/mylist/{id}, and /mylist or /my/mylist with the ID in the
// fragment (e.g. /mylist/#/{id}).
func mylistIDFromNicoPath(path, fragment string) (string, error) {
	parts := splitPath(path)
	if (len(parts) == 2 && parts[0] == "my" && parts[1] == "mylist") ||
		(len(parts) == 1 && parts[0] == "mylist") {
		if isDigits(fragment) {
			return fragment, nil
		}
		return "", errMylistInput
	}
	if len(parts) == 4 && parts[0] == "user" && isDigits(parts[1]) && parts[2] == "mylist" && isDigits(parts[3]) {
		return parts[3], nil
	}
	return mylistIDFromSegments(path, "mylist")
}

func mylistIDFromSegments(path, first string) (string, error) {
	parts := splitPath(path)
	if len(parts) == 2 && parts[0] == first && isDigits(parts[1]) {
		return parts[1], nil
	}
	return "", errMylistInput
}

func splitPath(path string) []string {
	return strings.Split(strings.Trim(path, "/"), "/")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
