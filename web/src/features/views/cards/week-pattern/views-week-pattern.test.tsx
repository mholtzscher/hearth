import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { pairedEntityResponse } from "../../../../../.storybook/paired-fixtures";
import type { Resolved, TextData, WeekPatternData } from "../../bindings/types";
import type { ViewDefinition } from "../../catalog";
import { ViewDataProvider } from "../../data/provider";
import { ViewEditor } from "../../editor/view-editor";
import { ViewRenderer } from "../../renderer/view-renderer";
import { WeekPatternEditor } from "./editor";
import { WeekPattern } from "./week-pattern";

const id = "ent_019a3e6a-4111-7000-8000-000000000001";
const from = Date.parse("2026-11-02T00:00:00Z");
const to = from + 6 * 86_400_000 + 12 * 3_600_000;
const week: WeekPatternData = {
  from,
  to,
  days: Array.from({ length: 7 }, (_, day) => ({
    start: from + day * 86_400_000,
    hours: Array.from({ length: 24 }, (_hour, hour) => ({
      start: from + day * 86_400_000 + hour * 3_600_000,
      end: from + day * 86_400_000 + (hour + 1) * 3_600_000,
      seconds:
        day === 6 && hour > 12 ? null : [0, 60, 360, 960, 1860][hour % 5],
      entries: hour === 3 ? 1 : 0,
      future: day === 6 && hour > 12,
    })),
  })),
};
const metadata = {
  id,
  label: "Door",
  availability: "available",
  enabled: true,
};
const data: { current?: TextData; week?: WeekPatternData } = {
  current: { text: "Closed", observed_at: new Date(to).toISOString() },
  week,
};
const ready: Resolved<{ current?: TextData; week?: WeekPatternData }> = {
  status: "ready",
  metadata,
  data,
};

describe("Week pattern cards", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("draws seven days with five levels and future cells", () => {
    render(
      <WeekPattern
        label="Door"
        entityId={id}
        resource={ready}
        trueWord="Open"
      />,
      { wrapper: MemoryRouter }
    );
    expect(document.querySelectorAll('[data-slot="week-hours"]')).toHaveLength(
      7
    );
    expect(
      document.querySelectorAll(
        '[data-slot="week-hours"] [data-slot="week-cell"]'
      )
    ).toHaveLength(168);
    for (let n = 0; n < 5; n += 1) {
      expect(
        document.querySelector(`[data-slot="week-hours"] [data-level="${n}"]`)
      ).toBeTruthy();
    }
    expect(
      document.querySelectorAll('[data-slot="week-hours"] [data-future]')
    ).toHaveLength(11);
    expect(
      document
        .querySelector('[data-slot="week-grid"]')
        ?.getAttribute("aria-hidden")
    ).toBe("true");
  });

  it("summarizes each day and labels the minutes scale", () => {
    render(
      <WeekPattern
        label="Door"
        entityId={id}
        resource={ready}
        trueWord="Open"
      />,
      { wrapper: MemoryRouter }
    );
    expect(screen.getAllByText(/times, .* open\./u)).toHaveLength(7);
    expect(screen.getByText("Minutes open per hour")).toBeTruthy();
  });

  it.each([
    [{ status: "loading" }, "Loading…"],
    [{ status: "missing" }, "Entity not found"],
    [
      {
        status: "error",
        error: "Too many readings for this window. Choose a shorter one.",
      },
      "Too many readings for this window. Choose a shorter one.",
    ],
    [{ ...ready, status: "unobserved", data: { week } }, "Never observed"],
    [{ ...ready, status: "absent", data: { week } }, "No value"],
    [
      { ...ready, metadata: { ...metadata, availability: "unavailable" } },
      "Unavailable",
    ],
    [{ ...ready, metadata: { ...metadata, enabled: false } }, "Disabled"],
    [{ ...ready, refreshFailed: true }, "Couldn't refresh"],
  ] as [Resolved<{ current?: TextData; week?: WeekPatternData }>, string][])(
    "shows %s status",
    (resource, text) => {
      render(<WeekPattern label="Door" entityId={id} resource={resource} />, {
        wrapper: MemoryRouter,
      });
      expect(screen.getByText(text, { exact: false })).toBeTruthy();
    }
  );

  it("glows only when a newer summary changes displayed cells or daily totals", () => {
    const view = render(
      <WeekPattern label="Door" entityId={id} resource={ready} />,
      { wrapper: MemoryRouter }
    );
    expect(document.querySelector('[data-slot="card-arrival"]')).toBeNull();
    const changedCurrent = {
      ...ready,
      data: {
        ...data,
        current: {
          text: "Open",
          observed_at: new Date(to + 1000).toISOString(),
        },
      },
    };
    view.rerender(
      <WeekPattern label="Door" entityId={id} resource={changedCurrent} />
    );
    expect(document.querySelector('[data-slot="card-arrival"]')).toBeNull();
    const later = {
      ...week,
      to: to + 60_000,
      days: week.days.map((day, i) =>
        i === 0
          ? {
              ...day,
              hours: day.hours.map((cell, j) =>
                j === 0 ? { ...cell, seconds: 60 } : cell
              ),
            }
          : day
      ),
    };
    view.rerender(
      <WeekPattern
        label="Door"
        entityId={id}
        resource={{
          ...changedCurrent,
          data: { ...changedCurrent.data, week: later },
        }}
      />
    );
    expect(document.querySelector('[data-slot="card-arrival"]')).toBeTruthy();
  });

  it("does not glow when a newer summary leaves shading and formatted totals unchanged", () => {
    const view = render(
      <WeekPattern label="Door" entityId={id} resource={ready} />,
      { wrapper: MemoryRouter }
    );
    const sameDisplay = {
      ...week,
      to: to + 60_000,
      days: week.days.map((day, i) =>
        i === 0
          ? {
              ...day,
              hours: day.hours.map((cell, j) =>
                j === 1 ? { ...cell, seconds: 61 } : cell
              ),
            }
          : day
      ),
    };
    view.rerender(
      <WeekPattern
        label="Door"
        entityId={id}
        resource={{ ...ready, data: { ...data, week: sameDisplay } }}
      />
    );
    expect(document.querySelector('[data-slot="card-arrival"]')).toBeNull();
  });

  it("does not glow for unavailable changes or replay them on recovery", () => {
    const view = render(
      <WeekPattern label="Door" entityId={id} resource={ready} />,
      { wrapper: MemoryRouter }
    );
    const later = {
      ...week,
      to: to + 60_000,
      days: week.days.map((day, i) =>
        i === 0
          ? {
              ...day,
              hours: day.hours.map((cell, j) =>
                j === 0 ? { ...cell, seconds: 60 } : cell
              ),
            }
          : day
      ),
    };
    const changed = { ...ready, data: { ...data, week: later } };
    view.rerender(
      <WeekPattern
        label="Door"
        entityId={id}
        resource={{
          ...changed,
          metadata: { ...metadata, availability: "unavailable" },
        }}
      />
    );
    expect(document.querySelector('[data-slot="card-arrival"]')).toBeNull();
    view.rerender(
      <WeekPattern label="Door" entityId={id} resource={changed} />
    );
    expect(document.querySelector('[data-slot="card-arrival"]')).toBeNull();
  });

  it("edits the label and optional boolean words", () => {
    const value = {
      source: {
        kind: "entity-state" as const,
        entity_id: id,
        entity_type: "hearth.power/v1",
      },
    };
    const onChange = vi.fn<(value: unknown) => void>();
    render(<WeekPatternEditor value={value} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Label"), {
      target: { value: "Front door" },
    });
    expect(onChange).toHaveBeenLastCalledWith({
      ...value,
      label: "Front door",
    });
    fireEvent.click(screen.getByLabelText("Use custom words"));
    expect(onChange).toHaveBeenLastCalledWith({
      ...value,
      words: { true: "On", false: "Off" },
    });
  });

  it("renders both registered nodes through shared State and real change-response bindings", async () => {
    const source = {
      kind: "entity-state" as const,
      entity_id: id,
      entity_type: "hearth.power/v1",
    };
    const definition = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Doors",
      spec: {
        root: "grid",
        elements: {
          grid: {
            type: "Grid",
            props: { columns: 2 },
            children: ["timeline", "week"],
          },
          timeline: {
            type: "StateTimeline",
            props: {
              title: "Changes",
              style: "lanes",
              window: "1h",
              rows: [{ source }],
            },
          },
          week: { type: "WeekPattern", props: { source, label: "Week" } },
        },
      },
    };
    const respond = (url: string, init?: RequestInit) => {
      if (!url.includes("/state/changes")) {
        const entity = {
          id,
          type: source.entity_type,
          name: "Door",
          enabled: true,
          availability: { status: "available" },
          state: { value: false, observed_at: new Date().toISOString() },
        };
        return Response.json(pairedEntityResponse(entity, entity));
      }
      const body = JSON.parse(String(init?.body));
      const base = {
        entity_id: id,
        select_field: "",
        range: body.range,
        time_axis: "observed_at",
        coverage: { basis: "retained_changes", seeded: true },
      };
      if (body.result.kind === "timeline") {
        return Response.json({
          ...base,
          kind: "timeline",
          intervals: [{ kind: "value", ...body.range, value: false }],
        });
      }
      const fromMs = Date.parse(body.range.from);
      const toMs = Date.parse(body.range.to);
      const buckets = Array.from(
        { length: Math.ceil((toMs - fromMs) / 3_600_000) },
        (_bucket, index) => {
          const start = fromMs + index * 3_600_000;
          const end = Math.min(start + 3_600_000, toMs);
          return {
            range: {
              from: new Date(start).toISOString(),
              to: new Date(end).toISOString(),
            },
            seconds_true: 0,
            known_seconds: (end - start) / 1000,
            transitions_into_true: 0,
          };
        }
      );
      return Response.json({
        ...base,
        kind: "summary",
        bucket: { duration: "1h0m0s", alignment: "range_start" },
        buckets,
      });
    };
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation((url, init) =>
        Promise.resolve(respond(String(url), init))
      );
    render(
      <ViewDataProvider>
        <ViewRenderer definition={definition} headingLevel={3} />
      </ViewDataProvider>,
      { wrapper: MemoryRouter }
    );
    await waitFor(() =>
      expect(
        document.querySelectorAll('[data-slot="week-hours"]')
      ).toHaveLength(7)
    );
    expect(screen.getByRole("article", { name: "Changes" })).toBeTruthy();
    expect(screen.getByRole("article", { name: "Week" })).toBeTruthy();
    expect(
      fetch.mock.calls.filter(
        ([url]) => !String(url).includes("/state/changes")
      )
    ).toHaveLength(1);
    expect(
      fetch.mock.calls.filter(([url]) => String(url).includes("/state/changes"))
    ).toHaveLength(2);
  });

  it("dispatches existing timeline and week nodes to their editors", () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      Response.json({ items: [], next_cursor: null })
    );
    const source = {
      kind: "entity-state" as const,
      entity_id: id,
      entity_type: "hearth.power/v1",
    };
    const definition: ViewDefinition = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Doors",
      spec: {
        root: "grid",
        elements: {
          grid: {
            type: "Grid",
            props: { columns: 2 },
            children: ["timeline", "week"],
          },
          timeline: {
            type: "StateTimeline",
            props: {
              title: "Changes",
              style: "lanes",
              window: "1h",
              rows: [{ source }],
            },
          },
          week: { type: "WeekPattern", props: { source, label: "Week" } },
        },
      },
    };
    render(
      <ViewEditor
        definition={definition}
        onChange={vi.fn<(definition: ViewDefinition) => void>()}
      />
    );
    fireEvent.click(screen.getByText("StateTimeline · timeline"));
    fireEvent.click(screen.getByText("WeekPattern · week"));
    expect(screen.getByLabelText("Timeline title")).toBeTruthy();
    expect(screen.getByLabelText("Style")).toBeTruthy();
    expect(screen.getByLabelText("Label")).toBeTruthy();
    expect(screen.queryByText("Add card")).toBeNull();
  });
});
