package main

import "github.com/egoist/mygo"

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
	var states map[string]mygo.WindowState
	ok, err := readJSONFile(path, maxWindowStateBody, &states, "ウィンドウの状態を読み込めませんでした", "ウィンドウの状態の記録が壊れています")
	if !ok || err != nil {
		return nil, err
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
	return writeJSONFile(path, states, "ウィンドウの状態を書き込めませんでした")
}
