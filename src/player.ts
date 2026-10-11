// Minimal local wrapper over the official embed player's
// reverse-engineered postMessage protocol, which is not officially
// documented. It never sends incrementViewCount: whether normal embed
// playback increments view counts is unverified, so nothing here promises it.
export const EMBED_ORIGIN = "https://embed.nicovideo.jp";

// sourceConnectorType: 1 for parent commands, 0 for player events.
const CONNECTOR_COMMAND = 1;
const CONNECTOR_EVENT = 0;

// Embed frame size; responsive CSS may scale it down.
const EMBED_WIDTH = 640;
const EMBED_HEIGHT = 360;

let nextPlayerId = 0;

// createPlayerId returns a fresh playerId for one iframe.
export function createPlayerId(): string {
  nextPlayerId += 1;
  return `vocabox-${nextPlayerId}`;
}

// buildEmbedUrl returns the first-party embed URL for a video. The player
// stays visible with its native controls; nothing is autoplayed.
export function buildEmbedUrl(videoId: string, playerId: string): string {
  return (
    `${EMBED_ORIGIN}/watch/${encodeURIComponent(videoId)}` +
    `?jsapi=1&playerId=${encodeURIComponent(playerId)}&w=${EMBED_WIDTH}px&h=${EMBED_HEIGHT}`
  );
}

export interface PlayerCommand {
  eventName: "play" | "pause" | "seek" | "volumeChange";
  data?: { time?: number; volume?: number };
  sourceConnectorType: 1;
  playerId: string;
}

function command(
  playerId: string,
  eventName: PlayerCommand["eventName"],
  data?: PlayerCommand["data"],
): PlayerCommand {
  return data === undefined
    ? { eventName, sourceConnectorType: CONNECTOR_COMMAND, playerId }
    : { eventName, data, sourceConnectorType: CONNECTOR_COMMAND, playerId };
}

// playCommand/pauseCommand need no payload.
export function playCommand(playerId: string): PlayerCommand {
  return command(playerId, "play");
}

// pauseCommand needs no payload.
export function pauseCommand(playerId: string): PlayerCommand {
  return command(playerId, "pause");
}

// seekCommand takes milliseconds, clamped to >= 0.
export function seekCommand(playerId: string, timeMs: number): PlayerCommand {
  return command(playerId, "seek", { time: Math.max(0, Math.round(timeMs)) });
}

// volumeCommand takes 0..1, clamped.
export function volumeCommand(playerId: string, volume: number): PlayerCommand {
  const v = Number.isFinite(volume) ? volume : 0;
  return command(playerId, "volumeChange", { volume: Math.min(1, Math.max(0, v)) });
}

// isPlayerEvent accepts only messages from the exact embed origin, sent by
// the given iframe, carrying the matching playerId as a player event.
export function isPlayerEvent(
  e: Pick<MessageEvent, "origin" | "source" | "data">,
  source: MessageEventSource | null,
  playerId: string,
): boolean {
  if (e.origin !== EMBED_ORIGIN) return false;
  if (e.source === null || e.source !== source) return false;
  const data = e.data;
  if (typeof data !== "object" || data === null) return false;
  const d = data as Record<string, unknown>;
  return d["playerId"] === playerId && d["sourceConnectorType"] === CONNECTOR_EVENT;
}

export interface PlayerCallbacks {
  onReady?: () => void;
  onPlayingChange?: (playing: boolean) => void;
  onError?: () => void;
  onMetadata?: (currentTimeMs: number, durationMs: number) => void;
  onVolume?: (volume: number) => void;
}

// playerStatusToPlaying maps a player status to a playing flag using the
// community-documented numbering (LOADING=1, PLAYING=2, PAUSE=3, END=4),
// which is unofficial like the rest of this protocol. It returns null when
// the status makes no claim, so callers never assert a state they did not hear.
export function playerStatusToPlaying(playerStatus: unknown): boolean | null {
  if (playerStatus === 2) return true;
  if (playerStatus === 3 || playerStatus === 4) return false;
  return null;
}

// EmbedPlayer owns one visible iframe and its message listener. dispose()
// removes both; call it before switching items.
export class EmbedPlayer {
  private readonly playerId: string;
  private readonly iframe: HTMLIFrameElement;
  private readonly onMessage: (e: MessageEvent) => void;

  constructor(slot: HTMLElement, videoId: string, title: string, cb: PlayerCallbacks = {}) {
    this.playerId = createPlayerId();
    const iframe = document.createElement("iframe");
    iframe.src = buildEmbedUrl(videoId, this.playerId);
    iframe.title = `ニコニコ動画: ${title}`;
    iframe.width = String(EMBED_WIDTH);
    iframe.height = String(EMBED_HEIGHT);
    // Permissions only; playback still starts from an explicit user action.
    iframe.allow = "autoplay; encrypted-media; fullscreen; picture-in-picture";
    slot.replaceChildren(iframe);
    this.iframe = iframe;
    this.onMessage = (e: MessageEvent) => {
      if (!isPlayerEvent(e, iframe.contentWindow, this.playerId)) return;
      const data = e.data as { eventName?: unknown; data?: unknown };
      if (data.eventName === "error") {
        cb.onError?.();
      } else if (data.eventName === "loadComplete") {
        cb.onReady?.();
      } else if (data.eventName === "playerStatusChange" || data.eventName === "statusChange") {
        const st = data.data as { playerStatus?: unknown } | null;
        const playing = playerStatusToPlaying(
          typeof st === "object" && st !== null ? st.playerStatus : undefined,
        );
        if (playing !== null) cb.onPlayingChange?.(playing);
      } else if (data.eventName === "playerMetadataChange") {
        const meta = data.data as {
          currentTime?: unknown;
          duration?: unknown;
          volume?: unknown;
          isVideoMetaDataLoaded?: unknown;
        } | null;
        if (
          meta !== null &&
          typeof meta === "object" &&
          typeof meta.currentTime === "number" &&
          typeof meta.duration === "number" &&
          Number.isFinite(meta.currentTime) &&
          Number.isFinite(meta.duration)
        ) {
          // currentTime/duration are milliseconds per community docs.
          cb.onMetadata?.(meta.currentTime, meta.duration);
          if (typeof meta.volume === "number" && Number.isFinite(meta.volume)) {
            cb.onVolume?.(meta.volume);
          }
          if (meta.isVideoMetaDataLoaded === true) cb.onReady?.();
        }
      }
    };
    window.addEventListener("message", this.onMessage);
  }

  private post(cmd: PlayerCommand): void {
    this.iframe.contentWindow?.postMessage(cmd, EMBED_ORIGIN);
  }

  play(): void {
    this.post(playCommand(this.playerId));
  }

  pause(): void {
    this.post(pauseCommand(this.playerId));
  }

  seek(timeMs: number): void {
    this.post(seekCommand(this.playerId, timeMs));
  }

  setVolume(volume: number): void {
    this.post(volumeCommand(this.playerId, volume));
  }

  dispose(): void {
    window.removeEventListener("message", this.onMessage);
    this.iframe.remove();
  }
}
