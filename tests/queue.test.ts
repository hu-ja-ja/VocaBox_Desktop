import { expect, test } from "bun:test";
import { PlayQueue } from "../src/queue";

interface Track {
  watchId: string;
}

const track = (watchId: string): Track => ({ watchId });

test("queue boundaries, shuffle, append, repeat, reset", () => {
  const q = new PlayQueue<Track>();
  expect([q.current(), q.moveNext(), q.movePrev()]).toEqual([null, null, null]);
  expect(q.hasNext() || q.hasPrev() || q.hasLoadedNext()).toBe(false);

  const abcd = ["a", "b", "c", "d"].map(track);
  const b = abcd[1]!;
  q.reset(abcd);
  expect(q.select(track("missing"))).toBeNull();
  expect(q.select(b)?.watchId).toBe("b");
  expect(q.hasLoadedNext()).toBe(true);
  expect(q.orderIds()).toEqual(["a", "b", "c", "d"]);
  expect(q.orderedItems().map((t) => t.watchId)).toEqual(["a", "b", "c", "d"]);

  // Shuffle lays the whole list out again: the track playing first, every
  // other entry once in a fresh random order.
  q.setShuffle(true);
  expect(q.isShuffle()).toBe(true);
  expect(q.orderIds()[0]).toBe("b");
  expect(q.orderIds().toSorted()).toEqual(["a", "b", "c", "d"]);
  expect(q.movePrev()).toBeNull();
  expect(q.hasPrev()).toBe(false);

  // Unshuffle restores loaded order on the same track, and shuffle again
  // lays the track playing out in front.
  q.setShuffle(false);
  expect(q.orderIds()).toEqual(["a", "b", "c", "d"]);
  q.setShuffle(true);
  expect(q.current()?.watchId).toBe("b");
  expect(q.orderIds()[0]).toBe("b");
  expect(q.orderIds().toSorted()).toEqual(["a", "b", "c", "d"]);

  q.select(b);
  q.append(["e", "f"].map(track));
  expect(q.length).toBe(6);
  expect(q.current()?.watchId).toBe("b");
  expect(q.orderIds().toSorted()).toEqual(["a", "b", "c", "d", "e", "f"]);

  // Drain from the head: every entry exactly once, then stop.
  while (q.movePrev() !== null) {}
  const head = q.current();
  const seen: string[] = head === null ? [] : [head.watchId];
  let item = q.moveNext();
  while (item !== null) {
    seen.push(item.watchId);
    item = q.moveNext();
  }
  expect([...seen].sort()).toEqual(["a", "b", "c", "d", "e", "f"]);
  expect(q.moveNext()).toBeNull();
  expect(q.hasLoadedNext()).toBe(false);

  // Repeat wraps the whole list both ways.
  q.repeat = true;
  expect(q.hasNext()).toBe(true);
  expect(q.moveNext()?.watchId).toBe(seen[0] ?? null);
  expect(q.movePrev()?.watchId).toBe(seen[seen.length - 1] ?? null);

  // Unshuffle restores loaded order on the same track.
  q.setShuffle(false);
  expect(q.isShuffle()).toBe(false);
  expect(q.current()?.watchId).toBe(seen[seen.length - 1] ?? null);

  // Shuffle with no selection permutes everything, including appends.
  q.reset(["x", "y"].map(track));
  q.setShuffle(true);
  q.append(["z"].map(track));
  expect(q.length).toBe(3);
  const trio: string[] = [];
  for (let next = q.moveNext(); next !== null; next = q.moveNext()) trio.push(next.watchId);
  expect([...trio].sort()).toEqual(["x", "y", "z"]);

  // Reset clears selection, shuffle, and repeat.
  q.reset([track("w")]);
  expect([q.current(), q.isShuffle(), q.repeat]).toEqual([null, false, false]);
  expect(q.moveNext()?.watchId).toBe("w");
  expect(q.moveNext()).toBeNull();
});

// Selecting an entry under shuffle starts the queue at it: the entry plays
// first, as turning shuffle on would lay the queue out, rather than sitting
// where the shuffle happened to put it.
test("select under shuffle pins the entry to the front", () => {
  const q = new PlayQueue<Track>();
  const items = ["a", "b", "c", "d", "e", "f"].map(track);
  q.reset(items);
  q.setShuffle(true);
  const c = items[2]!;
  q.move([q.orderIds().indexOf("c")], items.length); // c is last for certain
  expect(q.orderIds()[items.length - 1]).toBe("c");
  q.select(c);
  expect(q.orderIds()[0]).toBe("c");
  expect(q.orderIds().toSorted()).toEqual(["a", "b", "c", "d", "e", "f"]);
  expect(q.current()?.watchId).toBe("c");
  expect(q.hasPrev()).toBe(false);
});

// seq counts the changes to what plays and in which order, so the app can
// tell when a playlist view has to be laid out again.
test("seq counts playlist changes", () => {
  const q = new PlayQueue<Track>();
  const ab = ["a", "b"].map(track);
  q.reset(ab);
  q.setShuffle(true);
  const shuffled = q.seq();
  q.setShuffle(true);
  expect(q.seq()).toBe(shuffled);
  q.setShuffle(false);
  expect(q.seq()).toBeGreaterThan(shuffled);
  q.select(ab[1]!);
  const selected = q.seq();
  q.select(ab[1]!);
  expect(q.seq()).toBe(selected);
  q.append(["c"].map(track));
  expect(q.seq()).toBeGreaterThan(selected);
});

// remove takes entries out, keeping the track playing on its own.
test("remove keeps the playing track", () => {
  const q = new PlayQueue<Track>();
  const abcd = ["a", "b", "c", "d"].map(track);
  q.reset(abcd);
  q.select(abcd[1]!);
  q.remove("c");
  expect(q.length).toBe(3);
  expect(q.orderIds()).toEqual(["a", "b", "d"]);
  expect(q.current()?.watchId).toBe("b");
  expect(q.hasLoadedNext()).toBe(true);

  // Removing the track playing leaves nothing selected: the next command
  // moves to the first entry, rather than skipping one.
  q.remove("b");
  expect(q.current()).toBeNull();
  expect(q.moveNext()?.watchId).toBe("a");
  expect(q.moveNext()?.watchId).toBe("d");
  expect(q.moveNext()).toBeNull();

  q.remove("missing");
  expect(q.orderIds()).toEqual(["a", "d"]);
});

// move reorders the playing order, as dragging rows in the playlist does:
// to counts rows before the drop, in the list as it stands.
test("move reorders rows", () => {
  const q = new PlayQueue<Track>();
  const abcd = ["a", "b", "c", "d"].map(track);
  q.reset(abcd);
  q.select(abcd[0]!);
  q.move([2], 0);
  expect(q.orderIds()).toEqual(["c", "a", "b", "d"]);
  expect(q.orderedItems().map((t) => t.watchId)).toEqual(["c", "a", "b", "d"]);
  expect(q.current()?.watchId).toBe("a");

  q.move([0], 2);
  expect(q.orderIds()).toEqual(["a", "c", "b", "d"]);
  q.move([0], 4);
  expect(q.orderIds()).toEqual(["c", "b", "d", "a"]);
  q.move([3], 4);
  expect(q.orderIds()).toEqual(["c", "b", "d", "a"]);

  q.move([0, 2], 0);
  expect(q.orderIds()).toEqual(["c", "d", "b", "a"]);
});
