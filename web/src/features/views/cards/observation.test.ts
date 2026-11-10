import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ARRIVAL_GLOW_MS, observationAge, useArrival, type Arrival } from "./observation";

const observed = "2026-11-05T22:00:00Z";
const at = (minutes: number) => Date.parse(observed) + minutes * 60_000;
const later = (minutes: number) => new Date(at(minutes)).toISOString();

afterEach(() => vi.useRealTimers());

describe("observationAge", () => {
  it("keeps card ages compact at minute, hour, and day boundaries", () => {
    expect(observationAge(observed, at(0.5))).toBe("just now");
    expect(observationAge(observed, at(3))).toBe("3m ago");
    expect(observationAge(observed, at(59))).toBe("59m ago");
    expect(observationAge(observed, at(60))).toBe("1h ago");
    expect(observationAge(observed, at(150))).toBe("2h ago");
    expect(observationAge(observed, at(24 * 60))).toBe("1d ago");
    expect(observationAge(observed, at(45 * 24 * 60))).toBe("45d ago");
  });

  it("does not manufacture an age for invalid evidence", () => {
    expect(observationAge("invalid", at(1))).toBeUndefined();
  });

  it("uses full words for accessible labels and Source details", () => {
    expect(observationAge(observed, at(1), "long")).toBe("1 minute ago");
    expect(observationAge(observed, at(2), "long")).toBe("2 minutes ago");
    expect(observationAge(observed, at(60), "long")).toBe("1 hour ago");
    expect(observationAge(observed, at(150), "long")).toBe("2 hours ago");
    expect(observationAge(observed, at(1440), "long")).toBe("1 day ago");
    expect(observationAge(observed, at(2880), "long")).toBe("2 days ago");
  });
});

describe("useArrival", () => {
  type Props = { value: Arrival | undefined };
  const reading = (minutes: number, shown: string): Arrival => ({ updatedAt: later(minutes), shown });
  const mount = (value: Arrival | undefined) => renderHook(({ value }: Props) => useArrival(value), { initialProps: { value } });

  it("does not glow for the first reading a card shows", () => {
    expect(mount(reading(0, "21.5 °C")).result.current).toBeUndefined();
  });

  it("glows when a newer observation changes the shown value, for the glow duration", () => {
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
    act(() => { vi.advanceTimersByTime(ARRIVAL_GLOW_MS / 2); });
    rerender({ value: undefined });
    expect(result.current).toBeUndefined();
    rerender({ value: reading(1, "21.6 °C") });
    expect(result.current).toBeUndefined();
    rerender({ value: reading(2, "21.7 °C") });
    expect(result.current).toBe(2);
    // The cancelled first glow must not end the later glow early.
    act(() => { vi.advanceTimersByTime(ARRIVAL_GLOW_MS - 1); });
    expect(result.current).toBe(2);
    act(() => { vi.advanceTimersByTime(1); });
    expect(result.current).toBeUndefined();
  });
});
