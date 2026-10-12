import { events, PlayerBridge, type MylistItem, type PlayerCommand, type PlayerReport } from "./mygo";
import { EmbedPlayer, findTrack, previousRestarts } from "./player";
import { PlayQueue } from "./queue";

const slot = document.querySelector<HTMLElement>("#player-slot")!;
const queue = new PlayQueue<MylistItem>();
// items and listId are the list shown: they answer a row click. The queue is
// the list playing, which another list opening does not take over.
let items: MylistItem[] = [];
let listId = "";
let queueId = "";
// current is the track playing, apart from the queue: it keeps playing, and
// its metadata keeps being reported, while the shown list changes.
let current: MylistItem | null = null;
let player: EmbedPlayer | null = null;
let ready = false;
let playing = false;
let positionMs = 0;
let durationMs = NaN;
let volume = 1;
let error = "";
const session = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
let sequence = 0;

function playable(item: MylistItem): boolean {
  return item.available && !!item.title;
}

function report(type = "state"): void {
  const payload: PlayerReport = {
    type,
    session,
    sequence: ++sequence,
    watchId: current?.watchId ?? "",
    listId: queueId,
    title: current?.title ?? "",
    uploader: current?.uploader ?? "",
    thumbnailUrl: current?.thumbnailUrl ?? "",
    ready,
    playing,
    canPrev: queue.hasPrev(),
    canNext: queue.hasLoadedNext(),
    shuffle: queue.isShuffle(),
    repeat: queue.repeat,
    positionMs,
    durationMs: Number.isFinite(durationMs) ? durationMs : 0,
    volume,
    error,
  };
  void PlayerBridge.report(payload).catch((cause: unknown) => console.error("player bridge:", cause));
}

// adoptQueue makes the shown list the playback queue, keeping shuffle,
// repeat, and the track playing when the list still holds it. Items arrive
// as fresh objects with every command, so the track is matched by ID.
function adoptQueue(): void {
  const shuffle = queue.isShuffle();
  const repeat = queue.repeat;
  queue.reset(items);
  if (shuffle) queue.setShuffle(true);
  queue.repeat = repeat;
  queueId = listId;
  if (current !== null) {
    const again = findTrack(items, current.watchId);
    if (again !== null) {
      queue.select(again);
      current = again;
    }
  }
}

// play starts item from its beginning in a fresh embed player; the caller
// has positioned the queue on it.
function play(item: MylistItem, autoplay: boolean): void {
  current = item;
  player?.dispose();
  ready = false;
  playing = false;
  positionMs = 0;
  durationMs = NaN;
  error = "";
  let autoStarted = false;
  const next = new EmbedPlayer(slot, item.videoId ?? item.watchId, item.title ?? item.watchId, {
    onReady: () => {
      ready = true;
      next.setVolume(volume);
      if (autoplay && !autoStarted) {
        autoStarted = true;
        next.play();
      }
      report();
    },
    onPlayingChange: (value) => {
      playing = value;
      report();
    },
    onEnded: () => report("ended"),
    onError: () => {
      ready = false;
      error = "動画を読み込めませんでした。";
      report();
    },
    onMetadata: (position, duration) => {
      positionMs = position;
      durationMs = duration;
      report();
    },
    onVolume: (value) => {
      volume = Math.min(1, Math.max(0, value));
      report();
    },
  });
  player = next;
  report();
}

// select plays a track of the shown list; a queue owned by another list
// moves to the shown one first, so the track's neighbours come from there.
function select(item: MylistItem, autoplay = false): void {
  if (queueId !== listId) adoptQueue();
  if (queue.select(item) === null) return;
  play(item, autoplay);
}

function handle(command: PlayerCommand): void {
  switch (command.action) {
    case "setItems":
      listId = command.id ?? "";
      items = (command.items ?? []).filter(playable);
      // Opening a list shows its tracks without touching the queue playing:
      // the track keeps playing, and keeps its list, until a track of the
      // shown list is played.
      if (current === null || queueId === listId) adoptQueue();
      report();
      break;
    case "appendItems": {
      const more = (command.items ?? []).filter(playable);
      items.push(...more);
      if (queueId === listId) queue.append(more);
      report();
      break;
    }
    case "select": {
      const item = items.find((entry) => entry.watchId === command.watchId);
      if (item) select(item, command.autoplay);
      break;
    }
    case "next": {
      const item = queue.moveNext();
      if (item) play(item, command.autoplay ?? false);
      else report("needsNext");
      break;
    }
    case "previous": {
      // Past the opening of the track, the button restarts it; pressing it
      // again from the start then goes to the one before.
      if (previousRestarts(positionMs)) {
        player?.seek(0);
        positionMs = 0;
        report();
        break;
      }
      const item = queue.movePrev();
      if (item) play(item, true);
      report();
      break;
    }
    case "play":
      player?.play();
      break;
    case "pause":
      player?.pause();
      break;
    case "seek":
      player?.seek(command.positionMs ?? 0);
      break;
    case "volume":
      volume = command.volume ?? 1;
      player?.setVolume(volume);
      report();
      break;
    case "shuffle":
      queue.setShuffle(command.enabled ?? false);
      report();
      break;
    case "repeat":
      queue.repeat = command.enabled ?? false;
      report();
      break;
  }
}

declare global {
  interface Window {
    vocaboxPlayer: { handle(command: PlayerCommand): void };
  }
}

window.vocaboxPlayer = { handle };
events.playerCommand.on(handle);
report("ready");
