import "./style.css";
import { Mylist, type MylistItem, type MylistPage } from "./mygo";
import { EmbedPlayer } from "./player";

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

// Lookup state lives in this window only; nothing is persisted.
let currentInput = "";
let nextPage = 1;
let busy = false;
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

function renderItem(item: MylistItem): HTMLLIElement {
  const li = document.createElement("li");
  if (item.available && item.title) {
    li.textContent = item.title;
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

// selectItem replaces the official embed player with the chosen entry.
// Unavailable entries never reach here: they get no play button.
function selectItem(item: MylistItem): void {
  player?.dispose();
  playerStatus.textContent = "";
  const title = item.title ?? item.watchId;
  player = new EmbedPlayer(playerSlot, item.videoId ?? item.watchId, title, {
    onReady: () => {
      setPlayerControlsEnabled(true);
    },
    onPlayingChange: (next) => {
      setPlayingUI(next);
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
  nowPlaying.textContent = title;
  playing = false;
  positionMs = 0;
  durationMs = NaN;
  playerToggle.textContent = "再生";
  playerToggle.setAttribute("aria-pressed", "false");
  setPlayerControlsEnabled(false);
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
  if (!append) {
    items.replaceChildren();
  }
  items.append(...page.items.map(renderItem));
  result.hidden = false;
  more.hidden = !page.hasNext;
  const shown = items.childElementCount;
  status.textContent =
    page.items.length === 0 && shown === 0
      ? "マイリストに項目がありません。"
      : `全${page.totalItemCount}件中${shown}件を表示しています。`;
}

async function load(append: boolean): Promise<void> {
  if (busy) return;
  busy = true;
  submit.disabled = true;
  input.disabled = true;
  more.disabled = true;
  status.textContent = "読み込み中…";
  try {
    const page = await Mylist.getPage(currentInput, nextPage);
    nextPage += 1;
    showPage(page, append);
  } catch (err) {
    status.textContent = err instanceof Error ? err.message : String(err);
  } finally {
    busy = false;
    submit.disabled = false;
    input.disabled = false;
    more.disabled = false;
  }
}

form.addEventListener("submit", (e) => {
  e.preventDefault();
  if (busy) return;
  currentInput = input.value;
  nextPage = 1;
  result.hidden = true;
  more.hidden = true;
  disposePlayer();
  void load(false);
});

more.addEventListener("click", () => {
  void load(true);
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
