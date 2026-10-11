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
	"strings"
	"sync"
	"time"
)

const (
	// nvapiBaseURL is the only host this app fetches mylist metadata from.
	nvapiBaseURL = "https://nvapi.nicovideo.jp/v2"
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
	// savedMu serializes saved-list file operations on this instance.
	// HTTP fetches never take it.
	savedMu sync.Mutex
}

// NewMylist returns the bound Mylist service.
func NewMylist() *Mylist {
	return &Mylist{
		client:  &http.Client{Timeout: 15 * time.Second},
		baseURL: nvapiBaseURL,
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
	dir := m.configDir
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", errors.New("保存先フォルダーを取得できませんでした")
		}
		dir = filepath.Join(base, "VocaBox")
	}
	return filepath.Join(dir, "mylists.json"), nil
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
	req, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s/mylists/%s?pageSize=%d&page=%d", m.baseURL, id, mylistPageSize, page), nil)
	if err != nil {
		return MylistPage{}, errors.New("マイリストの取得に失敗しました")
	}
	req.Header.Set("X-Frontend-ID", "6")
	req.Header.Set("X-Frontend-Version", "0")
	req.Header.Set("User-Agent", mylistUserAgent)
	resp, err := m.client.Do(req)
	if err != nil {
		return MylistPage{}, errors.New("マイリストの取得に失敗しました（通信エラー）")
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

// fetchAll fetches every page of the mylist and returns one snapshot: header
// fields from page 1, all items merged, HasNext cleared. maxMylistPages bounds
// the loop.
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
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Duration int         `json:"duration"`
	Owner    *nvapiOwner `json:"owner"`
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
