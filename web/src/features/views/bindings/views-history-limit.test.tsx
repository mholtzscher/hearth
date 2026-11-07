import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { BoundTimeSeries } from "../cards/time-series/TimeSeries";
import { BoundMetric } from "../cards/metric/Metric";
import { ViewDataProvider } from "../data/provider";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const id = "ent_019a3e6a-4111-7000-8000-000000000001";
const entity = {
  id, name: "Outdoor", type: "hearth.temperature/v1", enabled: true, availability: { status: "available" },
  state: { value: 12500, observed_at: "2026-11-01T10:00:00Z" },
};

/** Core answers State reads, and refuses history with the given problem. */
function refuseHistory(problem: { status: number; code: string }) {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url, init) => String(url).includes("/state/history") && init?.method === "POST"
    ? new Response(JSON.stringify({ title: "Unprocessable Entity", detail: "History query failed.", ...problem }), { status: problem.status })
    : new Response(JSON.stringify(entity)));
}

it("asks for a shorter window when Core refuses a series as too much history", async () => {
  refuseHistory({ status: 422, code: "query_limit" });
  render(<ViewDataProvider>
    <BoundTimeSeries title="Outdoor history" series={[{ source: { entity_id: id, entity_type: entity.type, window: "7d" } }]} />
    <BoundMetric label="Outdoor" source={{ entity_id: id, entity_type: entity.type }} trend="7d" />
  </ViewDataProvider>, { wrapper: MemoryRouter });
  const chart = screen.getByRole("article", { name: "Outdoor history" });
  expect(await within(chart).findByText("Couldn't load this reading")).toBeTruthy();
  expect(within(chart).getByText("Too many readings for this window. Choose a shorter one.")).toBeTruthy();
  const metric = screen.getByRole("article", { name: "Outdoor" });
  expect(await within(metric).findByText("Too many readings for this window. Choose a shorter one.")).toBeTruthy();
});

it("keeps the generic wording for other history failures", async () => {
  refuseHistory({ status: 422, code: "expression_type" });
  render(<ViewDataProvider>
    <BoundMetric label="Outdoor" source={{ entity_id: id, entity_type: entity.type }} trend="7d" />
  </ViewDataProvider>, { wrapper: MemoryRouter });
  const metric = screen.getByRole("article", { name: "Outdoor" });
  expect(await within(metric).findByText("Couldn't load history.")).toBeTruthy();
  expect(within(metric).queryByText(/Too many readings/)).toBeNull();
});
