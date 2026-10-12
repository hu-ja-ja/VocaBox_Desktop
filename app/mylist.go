package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// nvapiBaseURL is the only host this app fetches mylist metadata from.
	nvapiBaseURL = "https://nvapi.nicovideo.jp/v2"
	// thumbnailCDNHost is the host this app derives picture URLs from.
	thumbnailCDNHost = "nicovideo.cdn.nimg.jp"
	// thumbnailImageDomain is nicovideo's image domain: the only domain
	// pictures are ever fetched from, however the API names their host.
	thumbnailImageDomain = "nimg.jp"
	// maxThumbBody bounds a thumbnail image response and the disk cache file.
	maxThumbBody = 1 << 20
	// Thumbnail fetches are spaced and bounded: at most thumbConcurrency run
	// at once, each started at least thumbInterval after the last, and a 429
	// pauses all of them for thumbCooldown.
	thumbConcurrency = 2
	thumbInterval    = 100 * time.Millisecond
	thumbCooldown    = 30 * time.Second
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

// MylistItem is one entry of a public mylist. Video metadata can be absent
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

// MylistPage is one page of public mylist metadata.
type MylistPage struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Description    string       `json:"description"`
	Owner          string       `json:"owner"`
	TotalItemCount int          `json:"totalItemCount"`
	HasNext        bool         `json:"hasNext"`
	Items          []MylistItem `json:"items"`
}

// Mylist looks up public NicoNico mylist metadata. This stage covers public
// metadata lookup plus locally held snapshots of saved public mylists:
// no auth, playback, or download. The lookup uses the community-documented
// unofficial NVAPI endpoint, with explicit user approval for public metadata.
type Mylist struct {
	client  *http.Client
	baseURL string
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
	// savedMu serializes saved-list file operations on this instance.
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

// GetPage returns page (1-based) of the public mylist identified by input,
// which is a numeric ID or a supported mylist URL.
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

// SavedMylist is one persisted public-mylist snapshot: ID, display name, and
// the mylist's contents as fetched when saved or last refreshed. This is the
// local source of truth for reading a saved mylist back. Tracks, credentials,
// and media are never persisted.
type SavedMylist struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Owner          string       `json:"owner,omitempty"`
	Description    string       `json:"description,omitempty"`
	TotalItemCount int          `json:"totalItemCount,omitempty"`
	Items          []MylistItem `json:"items,omitempty"`
}

// maxSavedBody bounds the saved-list file.
const maxSavedBody = 1 << 20

// maxSettingsBody bounds the settings file.
const maxSettingsBody = 64 << 10

// Settings holds the preferences kept across runs: the player's volume,
// shuffle and repeat. A missing file, or a field a file does not carry,
// leaves the default.
type Settings struct {
	Volume  float64 `json:"volume"`
	Shuffle bool    `json:"shuffle"`
	Repeat  bool    `json:"repeat"`
}

// defaultSettings is what a missing or reset settings file yields.
func defaultSettings() Settings { return Settings{Volume: 1} }

func (m *Mylist) settingsPath() (string, error) {
	return m.configPath("settings.json")
}

// LoadSettings returns the stored preferences, or the defaults when none are
// saved yet. Unmarshalling onto the defaults keeps fields an older file does
// not carry.
func (m *Mylist) LoadSettings() (Settings, error) {
	path, err := m.settingsPath()
	if err != nil {
		return Settings{}, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return defaultSettings(), nil
	}
	if err != nil {
		return Settings{}, errors.New("設定を読み込めませんでした")
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxSettingsBody+1))
	if err != nil || len(body) > maxSettingsBody {
		return Settings{}, errors.New("設定を読み込めませんでした")
	}
	settings := defaultSettings()
	if err := json.Unmarshal(body, &settings); err != nil {
		return Settings{}, errors.New("設定の記録が壊れています")
	}
	return settings, nil
}

// SaveSettings writes the preferences, creating the config directory as
// needed.
func (m *Mylist) SaveSettings(settings Settings) error {
	path, err := m.settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errors.New("保存先フォルダーを作成できませんでした")
	}
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return errors.New("設定を書き込めませんでした")
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return errors.New("設定を書き込めませんでした")
	}
	return nil
}

// ListSaved returns saved public-mylist snapshots, oldest first. A missing
// file means none saved yet.
func (m *Mylist) ListSaved() ([]SavedMylist, error) {
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	return m.readSaved()
}

// SaveMylist fetches every page of the public mylist identified by input and
// upserts a local snapshot of it under name, returning the saved snapshots.
// Input accepts an ID or a supported URL. Fetching happens before the file
// lock, so HTTP never blocks other file operations.
func (m *Mylist) SaveMylist(input, name string) ([]SavedMylist, error) {
	id, err := ParseMylistInput(input)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("保存するマイリストの名前がありません")
	}
	snapshot, err := m.fetchAll(id)
	if err != nil {
		return nil, err
	}
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	return m.upsertSaved(SavedMylist{
		ID:             id,
		Name:           name,
		Owner:          snapshot.Owner,
		Description:    snapshot.Description,
		TotalItemCount: snapshot.TotalItemCount,
		Items:          snapshot.Items,
	})
}

// upsertSaved replaces or appends entry and persists the list. Callers hold
// savedMu.
func (m *Mylist) upsertSaved(entry SavedMylist) ([]SavedMylist, error) {
	saved, err := m.readSaved()
	if err != nil {
		return nil, err
	}
	found := false
	for i, s := range saved {
		if s.ID == entry.ID {
			saved[i] = entry
			found = true
		}
	}
	if !found {
		saved = append(saved, entry)
	}
	if err := m.writeSaved(saved); err != nil {
		return nil, err
	}
	return saved, nil
}

// RemoveSaved drops the local snapshot of the mylist identified by input.
// Server data is never touched.
func (m *Mylist) RemoveSaved(input string) ([]SavedMylist, error) {
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	id, err := ParseMylistInput(input)
	if err != nil {
		return nil, err
	}
	saved, err := m.readSaved()
	if err != nil {
		return nil, err
	}
	kept := saved[:0]
	for _, s := range saved {
		if s.ID != id {
			kept = append(kept, s)
		}
	}
	if err := m.writeSaved(kept); err != nil {
		return nil, err
	}
	return kept, nil
}

func (m *Mylist) savedPath() (string, error) {
	return m.configPath("mylists.json")
}

// configPath returns the path of name inside the app's config directory.
func (m *Mylist) configPath(name string) (string, error) {
	dir := m.configDir
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", errors.New("保存先フォルダーを取得できませんでした")
		}
		dir = filepath.Join(base, "VocaBox")
	}
	return filepath.Join(dir, name), nil
}

// Thumbnail returns the picture of the video at watchID: the disk cache
// when it has one, the picture at thumbURL fetched and cached otherwise.
// Empty thumbURL comes from entries saved before pictures were kept, where
// the CDN's own URL shape is derived from the watch ID. Pictures are only
// ever fetched from nicovideo's image domain.
func (m *Mylist) Thumbnail(watchID, thumbURL string) ([]byte, error) {
	cache, cacheOK := m.thumbCachePath(watchID)
	if cacheOK {
		if body, ok := readThumbFile(cache); ok {
			return body, nil
		}
	}
	var lastErr error
	for _, u := range m.thumbnailCandidates(watchID, thumbURL) {
		body, err := m.fetchThumbnail(u)
		if err == nil {
			if cacheOK {
				writeThumbFile(cache, body)
			}
			return body, nil
		}
		lastErr = err
		if !errors.Is(err, errThumbMissing) {
			// Only a picture the CDN genuinely does not have moves on to the
			// smaller sibling. A temporary failure, or a 429 that paused the
			// fetches, must leave no lesser picture cached in its place.
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("サムネイルがありません")
	}
	return nil, lastErr
}

// errThumbPaused reports the CDN rate limiting: the limiter has paused every
// fetch, and the caller should stop rather than try another URL.
var errThumbPaused = errors.New("ニコニコが混み合っています。しばらくしてからもう一度お試しください")

// errThumbMissing reports the CDN has no picture at a URL, as an older video
// has no .M size: the caller may move on to the URL's smaller sibling.
var errThumbMissing = errors.New("サムネイルが見つかりません")

// thumbnailLimiter bounds how fast pictures are fetched: at most the slot
// count run at once, each start at least interval after the last, and a
// cooldown after the CDN asked to slow down.
type thumbnailLimiter struct {
	slots    chan struct{}
	interval time.Duration
	cooldown time.Duration
	mu       sync.Mutex
	next     time.Time
	paused   time.Time
}

func newThumbnailLimiter(slots int, interval, cooldown time.Duration) *thumbnailLimiter {
	return &thumbnailLimiter{
		slots:    make(chan struct{}, slots),
		interval: interval,
		cooldown: cooldown,
	}
}

// acquire takes a slot and waits until this fetch's turn. release returns
// the slot once the fetch is done.
func (l *thumbnailLimiter) acquire() {
	l.slots <- struct{}{}
	l.mu.Lock()
	now := time.Now()
	start := l.next
	if start.Before(now) {
		start = now
	}
	if l.paused.After(start) {
		start = l.paused
	}
	l.next = start.Add(l.interval)
	l.mu.Unlock()
	if wait := time.Until(start); wait > 0 {
		time.Sleep(wait)
	}
}

func (l *thumbnailLimiter) release() { <-l.slots }

// pausedFor stops every fetch for d, as after a 429. A pause only ever
// lengthens.
func (l *thumbnailLimiter) pausedFor(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if until := time.Now().Add(d); until.After(l.paused) {
		l.paused = until
	}
}

// HasThumbnail reports whether the picture of watchID is already in the
// disk cache.
func (m *Mylist) HasThumbnail(watchID string) bool {
	path, ok := m.thumbCachePath(watchID)
	if !ok {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// thumbCachePath returns the disk cache path of watchID's picture, or
// ok=false for an ID of an unexpected shape: cache file names come from
// IDs, so only the simple shape CDN IDs have becomes a path.
func (m *Mylist) thumbCachePath(watchID string) (path string, ok bool) {
	if !isSafeID(watchID) {
		return "", false
	}
	path, err := m.configPath(filepath.Join("thumbs", watchID+".jpg"))
	if err != nil {
		return "", false
	}
	return path, true
}

func readThumbFile(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxThumbBody+1))
	if err != nil || len(body) == 0 || len(body) > maxThumbBody {
		return nil, false
	}
	return body, true
}

// writeThumbFile stores a picture through a temp file and rename, so a
// crash mid-write never leaves a torn image behind. Caching is best effort:
// a write that fails leaves the picture uncached, not the row empty.
func writeThumbFile(path string, body []byte) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, "thumb-*.tmp")
	if err != nil {
		return
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
	}
}

// thumbnailCandidates returns the URLs to try for one video: the .M picture
// before the small one the API's URL names. The CDN asks for sizes by
// suffix, but its picture URLs are either extension-less or carry a version
// hash (…/{number}.{hash}), so the path decides: everything under the CDN's
// /thumbnails/ takes .M, and a URL of any other shape is used as it is (the
// deleted-video placeholder, for one).
func (m *Mylist) thumbnailCandidates(watchID, thumbURL string) []string {
	base := thumbURL
	if base == "" {
		if num := videoNumber(watchID); num != "" {
			base = "https://" + m.thumbHostName() + "/thumbnails/" + num + "/" + num
		}
	}
	if base == "" {
		return nil
	}
	if !m.isThumbnailCDNURL(base) {
		return []string{base}
	}
	return []string{base + ".M", base}
}

// isThumbnailCDNURL reports whether rawURL is one of the thumbnail CDN's
// /thumbnails/ pictures, whose .M and .L sizes are asked for by suffix.
func (m *Mylist) isThumbnailCDNURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host != m.thumbHostName() {
		return false
	}
	return strings.HasPrefix(u.Path, "/thumbnails/")
}

// thumbHostName is the host derived picture URLs are built from: the CDN,
// or the test server when a test set the field.
func (m *Mylist) thumbHostName() string {
	if m.thumbHost != "" {
		return m.thumbHost
	}
	return thumbnailCDNHost
}

// thumbHostAllowed reports whether a picture URL's host may be fetched:
// nicovideo's image domain, whose subdomains the API uses for pictures and
// for the deleted-video placeholder, or the test server when tests set the
// field. Every other host is never contacted.
func (m *Mylist) thumbHostAllowed(host string) bool {
	host = strings.ToLower(host)
	if m.thumbHost != "" {
		return host == strings.ToLower(m.thumbHost)
	}
	return host == thumbnailImageDomain || strings.HasSuffix(host, "."+thumbnailImageDomain)
}

func (m *Mylist) fetchThumbnail(rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !m.thumbHostAllowed(u.Host) {
		return nil, errors.New("サムネイルの取得先が不正です")
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("サムネイルの取得に失敗しました")
	}
	req.Header.Set("User-Agent", mylistUserAgent)
	if m.thumbLimiter != nil {
		m.thumbLimiter.acquire()
		defer m.thumbLimiter.release()
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, errors.New("サムネイルの取得に失敗しました（通信エラー）")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		if m.thumbLimiter != nil {
			m.thumbLimiter.pausedFor(m.thumbLimiter.cooldown)
		}
		return nil, errThumbPaused
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, errThumbMissing
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("サムネイルの取得に失敗しました（HTTP %d）", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		return nil, errors.New("サムネイルの応答が画像ではありません")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxThumbBody+1))
	if err != nil || len(body) == 0 {
		return nil, errors.New("サムネイルの取得に失敗しました")
	}
	if len(body) > maxThumbBody {
		return nil, errors.New("サムネイルの応答が大きすぎます")
	}
	return body, nil
}

// videoNumber returns the numeric part of a video ID like sm123 or nm456,
// which the CDN's thumbnail paths use; "" when the ID has no number.
func videoNumber(id string) string {
	i := 0
	for i < len(id) && (id[i] < '0' || id[i] > '9') {
		i++
	}
	j := i
	for j < len(id) && id[j] >= '0' && id[j] <= '9' {
		j++
	}
	if i == j {
		return ""
	}
	return id[i:j]
}

// isSafeID reports whether id has only letters, digits, hyphens and
// underscores: the shape CDN video IDs have, safe as a file name.
func isSafeID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func (m *Mylist) readSaved() ([]SavedMylist, error) {
	path, err := m.savedPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return []SavedMylist{}, nil
	}
	if err != nil {
		return nil, errors.New("保存したマイリストを読み込めませんでした")
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxSavedBody+1))
	if err != nil {
		return nil, errors.New("保存したマイリストを読み込めませんでした")
	}
	if len(body) > maxSavedBody {
		return nil, errors.New("保存したマイリストの記録が大きすぎます")
	}
	var saved []SavedMylist
	if err := json.Unmarshal(body, &saved); err != nil {
		return nil, errors.New("保存したマイリストの記録が壊れています")
	}
	if saved == nil {
		saved = []SavedMylist{}
	}
	return saved, nil
}

func (m *Mylist) writeSaved(saved []SavedMylist) error {
	path, err := m.savedPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errors.New("保存先フォルダーを作成できませんでした")
	}
	body, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return errors.New("保存したマイリストを書き込めませんでした")
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return errors.New("保存したマイリストを書き込めませんでした")
	}
	return nil
}

func (m *Mylist) fetchPage(id string, page int) (MylistPage, error) {
	resp, err := m.mylistRequest(id, page)
	if err != nil {
		return MylistPage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return MylistPage{}, mylistHTTPError(resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMylistBody+1))
	if err != nil {
		return MylistPage{}, errors.New("マイリストの取得に失敗しました")
	}
	if len(body) > maxMylistBody {
		return MylistPage{}, errors.New("マイリストの応答が大きすぎます")
	}
	return mapMylistPage(id, body)
}

// mylistRequest sends one page request, waiting out one Retry-After when the
// API asks to slow down. A second refusal returns, so a busy API does not
// hold the caller any longer.
func (m *Mylist) mylistRequest(id string, page int) (*http.Response, error) {
	rawURL := fmt.Sprintf("%s/mylists/%s?pageSize=%d&page=%d", m.baseURL, id, mylistPageSize, page)
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, errors.New("マイリストの取得に失敗しました")
		}
		req.Header.Set("X-Frontend-ID", "6")
		req.Header.Set("X-Frontend-Version", "0")
		req.Header.Set("User-Agent", mylistUserAgent)
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

func mylistHTTPError(status int) error {
	if status == http.StatusNotFound {
		return errors.New("マイリストが見つかりません（公開マイリストのIDまたはURLを入力してください）")
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return errors.New("マイリストを表示できません（非公開または利用できない可能性があります。公開マイリストのIDまたはURLを入力してください）")
	}
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		return errors.New("ニコニコが混み合っています。しばらくしてからもう一度お試しください")
	}
	return fmt.Errorf("マイリストの取得に失敗しました（HTTP %d）", status)
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
	if !ml.IsPublic {
		return MylistPage{}, errors.New("非公開マイリストには対応していません")
	}
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
		out.Items = append(out.Items, item)
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
		return "", errors.New("対応していないURLです（nicovideo.jp / nico.ms の公開マイリストのみ）")
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
