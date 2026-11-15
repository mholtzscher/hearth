import { act, cleanup, render, screen } from "@testing-library/react";
import { useMemo } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NumericAdapters } from "../bindings/registry";
import type { NumericAdapter } from "../bindings/types";
import { parseDefinition } from "../catalog/compatibility";
import { ViewDataProvider } from "../data/provider";
import { BoundMetric } from "./metric/metric";
import { BoundTimeSeries } from "./time-series/time-series";

const AdapterCards = ({
  adapter,
  source,
}: {
  adapter: NumericAdapter;
  source: { entity_id: string; entity_type: string };
}) => {
  const adapters = useMemo(
    () => ({ "test.percentage/v1": adapter }),
    [adapter]
  );
  return (
    <ViewDataProvider>
      <NumericAdapters.Provider value={adapters}>
        <BoundMetric source={source} />
        <BoundMetric source={source} label="Second card" />
        <BoundTimeSeries
          title="Battery history"
          series={[{ source: { ...source, window: "6h" } }]}
        />
      </NumericAdapters.Provider>
    </ViewDataProvider>
  );
};

const renderAdapterCards = async () => {
  // Test initial sharing, not a wall-clock minute refresh; the cache owns that schedule's tests.
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-11-01T10:00:13Z"));
  const stateLoad = vi.fn<() => Promise<number>>().mockResolvedValue(65);
  const historyLoad = vi.fn<() => Promise<number>>().mockResolvedValue(65);
  const format = { unit_label: "%", fraction_digits: 0 };
  const series = vi.fn<NumericAdapter["series"]>(() => ({
    from: "2026-11-01T04:00:00Z",
    to: "2026-11-01T10:00:00Z",
    format,
    buckets: [
      {
        start: "2026-11-01T04:00:00Z",
        end: "2026-11-01T04:01:15Z",
        min: 65,
        max: 65,
        mean: 65,
      },
    ],
  }));
  const adapter: NumericAdapter = {
    stateRequest: (source) => ({
      key: `state:${source.entity_id}`,
      kind: "state",
      load: stateLoad,
    }),
    seriesRequest: (source) => ({
      key: `series:${source.entity_id}`,
      kind: "series",
      load: historyLoad,
    }),
    metadata: (_, source) => ({
      id: source.entity_id,
      label: "Battery",
      enabled: true,
      availability: "available",
    }),
    metric: (raw) => ({
      value: Number(raw),
      format,
      observed_at: "2026-11-01T10:00:00Z",
    }),
    series,
  };
  const source = {
    entity_id: "ent_019a3e6a-4111-7000-8000-000000000001",
    entity_type: "test.percentage/v1",
  };
  await act(async () => {
    render(<AdapterCards adapter={adapter} source={source} />, {
      wrapper: MemoryRouter,
    });
    await Promise.resolve();
  });
  return { stateLoad, historyLoad, series, source };
};

describe("Views extension adapters", () => {
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders an independently registered percentage adapter with the same numeric cards and cache", async () => {
    const { stateLoad, historyLoad } = await renderAdapterCards();
    expect(screen.getAllByText("65")).toHaveLength(2);
    expect(screen.getByText("Battery history")).toBeTruthy();
    expect(stateLoad).toHaveBeenCalledOnce();
    expect(historyLoad).toHaveBeenCalledOnce();
  });

  it("shares one adapter read and rejects the unregistered percentage definition", async () => {
    const { series, source } = await renderAdapterCards();
    expect(series).toHaveBeenCalledWith(
      65,
      expect.objectContaining(source),
      65
    );
    expect(
      parseDefinition({
        schema_version: 1,
        catalog_version: "hearth.dashboard/v1",
        title: "Test",
        spec: {
          root: "metric",
          elements: {
            metric: {
              type: "Metric",
              props: { source: { kind: "entity-state", ...source } },
            },
          },
        },
      })
    ).toBeNull();
  });
});
