package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// SavedMylist is one saved mylist body: the mylist's ID, display name,
// header fields, and contents as fetched when last saved or refreshed. It is
// one file per mylist under the mylists directory, read when its mylist
// opens rather than at startup. Tracks, credentials, and media are never
// persisted.
type SavedMylist struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Owner          string       `json:"owner,omitempty"`
	Description    string       `json:"description,omitempty"`
	TotalItemCount int          `json:"totalItemCount,omitempty"`
	Items          []MylistItem `json:"items,omitempty"`
}

// SavedIndexEntry is one mylist's line in the saved index: everything the
// home and the sidebar draw its card from — including the leading entries
// for the card's pictures — so listing the saved mylists reads no body file.
type SavedIndexEntry struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Owner          string `json:"owner,omitempty"`
	TotalItemCount int    `json:"totalItemCount,omitempty"`
	// Pinned is the mylist pinned to the top of the home.
	Pinned bool `json:"pinned,omitempty"`
	// Mine is the entry being one of the signed-in account's own mylists;
	// Private, that only its owner sees it.
	Mine    bool `json:"mine,omitempty"`
	Private bool `json:"private,omitempty"`
	// Members names the saved mylists a combined list plays, in order. A
	// combined list is made on this PC and has no body of its own: its
	// tracks come from its members' saved copies. Empty for a mylist read
	// from niconico.
	Members []string `json:"members,omitempty"`
	// Sample is the mylist's leading entries, for a card's pictures.
	Sample []MylistItem `json:"sample,omitempty"`
}

// Bounds on the saved files. The index is read on every start and stays
// small; a body is read when its mylist opens. Both bounds apply to writing
// too, so a file the reader would reject is never written.
const (
	maxIndexBody = 1 << 20
	maxListBody  = 16 << 20
)

// maxSettingsBody bounds the settings file.
const maxSettingsBody = 64 << 10

// cardSampleCount is how many leading entries a card's 2×2 pictures need.
const cardSampleCount = 4

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
	settings := defaultSettings()
	if _, err := readJSONFile(path, maxSettingsBody, &settings, "設定を読み込めませんでした", "設定の記録が壊れています"); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

// SaveSettings writes the preferences atomically, creating the config
// directory as needed.
func (m *Mylist) SaveSettings(settings Settings) error {
	path, err := m.settingsPath()
	if err != nil {
		return err
	}
	return writeJSONFile(path, settings, maxSettingsBody, "設定を書き込めませんでした")
}

// ListSaved returns the saved mylists index, oldest first. A missing index
// means none saved yet. Only the index is read: the bodies stay on disk
// until their mylist opens.
func (m *Mylist) ListSaved() ([]SavedIndexEntry, error) {
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	return m.readIndex()
}

// LoadSavedBody returns the saved body of the mylist id names, for opening
// the mylist without the network. A mylist not saved yet reports
// os.ErrNotExist.
func (m *Mylist) LoadSavedBody(id string) (SavedMylist, error) {
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	return m.readBody(id)
}

// HasSavedBody reports whether the mylist has a body saved.
func (m *Mylist) HasSavedBody(id string) bool {
	path, err := m.bodyPath(id)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// SaveMylist fetches every page of the mylist identified by input, saves its
// body, and pins the mylist on the home. Input accepts an ID or a supported
// URL; the account's own private mylists refresh while signed in. Fetching
// happens before the file lock, so HTTP never blocks other file operations.
func (m *Mylist) SaveMylist(input string) ([]SavedIndexEntry, error) {
	id, err := ParseMylistInput(input)
	if err != nil {
		return nil, err
	}
	snapshot, err := m.fetchAll(id)
	if err != nil {
		return nil, err
	}
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	pinned := true
	return m.storeBody(snapshot, &pinned)
}

// RefreshMylist fetches every page of the mylist id names and replaces its
// saved body, keeping the pin. It is what the refresh button and the
// background saving of the account's mylists use.
func (m *Mylist) RefreshMylist(id string) ([]SavedIndexEntry, error) {
	if !isDigits(id) {
		return nil, errMylistInput
	}
	snapshot, err := m.fetchAll(id)
	if err != nil {
		return nil, err
	}
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	return m.storeBody(snapshot, nil)
}

// TrackMylist records a loaded mylist in the index — live name, size, and
// sample — so an opened mylist shows on the home before its body is saved.
// The pin and own flags are kept; a mylist not in the index is added.
func (m *Mylist) TrackMylist(page MylistPage) ([]SavedIndexEntry, error) {
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	index, err := m.readIndex()
	if err != nil {
		return nil, err
	}
	index = upsertIndex(index, SavedIndexEntry{
		ID:             page.ID,
		Name:           pageDisplayName(page.ID, page.Name),
		Owner:          page.Owner,
		TotalItemCount: page.TotalItemCount,
		Sample:         sampleItems(page.Items),
	}, nil)
	if err := m.writeIndex(index); err != nil {
		return nil, err
	}
	return index, nil
}

// SetPinned pins or unpins a saved mylist, writing only the index: the body
// stays where it is. A mylist just pinned joins the end of the pinned group.
// An unknown mylist changes nothing.
func (m *Mylist) SetPinned(id string, pinned bool) ([]SavedIndexEntry, error) {
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	index, err := m.readIndex()
	if err != nil {
		return nil, err
	}
	changed, at := false, -1
	for i := range index {
		if index[i].ID == id && index[i].Pinned != pinned {
			index[i].Pinned = pinned
			changed, at = true, i
		}
	}
	if changed {
		if pinned {
			index = moveToPinnedEnd(index, at)
		}
		if err := m.writeIndex(index); err != nil {
			return nil, err
		}
	}
	return index, nil
}

// moveToPinnedEnd moves the entry at at to just after the last other pinned
// entry, so a mylist just pinned joins the end of the pinned group rather than
// its front. With no other pin the entry stays where it is.
func moveToPinnedEnd(index []SavedIndexEntry, at int) []SavedIndexEntry {
	last := -1
	for i := range index {
		if i != at && index[i].Pinned {
			last = i
		}
	}
	if last < at {
		return index
	}
	entry := index[at]
	index = append(index[:at], index[at+1:]...)
	index = append(index, SavedIndexEntry{})
	copy(index[last+1:], index[last:])
	index[last] = entry
	return index
}

// RemoveSaved deletes a saved mylist: its index line and its body. Server
// data is never touched. A combined list is named by its own local ID rather
// than a niconico one, so it is removed by ID.
func (m *Mylist) RemoveSaved(input string) ([]SavedIndexEntry, error) {
	id, err := ParseMylistInput(input)
	if err != nil {
		if !isCombinedID(input) {
			return nil, err
		}
		id = input
	}
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	index, err := m.readIndex()
	if err != nil {
		return nil, err
	}
	kept := index[:0]
	for _, entry := range index {
		if entry.ID != id {
			kept = append(kept, entry)
		}
	}
	if len(kept) != len(index) {
		if err := m.writeIndex(kept); err != nil {
			return nil, err
		}
	}
	m.dropBody(id)
	return kept, nil
}

// SyncOwn merges the signed-in account's mylists into the index: each gets
// its live name, size, privacy, and sample. An own mylist the account no
// longer has is dropped — body and all — unless it is pinned, the user's own
// request to keep it.
func (m *Mylist) SyncOwn(own []OwnMylist) ([]SavedIndexEntry, error) {
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	index, err := m.readIndex()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]OwnMylist, len(own))
	for _, o := range own {
		byID[o.ID] = o
	}
	kept := index[:0]
	for _, entry := range index {
		if o, ok := byID[entry.ID]; ok {
			entry.Name = pageDisplayName(o.ID, o.Name)
			entry.TotalItemCount = o.ItemCount
			entry.Sample = sampleItems(o.Items)
			entry.Mine, entry.Private = true, !o.IsPublic
		} else if entry.Mine && !entry.Pinned {
			m.dropBody(entry.ID)
			continue
		}
		kept = append(kept, entry)
	}
	for _, o := range own {
		if _, ok := indexAt(kept, o.ID); !ok {
			kept = append(kept, SavedIndexEntry{
				ID:             o.ID,
				Name:           pageDisplayName(o.ID, o.Name),
				TotalItemCount: o.ItemCount,
				Mine:           true,
				Private:        !o.IsPublic,
				Sample:         sampleItems(o.Items),
			})
		}
	}
	if err := m.writeIndex(kept); err != nil {
		return nil, err
	}
	return kept, nil
}

// storeBody writes the fetched snapshot as the mylist's body and refreshes
// its index line, keeping the mine and private flags and the pin, which pin
// overrides when it is not nil. Callers hold savedMu.
func (m *Mylist) storeBody(snapshot MylistPage, pin *bool) ([]SavedIndexEntry, error) {
	body := SavedMylist{
		ID:             snapshot.ID,
		Name:           pageDisplayName(snapshot.ID, snapshot.Name),
		Owner:          snapshot.Owner,
		Description:    snapshot.Description,
		TotalItemCount: snapshot.TotalItemCount,
		Items:          snapshot.Items,
	}
	if err := m.writeBody(body); err != nil {
		return nil, err
	}
	index, err := m.readIndex()
	if err != nil {
		return nil, err
	}
	index = upsertIndex(index, SavedIndexEntry{
		ID:             body.ID,
		Name:           body.Name,
		Owner:          body.Owner,
		TotalItemCount: body.TotalItemCount,
		Sample:         sampleItems(body.Items),
	}, pin)
	if err := m.writeIndex(index); err != nil {
		return nil, err
	}
	return index, nil
}

// upsertIndex replaces id's index line with entry's fields, keeping the pin,
// mine, and private flags of an existing line — the pin overridden when pin
// is not nil — or appending a new line. A pin turning on moves the line to
// the end of the pinned group.
func upsertIndex(index []SavedIndexEntry, entry SavedIndexEntry, pin *bool) []SavedIndexEntry {
	at, found := indexAt(index, entry.ID)
	if !found {
		index = append(index, entry)
		at = len(index) - 1
	} else {
		entry.Pinned = index[at].Pinned
		entry.Mine = index[at].Mine
		entry.Private = index[at].Private
		index[at] = entry
	}
	if pin != nil {
		was := index[at].Pinned
		index[at].Pinned = *pin
		if *pin && !was {
			index = moveToPinnedEnd(index, at)
		}
	}
	return index
}

// indexAt is the index line of the mylist id names.
func indexAt(index []SavedIndexEntry, id string) (int, bool) {
	for i := range index {
		if index[i].ID == id {
			return i, true
		}
	}
	return -1, false
}

// dropBody deletes a mylist's body file, if one is saved.
func (m *Mylist) dropBody(id string) {
	path, err := m.bodyPath(id)
	if err != nil {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("mylists: removing %s: %v", path, err)
	}
}

// pageDisplayName is the name to show a mylist under: a mylist without one
// is shown by its ID rather than as a blank.
func pageDisplayName(id, name string) string {
	if name == "" {
		return id
	}
	return name
}

// sampleItems copies the leading entries a card's pictures need; copying
// keeps an index line from holding a whole body's backing array alive.
func sampleItems(items []MylistItem) []MylistItem {
	n := min(len(items), cardSampleCount)
	if n == 0 {
		return nil
	}
	out := make([]MylistItem, n)
	copy(out, items[:n])
	return out
}

// mylistsDir is the directory holding the saved index and the bodies.
func (m *Mylist) mylistsDir() (string, error) {
	return m.configPath("mylists")
}

func (m *Mylist) indexPath() (string, error) {
	dir, err := m.mylistsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "index.json"), nil
}

// bodyPath is where the mylist id names is saved. Only numeric IDs name
// files, so a hand-edited index can never point outside the directory.
func (m *Mylist) bodyPath(id string) (string, error) {
	if !isDigits(id) {
		return "", errMylistInput
	}
	dir, err := m.mylistsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".json"), nil
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

var errFileTooLarge = errors.New("file too large")

func (m *Mylist) readIndex() ([]SavedIndexEntry, error) {
	path, err := m.indexPath()
	if err != nil {
		return nil, err
	}
	var index []SavedIndexEntry
	if _, err := readJSONFile(path, maxIndexBody, &index, "保存したマイリストを読み込めませんでした", "保存したマイリストの記録が壊れています"); err != nil {
		return nil, err
	}
	if index == nil {
		index = []SavedIndexEntry{}
	}
	return index, nil
}

func (m *Mylist) writeIndex(index []SavedIndexEntry) error {
	path, err := m.indexPath()
	if err != nil {
		return err
	}
	return writeJSONFile(path, index, maxIndexBody, "保存したマイリストを書き込めませんでした")
}

func (m *Mylist) readBody(id string) (SavedMylist, error) {
	path, err := m.bodyPath(id)
	if err != nil {
		return SavedMylist{}, err
	}
	var body SavedMylist
	present, err := readJSONFile(path, maxListBody, &body, "保存したマイリストを読み込めませんでした", "保存したマイリストの記録が壊れています")
	if err != nil {
		return SavedMylist{}, err
	}
	if !present {
		return SavedMylist{}, os.ErrNotExist
	}
	return body, nil
}

func (m *Mylist) writeBody(body SavedMylist) error {
	path, err := m.bodyPath(body.ID)
	if err != nil {
		return err
	}
	return writeJSONFileCompact(path, body, maxListBody, "マイリストを書き込めませんでした")
}

// readBoundedFile reads at most maxBytes from path; a missing file returns
// os.ErrNotExist.
func readBoundedFile(path string, maxBytes int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBytes {
		return nil, errFileTooLarge
	}
	return body, nil
}

// readJSONFile unmarshals a bounded JSON file into dst. A missing file
// reports present=false and nil error.
func readJSONFile(path string, maxBytes int, dst any, readErr, parseErr string) (bool, error) {
	body, err := readBoundedFile(path, maxBytes)
	if os.IsNotExist(err) {
		return false, nil
	}
	if errors.Is(err, errFileTooLarge) {
		return false, fmt.Errorf("%s: %w", readErr, errFileTooLarge)
	}
	if err != nil {
		return false, errors.New(readErr)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return false, errors.New(parseErr)
	}
	return true, nil
}

// writeJSONFile marshals src as indented JSON and writes it through a temp
// file and rename, refusing a body over maxBytes so a file the reader would
// reject is never written.
func writeJSONFile(path string, src any, maxBytes int, writeErr string) error {
	body, err := json.MarshalIndent(src, "", "  ")
	if err != nil || len(body) > maxBytes {
		return errors.New(writeErr)
	}
	return writeFileAtomic(path, body, 0o644, writeErr)
}

// writeJSONFileCompact is writeJSONFile without the indentation: a mylist
// body is read by the app, not by eye, and the compact form is what keeps a
// 1000-entry body around 300 KB.
func writeJSONFileCompact(path string, src any, maxBytes int, writeErr string) error {
	body, err := json.Marshal(src)
	if err != nil || len(body) > maxBytes {
		return errors.New(writeErr)
	}
	return writeFileAtomic(path, body, 0o644, writeErr)
}

// writeFileAtomic writes body to path through a sibling temp file and rename,
// so a crash mid-write never leaves a torn file behind.
func writeFileAtomic(path string, body []byte, perm os.FileMode, writeErr string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.New("保存先フォルダーを作成できませんでした")
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return errors.New(writeErr)
	}
	name := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(name)
		return errors.New(writeErr)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return errors.New(writeErr)
	}
	if perm != 0 {
		_ = os.Chmod(name, perm)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return errors.New(writeErr)
	}
	return nil
}
