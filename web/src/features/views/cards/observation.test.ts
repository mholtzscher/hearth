import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ARRIVAL_GLOW_MS, observationAge, useArrival } from "./observation";

const observed = "2026-11-05T22:00:00Z";
const at = (minutes: number) => Date.parse(observed) + minutes * 60_000;
const later = (minutes: number) => new Date(at(minutes)).toISOString();

afterEach(() => vi.useRealTimers());

describe("observationAge", () => {
  it("names recent ages in words", () => {
    expect(observationAge(observed, at(0.5))).toBe("just now");
    expect(observationAge(observed, at(3))).toBe("3 min ago");
    expect(observationAge(observed, at(150))).toBe("2 h ago");
  });

  it("leaves day-old observations to the absolute time", () => {
    expect(observationAge(observed, at(24 * 60))).toBeUndefined();
  });
});

describe("useArrival", () => {
  it("does not glow for the first observation a card shows", () => {
    const { result } = renderHook(({ value }) => useArrival(value), { initialProps: { value: observed as string | undefined } });
    expect(result.current).toBeUndefined();
  });

  it("glows when a newer observation arrives, for one minute", () => {
    vi.useFakeTimers();
    const { result, rerender } = renderHook(({ value }) => useArrival(value), { initialProps: { value: observed as string | undefined } });
    rerender({ value: later(1) });
    expect(result.current).toBe(1);
    act(() => { vi.advanceTimersByTime(ARRIVAL_GLOW_MS - 1); });
    expect(result.current).toBe(1);
    act(() => { vi.advanceTimersByTime(1); });
    expect(result.current).toBeUndefined();
  });

  it("restarts with a new key for each later arrival", () => {
    const { result, rerender } = renderHook(({ value }) => useArrival(value), { initialProps: { value: observed as string | undefined } });
    rerender({ value: later(1) });
    rerender({ value: later(2) });
    expect(result.current).toBe(2);
  });

  it("ignores an older or repeated observation and an ineligible gap", () => {
    const { result, rerender } = renderHook(({ value }) => useArrival(value), { initialProps: { value: later(5) as string | undefined } });
    rerender({ value: later(5) });
    rerender({ value: later(1) });
    rerender({ value: undefined });
    rerender({ value: later(5) });
    expect(result.current).toBeUndefined();
  });
});
