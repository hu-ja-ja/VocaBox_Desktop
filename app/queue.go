package main

import (
	"math/rand"
	"sort"
)

// playQueue is one mylist's playing queue, the queue the queue screen shows:
// the entries in the order they play. Callers keep unplayable entries out;
// navigation here is pure. Repeat wraps the whole queue; shuffle permutes
// every entry with the track playing first, so the order it reports is the
// order it plays. It is the Go home of the former src/queue.ts.
type playQueue struct {
	items []MylistItem
	order []int
	pos   int
	// shuffled and repeat are the queue's own modes; the app hands them over
	// with the commands and reads them back for the player bar.
	shuffled bool
	repeat   bool
}

func (q *playQueue) len() int { return len(q.items) }

func (q *playQueue) isShuffle() bool { return q.shuffled }

// orderIDs is the queue as it plays: the watch ID of each track in playing
// order.
func (q *playQueue) orderIDs() []string {
	ids := make([]string, 0, len(q.order))
	for _, i := range q.order {
		if i >= 0 && i < len(q.items) {
			ids = append(ids, q.items[i].WatchID)
		}
	}
	return ids
}

// orderedItems is the queue in playing order, the order the queue screen
// shows.
func (q *playQueue) orderedItems() []MylistItem {
	items := make([]MylistItem, 0, len(q.order))
	for _, i := range q.order {
		if i >= 0 && i < len(q.items) {
			items = append(items, q.items[i])
		}
	}
	return items
}

// currentIndex is where the track playing sits in the playing order, for the
// queue screen to keep it in sight, or -1 with nothing playing.
func (q *playQueue) currentIndex() int {
	if _, ok := q.current(); !ok {
		return -1
	}
	return q.pos
}

// remove takes the entries carrying watchID out, keeping the track playing
// in place when it stays.
func (q *playQueue) remove(watchID string) {
	for i := len(q.items) - 1; i >= 0; i-- {
		if q.items[i].WatchID != watchID {
			continue
		}
		at := indexOf(q.order, i)
		if at >= 0 {
			q.order = append(q.order[:at], q.order[at+1:]...)
		}
		q.items = append(q.items[:i], q.items[i+1:]...)
		for j := range q.order {
			if q.order[j] > i {
				q.order[j]--
			}
		}
		if q.pos == at {
			q.pos = -1
		} else if at >= 0 && at < q.pos {
			q.pos--
		}
	}
}

// move places the entries of the rows from at the row before to, as a
// hand-ordered queue: the drag knows the playing order only, so rows are
// its positions.
func (q *playQueue) move(from []int, to int) {
	seen := map[int]bool{}
	rows := make([]int, 0, len(from))
	for _, row := range from {
		if row >= 0 && row < len(q.order) && !seen[row] {
			seen[row] = true
			rows = append(rows, row)
		}
	}
	sort.Ints(rows)
	if len(rows) == 0 {
		return
	}
	moved := make([]int, len(rows))
	for k, row := range rows {
		moved[k] = q.order[row]
	}
	// Count the rows dropped before that are not moving: rows moving down
	// land after those they passed.
	at := 0
	for i := 0; i < min(to, len(q.order)); i++ {
		if !seen[i] {
			at++
		}
	}
	cur := q.playingIndex()
	for k := len(rows) - 1; k >= 0; k-- {
		row := rows[k]
		q.order = append(q.order[:row], q.order[row+1:]...)
	}
	order := make([]int, 0, len(q.order)+len(moved))
	order = append(order, q.order[:at]...)
	order = append(order, moved...)
	order = append(order, q.order[at:]...)
	q.order = order
	if cur >= 0 {
		q.pos = indexOf(q.order, cur)
	}
}

// reset makes items the queue, dropped on no track: the caller selects one
// to play.
func (q *playQueue) reset(items []MylistItem) {
	q.items = append([]MylistItem(nil), items...)
	q.order = make([]int, len(items))
	for i := range items {
		q.order[i] = i
	}
	q.pos = -1
	q.shuffled = false
	q.repeat = false
}

// append adds the entries of a mylist's next page: in place without shuffle,
// and folded into the remaining shuffle with one.
func (q *playQueue) append(items []MylistItem) {
	base := len(q.items)
	q.items = append(q.items, items...)
	if !q.shuffled {
		for i := range items {
			q.order = append(q.order, base+i)
		}
		return
	}
	start := 0
	if pos := q.playingIndex(); pos >= 0 {
		start = pos + 1
	}
	for k := range items {
		at := start + rand.Intn(len(q.order)-start+1)
		order := make([]int, 0, len(q.order)+1)
		order = append(order, q.order[:at]...)
		order = append(order, base+k)
		order = append(order, q.order[at:]...)
		q.order = order
	}
}

// current is the track playing, if any.
func (q *playQueue) current() (MylistItem, bool) { return q.at(q.pos) }

// at is the entry at position p of the playing order.
func (q *playQueue) at(p int) (MylistItem, bool) {
	if p < 0 || p >= len(q.order) {
		return MylistItem{}, false
	}
	idx := q.order[p]
	if idx < 0 || idx >= len(q.items) {
		return MylistItem{}, false
	}
	return q.items[idx], true
}

// selectTrack puts the queue on the entry carrying item's watch ID: under shuffle
// it starts at the front of a fresh shuffle, as turning shuffle on would lay
// the queue out, rather than sitting where the shuffle happened to put it.
func (q *playQueue) selectTrack(item MylistItem) (MylistItem, bool) {
	p := -1
	for i := range q.items {
		if q.items[i].WatchID == item.WatchID {
			p = i
			break
		}
	}
	if p < 0 {
		return MylistItem{}, false
	}
	at := indexOf(q.order, p)
	if at != q.pos {
		q.pos = at
		if q.shuffled {
			q.shuffleRange()
		}
	}
	return q.current()
}

// moveNext moves to the entry after the one playing, or reports none: the
// stop at the end is what tells the player a mylist ran out.
func (q *playQueue) moveNext() (MylistItem, bool) {
	if len(q.order) == 0 {
		return MylistItem{}, false
	}
	switch {
	case q.pos < 0:
		q.pos = 0
	case q.pos < len(q.order)-1:
		q.pos++
	case q.repeat:
		q.pos = 0
	default:
		return MylistItem{}, false
	}
	return q.current()
}

func (q *playQueue) movePrev() (MylistItem, bool) {
	if len(q.order) == 0 || q.pos < 0 {
		return MylistItem{}, false
	}
	switch {
	case q.pos > 0:
		q.pos--
	case q.repeat:
		q.pos = len(q.order) - 1
	default:
		return MylistItem{}, false
	}
	return q.current()
}

// hasLoadedNext reports whether the next track is already in the queue,
// rather than one a mylist's next page would have to bring.
func (q *playQueue) hasLoadedNext() bool {
	if len(q.order) == 0 || q.pos < 0 {
		return false
	}
	return q.pos < len(q.order)-1
}

// setShuffle turns shuffle on or off, keeping the track playing. Both
// directions recover the track from the items, not from the order being
// replaced.
func (q *playQueue) setShuffle(on bool) {
	if q.shuffled == on {
		return
	}
	q.shuffled = on
	// The track playing is kept by its item index, which an identity order
	// makes its position.
	cur := q.playingIndex()
	order := make([]int, len(q.items))
	for i := range order {
		order[i] = i
	}
	q.order = order
	q.pos = cur
	if on {
		q.shuffleRange()
	}
}

// shuffleRange lays the whole queue out again: the track playing first
// (until one is selected there is none, so the whole queue is shuffled), the
// rest in a fresh random order, every entry exactly once.
func (q *playQueue) shuffleRange() {
	head := q.playingIndex()
	tail := make([]int, 0, len(q.order))
	for i := range q.order {
		if i != q.pos {
			tail = append(tail, q.order[i])
		}
	}
	rand.Shuffle(len(tail), func(i, j int) { tail[i], tail[j] = tail[j], tail[i] })
	if head < 0 {
		q.order = tail
		q.pos = -1
		return
	}
	q.order = append([]int{head}, tail...)
	q.pos = 0
}

// playingIndex is the item index of the track playing, or -1 for none: an
// empty or unselected queue has none, whatever pos once held.
func (q *playQueue) playingIndex() int {
	if q.pos < 0 || q.pos >= len(q.order) {
		return -1
	}
	return q.order[q.pos]
}

// indexOf returns where v sits in xs, or -1.
func indexOf(xs []int, v int) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return -1
}
