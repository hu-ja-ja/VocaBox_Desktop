package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SavedMylist is one persisted mylist snapshot — a pinned mylist on the home:
// ID, display name, and the mylist's contents as fetched when pinned or last
// refreshed. This is the local source of truth for reading it back. Tracks,
// credentials, and media are never persisted.
type SavedMylist struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Owner          string       `json:"owner,omitempty"`
	Description    string       `json:"description,omitempty"`
	TotalItemCount int          `json:"totalItemCount,omitempty"`
	Items          []MylistItem `json:"items,omitempty"`
}

// maxSavedBody bounds the saved mylists file.
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
	return writeJSONFile(path, settings, "設定を書き込めませんでした")
}

// ListSaved returns saved mylist snapshots, oldest first. A missing
// file means none saved yet.
func (m *Mylist) ListSaved() ([]SavedMylist, error) {
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	return m.readSaved()
}

// SaveMylist fetches every page of the mylist identified by input and
// upserts a local snapshot of it under name, returning the saved snapshots.
// Input accepts an ID or a supported URL; the account's own private mylists
// refresh while signed in. Fetching happens before the file
// lock, so HTTP never blocks other file operations.
func (m *Mylist) SaveMylist(input, name string) ([]SavedMylist, error) {
	id, err := ParseMylistInput(input)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("ピン留めするマイリストの名前がありません")
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

// upsertSaved replaces or appends entry and persists the mylists. Callers hold
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

var errFileTooLarge = errors.New("file too large")

func (m *Mylist) readSaved() ([]SavedMylist, error) {
	path, err := m.savedPath()
	if err != nil {
		return nil, err
	}
	var saved []SavedMylist
	if _, err := readJSONFile(path, maxSavedBody, &saved, "ピン留めしたマイリストを読み込めませんでした", "ピン留めしたマイリストの記録が壊れています"); err != nil {
		if errors.Is(err, errFileTooLarge) {
			return nil, errors.New("ピン留めしたマイリストの記録が大きすぎます")
		}
		return nil, err
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
	return writeJSONFile(path, saved, "ピン留めしたマイリストを書き込めませんでした")
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

// writeJSONFile marshals src and writes it through a temp file and rename.
func writeJSONFile(path string, src any, writeErr string) error {
	body, err := json.MarshalIndent(src, "", "  ")
	if err != nil {
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
