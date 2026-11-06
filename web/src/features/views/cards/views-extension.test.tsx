import { act, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { NumericAdapters } from "../bindings/registry";
import type { NumericAdapter } from "../bindings/types";
import { ViewDataProvider } from "../data/provider";
import { BoundMetric } from "./metric/Metric";
import { BoundTimeSeries } from "./time-series/TimeSeries";
import { parseDefinition } from "../catalog/compatibility";
import { UnitSystemOverride } from "../../units/household";

afterEach(() => vi.restoreAllMocks());
it("renders an independently registered percentage adapter with the same numeric cards and cache", async () => {
  const stateLoad = vi.fn().mockResolvedValue(65); const historyLoad = vi.fn().mockResolvedValue(65);
  const format = { unit_label: "%", fraction_digits: 0 };
  const adapter: NumericAdapter = {
    stateRequest: source => ({ key: `state:${source.entity_id}`, kind: "state", load: stateLoad }),
    seriesRequest: source => ({ key: `series:${source.entity_id}`, kind: "series", load: historyLoad }),
    metadata: (_, source) => ({ id: source.entity_id, label: "Battery", enabled: true, availability: "available" }),
    metric: raw => ({ value: Number(raw), format, observed_at: "2026-11-01T10:00:00Z" }),
    series: () => ({ from: "2026-11-01T04:00:00Z", to: "2026-11-01T10:00:00Z", format, buckets: [{ start: "2026-11-01T04:00:00Z", end: "2026-11-01T04:01:15Z", min: 65, max: 65, mean: 65 }] }),
  };
  const source = { entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: "test.percentage/v1" };
  await act(async () => render(<ViewDataProvider><UnitSystemOverride.Provider value="metric"><NumericAdapters.Provider value={{ "test.percentage/v1": adapter }}>
    <BoundMetric source={source} />
    <BoundMetric source={source} label="Second card" />
    <BoundTimeSeries title="Battery history" series={[{ source: { ...source, window: "6h" } }]} />
  </NumericAdapters.Provider></UnitSystemOverride.Provider></ViewDataProvider>, { wrapper: MemoryRouter }));
  expect(screen.getAllByText("65")).toHaveLength(2);
  expect(screen.getByText("Battery history")).toBeTruthy();
  expect(stateLoad).toHaveBeenCalledTimes(1); expect(historyLoad).toHaveBeenCalledTimes(1);
  expect(parseDefinition({ schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Test", spec: { root: "metric", elements: { metric: { type: "Metric", props: { source: { kind: "entity-state", ...source } } } } } })).toBeNull();
});
