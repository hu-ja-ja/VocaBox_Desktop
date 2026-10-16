package main

import (
	"testing"
)

// writeSaved records a mylist as an open or the background saving leaves it:
// its index line and its body.
func writeSaved(t *testing.T, m *Mylist, id, name string, items []MylistItem) {
	t.Helper()
	if _, err := m.TrackMylist(MylistPage{ID: id, Name: name, TotalItemCount: len(items), Items: items}); err != nil {
		t.Fatal(err)
	}
	if err := m.writeBody(SavedMylist{ID: id, Name: name, TotalItemCount: len(items), Items: items}); err != nil {
		t.Fatal(err)
	}
}

// TestCreateCombined saves two mylists sharing a track, combines them, and
// checks the merged tracks, the refusal of a part-saved mylist, and the
// removal by the combined list's own ID.
func TestCreateCombined(t *testing.T) {
	m := &Mylist{configDir: t.TempDir()}
	writeSaved(t, m, "111", "朝のリスト", []MylistItem{
		{WatchID: "sm1", Title: "朝1", Available: true, Duration: 120},
		{WatchID: "sm2", Title: "共有", Available: true, Duration: 120},
	})
	writeSaved(t, m, "222", "夜のリスト", []MylistItem{
		{WatchID: "sm2", Title: "共有", Available: true, Duration: 120},
		{WatchID: "sm3", Title: "夜3", Available: true, Duration: 120},
	})

	// A mylist whose body is not saved yet cannot join, and neither can a
	// combined list itself.
	if _, err := m.CreateCombined("だめ", []string{"111", "999"}); err == nil {
		t.Error("CreateCombined with a mylist still saving = nil error; want an error")
	}
	if _, err := m.CreateCombined("だめ", nil); err == nil {
		t.Error("CreateCombined with no member = nil error; want an error")
	}
	if _, err := m.CreateCombined("  ", []string{"111"}); err == nil {
		t.Error("CreateCombined without a heading = nil error; want an error")
	}

	got, err := m.CreateCombined("朝と夜", []string{"111", "222"})
	if err != nil {
		t.Fatalf("CreateCombined = %v; want nil", err)
	}
	if len(got) != 3 {
		t.Fatalf("index = %+v; want the two mylists and the combined one", got)
	}
	entry := got[2]
	if !entry.IsCombined() || entry.Name != "朝と夜" || len(entry.Members) != 2 || entry.Members[0] != "111" {
		t.Fatalf("combined entry = %+v; want the members in order under the heading", entry)
	}
	// The shared track is played once, and the card's sample carries the
	// merged tracks for its pictures.
	if entry.TotalItemCount != 3 || len(entry.Sample) != 3 {
		t.Fatalf("combined entry = %+v; want 3 tracks after de-duplication", entry)
	}
	items := m.CombinedItems(entry)
	if len(items) != 3 || items[0].WatchID != "sm1" || items[1].WatchID != "sm2" || items[2].WatchID != "sm3" {
		t.Fatalf("CombinedItems = %+v; want the members' tracks merged in order", items)
	}

	// A combined list cannot be combined again.
	if _, err := m.CreateCombined("入れ子", []string{entry.ID}); err == nil {
		t.Error("CreateCombined of a combined list = nil error; want an error")
	}

	// A member whose saved copy is gone contributes nothing.
	m.dropBody("111")
	if items := m.CombinedItems(entry); len(items) != 2 || items[0].WatchID != "sm2" {
		t.Fatalf("CombinedItems with a member's copy gone = %+v; want the rest", items)
	}
	writeSaved(t, m, "111", "朝のリスト", []MylistItem{
		{WatchID: "sm1", Title: "朝1", Available: true, Duration: 120},
		{WatchID: "sm2", Title: "共有", Available: true, Duration: 120},
	})

	// A fresh service over the same dir reads it, a sync of the account's
	// mylists keeps it, and it goes by its own local ID.
	same := &Mylist{configDir: m.configDir}
	kept, err := same.SyncOwn(nil)
	if err != nil || len(kept) != 3 || !kept[2].IsCombined() {
		t.Fatalf("SyncOwn = %+v, %v; want the combined list kept", kept, err)
	}
	kept, err = same.RemoveSaved(entry.ID)
	if err != nil || len(kept) != 2 {
		t.Fatalf("RemoveSaved(combined) = %+v, %v; want it removed", kept, err)
	}
	for _, e := range kept {
		if e.IsCombined() {
			t.Errorf("the combined list stays in the index: %+v", e)
		}
	}
}
