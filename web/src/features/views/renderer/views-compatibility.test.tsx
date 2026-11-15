import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { parseDefinition, readDefinition } from "../catalog/compatibility";
import { ViewDataProvider } from "../data/provider";
import { ViewRenderer } from "./view-renderer";

const source = {
  kind: "entity-state",
  entity_id: "ent_019a3e6a-4111-7000-8000-000000000001",
  entity_type: "hearth.temperature/v1",
};
const metric = { type: "Metric", props: { source } };
const futureDefinition = () => ({
  schema_version: 1,
  catalog_version: "hearth.dashboard/v1",
  title: "Future",
  spec: {
    root: "root",
    elements: {
      root: {
        type: "Stack",
        props: { gap: "medium" },
        children: ["note", "future"],
      },
      note: { type: "Note", props: { text: "Still readable" } },
      future: {
        type: "FutureLayout",
        props: { opaque: "preserve me" },
        children: ["metric"],
      },
      metric,
    },
  },
});
const renderDefinition = async (definition: unknown, active?: boolean) => {
  const fetch = vi.spyOn(globalThis, "fetch");
  await act(() => {
    render(
      <ViewDataProvider>
        <ViewRenderer
          headingLevel={3}
          definition={definition}
          active={active}
        />
      </ViewDataProvider>
    );
  });
  return fetch;
};

describe("view compatibility", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("preserves an unknown container and suppresses descendant reads while showing supported siblings", async () => {
    const raw = futureDefinition();
    const fetch = await renderDefinition(raw);
    expect(screen.getByText("Still readable")).toBeTruthy();
    expect(screen.getByText(/Unsupported component/u)).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Download original definition" })
    ).toBeTruthy();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("leaves the original definition unchanged and reports partial compatibility", async () => {
    const raw = futureDefinition();
    const before = JSON.stringify(raw);
    await renderDefinition(raw);
    expect(JSON.stringify(raw)).toBe(before);
    expect(readDefinition(raw).status).toBe("partial");
  });

  it.each([
    { ...metric.props, futureOption: true },
    { ...metric.props, source: { ...source, kind: "future-binding" } },
  ])(
    "does not pass unknown properties or bindings to resource adapters",
    async (props) => {
      const raw = {
        schema_version: 1,
        catalog_version: "hearth.dashboard/v1",
        title: "Future",
        spec: {
          root: "metric",
          elements: { metric: { ...metric, props } },
        },
      };
      const fetch = await renderDefinition(raw);
      expect(screen.getByText(/Unsupported component/u)).toBeTruthy();
      expect(fetch).not.toHaveBeenCalled();
    }
  );

  it.each([
    {
      schema_version: 2,
      catalog_version: "hearth.dashboard/v2",
      title: "Future",
      spec: { root: "metric", elements: { metric } },
    },
    {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Invalid",
      spec: { root: "missing", elements: { metric } },
    },
  ])(
    "renders whole-view recovery for invalid graphs and unsupported majors without fetching",
    async (raw) => {
      const fetch = await renderDefinition(raw);
      expect(
        screen.getByRole("button", { name: "Download original definition" })
      ).toBeTruthy();
      expect(readDefinition(raw).status).toBe("unavailable");
      expect(fetch).not.toHaveBeenCalled();
    }
  );

  it("keeps older supported cards as summaries without live subscriptions", async () => {
    const raw = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Earlier view",
      spec: { root: "metric", elements: { metric } },
    };
    const fetch = await renderDefinition(raw, false);
    expect(screen.getByText("Earlier view")).toBeTruthy();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("reads cards stored with a legacy display unit and drops it from the supported definition", () => {
    const raw = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Legacy",
      spec: {
        root: "grid",
        elements: {
          grid: {
            type: "Grid",
            props: { columns: 2 },
            children: ["metric", "chart"],
          },
          metric: {
            ...metric,
            props: { ...metric.props, display: { unit: "fahrenheit" } },
          },
          chart: {
            type: "TimeSeries",
            props: {
              title: "History",
              display: { unit: "celsius" },
              series: [
                { source: { ...source, kind: "entity-series", window: "6h" } },
              ],
            },
          },
        },
      },
    };
    const before = JSON.stringify(raw);
    const read = readDefinition(raw);
    expect(read.status).toBe("supported");
    if (read.status !== "supported") {
      return;
    }
    expect(JSON.stringify(read.definition)).not.toContain("display");
    expect(JSON.stringify(read.raw)).toBe(before);
    expect(parseDefinition(raw)).toBeNull();
    expect(parseDefinition(read.definition)).not.toBeNull();
  });
});
