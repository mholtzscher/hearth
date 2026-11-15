import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { DataCard, StatusList } from "./data-card";

const observedAt = "2026-11-04T19:40:00Z";
const sources = [{ entityId: "ent-temperature", label: "Temperature" }];

const mount = (
  extra: Partial<Parameters<typeof DataCard>[0]> = {},
  fakeIntervals = false
) => {
  vi.useFakeTimers({
    toFake: fakeIntervals ? ["Date", "setInterval", "clearInterval"] : ["Date"],
  });
  vi.setSystemTime(new Date("2026-11-06T19:42:00Z"));
  return render(
    <DataCard
      title="Living room"
      status={[]}
      observedAt={observedAt}
      sources={sources}
      {...extra}
    >
      21.6 °C
    </DataCard>,
    { wrapper: MemoryRouter }
  );
};

describe("card source", () => {
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("shows only compact recency on the card", () => {
    mount();
    const card = screen.getByRole("article", { name: "Living room" });
    expect(
      within(card).getByText("Observed 2d ago").getAttribute("dateTime")
    ).toBe(observedAt);
    expect(screen.queryByRole("link", { name: "Temperature" })).toBeNull();
  });

  it("keeps exact dated evidence in the Source panel", () => {
    mount();
    const card = screen.getByRole("article", { name: "Living room" });
    fireEvent.click(within(card).getByRole("button", { name: "Source" }));
    const panel = screen.getByRole("dialog", { name: "Reading details" });
    expect(card.contains(panel)).toBeFalsy();
    expect(within(panel).getByText("Observed at")).toBeTruthy();
    expect(panel.querySelector("time")?.dateTime).toBe(observedAt);
    expect(panel.querySelector("time")?.textContent).toMatch(/2026/u);
  });

  it("links the Source evidence and marks the trigger expanded", async () => {
    mount();
    const card = screen.getByRole("article", { name: "Living room" });
    const trigger = within(card).getByRole("button", { name: "Source" });
    fireEvent.click(trigger);
    const panel = screen.getByRole("dialog", { name: "Reading details" });
    expect(
      within(panel)
        .getByRole("link", { name: "Temperature" })
        .getAttribute("href")
    ).toBe("/entities/ent-temperature");
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(
      within(panel).getByRole("button", { name: "Close details" })
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(trigger));
  });

  it("merges caller classes onto the card frame", () => {
    mount({
      compactTimeLabel: true,
      className: "p-6",
      classNames: {
        body: "grid",
        footer: "gap-1",
        timeLabel: "hidden",
        sourceToggle: "px-2",
      },
    });
    const card = screen.getByRole("article", { name: "Living room" });
    expect(card.classList.contains("p-6")).toBeTruthy();
    expect(card.classList.contains("p-[18px]")).toBeFalsy();
    expect(
      card.querySelector('[data-slot="card-body"]')?.classList.contains("grid")
    ).toBeTruthy();
    expect(
      card
        .querySelector('[data-slot="card-footer"]')
        ?.classList.contains("gap-1")
    ).toBeTruthy();
    expect(
      card
        .querySelector('[data-slot="card-footer"]')
        ?.classList.contains("gap-3")
    ).toBeFalsy();
  });

  it("merges caller classes onto named slots", () => {
    mount({
      compactTimeLabel: true,
      classNames: {
        timeLabel: "hidden",
        sourceToggle: "px-2",
      },
    });
    const card = screen.getByRole("article", { name: "Living room" });
    expect(
      card
        .querySelector('[data-slot="card-time-label"]')
        ?.classList.contains("hidden")
    ).toBeTruthy();
    expect(
      within(card)
        .getByRole("button", { name: "Source" })
        .classList.contains("px-2")
    ).toBeTruthy();
  });

  it("adds caller classes to the status list and resolves display overrides", () => {
    render(
      <StatusList
        entries={[{ key: "unavailable", tone: "warn", text: "Unavailable" }]}
        className="grid"
      />
    );
    const list = screen.getByRole("list");
    expect(list.classList.contains("grid")).toBeTruthy();
    expect(list.classList.contains("flex")).toBeFalsy();
    expect(list.classList.contains("text-gray-9")).toBeTruthy();
    expect(within(list).getByRole("listitem").dataset.tone).toBe("warn");
  });

  it("advances recency while preserving the meaning of an oldest-reading footer", () => {
    mount(
      { observedAt: "2026-11-06T19:40:00Z", observedLabel: "Oldest reading" },
      true
    );
    expect(screen.getByText("Oldest 2m ago").getAttribute("aria-label")).toBe(
      "Oldest reading 2 minutes ago"
    );
    act(() => vi.advanceTimersByTime(60_000));
    expect(screen.getByText("Oldest 3m ago")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Source" }));
    expect(screen.getByText("Oldest reading observed at")).toBeTruthy();
  });

  it("uses a modal sheet on phones and closes before guarded Source navigation", () => {
    vi.stubGlobal("matchMedia", () => ({
      matches: true,
      addEventListener() {},
      removeEventListener() {},
    }));
    mount({
      sourceExtra: <p>Each point averages samples in a 15-minute interval.</p>,
    });
    const trigger = screen.getByRole("button", { name: "Source" });
    fireEvent.click(trigger);
    const panel = screen.getByRole("dialog", { name: "Reading details" });
    expect(panel.getAttribute("aria-modal")).toBe("true");
    expect(panel.dataset.placement).toBe("bottom");
    expect(within(panel).getByText(/15-minute/u)).toBeTruthy();
    fireEvent.click(within(panel).getByRole("link", { name: "Temperature" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("keeps Source-only cards usable without inventing an observation time", () => {
    mount({
      observedAt: undefined,
      sourceExtra: <p>History interval and method</p>,
    });
    expect(screen.queryByText(/Observed/u)).toBeNull();
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
});
