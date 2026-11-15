import { cleanup, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { pairedEntityResponse } from "../../../../.storybook/paired-fixtures";
import { BoundMetric } from "../cards/metric/metric";
import { BoundTimeSeries } from "../cards/time-series/time-series";
import { ViewDataProvider } from "../data/provider";

const id = "ent_019a3e6a-4111-7000-8000-000000000001";
const entity = {
  id,
  name: "Outdoor",
  type: "hearth.temperature/v1",
  enabled: true,
  availability: { status: "available" },
  quantities: { "": { unit: "Cel", label: "°C", fraction_digits: 1 } },
  state: { value: 12.5, observed_at: "2026-11-01T10:00:00Z" },
};

/** Core answers State reads, and refuses history with the given problem. */
const refuseHistory = (problem: { status: number; code: string }) => {
  vi.spyOn(globalThis, "fetch").mockImplementation((url, init) =>
    Promise.resolve(
      String(url).includes("/state/history") && init?.method === "POST"
        ? Response.json(
            {
              title: "Unprocessable Entity",
              detail: "History query failed.",
              ...problem,
            },
            { status: problem.status }
          )
        : Response.json(
            pairedEntityResponse(entity, {
              ...entity,
              quantities: {
                "": { unit: "mCel", label: "mCel", fraction_digits: 0 },
              },
              state: { ...entity.state, value: 12_500 },
            })
          )
    )
  );
};

describe("History query limits", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("asks for a shorter window when Core refuses a series as too much history", async () => {
    refuseHistory({ status: 422, code: "query_limit" });
    render(
      <ViewDataProvider>
        <BoundTimeSeries
          title="Outdoor history"
          series={[
            {
              source: {
                entity_id: id,
                entity_type: entity.type,
                window: "7d",
              },
            },
          ]}
        />
        <BoundMetric
          label="Outdoor"
          source={{ entity_id: id, entity_type: entity.type }}
          trend="7d"
        />
      </ViewDataProvider>,
      { wrapper: MemoryRouter }
    );
    const chart = screen.getByRole("article", { name: "Outdoor history" });
    await expect(
      within(chart).findByText("Couldn't load this reading")
    ).resolves.toBeTruthy();
    expect(
      within(chart).getByText(
        "Too many readings for this window. Choose a shorter one."
      )
    ).toBeTruthy();
    const metric = screen.getByRole("article", { name: "Outdoor" });
    await expect(
      within(metric).findByText(
        "Too many readings for this window. Choose a shorter one."
      )
    ).resolves.toBeTruthy();
  });

  it("keeps the generic wording for other history failures", async () => {
    refuseHistory({ status: 422, code: "value_type" });
    render(
      <ViewDataProvider>
        <BoundMetric
          label="Outdoor"
          source={{ entity_id: id, entity_type: entity.type }}
          trend="7d"
        />
      </ViewDataProvider>,
      { wrapper: MemoryRouter }
    );
    const metric = screen.getByRole("article", { name: "Outdoor" });
    await expect(
      within(metric).findByText("Couldn't load history.")
    ).resolves.toBeTruthy();
    expect(within(metric).queryByText(/Too many readings/u)).toBeNull();
  });
});
