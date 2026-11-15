import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { setBaseUrl } from "./client";
import { useApi, usePolling } from "./hooks";

describe("API request ownership", () => {
  afterEach(() => {
    cleanup();
    setBaseUrl("");
    localStorage.clear();
    vi.useRealTimers();
  });

  it("keeps callbacks and the captured fetcher stable within a request scope", async () => {
    const first = vi.fn<() => Promise<string>>().mockResolvedValue("first");
    const next = vi.fn<() => Promise<string>>().mockResolvedValue("next");
    const { result, rerender } = renderHook(
      ({ fetcher }) => useApi("same", fetcher),
      { initialProps: { fetcher: first } }
    );
    await waitFor(() => expect(result.current.data).toBe("first"));
    const { refresh, setData } = result.current;
    rerender({ fetcher: next });
    expect(result.current.refresh).toBe(refresh);
    expect(result.current.setData).toBe(setData);
    await act(() => result.current.refresh());
    expect(first).toHaveBeenCalledTimes(2);
    expect(next).not.toHaveBeenCalled();
  });

  it("retains data on a failed refresh and clears its error while refreshing again", async () => {
    const failure = new Error("Read failed");
    const fetcher = vi
      .fn<() => Promise<string>>()
      .mockResolvedValue("retained");
    const { result } = renderHook(() => useApi("manual", fetcher));
    await waitFor(() => expect(result.current.data).toBe("retained"));
    fetcher.mockRejectedValueOnce(failure);
    await act(() => result.current.refresh());
    expect({
      data: result.current.data,
      error: result.current.error,
      loading: result.current.loading,
    }).toStrictEqual({ data: "retained", error: failure, loading: false });
    const pending = Promise.withResolvers<string>();
    fetcher.mockReturnValueOnce(pending.promise);
    let refreshed: Promise<void> | undefined;
    act(() => {
      refreshed = result.current.refresh();
    });
    expect({
      data: result.current.data,
      error: result.current.error,
      loading: result.current.loading,
    }).toStrictEqual({ data: "retained", error: null, loading: true });
    await act(async () => {
      pending.resolve("updated");
      await refreshed;
    });
    expect({
      data: result.current.data,
      error: result.current.error,
      loading: result.current.loading,
    }).toStrictEqual({ data: "updated", error: null, loading: false });
  });

  it("rejects a late response after the request key changes", async () => {
    const pending = Promise.withResolvers<string>();
    const oldFetcher = vi.fn<() => Promise<string>>(() => pending.promise);
    const newFetcher = vi.fn<() => Promise<string>>().mockResolvedValue("new");
    const { result, rerender } = renderHook(
      ({ requestKey, fetcher }) => useApi(requestKey, fetcher),
      { initialProps: { requestKey: "old", fetcher: oldFetcher } }
    );
    rerender({ requestKey: "new", fetcher: newFetcher });
    await waitFor(() => expect(result.current.data).toBe("new"));
    await act(async () => {
      pending.resolve("old");
      await pending.promise;
    });
    expect({
      data: result.current.data,
      error: result.current.error,
      loading: result.current.loading,
    }).toStrictEqual({ data: "new", error: null, loading: false });
    expect(oldFetcher).toHaveBeenCalledOnce();
    expect(newFetcher).toHaveBeenCalledOnce();
  });

  it("clears old-server data and refetches when the Core connection changes", async () => {
    const pending = Promise.withResolvers<string>();
    const fetcher = vi
      .fn<() => Promise<string>>()
      .mockResolvedValueOnce("old server")
      .mockReturnValueOnce(pending.promise);
    const { result } = renderHook(() => useApi("connection", fetcher));
    await waitFor(() => expect(result.current.data).toBe("old server"));
    act(() => setBaseUrl("http://other-core"));
    expect({
      data: result.current.data,
      error: result.current.error,
      loading: result.current.loading,
    }).toStrictEqual({ data: null, error: null, loading: true });
    await act(async () => {
      pending.resolve("new server");
      await pending.promise;
    });
    expect(result.current.data).toBe("new server");
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("skips polling ticks until the current request settles", async () => {
    vi.useFakeTimers();
    const pending = Promise.withResolvers<string>();
    const fetcher = vi
      .fn<() => Promise<string>>()
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValue("polled");
    const { result } = renderHook(() => usePolling("polling", fetcher, 1000));
    expect(fetcher).toHaveBeenCalledOnce();
    await act(() => vi.advanceTimersByTimeAsync(3000));
    expect(fetcher).toHaveBeenCalledOnce();
    await act(async () => {
      pending.resolve("first");
      await pending.promise;
    });
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(result.current.data).toBe("polled");
  });

  it("keeps polling after StrictMode replays mount effects", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn<() => Promise<string>>().mockResolvedValue("ready");
    const { result } = renderHook(() => usePolling("strict", fetcher, 1000), {
      wrapper: StrictMode,
    });
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(result.current.data).toBe("ready");
    const mountedReads = fetcher.mock.calls.length;
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(fetcher).toHaveBeenCalledTimes(mountedReads + 1);
  });
});
