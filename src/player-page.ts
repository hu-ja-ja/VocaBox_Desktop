import { events, PlayerBridge, type MylistItem, type PlayerCommand, type PlayerReport } from "./mygo";
import { EmbedPlayer, previousRestarts } from "./player";
import { PlayQueue } from "./queue";

const slot = document.querySelector<HTMLElement>("#player-slot")!;
const queue = new PlayQueue<MylistItem>();
let items: MylistItem[] = [];
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
  const current = queue.current();
  const payload: PlayerReport = {
    type,
    session,
    sequence: ++sequence,
    watchId: current?.watchId ?? "",
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

function select(item: MylistItem, autoplay = false): void {
  if (queue.select(item) === null) return;
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

function handle(command: PlayerCommand): void {
  switch (command.action) {
    case "setItems":
      player?.dispose();
      player = null;
      items = (command.items ?? []).filter(playable);
      queue.reset(items);
      ready = false;
      playing = false;
      positionMs = 0;
      durationMs = NaN;
      error = "";
      report();
      break;
    case "replaceItems": {
      const current = queue.current();
      const shuffle = queue.isShuffle();
      const repeat = queue.repeat;
      items = (command.items ?? []).filter(playable);
      queue.reset(items);
      if (shuffle) queue.setShuffle(true);
      queue.repeat = repeat;
      const replacement = current && items.find((item) => item.watchId === current.watchId);
      if (replacement) {
        queue.select(replacement);
      } else {
        player?.dispose();
        player = null;
        ready = false;
        playing = false;
        positionMs = 0;
        durationMs = NaN;
      }
      error = "";
      report();
      break;
    }
    case "appendItems": {
      const more = (command.items ?? []).filter(playable);
      items.push(...more);
      queue.append(more);
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
      if (item) select(item, command.autoplay);
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
      if (item) select(item, true);
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
