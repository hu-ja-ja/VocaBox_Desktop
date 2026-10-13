package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// maxWindowStateBody bounds the window-state file.
const maxWindowStateBody = 64 << 10

// windowStateStore keeps the window's position, size and state in VocaBox's
// config directory, beside settings.json, instead of mygo's default
// window-state.json in the user data directory. It is the store of
// mygo.App.SetWindowStateStore.
type windowStateStore struct {
	mylist *Mylist
}

// path is the file the store keeps its states in.
func (s windowStateStore) path() (string, error) {
	return s.mylist.configPath("window-state.json")
}

// LoadWindowStates returns the saved states, or none when no file exists
// yet, so the window gets the options it was created with.
func (s windowStateStore) LoadWindowStates() (map[string]mygo.WindowState, error) {
	path, err := s.path()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxWindowStateBody+1))
	if err != nil || len(body) > maxWindowStateBody {
		return nil, errors.New("ウィンドウの状態を読み込めませんでした")
	}
	var states map[string]mygo.WindowState
	if err := json.Unmarshal(body, &states); err != nil {
		return nil, errors.New("ウィンドウの状態の記録が壊れています")
	}
	return states, nil
}

// SaveWindowStates writes the states through a temp file and rename, so a
// crash mid-write never leaves a torn file behind.
func (s windowStateStore) SaveWindowStates(states map[string]mygo.WindowState) error {
	path, err := s.path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errors.New("保存先フォルダーを作成できませんでした")
	}
	body, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return errors.New("ウィンドウの状態を書き込めませんでした")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return errors.New("ウィンドウの状態を書き込めませんでした")
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return errors.New("ウィンドウの状態を書き込めませんでした")
	}
	return nil
}
