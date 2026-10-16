package main

import (
	"slices"
	"testing"
)

func queueTracks(ids ...string) []MylistItem {
	items := make([]MylistItem, len(ids))
	for i, id := range ids {
		items[i] = MylistItem{WatchID: id}
	}
	return items
}

func queueIDs(items []MylistItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.WatchID
	}
	return ids
}

func sortedQueueIDs(ids []string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// TestPlayQueueBoundariesShuffleAppendRepeatReset ports the web player's
// queue tests: boundaries, shuffle both ways, appends, repeat and reset.
func TestPlayQueueBoundariesShuffleAppendRepeatReset(t *testing.T) {
	q := &playQueue{}
	if _, ok := q.current(); ok {
		t.Fatal("an empty queue has no current track")
	}
	if _, ok := q.moveNext(); ok {
		t.Fatal("an empty queue cannot move next")
	}
	if _, ok := q.movePrev(); ok {
		t.Fatal("an empty queue cannot move back")
	}
	if q.hasLoadedNext() {
		t.Fatal("an empty queue claims neighbours")
	}

	items := queueTracks("a", "b", "c", "d")
	q.reset(items)
	if _, ok := q.selectTrack(MylistItem{WatchID: "missing"}); ok {
		t.Fatal("selecting a track outside the queue must fail")
	}
	selected, ok := q.selectTrack(items[1])
	if !ok || selected.WatchID != "b" {
		t.Fatalf("selecting b = %#v, %v", selected, ok)
	}
	if !q.hasLoadedNext() {
		t.Fatal("b has a loaded next")
	}
	if got := q.orderIDs(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("order = %v", got)
	}
	if got := queueIDs(q.orderedItems()); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("ordered items = %v", got)
	}

	// Shuffle lays the whole queue out again: the track playing first, every
	// other entry once in a fresh random order.
	q.setShuffle(true)
	if !q.isShuffle() {
		t.Fatal("shuffle did not turn on")
	}
	if q.orderIDs()[0] != "b" {
		t.Fatalf("shuffle did not pin b to the front: %v", q.orderIDs())
	}
	if got := sortedQueueIDs(q.orderIDs()); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("shuffle lost or doubled entries: %v", got)
	}
	if _, ok := q.movePrev(); ok {
		t.Fatal("b is first under shuffle, so there is no previous")
	}

	// Unshuffle restores loaded order on the same track, and shuffle again
	// lays the track playing out in front.
	q.setShuffle(false)
	if got := q.orderIDs(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("unshuffle did not restore the order: %v", got)
	}
	q.setShuffle(true)
	if cur, _ := q.current(); cur.WatchID != "b" || q.orderIDs()[0] != "b" {
		t.Fatalf("reshuffle lost the track playing: %v", q.orderIDs())
	}
	if got := sortedQueueIDs(q.orderIDs()); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("reshuffle lost or doubled entries: %v", got)
	}

	q.selectTrack(items[1])
	q.append(queueTracks("e", "f"))
	if q.len() != 6 {
		t.Fatalf("append length = %d", q.len())
	}
	if cur, _ := q.current(); cur.WatchID != "b" {
		t.Fatalf("append moved the track playing: %#v", cur)
	}
	if got := sortedQueueIDs(q.orderIDs()); !slices.Equal(got, []string{"a", "b", "c", "d", "e", "f"}) {
		t.Fatalf("append lost or doubled entries: %v", got)
	}

	// Drain from the head: every entry exactly once, then stop.
	for {
		if _, ok := q.movePrev(); !ok {
			break
		}
	}
	seen := []string{}
	if head, ok := q.current(); ok {
		seen = append(seen, head.WatchID)
	}
	for {
		item, ok := q.moveNext()
		if !ok {
			break
		}
		seen = append(seen, item.WatchID)
	}
	if got := sortedQueueIDs(seen); !slices.Equal(got, []string{"a", "b", "c", "d", "e", "f"}) {
		t.Fatalf("drain saw %v", got)
	}
	if _, ok := q.moveNext(); ok {
		t.Fatal("the queue's end kept moving")
	}
	if q.hasLoadedNext() {
		t.Fatal("the queue's end still claims a next")
	}

	// Repeat wraps the whole queue both ways.
	q.repeat = true
	if next, ok := q.moveNext(); !ok || next.WatchID != seen[0] {
		t.Fatalf("repeat did not wrap to the head: %#v, %v", next, ok)
	}
	if prev, ok := q.movePrev(); !ok || prev.WatchID != seen[len(seen)-1] {
		t.Fatalf("repeat did not wrap back to the tail: %#v, %v", prev, ok)
	}

	// Unshuffle restores loaded order on the same track.
	q.setShuffle(false)
	if q.isShuffle() {
		t.Fatal("shuffle stayed on")
	}
	if cur, _ := q.current(); cur.WatchID != seen[len(seen)-1] {
		t.Fatalf("unshuffle moved the track playing: %#v", cur)
	}

	// Shuffle with no selection permutes everything, including appends.
	q.reset(queueTracks("x", "y"))
	q.setShuffle(true)
	q.append(queueTracks("z"))
	if q.len() != 3 {
		t.Fatalf("length = %d", q.len())
	}
	trio := []string{}
	for {
		next, ok := q.moveNext()
		if !ok {
			break
		}
		trio = append(trio, next.WatchID)
	}
	if got := sortedQueueIDs(trio); !slices.Equal(got, []string{"x", "y", "z"}) {
		t.Fatalf("shuffled walk saw %v", got)
	}

	// Reset clears selection, shuffle, and repeat.
	q.reset(queueTracks("w"))
	if _, ok := q.current(); ok || q.isShuffle() || q.repeat {
		t.Fatal("reset left state behind")
	}
	if next, ok := q.moveNext(); !ok || next.WatchID != "w" {
		t.Fatalf("moveNext after reset = %#v, %v", next, ok)
	}
	if _, ok := q.moveNext(); ok {
		t.Fatal("a one-track queue has no second entry")
	}
}

// TestPlayQueueSelectUnderShufflePinsTheEntryToTheFront checks that selecting
// an entry under shuffle starts the queue at it: the entry plays first, as
// turning shuffle on would lay the queue out.
func TestPlayQueueSelectUnderShufflePinsTheEntryToTheFront(t *testing.T) {
	q := &playQueue{}
	items := queueTracks("a", "b", "c", "d", "e", "f")
	q.reset(items)
	q.setShuffle(true)
	c := items[2]
	q.move([]int{indexOfStrings(q.orderIDs(), "c")}, q.len()) // c is last for certain
	if got := q.orderIDs()[q.len()-1]; got != "c" {
		t.Fatalf("c is not last: %v", q.orderIDs())
	}
	q.selectTrack(c)
	if q.orderIDs()[0] != "c" {
		t.Fatalf("select did not pin c to the front: %v", q.orderIDs())
	}
	if got := sortedQueueIDs(q.orderIDs()); !slices.Equal(got, []string{"a", "b", "c", "d", "e", "f"}) {
		t.Fatalf("select lost or doubled entries: %v", got)
	}
	if cur, _ := q.current(); cur.WatchID != "c" {
		t.Fatalf("current = %#v", cur)
	}
	if _, ok := q.movePrev(); ok {
		t.Fatal("c is first after selecting it under shuffle")
	}
}

// TestPlayQueueRemoveKeepsThePlayingTrack checks entries come out without
// moving the track playing.
func TestPlayQueueRemoveKeepsThePlayingTrack(t *testing.T) {
	q := &playQueue{}
	abcd := queueTracks("a", "b", "c", "d")
	q.reset(abcd)
	q.selectTrack(abcd[1])
	q.remove("c")
	if q.len() != 3 {
		t.Fatalf("length = %d", q.len())
	}
	if got := q.orderIDs(); !slices.Equal(got, []string{"a", "b", "d"}) {
		t.Fatalf("order = %v", got)
	}
	if cur, _ := q.current(); cur.WatchID != "b" {
		t.Fatalf("removing c moved the track playing: %#v", cur)
	}
	if !q.hasLoadedNext() {
		t.Fatal("b still has a next")
	}

	// Removing the track playing leaves nothing selected: the next command
	// moves to the first entry, rather than skipping one.
	q.remove("b")
	if _, ok := q.current(); ok {
		t.Fatal("removing the track playing left it selected")
	}
	if next, _ := q.moveNext(); next.WatchID != "a" {
		t.Fatalf("moveNext = %#v", next)
	}
	if next, _ := q.moveNext(); next.WatchID != "d" {
		t.Fatalf("moveNext = %#v", next)
	}
	if _, ok := q.moveNext(); ok {
		t.Fatal("the queue kept moving past its end")
	}

	q.remove("missing")
	if got := q.orderIDs(); !slices.Equal(got, []string{"a", "d"}) {
		t.Fatalf("order = %v", got)
	}
}

// TestPlayQueueUpdateTakesThePagesCopies checks the refresh of a playing
// mylist: the queue's own copies follow the page that just arrived, so a
// track deleted since the queue was built stops reading as playable, while
// the order, the selection and the entries the page does not hold stay put.
func TestPlayQueueUpdateTakesThePagesCopies(t *testing.T) {
	q := &playQueue{}
	abcd := queueTracks("a", "b", "c", "d")
	q.reset(abcd)
	q.selectTrack(abcd[1])

	page := []MylistItem{
		{WatchID: "b", Title: "曲B", Available: true, Duration: 125},
		{WatchID: "c", Title: "削除された動画", Available: true},
	}
	q.update(page)

	if got := q.orderIDs(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("the update reordered the queue: %v", got)
	}
	if cur, _ := q.current(); cur.WatchID != "b" || cur.Duration != 125 {
		t.Fatalf("the track playing did not take the page's copy: %#v", cur)
	}
	items := q.orderedItems()
	if isPlayable(items[2]) {
		t.Fatalf("a deleted track still reads as playable: %#v", items[2])
	}
	if items[0].WatchID != "a" || items[3].WatchID != "d" {
		t.Fatalf("an entry the page does not hold changed: %#v", queueIDs(items))
	}
}

// TestPlayQueueMoveReordersRows checks the drag's reordering: to counts rows
// before the drop, in the queue as it stands.
func TestPlayQueueMoveReordersRows(t *testing.T) {
	q := &playQueue{}
	abcd := queueTracks("a", "b", "c", "d")
	q.reset(abcd)
	q.selectTrack(abcd[0])
	q.move([]int{2}, 0)
	if got := q.orderIDs(); !slices.Equal(got, []string{"c", "a", "b", "d"}) {
		t.Fatalf("order = %v", got)
	}
	if got := queueIDs(q.orderedItems()); !slices.Equal(got, []string{"c", "a", "b", "d"}) {
		t.Fatalf("ordered items = %v", got)
	}
	if cur, _ := q.current(); cur.WatchID != "a" {
		t.Fatalf("the move moved the track playing: %#v", cur)
	}

	q.move([]int{0}, 2)
	if got := q.orderIDs(); !slices.Equal(got, []string{"a", "c", "b", "d"}) {
		t.Fatalf("order = %v", got)
	}
	q.move([]int{0}, 4)
	if got := q.orderIDs(); !slices.Equal(got, []string{"c", "b", "d", "a"}) {
		t.Fatalf("order = %v", got)
	}
	q.move([]int{3}, 4)
	if got := q.orderIDs(); !slices.Equal(got, []string{"c", "b", "d", "a"}) {
		t.Fatalf("a move to where the row already sits changed the order: %v", got)
	}
	q.move([]int{0, 2}, 0)
	if got := q.orderIDs(); !slices.Equal(got, []string{"c", "d", "b", "a"}) {
		t.Fatalf("order = %v", got)
	}
}

func indexOfStrings(xs []string, v string) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return -1
}

// TestPlayQueueZeroValueShufflesSafely: the engine turns the saved shuffle
// preference on before any mylist arrives, on a queue nothing selected.
func TestPlayQueueZeroValueShufflesSafely(t *testing.T) {
	q := &playQueue{}
	q.setShuffle(true)
	if !q.isShuffle() || q.len() != 0 || len(q.orderIDs()) != 0 {
		t.Fatalf("shuffling an empty queue = %#v", q)
	}
	q.setShuffle(false)
	q.reset(queueTracks("a"))
	q.setShuffle(true)
	if got := q.orderIDs(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("order = %v", got)
	}
}
