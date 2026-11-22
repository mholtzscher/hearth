import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { formatDuration, usePlayhead } from "./playhead";

describe("media playhead", () => {
  it("advances from the anchor, clamps to duration, and freezes with a paused observation", () => {
    vi.useFakeTimers();
    try {
      const anchoredAt = "2026-11-09T10:00:00Z";
      vi.setSystemTime(new Date("2026-11-09T10:00:01Z"));
      const initialProps = {
        anchor: { seconds: 10, anchoredAt },
        playing: true,
        durationSeconds: 13,
      };
      const { result, rerender, unmount } = renderHook(usePlayhead, {
        initialProps,
      });
      expect(result.current).toBe(11);
      act(() => vi.advanceTimersByTime(1000));
      expect(result.current).toBe(12);
      act(() => vi.advanceTimersByTime(5000));
      expect(result.current).toBe(13);

      rerender({
        ...initialProps,
        anchor: { seconds: 12, anchoredAt: new Date().toISOString() },
        playing: false,
      });
      act(() => vi.advanceTimersByTime(5000));
      expect(result.current).toBe(12);
      expect(vi.getTimerCount()).toBe(0);
      unmount();
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not invent a position without an anchor", () => {
    const { result } = renderHook(() =>
      usePlayhead({ playing: true, durationSeconds: 20 })
    );
    expect(result.current).toBeUndefined();
  });

  it.each([
    [0, "0:00"],
    [65, "1:05"],
    [3661, "1:01:01"],
  ])("formats %s seconds as %s", (seconds, text) => {
    expect(formatDuration(seconds)).toBe(text);
  });
});
