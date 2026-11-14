import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ReadCache, type ReadRequest } from "./cache";

describe("shared view reads", () => {
  let cache: ReadCache;
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date("2026-11-01T10:00:13Z")); cache = new ReadCache(); });
  afterEach(() => { cache.dispose(); vi.useRealTimers(); });
  it("shares subscriptions, refreshes State at ten seconds, and disposes the final subscriber", async () => {
    const load = vi.fn().mockResolvedValue({ reading: 1 });
    const request: ReadRequest = { key: "state:one", kind: "state", load };
    const a = cache.subscribe(request, vi.fn()); const b = cache.subscribe(request, vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    expect(load).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(load).toHaveBeenCalledTimes(2);
    a(); await vi.advanceTimersByTimeAsync(10_000); expect(load).toHaveBeenCalledTimes(3);
    b(); await vi.advanceTimersByTimeAsync(20_000); expect(load).toHaveBeenCalledTimes(3);
  });
  it("re-reads a boosted State entry every second until every holder releases it", async () => {
    const boosted = vi.fn().mockResolvedValue(1), other = vi.fn().mockResolvedValue(2);
    cache.subscribe({ key: "state:cover", kind: "state", load: boosted }, vi.fn());
    cache.subscribe({ key: "state:other", kind: "state", load: other }, vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    const first = cache.boost("state:cover"), second = cache.boost("state:cover");
    await vi.advanceTimersByTimeAsync(3_000);
    expect(boosted).toHaveBeenCalledTimes(4);
    expect(other).toHaveBeenCalledTimes(1);
    first(); first();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(boosted).toHaveBeenCalledTimes(5);
    second();
    await vi.advanceTimersByTimeAsync(5_000);
    expect(boosted).toHaveBeenCalledTimes(5);
  });
  it("uses one minute boundary for all series and retains successful data after failure", async () => {
    const one = vi.fn().mockResolvedValue("retained"); const two = vi.fn().mockResolvedValue("other");
    cache.subscribe({ key: "series:one", kind: "series", load: one }, vi.fn());
    cache.subscribe({ key: "series:two", kind: "series", load: two }, vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    expect(one.mock.calls[0][1].toISOString()).toBe("2026-11-01T10:00:13.000Z");
    one.mockRejectedValue(new Error("network"));
    await vi.advanceTimersByTimeAsync(47_000);
    expect(one.mock.calls[1][1].toISOString()).toBe("2026-11-01T10:01:00.000Z");
    expect(two.mock.calls[1][1]).toEqual(one.mock.calls[1][1]);
    expect(cache.snapshot("series:one").data).toBe("retained");
    expect(cache.snapshot("series:one").error?.message).toBe("network");
  });
  it("pauses hidden refreshes, resumes immediately, and aborts an outstanding read on disposal", async () => {
    let visible = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visible as DocumentVisibilityState);
    const load = vi.fn().mockResolvedValue(1);
    cache.subscribe({ key: "state:one", kind: "state", load }, vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    visible = "hidden"; document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(60_000); expect(load).toHaveBeenCalledTimes(1);
    visible = "visible"; document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(0); expect(load).toHaveBeenCalledTimes(2);
    const pending = vi.fn((_signal: AbortSignal) => new Promise(() => {}));
    cache.subscribe({ key: "pending", kind: "state", load: pending }, vi.fn());
    cache.dispose(); expect(pending.mock.calls[0][0].aborted).toBe(true);
    vi.restoreAllMocks();
  });
});

describe("refreshing State after a Command", () => {
  let cache: ReadCache;
  beforeEach(() => { vi.useFakeTimers(); cache = new ReadCache(); });
  afterEach(() => { cache.dispose(); vi.useRealTimers(); vi.restoreAllMocks(); });

  function deferredLoad() {
    const resolves: Array<(value: unknown) => void> = [];
    const load = vi.fn(() => new Promise(resolve => resolves.push(resolve)));
    return { load, resolves };
  }

  it("reads every State entry at once and leaves series alone", async () => {
    const state = vi.fn().mockResolvedValue("state"); const series = vi.fn().mockResolvedValue("series");
    cache.subscribe({ key: "state:one", kind: "state", load: state }, vi.fn());
    cache.subscribe({ key: "series:one", kind: "series", load: series }, vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    const done = vi.fn();
    void cache.refreshState().then(done);
    await vi.advanceTimersByTimeAsync(0);
    expect(state).toHaveBeenCalledTimes(2);
    expect(series).toHaveBeenCalledTimes(1);
    expect(done).toHaveBeenCalled();
  });

  it("follows a read already in flight with one more, and resolves after it lands", async () => {
    const { load, resolves } = deferredLoad();
    cache.subscribe({ key: "state:one", kind: "state", load }, vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    expect(load).toHaveBeenCalledTimes(1);
    const done = vi.fn();
    void cache.refreshState().then(done);
    resolves[0]("before the change");
    await vi.advanceTimersByTimeAsync(0);
    expect(load).toHaveBeenCalledTimes(2);
    expect(done).not.toHaveBeenCalled();
    expect(cache.snapshot("state:one").data).toBe("before the change");
    resolves[1]("after the change");
    await vi.advanceTimersByTimeAsync(0);
    expect(cache.snapshot("state:one").data).toBe("after the change");
    expect(done).toHaveBeenCalled();
  });

  it("reads while the page is hidden and settles when a read fails", async () => {
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    const load = vi.fn().mockRejectedValue(new Error("network"));
    cache.subscribe({ key: "state:one", kind: "state", load }, vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    expect(load).not.toHaveBeenCalled();
    await expect(cache.refreshState()).resolves.toBeUndefined();
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("settles when the entry's last subscriber leaves during the read", async () => {
    const { load } = deferredLoad();
    const stop = cache.subscribe({ key: "state:one", kind: "state", load }, vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    const done = vi.fn();
    void cache.refreshState().then(done);
    stop();
    await vi.advanceTimersByTimeAsync(0);
    expect(done).toHaveBeenCalled();
  });
});
