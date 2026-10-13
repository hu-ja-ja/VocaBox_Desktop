import { expect, test } from "bun:test";
import { createReportQueue, type ReportClock } from "../src/report";

// fakeClock runs the timers by hand, so the test sees exactly which reports
// the queue sends and when.
function fakeClock() {
  let next = 0;
  const tasks = new Map<number, { fn: () => void; cleared: boolean }>();
  const clock: ReportClock = {
    setTimeout(fn) {
      const id = next++;
      tasks.set(id, { fn, cleared: false });
      return id;
    },
    clearTimeout(handle) {
      const task = tasks.get(handle as number);
      if (task) task.cleared = true;
    },
  };
  const runTimers = (): void => {
    const due = [...tasks.values()];
    tasks.clear();
    for (const task of due) if (!task.cleared) task.fn();
  };
  return { clock, runTimers };
}

test("a burst of state reports goes out as one plus the latest", () => {
  const sent: string[] = [];
  const { clock, runTimers } = fakeClock();
  const report = createReportQueue<string>((payload) => sent.push(payload), clock);

  report("a");
  expect(sent).toEqual(["a"]);
  report("b");
  report("c");
  expect(sent).toEqual(["a"]);
  runTimers();
  expect(sent).toEqual(["a", "c"]);

  // A state report after the interval opens a fresh window.
  report("d");
  expect(sent).toEqual(["a", "c", "d"]);
});

test("an immediate report cancels the queued state behind it", () => {
  const sent: string[] = [];
  const { clock, runTimers } = fakeClock();
  const report = createReportQueue<string>((payload) => sent.push(payload), clock);

  report("a");
  report("stale");
  report("ended", true);
  runTimers();
  expect(sent).toEqual(["a", "ended"]);

  // The queue keeps working after an immediate report.
  report("b");
  report("c");
  runTimers();
  expect(sent).toEqual(["a", "ended", "b", "c"]);
});

test("steady reports keep flowing at the interval", () => {
  const sent: string[] = [];
  const { clock, runTimers } = fakeClock();
  const report = createReportQueue<string>((payload) => sent.push(payload), clock);

  for (const payload of ["a", "b", "c", "d"]) {
    report(payload);
    runTimers();
  }
  expect(sent).toEqual(["a", "b", "c", "d"]);
});
