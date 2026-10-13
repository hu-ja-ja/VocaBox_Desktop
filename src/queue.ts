// Playable-entry queue for one imported mylist. Callers keep unavailable
// entries out; navigation here is pure. Repeat wraps the whole list; shuffle
// permutes every entry with the track playing first, so the order it reports
// is the order the playlist view shows.
export class PlayQueue<T extends { watchId: string }> {
  private items: T[] = [];
  private order: number[] = [];
  private pos = -1;
  private shuffled = false;
  private sequence = 0;
  repeat = false;

  get length(): number {
    return this.items.length;
  }

  isShuffle(): boolean {
    return this.shuffled;
  }

  // seq counts changes to what is playing and in which order, so a report
  // carrying the same number carries the same playlist.
  seq(): number {
    return this.sequence;
  }

  orderIds(): string[] {
    return this.order.map((i) => this.items[i]?.watchId ?? "");
  }

  // currentIndex is where the track playing sits in the playing order, for
  // the playlist view to keep it in sight, or -1 with nothing playing.
  currentIndex(): number {
    return this.pos;
  }

  // remove takes the entries carrying watchId out, keeping the track playing
  // in place when it stays.
  remove(watchId: string): void {
    for (let i = this.items.length - 1; i >= 0; i--) {
      if (this.items[i]?.watchId !== watchId) continue;
      const at = this.order.indexOf(i);
      if (at >= 0) this.order.splice(at, 1);
      this.items.splice(i, 1);
      for (let j = 0; j < this.order.length; j++) {
        if (this.order[j]! > i) this.order[j]!--;
      }
      if (this.pos === at) this.pos = -1;
      else if (at >= 0 && at < this.pos) this.pos--;
      this.sequence++;
    }
  }

  // move places the entries of the rows from at the row before to, as a
  // hand-ordered playlist: the drag knows the playing order only, so rows
  // are its positions.
  move(from: number[], to: number): void {
    const rows = [...new Set(from)].sort((a, b) => a - b).filter((r) => r >= 0 && r < this.order.length);
    if (rows.length === 0) return;
    const moved = rows.map((r) => this.order[r]!);
    // Count the rows dropped before that are not moving: rows moving down
    // land after those they passed.
    let at = 0;
    for (let i = 0; i < Math.min(to, this.order.length); i++) {
      if (!rows.includes(i)) at++;
    }
    const cur = this.pos >= 0 ? this.order[this.pos] : undefined;
    for (let i = rows.length - 1; i >= 0; i--) this.order.splice(rows[i]!, 1);
    this.order.splice(at, 0, ...moved);
    if (cur !== undefined) this.pos = this.order.indexOf(cur);
    this.sequence++;
  }

  reset(items: T[]): void {
    this.items = [...items];
    this.order = items.map((_, i) => i);
    this.pos = -1;
    this.shuffled = false;
    this.repeat = false;
    this.sequence++;
  }

  append(items: T[]): void {
    const base = this.items.length;
    this.items.push(...items);
    if (!this.shuffled) {
      for (let i = 0; i < items.length; i++) this.order.push(base + i);
      this.sequence++;
      return;
    }
    const start = this.pos < 0 ? 0 : this.pos + 1;
    for (let k = 0; k < items.length; k++) {
      const at = start + Math.floor(Math.random() * (this.order.length - start + 1));
      this.order.splice(at, 0, base + k);
    }
    this.sequence++;
  }

  current(): T | null {
    return this.at(this.pos);
  }

  select(item: T): T | null {
    const p = this.items.indexOf(item);
    if (p < 0) return null;
    const at = this.order.indexOf(p);
    if (at !== this.pos) this.sequence++;
    this.pos = at;
    return this.current();
  }

  moveNext(): T | null {
    if (this.order.length === 0) return null;
    if (this.pos < 0) this.pos = 0;
    else if (this.pos < this.order.length - 1) this.pos += 1;
    else if (this.repeat) this.pos = 0;
    else return null;
    return this.current();
  }

  movePrev(): T | null {
    if (this.order.length === 0 || this.pos < 0) return null;
    if (this.pos > 0) this.pos -= 1;
    else if (this.repeat) this.pos = this.order.length - 1;
    else return null;
    return this.current();
  }

  hasNext(): boolean {
    if (this.order.length === 0 || this.pos < 0) return false;
    return this.pos < this.order.length - 1 || this.repeat;
  }

  hasLoadedNext(): boolean {
    if (this.order.length === 0 || this.pos < 0) return false;
    return this.pos < this.order.length - 1;
  }

  hasPrev(): boolean {
    if (this.order.length === 0 || this.pos < 0) return false;
    return this.pos > 0 || this.repeat;
  }

  setShuffle(on: boolean): void {
    if (this.shuffled === on) return;
    this.shuffled = on;
    // The track playing stays on, at the front of a new shuffle; without one
    // the whole list is shuffled. Both recover the track from the items, not
    // from the order being replaced.
    const cur = this.pos >= 0 ? this.order[this.pos] : undefined;
    this.order = this.items.map((_, i) => i);
    this.pos = cur === undefined ? -1 : cur;
    if (on) this.shuffleRange();
    this.sequence++;
  }

  // shuffleRange lays the whole playlist out again: the track playing first
  // (until one is selected there is none, so the whole list is shuffled), the
  // rest in a fresh random order, every entry exactly once.
  private shuffleRange(): void {
    const head = this.pos >= 0 ? this.order[this.pos] : undefined;
    const tail: number[] = [];
    for (let i = 0; i < this.order.length; i++) {
      if (i !== this.pos) tail.push(this.order[i]!);
    }
    for (let i = tail.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      const a = tail[i]!;
      const b = tail[j]!;
      tail[i] = b;
      tail[j] = a;
    }
    this.order = head === undefined ? tail : [head, ...tail];
    this.pos = head === undefined ? -1 : 0;
  }

  private at(p: number): T | null {
    if (p < 0) return null;
    const idx = this.order[p];
    if (idx === undefined) return null;
    return this.items[idx] ?? null;
  }
}
