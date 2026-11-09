import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import type { Resolved, TextData, TimelineData } from "../../bindings/types";
import { ViewDataProvider } from "../../data/provider";
import { StateTimeline, BoundStateTimeline } from "./StateTimeline";
import type { TimelineRow } from "./Lanes";
import type { TimelineStyle } from "./schema";
import { StateTimelineEditor } from "./Editor";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
const id = "ent_019a3e6a-4111-7000-8000-000000000001";
const start = Date.parse("2026-11-08T13:00:00Z"), end = start + 3_600_000;
const timeline: TimelineData = { from: start, to: end, window: "1h", intervals: [
  { kind: "unknown", from: start, to: start + 600_000 },
  { kind: "value", from: start + 600_000, to: start + 1_800_000, text: "Open", truthy: true },
  { kind: "value", from: start + 1_800_000, to: end, text: "Closed", truthy: false },
], totals: [{ text: "Open", truthy: true, seconds: 1200, entries: 0 }, { text: "Closed", truthy: false, seconds: 1800, entries: 1 }] };
const ready: Resolved<{ current?: TextData; timeline?: TimelineData }> = { status: "ready", metadata: { id, label: "Door", availability: "available", enabled: true }, data: { current: { text: "Closed", observed_at: new Date(end).toISOString() }, timeline } };
const row = (resource = ready): TimelineRow => ({ key: id, label: "Door", entityId: id, resource });

it.each(["lanes", "ledger", "feed"] as TimelineStyle[])("%s presents each card status", (style) => {
  const cases: Array<[Resolved<{ current?: TextData; timeline?: TimelineData }>, string]> = [
    [{ status: "loading" }, "Loading…"], [{ status: "missing" }, "Entity not found"],
    [{ status: "error", error: "Too many readings for this window. Choose a shorter one." }, "Too many readings for this window. Choose a shorter one."],
    [{ ...ready, status: "unobserved", data: { timeline } }, "Never observed"],
    [{ ...ready, status: "absent", data: { timeline } }, "No value"],
    [{ ...ready, metadata: { ...ready.metadata!, availability: "unavailable" } }, "Unavailable"],
    [{ ...ready, metadata: { ...ready.metadata!, enabled: false } }, "Disabled"],
    [{ ...ready, refreshFailed: true }, "Couldn't refresh"],
  ];
  for (const [resource, text] of cases) {
    const view = render(<StateTimeline title="Doors" window="1h" style={style} rows={[row(resource)]} />, { wrapper: MemoryRouter });
    expect(screen.getByText(text, { exact: false })).toBeTruthy();
    view.unmount();
  }
});

it("draws lanes with unknown spans and dimming, and exposes a text summary", () => {
  const unavailable = { ...ready, metadata: { ...ready.metadata!, availability: "unavailable" } };
  render(<StateTimeline title="Doors" window="1h" style="lanes" rows={[row(unavailable)]} />, { wrapper: MemoryRouter });
  expect(document.querySelector(".view-timeline-segment[data-unknown]")).toBeTruthy();
  expect(document.querySelector(".view-timeline-track[data-dimmed]")).toBeTruthy();
  expect(screen.getByText(/Door: Closed since/)).toBeTruthy();
  expect(document.querySelector(".view-timeline-track")?.getAttribute("aria-hidden")).toBe("true");
  expect(document.querySelector(".view-card-arrival")).toBeNull();
});

it("ledger omits since when current State disagrees with history", () => {
  const resource = { ...ready, data: { ...ready.data!, current: { text: "Open", observed_at: new Date(end).toISOString() } } };
  render(<StateTimeline title="Doors" window="1h" style="ledger" rows={[row(resource)]} />, { wrapper: MemoryRouter });
  expect(document.querySelector(".view-timeline-current")?.textContent).toBe("Open");
  expect(document.querySelector(".view-timeline-track-compact")).toBeTruthy();
  expect(document.querySelector(".view-timeline-axis")).toBeTruthy();
});

it("feed groups, pages twelve changes at a time, and names an empty window", () => {
  const days = Array.from({ length: 7 }, (_, i) => start - i * 86_400_000);
  const make = (offset: number): TimelineData => ({ from: start - 7 * 86_400_000, to: end, window: "7d", totals: [], intervals: days.flatMap(at => [0, 600_000].flatMap(add => [
    { kind: "value" as const, from: at + offset + add, to: at + offset + add + 60_000, text: "Open", truthy: true },
    { kind: "value" as const, from: at + offset + add + 60_000, to: at + offset + add + 120_000, text: "Closed", truthy: false },
  ])) });
  const rows = [0, 1].map((n): TimelineRow => ({ key: String(n), label: `Door ${n}`, entityId: `${id}${n}`, resource: { ...ready, data: { ...ready.data, timeline: make(n * 120_000) } } }));
  const view = render(<StateTimeline title="Doors" window="7d" style="feed" rows={rows} />, { wrapper: MemoryRouter });
  expect(document.querySelectorAll(".view-timeline-feed-entry")).toHaveLength(12);
  fireEvent.click(screen.getByRole("button", { name: "Show 16 earlier changes" }));
  expect(document.querySelectorAll(".view-timeline-feed-entry")).toHaveLength(24);
  fireEvent.click(screen.getByRole("button", { name: "Show 4 earlier changes" }));
  expect(document.querySelectorAll(".view-timeline-feed-entry")).toHaveLength(28);
  view.rerender(<StateTimeline title="Doors" window="24h" style="feed" rows={[row({ ...ready, data: { ...ready.data, timeline: { ...timeline, intervals: [] } } })]} />);
  expect(screen.getByText("No changes in the last 24 hours")).toBeTruthy();
});

it("glows only when a newer timeline changes the latest words", () => {
  const view = render(<StateTimeline title="Doors" window="1h" style="ledger" rows={[row()]} />, { wrapper: MemoryRouter });
  const changedState = { ...ready, data: { ...ready.data!, current: { text: "Open", observed_at: new Date(end + 1000).toISOString() } } };
  view.rerender(<StateTimeline title="Doors" window="1h" style="ledger" rows={[row(changedState)]} />);
  expect(document.querySelector(".view-card-arrival")).toBeNull();
  const newer = { ...timeline, to: end + 60_000, intervals: [...timeline.intervals.slice(0, -1), { kind: "value" as const, from: start + 1_800_000, to: end + 60_000, text: "Open", truthy: true }] };
  view.rerender(<StateTimeline title="Doors" window="1h" style="ledger" rows={[row({ ...changedState, data: { ...changedState.data, timeline: newer } })]} />);
  expect(document.querySelector(".view-card-arrival")).toBeTruthy();
});

it("does not replay a history change seen while an Entity was disabled", () => {
  const view = render(<StateTimeline title="Doors" window="1h" style="lanes" rows={[row()]} />, { wrapper: MemoryRouter });
  const newer = { ...timeline, to: end + 60_000, intervals: [...timeline.intervals.slice(0, -1), { kind: "value" as const, from: start + 1_800_000, to: end + 60_000, text: "Open", truthy: true }] };
  const changed = { ...ready, data: { ...ready.data!, timeline: newer } };
  view.rerender(<StateTimeline title="Doors" window="1h" style="lanes" rows={[row({ ...changed, metadata: { ...ready.metadata!, enabled: false } })]} />);
  expect(document.querySelector(".view-card-arrival")).toBeNull();
  view.rerender(<StateTimeline title="Doors" window="1h" style="lanes" rows={[row(changed)]} />);
  expect(document.querySelector(".view-card-arrival")).toBeNull();
});

it("surfaces a query-limit refusal from the real binding", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => String(url).includes("/state/changes")
    ? new Response(JSON.stringify({ title: "Limit", detail: "limit", code: "query_limit" }), { status: 422 })
    : new Response(JSON.stringify({ id, name: "Door", type: "hearth.power/v1", enabled: true, availability: { status: "available" }, state: { value: false, observed_at: new Date(end).toISOString() } })));
  render(<ViewDataProvider><BoundStateTimeline title="Doors" style="lanes" window="7d" rows={[{ source: { kind: "entity-state", entity_id: id, entity_type: "hearth.power/v1" } }]} /></ViewDataProvider>, { wrapper: MemoryRouter });
  const card = screen.getByRole("article", { name: "Doors" });
  expect(await within(card).findByText("Too many readings for this window. Choose a shorter one.")).toBeTruthy();
});

it("edits style, window and rows while offering only Entities with text fields", () => {
  const source = { kind: "entity-state" as const, entity_id: id, entity_type: "hearth.power/v1" };
  const value = { title: "Doors", style: "lanes" as const, window: "1h" as const, rows: [{ source }] };
  const choices = [{ id: `${id.slice(0, -1)}2`, name: "Switch", type: "hearth.power/v1" }, { id: `${id.slice(0, -1)}3`, name: "Temperature", type: "hearth.temperature/v1" }];
  const onChange = vi.fn();
  render(<StateTimelineEditor value={value} choices={choices} onChange={onChange} />);
  expect(screen.getByRole("option", { name: "Switch" })).toBeTruthy();
  expect(screen.queryByRole("option", { name: "Temperature" })).toBeNull();
  fireEvent.change(screen.getByLabelText("Style"), { target: { value: "feed" } });
  expect(onChange).toHaveBeenLastCalledWith({ ...value, style: "feed" });
  fireEvent.change(screen.getByLabelText("Window"), { target: { value: "7d" } });
  expect(onChange).toHaveBeenLastCalledWith({ ...value, window: "7d" });
  fireEvent.change(screen.getByLabelText("Add reading"), { target: { value: choices[0].id } });
  expect(onChange.mock.calls[2][0].rows).toHaveLength(2);
});

it("words short durations and counts only changes into a yes/no value", async () => {
  const { durationWords } = await import("../../bindings/changes");
  const { totalsText } = await import("./Lanes");
  expect(durationWords(10)).toBe("under 1 min");
  expect(durationWords(90)).toBe("2 min");
  expect(durationWords(3_900)).toBe("1 h 5 min");
  const base = { from: 0, to: 3_600_000, window: "1h" as const, intervals: [] };
  expect(totalsText({ ...base, totals: [
    { text: "Open", truthy: true, seconds: 2_580, entries: 7 }, { text: "Closed", truthy: false, seconds: 1_020, entries: 6 },
  ] }, "Open")).toBe("Open 43 min · 7×");
  expect(totalsText({ ...base, totals: [
    { text: "Heat", truthy: null, seconds: 2_400, entries: 2 }, { text: "Eco", truthy: null, seconds: 1_200, entries: 1 },
  ] })).toBe("Heat 40 min · Eco 20 min");
});

it("names no since time for a value held since before the window", () => {
  const held: TimelineData = { ...timeline, intervals: [{ kind: "value", from: start, to: end, text: "Closed", truthy: false }] };
  const resource = { ...ready, data: { ...ready.data!, timeline: held } };
  render(<StateTimeline title="Doors" window="1h" style="ledger" rows={[row(resource)]} />, { wrapper: MemoryRouter });
  expect(document.querySelector(".view-timeline-current")?.textContent).toBe("Closed");
  cleanup();
  render(<StateTimeline title="Doors" window="1h" style="ledger" rows={[row()]} />, { wrapper: MemoryRouter });
  expect(document.querySelector(".view-timeline-current")?.textContent).toMatch(/^Closedsince /);
});
