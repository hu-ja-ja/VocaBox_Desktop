// reportQueue keeps the app from being flooded with player reports. The
// embed answers one change with a burst of events, and every report costs
// the app a rebuild; the queue sends the state at most once per interval,
// the latest state winning. Reports the app acts on go out at once.

export interface ReportClock {
  setTimeout(fn: () => void, delayMs: number): unknown;
  clearTimeout(handle: unknown): void;
}

// reportIntervalMs bounds how often the state crosses to the app: ten a
// second keeps the player's controls live without rebuilding it per event.
export const reportIntervalMs = 100;

const defaultClock: ReportClock = {
  setTimeout: (fn, ms) => window.setTimeout(fn, ms),
  clearTimeout: (handle) => window.clearTimeout(handle as number),
};

export function createReportQueue<T>(
  send: (payload: T) => void,
  clock: ReportClock = defaultClock,
  intervalMs: number = reportIntervalMs,
): (payload: T, immediate?: boolean) => void {
  let handle: unknown = null;
  let pending: T | null = null;
  const flush = (): void => {
    handle = null;
    const next = pending;
    pending = null;
    if (next !== null) send(next);
  };
  return (payload, immediate = false) => {
    if (immediate) {
      // It carries the latest state, so a queued one is old news.
      if (handle !== null) {
        clock.clearTimeout(handle);
        handle = null;
        pending = null;
      }
      send(payload);
      return;
    }
    if (handle !== null) {
      pending = payload;
      return;
    }
    send(payload);
    handle = clock.setTimeout(flush, intervalMs);
  };
}
