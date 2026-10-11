import { describe, expect, test } from "bun:test";
import {
  EMBED_ORIGIN,
  buildEmbedUrl,
  createPlayerId,
  isPlayerEvent,
  pauseCommand,
  playCommand,
  playerStatusToPlaying,
  seekCommand,
  shouldFireEnded,
  volumeCommand,
} from "../src/player";

const PLAYER_ID = "vocabox-1";

function message(overrides: Record<string, unknown> = {}) {
  return {
    origin: EMBED_ORIGIN,
    source: "window",
    data: { playerId: PLAYER_ID, sourceConnectorType: 0, ...overrides },
  };
}

describe("buildEmbedUrl", () => {
  test("uses the first-party embed route with jsapi and playerId", () => {
    expect(buildEmbedUrl("sm9", PLAYER_ID)).toBe(
      "https://embed.nicovideo.jp/watch/sm9?jsapi=1&playerId=vocabox-1&w=640px&h=360",
    );
  });

  test("encodes videoId and playerId", () => {
    expect(buildEmbedUrl("a/b", "c&d")).toBe(
      "https://embed.nicovideo.jp/watch/a%2Fb?jsapi=1&playerId=c%26d&w=640px&h=360",
    );
  });
});

describe("createPlayerId", () => {
  test("returns unique ids", () => {
    expect(createPlayerId()).not.toBe(createPlayerId());
  });
});

describe("commands", () => {
  test("play and pause carry connector type 1 and the playerId", () => {
    expect(playCommand(PLAYER_ID)).toEqual({
      eventName: "play",
      sourceConnectorType: 1,
      playerId: PLAYER_ID,
    });
    expect(pauseCommand(PLAYER_ID)).toEqual({
      eventName: "pause",
      sourceConnectorType: 1,
      playerId: PLAYER_ID,
    });
  });

  test("seek takes milliseconds clamped to >= 0", () => {
    expect(seekCommand(PLAYER_ID, 10250.6).data).toEqual({ time: 10251 });
    expect(seekCommand(PLAYER_ID, -5).data).toEqual({ time: 0 });
  });

  test("volume takes 0..1 clamped", () => {
    expect(volumeCommand(PLAYER_ID, 0.5).data).toEqual({ volume: 0.5 });
    expect(volumeCommand(PLAYER_ID, 1.5).data).toEqual({ volume: 1 });
    expect(volumeCommand(PLAYER_ID, -0.2).data).toEqual({ volume: 0 });
  });

  test("no builder produces the manual view-count command", () => {
    const payloads = [
      playCommand(PLAYER_ID),
      pauseCommand(PLAYER_ID),
      seekCommand(PLAYER_ID, 1000),
      volumeCommand(PLAYER_ID, 0.5),
    ];
    for (const p of payloads) {
      expect(JSON.stringify(p)).not.toContain("incrementViewCount");
    }
  });
});

describe("playerStatusToPlaying", () => {
  test("maps the community status numbering without claiming unknowns", () => {
    expect(playerStatusToPlaying(2)).toBe(true);
    expect(playerStatusToPlaying(3)).toBe(false);
    expect(playerStatusToPlaying(4)).toBe(false);
    expect(playerStatusToPlaying(1)).toBeNull();
    expect(playerStatusToPlaying(0)).toBeNull();
    expect(playerStatusToPlaying("2")).toBeNull();
    expect(playerStatusToPlaying(undefined)).toBeNull();
  });
});

describe("shouldFireEnded", () => {
  test("one END transition fires once across duplicate status aliases", () => {
    // playerStatusChange(END) then statusChange(END) for one transition.
    let last: number | null = null;
    let fires = 0;
    for (const status of [4, 4]) {
      if (shouldFireEnded(last, status)) fires += 1;
      last = status;
    }
    expect(fires).toBe(1);
    // A new END after leaving END fires again; non-END never fires.
    expect(shouldFireEnded(4, 4)).toBe(false);
    expect(shouldFireEnded(2, 4)).toBe(true);
    expect(shouldFireEnded(null, 4)).toBe(true);
    expect(shouldFireEnded(null, 2)).toBe(false);
    expect(shouldFireEnded(4, 2)).toBe(false);
  });
});

describe("isPlayerEvent", () => {
  test("accepts the iframe's own event", () => {
    expect(isPlayerEvent(message(), "window", PLAYER_ID)).toBe(true);
  });

  test("rejects other origins, sources, players, and parent commands", () => {
    expect(isPlayerEvent(message(), "other-window", PLAYER_ID)).toBe(false);
    expect(isPlayerEvent(message(), null, PLAYER_ID)).toBe(false);
    expect(isPlayerEvent(message(), "window", "vocabox-2")).toBe(false);
    expect(isPlayerEvent(message({ sourceConnectorType: 1 }), "window", PLAYER_ID)).toBe(false);
    expect(isPlayerEvent(message({ playerId: undefined }), "window", PLAYER_ID)).toBe(false);
    expect(
      isPlayerEvent({ ...message(), origin: "https://nicovideo.jp" }, "window", PLAYER_ID),
    ).toBe(false);
  });

  test("rejects malformed payloads", () => {
    expect(isPlayerEvent({ ...message(), data: null }, "window", PLAYER_ID)).toBe(false);
    expect(isPlayerEvent({ ...message(), data: "play" }, "window", PLAYER_ID)).toBe(false);
    expect(isPlayerEvent({ ...message(), data: {} }, "window", PLAYER_ID)).toBe(false);
  });
});
