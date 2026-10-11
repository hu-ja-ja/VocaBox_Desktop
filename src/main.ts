import "./style.css";
import { Mylist, type MylistItem, type MylistPage } from "./mygo";
import { EmbedPlayer } from "./player";
import { PlayQueue } from "./queue";

const $ = <T extends HTMLElement = HTMLElement>(selector: string) =>
  document.querySelector<T>(selector)!;

const form = $<HTMLFormElement>("#mylist-form");
const input = $<HTMLInputElement>("#mylist-input");
const submit = form.querySelector<HTMLButtonElement>("button[type=submit]")!;
const status = $("#mylist-status");
const result = $("#mylist-result");
const name = $("#mylist-name");
const meta = $("#mylist-meta");
const description = $("#mylist-description");
const items = $<HTMLOListElement>("#mylist-items");
const more = $<HTMLButtonElement>("#mylist-more");
const nowPlaying = $("#now-playing");
const playerSlot = $("#player-slot");
const playerToggle = $<HTMLButtonElement>("#player-toggle");
const seekBack = $<HTMLButtonElement>("#seek-back");
const seekForward = $<HTMLButtonElement>("#seek-forward");
const playerVolume = $<HTMLInputElement>("#player-volume");
const playerStatus = $("#player-status");
const shuffleToggle = $<HTMLButtonElement>("#shuffle-toggle");
const loopToggle = $<HTMLButtonElement>("#loop-toggle");
const prevTrack = $<HTMLButtonElement>("#prev-track");
const nextTrack = $<HTMLButtonElement>("#next-track");

// Lookup state lives in this window only; nothing is persisted.
let currentInput = "";
let nextPage = 1;
let busy = false;
// Playable entries live only in the queue; unavailable entries stay visible
// in the list but never enter it.
const queue = new PlayQueue<MylistItem>();
// Guards overlapping previous/next advances; invalidated by user selection.
let stepping = false;
let playToken = 0;
// The active embed player, if an entry was selected. No autoplay: selecting
// an entry only loads the official player, paused.
let player: EmbedPlayer | null = null;
let playing = false;
let positionMs = 0;
let durationMs = NaN;

function formatDuration(totalSeconds: number): string {
  const m = Math.floor(totalSeconds / 60);
  const s = totalSeconds % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}

// Same criterion everywhere: unavailable entries stay visible but never
// enter the queue and get no play button.
function isPlayable(item: MylistItem): boolean {
  return item.available && !!item.title;
}

function renderItem(item: MylistItem): HTMLLIElement {
  const li = document.createElement("li");
  if (isPlayable(item)) {
    li.textContent = item.title ?? "";
    const sub = document.createElement("span");
    const parts = [item.uploader, item.duration ? formatDuration(item.duration) : ""].filter(Boolean);
    parts.unshift(item.watchId);
    sub.textContent = `（${parts.join(" / ")}）`;
    li.append(sub);
    const play = document.createElement("button");
    play.type = "button";
    play.textContent = "再生";
    play.addEventListener("click", () => selectItem(item));
    li.append(play);
  } else {
    li.textContent = `取得できません（${item.watchId}）`;
  }
  return li;
}

// selectItem replaces the official embed player with the chosen entry and
// points the queue at it. Row selection loads paused; next/previous pass
// autoplay to request playback once the new iframe reports ready.
// Unavailable entries never reach here: they get no play button.
function selectItem(item: MylistItem, autoplay = false): void {
  if (queue.select(item) === null) return;
  playToken += 1;
  player?.dispose();
  playerStatus.textContent = "";
  const title = item.title ?? item.watchId;
  let autoStarted = false;
  const next = new EmbedPlayer(playerSlot, item.videoId ?? item.watchId, title, {
    onReady: () => {
      setPlayerControlsEnabled(true);
      if (autoplay && !autoStarted) {
        autoStarted = true;
        next.play();
      }
    },
    onPlayingChange: (playingValue) => {
      setPlayingUI(playingValue);
    },
    onEnded: () => {
      void stepNext();
    },
    onError: () => {
      setPlayerControlsEnabled(false);
      playerStatus.textContent = "動画を読み込めませんでした。";
    },
    onMetadata: (currentTimeMs, durationMsValue) => {
      positionMs = currentTimeMs;
      durationMs = durationMsValue;
    },
    onVolume: (volume) => {
      // Don't fight the user while they are dragging the range.
      if (document.activeElement !== playerVolume) {
        playerVolume.value = String(Math.round(Math.min(1, Math.max(0, volume)) * 100));
      }
    },
  });
  player = next;
  nowPlaying.textContent = title;
  playing = false;
  positionMs = 0;
  durationMs = NaN;
  playerToggle.textContent = "再生";
  playerToggle.setAttribute("aria-pressed", "false");
  setPlayerControlsEnabled(false);
  updateQueueButtons();
}

// Player controls stay disabled until the iframe reports readiness; the
// iframe's own visible controls remain usable before that.
function setPlayerControlsEnabled(enabled: boolean): void {
  playerToggle.disabled = !enabled;
  seekBack.disabled = !enabled;
  seekForward.disabled = !enabled;
  playerVolume.disabled = !enabled;
}

function setPlayingUI(next: boolean): void {
  playing = next;
  playerToggle.textContent = next ? "一時停止" : "再生";
  playerToggle.setAttribute("aria-pressed", String(next));
}

// Queue buttons need a current item; next additionally works while further
// pages can still be fetched on demand.
function updateQueueButtons(): void {
  const hasCurrent = queue.current() !== null;
  shuffleToggle.disabled = queue.length < 2;
  loopToggle.disabled = queue.length === 0;
  prevTrack.disabled = !(hasCurrent && queue.hasPrev());
  nextTrack.disabled = !(hasCurrent && (queue.hasNext() || hasMorePages()));
}

function hasMorePages(): boolean {
  return !result.hidden && !more.hidden;
}

// stepNext advances to the adjacent item and requests playback once its
// iframe is ready. With repeat on it still fetches remaining pages first and
// wraps only when every page is exhausted; it never crawls eagerly.
async function stepNext(): Promise<void> {
  if (stepping) return;
  stepping = true;
  try {
    if (!queue.hasLoadedNext() && hasMorePages()) {
      const token = playToken;
      while (!queue.hasLoadedNext() && hasMorePages()) {
        if (!(await requestPage(true))) {
          updateQueueButtons();
          return;
        }
        // A fresh queue has no selection yet: stop at the first arrivals.
        if (queue.current() === null && queue.length > 0) break;
      }
      if (token !== playToken) {
        updateQueueButtons();
        return;
      }
    }
    // Always reached with a valid token: wraps only via repeat after
    // pagination is exhausted.
    const item = queue.moveNext();
    if (item !== null) selectItem(item, true);
    updateQueueButtons();
  } finally {
    stepping = false;
  }
}

function stepPrev(): void {
  const item = queue.movePrev();
  if (item !== null) selectItem(item, true);
  updateQueueButtons();
}
function disposePlayer(): void {
  player?.dispose();
  player = null;
  playing = false;
  playerSlot.replaceChildren();
  nowPlaying.textContent = "再生中の曲はありません。";
  playerStatus.textContent = "";
  setPlayingUI(false);
  setPlayerControlsEnabled(false);
}

function showPage(page: MylistPage, append: boolean): void {
  name.textContent = page.name;
  meta.textContent = page.owner
    ? `${page.owner}・全${page.totalItemCount}件`
    : `全${page.totalItemCount}件`;
  description.textContent = page.description;
  const playableItems = page.items.filter(isPlayable);
  if (!append) {
    items.replaceChildren();
    queue.reset([]);
  }
  queue.append(playableItems);
  items.append(...page.items.map(renderItem));
  result.hidden = false;
  more.hidden = !page.hasNext;
  updateQueueButtons();
  const shown = items.childElementCount;
  status.textContent =
    page.items.length === 0 && shown === 0
      ? "マイリストに項目がありません。"
      : `全${page.totalItemCount}件中${shown}件を表示しています。`;
}

// One page fetch at a time; concurrent callers share the in-flight one, so
// a Next/end event during a manual load awaits the same page instead of
// starting another or losing the advancement. Resolves true when a page was
// fetched (nextPage advanced).
let pageLoad: Promise<boolean> | null = null;

function requestPage(append: boolean): Promise<boolean> {
  if (pageLoad !== null) return pageLoad;
  pageLoad = fetchPage(append);
  return pageLoad;
}

async function fetchPage(append: boolean): Promise<boolean> {
  busy = true;
  submit.disabled = true;
  input.disabled = true;
  more.disabled = true;
  prevTrack.disabled = true;
  nextTrack.disabled = true;
  status.textContent = "読み込み中…";
  try {
    const page = await Mylist.getPage(currentInput, nextPage);
    nextPage += 1;
    showPage(page, append);
    return true;
  } catch (err) {
    status.textContent = err instanceof Error ? err.message : String(err);
    return false;
  } finally {
    busy = false;
    submit.disabled = false;
    input.disabled = false;
    more.disabled = false;
    pageLoad = null;
    updateQueueButtons();
  }
}

form.addEventListener("submit", (e) => {
  e.preventDefault();
  if (busy) return;
  currentInput = input.value;
  nextPage = 1;
  result.hidden = true;
  more.hidden = true;
  queue.reset([]);
  setToggleUI(shuffleToggle, false, "シャッフル（オン）", "シャッフル");
  setToggleUI(loopToggle, false, "ループ（オン）", "ループ");
  disposePlayer();
  updateQueueButtons();
  void requestPage(false);
});

more.addEventListener("click", () => {
  void requestPage(true);
});

playerToggle.addEventListener("click", () => {
  if (player === null) return;
  // The label follows player status events only; sending a command alone
  // claims nothing.
  if (playing) {
    player.pause();
  } else {
    player.play();
  }
});

function seekBy(offsetMs: number): void {
  if (player === null) return;
  const target = positionMs + offsetMs;
  const end = Number.isFinite(durationMs) ? durationMs : target;
  player.seek(Math.min(Math.max(target, 0), Math.max(end, 0)));
}

seekBack.addEventListener("click", () => seekBy(-10_000));
seekForward.addEventListener("click", () => seekBy(10_000));

playerVolume.addEventListener("input", () => {
  player?.setVolume(Number(playerVolume.value) / 100);
});

function setToggleUI(button: HTMLButtonElement, on: boolean, onText: string, offText: string): void {
  button.textContent = on ? onText : offText;
  button.setAttribute("aria-pressed", String(on));
}

shuffleToggle.addEventListener("click", () => {
  queue.setShuffle(!queue.isShuffle());
  setToggleUI(
    shuffleToggle,
    queue.isShuffle(),
    "シャッフル（オン）",
    "シャッフル",
  );
  updateQueueButtons();
});

loopToggle.addEventListener("click", () => {
  queue.repeat = !queue.repeat;
  setToggleUI(loopToggle, queue.repeat, "ループ（オン）", "ループ");
  updateQueueButtons();
});

prevTrack.addEventListener("click", () => {
  stepPrev();
});

nextTrack.addEventListener("click", () => {
  void stepNext();
});
