import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { groupByRecency, useLocalDay } from "./recency";

afterEach(() => vi.useRealTimers());

const local = (...parts: [number, number, number, number?, number?]) => new Date(parts[0], parts[1], parts[2], parts[3] ?? 0, parts[4] ?? 0).toISOString();
const at = (id: string, time: string) => ({ id, created_at: "", last_message_at: time });
const labels = (groups: { label: string; items: { id: string }[] }[]) => groups.map(group => [group.label, group.items.map(item => item.id)]);

it("groups by local calendar day boundaries, keeping input order", () => {
  const now = new Date(2026, 10, 11, 9, 30);
  const groups = groupByRecency([
    at("later-today", local(2026, 10, 11, 12)),
    at("today", local(2026, 10, 11, 0, 5)),
    at("yesterday", local(2026, 10, 10, 23, 59)),
    at("week-edge", local(2026, 10, 4)),
    at("before-week", local(2026, 10, 3, 23, 59)),
    at("month-edge", local(2026, 9, 12)),
    at("october", local(2026, 9, 11, 23)),
    at("january", local(2026, 0, 2)),
    at("last-year", local(2025, 11, 1)),
  ], now);
  const month = (year: number, index: number, withYear: boolean) => new Date(year, index, 1).toLocaleDateString(undefined, withYear ? { month: "long", year: "numeric" } : { month: "long" });
  expect(labels(groups)).toEqual([
    ["Today", ["later-today", "today"]],
    ["Yesterday", ["yesterday"]],
    ["Previous 7 days", ["week-edge"]],
    ["Previous 30 days", ["before-week", "month-edge"]],
    [month(2026, 9, false), ["october"]],
    [month(2026, 0, false), ["january"]],
    [month(2025, 11, true), ["last-year"]],
  ]);
});

it("falls back to creation time and puts unreadable times last", () => {
  const now = new Date(2026, 10, 11, 9, 30);
  const groups = groupByRecency([
    { id: "untouched", created_at: local(2026, 10, 11, 8), last_message_at: null },
    { id: "unknown", created_at: "" },
    at("yesterday", local(2026, 10, 10, 8)),
  ], now);
  expect(labels(groups)).toEqual([["Today", ["untouched"]], ["Yesterday", ["yesterday"]], ["Earlier", ["unknown"]]]);
});

it("advances the local day at midnight and when a suspended tab returns", () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(2026, 10, 11, 23, 59, 30));
  const { result } = renderHook(() => useLocalDay());
  expect(result.current).toBe(new Date(2026, 10, 11).getTime());
  act(() => { vi.advanceTimersByTime(30_000); });
  expect(result.current).toBe(new Date(2026, 10, 12).getTime());
  // A suspended tab's timer may never fire; becoming visible catches up.
  act(() => {
    vi.setSystemTime(new Date(2026, 10, 14, 8));
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect(result.current).toBe(new Date(2026, 10, 14).getTime());
});
