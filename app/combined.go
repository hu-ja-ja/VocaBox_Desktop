package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// combinedIDPrefix names the combined lists made on this PC. Their IDs never
// reach niconico: a combined list is an index line over saved mylists.
const combinedIDPrefix = "combine-"

// combinedID is the ID a new combined list gets: unique per creation, so the
// player treats it as a mylist of its own rather than the one already playing.
func combinedID() string {
	return combinedIDPrefix + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func isCombinedID(id string) bool { return strings.HasPrefix(id, combinedIDPrefix) }

// IsCombined reports whether the entry is a combined list: one playing the
// saved mylists it names, with no body of its own.
func (e SavedIndexEntry) IsCombined() bool { return len(e.Members) > 0 }

// CreateCombined saves a combined list of the saved mylists members names, in
// that order, and returns the index with it. Every member must have its body
// saved: a combined list holds whole mylists, so one still being saved would
// enter only partly.
func (m *Mylist) CreateCombined(name string, members []string) ([]SavedIndexEntry, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("見出しを入力してください")
	}
	if len(members) == 0 {
		return nil, errors.New("結合するマイリストを選んでください")
	}
	index, err := m.ListSaved()
	if err != nil {
		return nil, err
	}
	for _, id := range members {
		at, ok := indexAt(index, id)
		if !ok || index[at].IsCombined() {
			return nil, errors.New("結合できないマイリストが選ばれています")
		}
		if !m.HasSavedBody(id) {
			return nil, fmt.Errorf("「%s」は保存がまだなので結合できません", pageDisplayName(id, index[at].Name))
		}
	}
	entry := SavedIndexEntry{ID: combinedID(), Name: name, Members: append([]string(nil), members...)}
	// shortcut: the members' bodies are read here, on the UI thread; a
	// library of many large mylists would want this in the background.
	items := m.CombinedItems(entry)
	entry.TotalItemCount, entry.Sample = len(items), sampleItems(items)
	m.savedMu.Lock()
	defer m.savedMu.Unlock()
	index, err = m.readIndex()
	if err != nil {
		return nil, err
	}
	index = upsertIndex(index, entry, nil)
	if err := m.writeIndex(index); err != nil {
		return nil, err
	}
	return index, nil
}

// CombinedItems returns the tracks a combined list plays: its members' saved
// bodies merged in member order, de-duplicated by watch ID so a track held by
// two members plays once. A member whose saved copy is gone contributes
// nothing.
func (m *Mylist) CombinedItems(entry SavedIndexEntry) []MylistItem {
	seen := map[string]bool{}
	items := []MylistItem{}
	for _, id := range entry.Members {
		body, err := m.LoadSavedBody(id)
		if err != nil {
			continue
		}
		for _, item := range body.Items {
			if item.WatchID == "" || seen[item.WatchID] {
				continue
			}
			seen[item.WatchID] = true
			items = append(items, item)
		}
	}
	return items
}
