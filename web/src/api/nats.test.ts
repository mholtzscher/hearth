import { afterEach, describe, expect, it, vi } from "vitest";
import { getNatsWsUrl, setNatsWsUrl } from "./nats.ts";

afterEach(() => {
  localStorage.clear();
  vi.unstubAllEnvs();
});

describe("getNatsWsUrl", () => {
  it("uses the worktree WebSocket listener supplied to Vite", () => {
    vi.stubEnv("VITE_NATS_WS_URL", "ws://127.0.0.1:4283");
    expect(getNatsWsUrl()).toBe("ws://127.0.0.1:4283");
  });

  it("keeps a saved browser override ahead of the worktree default", () => {
    vi.stubEnv("VITE_NATS_WS_URL", "ws://127.0.0.1:4283");
    setNatsWsUrl("ws://127.0.0.1:9999/");
    expect(getNatsWsUrl()).toBe("ws://127.0.0.1:9999");
  });

  it("keeps the standalone dashboard default without a stack URL", () => {
    vi.stubEnv("VITE_NATS_WS_URL", undefined);
    expect(getNatsWsUrl()).toBe("ws://127.0.0.1:4223");
  });
});
