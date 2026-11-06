import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ARRIVAL_GLOW_MS, observationAge, useArrival, type Arrival } from "./observation";

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
  type Props = { value: Arrival | undefined };
  const reading = (minutes: number, shown: string): Arrival => ({ updatedAt: later(minutes), shown });
  const mount = (value: Arrival | undefined) => renderHook(({ value }: Props) => useArrival(value), { initialProps: { value } });

  it("does not glow for the first reading a card shows", () => {
    expect(mount(reading(0, "21.5 °C")).result.current).toBeUndefined();
  });

  it("glows when a newer observation changes the shown value, for one minute", () => {
    vi.useFakeTimers();
    const { result, rerender } = mount(reading(0, "21.5 °C"));
    rerender({ value: reading(1, "21.6 °C") });
    expect(result.current).toBe(1);
    act(() => { vi.advanceTimersByTime(ARRIVAL_GLOW_MS - 1); });
    expect(result.current).toBe(1);
    act(() => { vi.advanceTimersByTime(1); });
    expect(result.current).toBeUndefined();
  });

  it("stays dark when a sensor re-reports the same value", () => {
    const { result, rerender } = mount(reading(0, "21.5 °C"));
    rerender({ value: reading(1, "21.5 °C") });
    rerender({ value: reading(2, "21.5 °C") });
    expect(result.current).toBeUndefined();
  });

  it("restarts with a new key for each later change", () => {
    const { result, rerender } = mount(reading(0, "21.5 °C"));
    rerender({ value: reading(1, "21.6 °C") });
    rerender({ value: reading(2, "21.7 °C") });
    expect(result.current).toBe(2);
  });

  it("ignores an older observation, an ineligible gap, and a unit change without a new observation", () => {
    const { result, rerender } = mount(reading(5, "21.5 °C"));
    rerender({ value: reading(1, "20.0 °C") });
    rerender({ value: undefined });
    rerender({ value: reading(5, "70.7 °F") });
    // The next report matches what the card already shows in the new unit.
    rerender({ value: reading(6, "70.7 °F") });
    expect(result.current).toBeUndefined();
  });

  it("clears an active glow on ineligibility without replaying it on recovery", () => {
    vi.useFakeTimers();
    const { result, rerender } = mount(reading(0, "21.5 °C"));
    rerender({ value: reading(1, "21.6 °C") });
    expect(result.current).toBe(1);
    act(() => { vi.advanceTimersByTime(10_000); });
    rerender({ value: undefined });
    expect(result.current).toBeUndefined();
    rerender({ value: reading(1, "21.6 °C") });
    expect(result.current).toBeUndefined();
    rerender({ value: reading(2, "21.7 °C") });
    expect(result.current).toBe(2);
    // The cancelled first glow must not end the later glow early.
    act(() => { vi.advanceTimersByTime(50_000); });
    expect(result.current).toBe(2);
    act(() => { vi.advanceTimersByTime(10_000); });
    expect(result.current).toBeUndefined();
  });
});
