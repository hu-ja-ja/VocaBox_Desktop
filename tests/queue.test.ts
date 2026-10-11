import { expect, test } from "bun:test";
import { PlayQueue } from "../src/queue";

test("queue boundaries, shuffle, append, repeat, reset", () => {
  const q = new PlayQueue<string>();
  expect([q.current(), q.moveNext(), q.movePrev()]).toEqual([null, null, null]);
  expect(q.hasNext() || q.hasPrev() || q.hasLoadedNext()).toBe(false);

  q.reset(["a", "b", "c", "d"]);
  expect(q.select("missing")).toBeNull();
  expect(q.select("b")).toBe("b");
  expect(q.hasLoadedNext()).toBe(true);

  // Shuffle keeps current and played prefix; upcoming is permuted.
  q.setShuffle(true);
  expect(q.isShuffle()).toBe(true);
  expect(q.current()).toBe("b");
  expect(q.movePrev()).toBe("a");
  expect(q.movePrev()).toBeNull();
  expect(q.hasPrev()).toBe(false);

  q.select("b");
  q.append(["e", "f"]);
  expect(q.length).toBe(6);
  expect(q.current()).toBe("b");

  // Drain from the head: every entry exactly once, then stop.
  while (q.movePrev() !== null) {}
  const head = q.current();
  const seen: string[] = head === null ? [] : [head];
  let item = q.moveNext();
  while (item !== null) {
    seen.push(item);
    item = q.moveNext();
  }
  expect([...seen].sort()).toEqual(["a", "b", "c", "d", "e", "f"]);
  expect(q.moveNext()).toBeNull();
  expect(q.hasLoadedNext()).toBe(false);

  // Repeat wraps the whole list both ways.
  q.repeat = true;
  expect(q.hasNext()).toBe(true);
  expect(q.moveNext()).toBe(seen[0] ?? null);
  expect(q.movePrev()).toBe(seen[seen.length - 1] ?? null);

  // Unshuffle restores loaded order on the same track.
  q.setShuffle(false);
  expect(q.isShuffle()).toBe(false);
  expect(q.current()).toBe(seen[seen.length - 1] ?? null);

  // Shuffle with no selection permutes everything, including appends.
  q.reset(["x", "y"]);
  q.setShuffle(true);
  q.append(["z"]);
  expect(q.length).toBe(3);
  const trio: string[] = [];
  for (let next = q.moveNext(); next !== null; next = q.moveNext()) trio.push(next);
  expect([...trio].sort()).toEqual(["x", "y", "z"]);

  // Reset clears selection, shuffle, and repeat.
  q.reset(["w"]);
  expect([q.current(), q.isShuffle(), q.repeat]).toEqual([null, false, false]);
  expect(q.moveNext()).toBe("w");
  expect(q.moveNext()).toBeNull();
});
