import { afterEach, describe, expect, it, vi } from "vitest";

import {
  pairedEntityResponse,
  pairedHistoryResponse,
} from "../../../../.storybook/paired-fixtures";
import type { FieldEntity } from "./entity";
import { pairedQuantityAdapter } from "./paired";
import type { EntitySource } from "./types";

const observed_at = "2026-11-14T21:00:00Z";
const pairedQuantityAdapters = {
  household: pairedQuantityAdapter("household"),
  canonical: pairedQuantityAdapter("canonical"),
};
const source: EntitySource = {
  entity_id: "ent_test",
  entity_type: "hearth.speed/v1",
};
const display: FieldEntity = {
  id: source.entity_id,
  type: source.entity_type,
  name: "Wind",
  enabled: true,
  availability: { status: "available" },
  support: { state: {}, operations: {} },
  quantities: { "": { unit: "[mi_i]/h", label: "mi/h", fraction_digits: 1 } },
  state: { observed_at, observation_id: "obs_one", value: 10 },
};
const displayState = display.state;
if (!displayState) {
  throw new Error("Expected a display State fixture");
}
const canonical: FieldEntity = {
  ...display,
  quantities: { "": { unit: "m/s", label: "m/s", fraction_digits: 1 } },
  state: { ...displayState, value: 4.4704 },
};
const signal = () => new AbortController().signal;
const respond = (raw: unknown) =>
  vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json(raw));

describe("Paired quantities", () => {
  afterEach(() => vi.restoreAllMocks());

  it("reads both unrounded values and shared evidence from one scalar response", async () => {
    const fetch = respond(pairedEntityResponse(display, canonical));
    const request = pairedQuantityAdapters.household.stateRequest(source);
    const raw = await request.load(signal(), new Date(observed_at));
    expect(fetch).toHaveBeenCalledWith(
      "/v1/entities/ent_test",
      expect.anything()
    );
    expect(pairedQuantityAdapters.canonical.stateRequest(source).key).toBe(
      request.key
    );
    expect(pairedQuantityAdapters.household.metric(raw, source)).toStrictEqual({
      value: 10,
      observed_at,
      format: { unit_label: "mi/h", fraction_digits: 1 },
    });
    expect(pairedQuantityAdapters.canonical.metric(raw, source)).toStrictEqual({
      value: 4.4704,
      observed_at,
      format: { unit_label: "m/s", fraction_digits: 1 },
    });
  });

  it("reads composite quantities without inventing absent optional fields", async () => {
    const thermostat = {
      entity_id: source.entity_id,
      entity_type: "hearth.thermostat/v1",
      field: "/current_temperature",
    };
    const shown = {
      ...display,
      type: thermostat.entity_type,
      quantities: {
        "/current_temperature": {
          unit: "[degF]",
          label: "°F",
          fraction_digits: 1,
        },
      },
      state: {
        observed_at,
        value: { mode: "heat", current_temperature: 69.8 },
      },
    };
    const working = {
      ...shown,
      quantities: {
        "/current_temperature": {
          unit: "mCel",
          label: "mCel",
          fraction_digits: 0,
        },
      },
      state: {
        observed_at,
        value: { mode: "heat", current_temperature: 21_000 },
      },
    };
    respond(pairedEntityResponse(shown, working));
    const raw = await pairedQuantityAdapters.household
      .stateRequest(thermostat)
      .load(signal(), new Date());
    expect(
      pairedQuantityAdapters.household.metric(raw, thermostat)
    ).toMatchObject({ value: 69.8 });
    expect(
      pairedQuantityAdapters.canonical.metric(raw, thermostat)
    ).toMatchObject({ value: 21_000 });
    expect(
      pairedQuantityAdapters.household.metric(raw, {
        ...thermostat,
        field: "/target_temperature",
      })
    ).toStrictEqual({ absent: true, observed_at });
  });

  it("keeps never-observed State null", async () => {
    respond(
      pairedEntityResponse(
        { ...display, state: null },
        { ...canonical, state: null }
      )
    );
    const raw = await pairedQuantityAdapters.household
      .stateRequest(source)
      .load(signal(), new Date());
    expect(pairedQuantityAdapters.household.metric(raw, source)).toBeNull();
    expect(pairedQuantityAdapters.canonical.metric(raw, source)).toBeNull();
  });

  it.each([
    { name: "legacy response", body: display },
    {
      name: "wrong canonical unit",
      body: pairedEntityResponse(display, {
        ...canonical,
        quantities: { "": { unit: "mm/h", label: "mm/h", fraction_digits: 1 } },
      }),
    },
    {
      name: "missing canonical reading",
      body: {
        ...display,
        state: {
          observed_at,
          display: { value: 10, ...display.quantities?.[""] },
        },
      },
    },
    {
      name: "different Entity",
      body: { ...pairedEntityResponse(display, canonical), id: "ent_other" },
    },
  ])("rejects a $name at the HTTP decode boundary", async ({ body }) => {
    respond(body);
    await expect(
      pairedQuantityAdapters.household
        .stateRequest(source)
        .load(signal(), new Date())
    ).rejects.toThrow(Error);
  });

  const history = () => {
    const buckets = Array.from({ length: 60 }, (_, index) => ({
      range: {
        from: new Date(
          Date.parse(observed_at) - (60 - index) * 60_000
        ).toISOString(),
        to: new Date(
          Date.parse(observed_at) - (59 - index) * 60_000
        ).toISOString(),
      },
      metrics: {
        min: index === 2 ? null : 10,
        max: index === 2 ? null : 10,
        mean: index === 2 ? null : 10,
      },
    }));
    const displayQuantity = display.quantities?.[""];
    const canonicalQuantity = canonical.quantities?.[""];
    if (!displayQuantity || !canonicalQuantity) {
      throw new Error("Expected paired quantity fixtures");
    }
    return pairedHistoryResponse(
      {
        range: { from: buckets[0].range.from, to: observed_at },
        quantity: displayQuantity,
        buckets,
      },
      {
        quantity: canonicalQuantity,
        buckets: buckets.map((bucket) => ({
          range: bucket.range,
          metrics: {
            min: bucket.metrics.min === null ? null : 4.4704,
            max: bucket.metrics.max === null ? null : 4.4704,
            mean: bucket.metrics.mean === null ? null : 4.4704,
          },
        })),
      }
    );
  };

  it("reads both history projections with the same buckets and gaps", async () => {
    const body = history();
    const fetch = respond(body);
    const request = pairedQuantityAdapters.household.seriesRequest(
      source,
      "1h"
    );
    const series = await request.load(signal(), new Date(observed_at));
    expect(series).toMatchObject({
      quantity: { unit: "[mi_i]/h" },
      canonical: { quantity: { unit: "m/s" } },
    });
    expect(fetch).toHaveBeenCalledWith(
      "/v1/entities/ent_test/state/history",
      expect.anything()
    );
    expect(
      pairedQuantityAdapters.canonical.seriesRequest(source, "1h").key
    ).toBe(request.key);
    expect(series).toMatchObject({
      buckets: expect.arrayContaining([
        expect.objectContaining({ mean: null }),
      ]),
      canonical: {
        buckets: expect.arrayContaining([
          expect.objectContaining({ mean: null }),
        ]),
      },
    });
  });

  it.each([
    "gap",
    "unit",
    "all canonical units",
    "precision",
    "count",
  ] as const)("rejects inconsistent paired history %s", async (kind) => {
    const body = history();
    if (kind === "gap") {
      body.buckets[2].canonical.metrics.mean = 4.4704;
    }
    if (kind === "unit") {
      body.buckets[2].display.unit = "mm/h";
    }
    if (kind === "all canonical units") {
      for (const bucket of body.buckets) {
        bucket.canonical.unit = "mm/h";
      }
    }
    if (kind === "precision") {
      body.buckets[2].display.fraction_digits = 2;
    }
    if (kind === "count") {
      body.buckets.pop();
    }
    respond(body);
    await expect(
      pairedQuantityAdapters.household
        .seriesRequest(source, "1h")
        .load(signal(), new Date(observed_at))
    ).rejects.toThrow(Error);
  });
});
