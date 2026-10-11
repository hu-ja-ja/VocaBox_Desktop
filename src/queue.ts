// Playable-entry queue for one imported mylist. Callers keep unavailable
// entries out; navigation here is pure. Repeat wraps the whole list;
// shuffle touches upcoming entries only, preserving current and prefix.
export class PlayQueue<T> {
  private items: T[] = [];
  private order: number[] = [];
  private pos = -1;
  private shuffled = false;
  repeat = false;

  get length(): number {
    return this.items.length;
  }

  isShuffle(): boolean {
    return this.shuffled;
  }

  reset(items: T[]): void {
    this.items = [...items];
    this.order = items.map((_, i) => i);
    this.pos = -1;
    this.shuffled = false;
    this.repeat = false;
  }

  append(items: T[]): void {
    const base = this.items.length;
    this.items.push(...items);
    if (!this.shuffled) {
      for (let i = 0; i < items.length; i++) this.order.push(base + i);
      return;
    }
    const start = this.pos < 0 ? 0 : this.pos + 1;
    for (let k = 0; k < items.length; k++) {
      const at = start + Math.floor(Math.random() * (this.order.length - start + 1));
      this.order.splice(at, 0, base + k);
    }
  }

  current(): T | null {
    return this.at(this.pos);
  }

  select(item: T): T | null {
    const p = this.items.indexOf(item);
    if (p < 0) return null;
    this.pos = this.order.indexOf(p);
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
    if (on) {
      this.shuffleRange(this.pos < 0 ? 0 : this.pos + 1);
      return;
    }
    const cur = this.pos >= 0 ? this.order[this.pos] : undefined;
    this.order = this.items.map((_, i) => i);
    this.pos = cur === undefined ? -1 : this.order.indexOf(cur);
  }

  private shuffleRange(from: number): void {
    for (let i = this.order.length - 1; i > from; i--) {
      const j = from + Math.floor(Math.random() * (i - from + 1));
      const a = this.order[i];
      const b = this.order[j];
      if (a === undefined || b === undefined) continue;
      this.order[i] = b;
      this.order[j] = a;
    }
  }

  private at(p: number): T | null {
    if (p < 0) return null;
    const idx = this.order[p];
    if (idx === undefined) return null;
    return this.items[idx] ?? null;
  }
}
