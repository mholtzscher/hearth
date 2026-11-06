import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { CardFrame } from "./CardFrame";

const observedAt = "2026-11-04T19:40:00Z";
const sources = [{ entityId: "ent-temperature", label: "Temperature" }];

afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });

function mount(extra: Partial<Parameters<typeof CardFrame>[0]> = {}, fakeIntervals = false) {
  vi.useFakeTimers({ toFake: fakeIntervals ? ["Date", "setInterval", "clearInterval"] : ["Date"] });
  vi.setSystemTime(new Date("2026-11-06T19:42:00Z"));
  return render(<CardFrame title="Living room" status={[]} observedAt={observedAt} sources={sources} {...extra}>21.6 °C</CardFrame>, { wrapper: MemoryRouter });
}

it("shows only compact recency on the card and keeps exact dated evidence in Source", async () => {
  mount();
  const card = screen.getByRole("article", { name: "Living room" });
  expect(within(card).getByText("Observed 2d ago").getAttribute("dateTime")).toBe(observedAt);
  const trigger = within(card).getByRole("button", { name: "Source" });
  expect(screen.queryByRole("link", { name: "Temperature" })).toBeNull();
  fireEvent.click(trigger);
  const panel = screen.getByRole("dialog", { name: "Reading details" });
  expect(card.contains(panel)).toBe(false);
  expect(within(panel).getByText("Observed at")).toBeTruthy();
  expect(panel.querySelector("time")?.dateTime).toBe(observedAt);
  expect(panel.querySelector("time")?.textContent).toMatch(/2026/);
  expect(within(panel).getByRole("link", { name: "Temperature" }).getAttribute("href")).toBe("/entities/ent-temperature");
  expect(trigger.getAttribute("aria-expanded")).toBe("true");
  fireEvent.click(within(panel).getByRole("button", { name: "Close details" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(trigger));
});

it("advances recency while preserving the meaning of an oldest-reading footer", () => {
  mount({ observedAt: "2026-11-06T19:40:00Z", observedLabel: "Oldest reading" }, true);
  expect(screen.getByText("Oldest 2m ago").getAttribute("aria-label")).toBe("Oldest reading 2 minutes ago");
  act(() => vi.advanceTimersByTime(60_000));
  expect(screen.getByText("Oldest 3m ago")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Source" }));
  expect(screen.getByText("Oldest reading observed at")).toBeTruthy();
});

it("uses a modal sheet on phones and closes before guarded Source navigation", () => {
  vi.stubGlobal("matchMedia", () => ({ matches: true, addEventListener() {}, removeEventListener() {} }));
  mount({ sourceExtra: <p>Each point averages samples in a 15-minute interval.</p> });
  const trigger = screen.getByRole("button", { name: "Source" });
  fireEvent.click(trigger);
  const panel = screen.getByRole("dialog", { name: "Reading details" });
  expect(panel.getAttribute("aria-modal")).toBe("true");
  expect(panel.classList.contains("view-source-sheet")).toBe(true);
  expect(within(panel).getByText(/15-minute/)).toBeTruthy();
  fireEvent.click(within(panel).getByRole("link", { name: "Temperature" }));
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("keeps Source-only cards usable without inventing an observation time", () => {
  mount({ observedAt: undefined, sourceExtra: <p>History interval and method</p> });
  expect(screen.queryByText(/Observed/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Source" }));
  const panel = screen.getByRole("dialog", { name: "Source details" });
  expect(within(panel).getByText("History interval and method")).toBeTruthy();
  expect(panel.querySelector("time")).toBeNull();
});

it("labels an invalid observation time without inventing recency", () => {
  mount({ observedAt: "invalid" });
  expect(screen.getByText("Observation time unavailable")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Source" }));
  const panel = screen.getByRole("dialog", { name: "Reading details" });
  expect(within(panel).getByText("Unavailable")).toBeTruthy();
  expect(within(panel).queryByText("Observation age")).toBeNull();
});
