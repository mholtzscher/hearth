import { describe, expect, it, vi } from "vitest";

import { pairedEntityResponse } from "../../../../.storybook/paired-fixtures";
import { ReadCache } from "../data/cache";
import {
  deviceRequest,
  lightReading,
  parseDevice,
  swatchColor,
} from "./device";
import { entityStateRequest } from "./entity";
import { historySeriesRequest } from "./history";
import { quantityAdapter as readingAdapter } from "./quantity-adapter";
import {
  isQuantityField,
  parseStateFields,
  stateFields,
  quantityTypes,
  textTypes,
} from "./state-fields";

// Quantity conversion is independent of the loader's chosen resolution.
const seriesBuckets = 360;
const id = "ent_019a3e6a-4111-7000-8000-000000000001";
const observed_at = "2026-11-01T10:00:00Z";
const defaultSupport = { state: {}, operations: {} };

const quantity = (unit: string, label: string, fraction_digits: number) => ({
  unit,
  label,
  fraction_digits,
});

const entityOf = (
  type: string,
  value: unknown,
  support: unknown = defaultSupport
) => {
  const quantities: Record<
    string,
    Record<string, ReturnType<typeof quantity>>
  > = {
    "hearth.brightness/v1": { "": quantity("%", "%", 0) },
    "hearth.colorhs/v1": {
      "/hue": quantity("deg", "°", 0),
      "/saturation": quantity("%", "%", 0),
    },
    "hearth.colortemp/v1": { "/value": quantity("/MK", "mired", 0) },
    "hearth.colorxy/v1": {
      "/x": quantity("1", "", 4),
      "/y": quantity("1", "", 4),
    },
    "hearth.numericsetting/v1": { "/value": quantity("1", "", 1) },
    "hearth.numericsensor/v1": { "": quantity("lqi", "lqi", 1) },
    "hearth.pressure/v1": { "": quantity("hPa", "hPa", 1) },
    "hearth.relativehumidity/v1": { "": quantity("%", "%", 1) },
    "hearth.speed/v1": { "": quantity("m/s", "m/s", 1) },
    "hearth.temperature/v1": { "": quantity("Cel", "°C", 1) },
    "hearth.thermostat/v1": {
      "/current_temperature": quantity("Cel", "°C", 1),
      "/target_temperature": quantity("Cel", "°C", 1),
      "/target_temperature_low": quantity("Cel", "°C", 1),
      "/target_temperature_high": quantity("Cel", "°C", 1),
      "/current_humidity": quantity("%", "%", 1),
      "/target_humidity": quantity("%", "%", 1),
    },
  };
  return {
    id,
    type,
    name: "Hallway",
    enabled: true,
    availability: { status: "available" },
    support,
    quantities: quantities[type] ?? {},
    state: { value, observed_at },
  };
};
const source = (type: string, field?: string) => ({
  entity_id: id,
  entity_type: type,
  field,
});

const deliveredTemperatureSeries = () => {
  const type = "hearth.temperature/v1";
  const from = "2026-11-01T04:00:00Z";
  const buckets: {
    start: string;
    end: string;
    min: number | null;
    max: number | null;
    mean: number | null;
  }[] = Array.from({ length: seriesBuckets }, () => ({
    start: from,
    end: "2026-11-01T04:01:15Z",
    min: null,
    max: null,
    mean: null,
  }));
  buckets[0] = {
    start: from,
    end: "2026-11-01T04:01:15Z",
    min: 69.8,
    max: 71.6,
    mean: 70.7,
  };
  return readingAdapter.series(
    {
      from,
      to: observed_at,
      buckets,
      quantity: { unit: "[degF]", label: "°F", fraction_digits: 1 },
    },
    source(type),
    entityOf(type, 70.7)
  );
};

const fieldCatalog = (quantityDefinition: unknown) => ({
  $comment: "test",
  types: {
    "test/v1": {
      "": {
        value_type: "number",
        presence: "always",
        quantity: quantityDefinition,
      },
    },
  },
});

describe("State field catalog", () => {
  it("accepts quantity kinds and support references but rejects obsolete aliases", () => {
    expect(
      parseStateFields(
        fieldCatalog({ unit: "mCel", quantity_kind: "temperature" })
      )["test/v1"][""]
    ).toMatchObject({ quantity: { quantity_kind: "temperature" } });
    expect(
      parseStateFields(
        fieldCatalog({
          unit: "W",
          quantity_kind: { root: "support", path: "/state/quantity_kind" },
          quantity_kind_optional: true,
        })
      )["test/v1"][""]
    ).toMatchObject({ quantity: { quantity_kind_optional: true } });
    expect(() =>
      parseStateFields(fieldCatalog({ unit: "W", kind: "electrical_power" }))
    ).toThrow(/.+/u);
    expect(() =>
      parseStateFields(fieldCatalog({ unit: "W", kind_optional: true }))
    ).toThrow(/.+/u);
  });

  it("derives numeric eligibility without enabling nonnumeric cards", () => {
    expect(quantityTypes).toStrictEqual([
      "hearth.brightness/v1",
      "hearth.colorhs/v1",
      "hearth.colorrgb/v1",
      "hearth.colortemp/v1",
      "hearth.colorxy/v1",
      "hearth.cover/v1",
      "hearth.mediaplayer/v1",
      "hearth.numericsensor/v1",
      "hearth.numericsetting/v1",
      "hearth.pressure/v1",
      "hearth.relativehumidity/v1",
      "hearth.speed/v1",
      "hearth.temperature/v1",
      "hearth.thermostat/v1",
    ]);
    expect(textTypes).toContain("hearth.textsensor/v1");
    expect(textTypes).toContain("hearth.colorrgb/v1");
    expect(textTypes).toContain("hearth.cover/v1");
    expect(textTypes).toContain("hearth.thermostat/v1");
  });

  it("declares units for fixed numeric fields", () => {
    const fields = Object.values(stateFields).flatMap((typeFields) =>
      Object.values(typeFields)
    );
    expect(fields).toHaveLength(43);
    expect(stateFields["hearth.lock/v1"][""]).toMatchObject({
      value_type: "string",
      enum: ["locked", "unlocked", "locking", "unlocking", "jammed", "unknown"],
    });
    expect(stateFields["hearth.cover/v1"]["/position"]).toStrictEqual({
      value_type: "integer",
      presence: "always",
      quantity: { unit: "%", range: { minimum: 0, maximum: 100 } },
    });
    expect(stateFields["hearth.cover/v1"]["/motion"]).toStrictEqual({
      value_type: "string",
      presence: "always",
      enum: ["idle", "opening", "closing"],
    });
    expect(fields.filter(isQuantityField)).toHaveLength(23);
    for (const field of fields) {
      if (!isQuantityField(field) || typeof field.quantity.unit !== "string") {
        continue;
      }
      expect(field.quantity.unit.length).toBeGreaterThan(0);
    }
  });

  it("exposes media playback state and optional volume to generic cards", () => {
    const fields = stateFields["hearth.mediaplayer/v1"];
    expect(textTypes).toContain("hearth.mediaplayer/v1");
    expect(fields["/playback"]).toStrictEqual({
      value_type: "string",
      presence: "always",
      enum: ["idle", "playing", "paused", "unknown"],
    });
    expect(fields["/volume_percent"]).toStrictEqual({
      value_type: "integer",
      presence: "sometimes",
      quantity: { unit: "%" },
    });
  });

  it("declares thermostat mode and activity fields", () => {
    expect(stateFields["hearth.thermostat/v1"]["/mode"]).toMatchObject({
      value_type: "string",
      presence: "always",
    });
    expect(stateFields["hearth.thermostat/v1"]["/activity"]).toMatchObject({
      value_type: "string",
      presence: "sometimes",
    });
  });
});

describe("reading adapter", () => {
  it("reads delivered thermostat quantities and preserves unknown optional targets", () => {
    const type = "hearth.thermostat/v1";
    const entity = {
      ...entityOf(type, {
        mode: "heat",
        current_temperature: 68,
        target_temperature_low: 64.4,
      }),
      quantities: {
        ...entityOf(type, {}).quantities,
        "/current_temperature": {
          unit: "[degF]",
          label: "°F",
          fraction_digits: 1,
        },
        "/target_temperature_low": {
          unit: "[degF]",
          label: "°F",
          fraction_digits: 1,
        },
      },
    };
    expect(
      readingAdapter.metric(entity, source(type, "/current_temperature"))
    ).toMatchObject({
      value: 68,
      format: { unit_label: "°F", fraction_digits: 1 },
    });
    expect(
      readingAdapter.metric(entity, source(type, "/target_temperature_low"))
    ).toMatchObject({ value: 64.4 });
    expect(
      readingAdapter.metric(entity, source(type, "/target_temperature_high"))
    ).toStrictEqual({ absent: true, observed_at });
    expect(
      readingAdapter.metric(
        { ...entity, state: null },
        source(type, "/current_temperature")
      )
    ).toBeNull();
    expect(() =>
      readingAdapter.metric(
        entityOf(type, { mode: "heat", current_temperature: "unknown" }),
        source(type, "/current_temperature")
      )
    ).toThrow(/.+/u);
  });

  it("uses paired Entity and history requests while keeping Device projections separate", async () => {
    const selected = source("hearth.temperature/v1");
    const normal = entityStateRequest(selected);
    expect(normal.key).toBe(`entity-state:${id}:paired`);
    expect(deviceRequest("dev_a").key).not.toBe(
      deviceRequest("dev_a", "canonical").key
    );
    expect(historySeriesRequest(selected, "1h").key).toBe(
      `entity-series:${id}::1h:reject:paired`
    );
    const display = entityOf(selected.entity_type, 21.5);
    const canonical = {
      ...display,
      quantities: { "": { unit: "mCel", label: "mCel", fraction_digits: 0 } },
      state: { ...display.state, value: 21_500 },
    };
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(
        Response.json(pairedEntityResponse(display, canonical))
      );
    await normal.load(new AbortController().signal, new Date());
    expect(fetch.mock.calls[0][0]).toBe(`/v1/entities/${id}`);
    fetch.mockRestore();
  });

  it("shares simultaneous reads and caches both projections", async () => {
    vi.useFakeTimers();
    const type = "hearth.temperature/v1";
    const selected = source(type);
    const cache = new ReadCache();
    const household = entityStateRequest(selected);
    const canonical = entityStateRequest(selected);
    const display = {
      ...entityOf(type, 70.7),
      quantities: { "": { unit: "[degF]", label: "°F", fraction_digits: 1 } },
    };
    const working = {
      ...entityOf(type, 21_500),
      quantities: { "": { unit: "mCel", label: "mCel", fraction_digits: 0 } },
    };
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(Response.json(pairedEntityResponse(display, working)));
    try {
      cache.subscribe(household, vi.fn());
      cache.subscribe(canonical, vi.fn());
      await vi.advanceTimersByTimeAsync(0);
      expect(
        (cache.snapshot(household.key).data as { state: { value: number } })
          .state.value
      ).toBe(70.7);
      expect(
        (
          cache.snapshot(canonical.key).data as {
            canonical: { state: { value: number } };
          }
        ).canonical.state.value
      ).toBe(21_500);
      expect(fetch).toHaveBeenCalledOnce();
    } finally {
      cache.dispose();
      fetch.mockRestore();
      vi.useRealTimers();
    }
  });

  it("keeps canonical override on every Device membership page", async () => {
    const urls: string[] = [];
    const fetch = vi.spyOn(globalThis, "fetch").mockImplementation((input) => {
      const url = String(input);
      urls.push(url);
      return Promise.resolve(
        Response.json({
          id: "dev_a",
          name: "A",
          entities: [],
          next_entity_cursor: url.includes("entity_cursor=") ? null : "next",
        })
      );
    });
    try {
      await deviceRequest("dev_a", "canonical").load(
        new AbortController().signal,
        new Date()
      );
      expect(urls).toHaveLength(2);
      expect(
        urls.every(
          (url) =>
            new URL(url, "http://localhost").searchParams.get("units") ===
            "canonical"
        )
      ).toBeTruthy();
      expect(
        new URL(urls[1], "http://localhost").searchParams.get("entity_cursor")
      ).toBe("next");
    } finally {
      fetch.mockRestore();
    }
  });

  it("uses delivered chromaticity for text and canonical chromaticity for the swatch", () => {
    const chromaticityQuantity = { unit: "1", label: "", fraction_digits: 4 };
    const common = {
      name: "Light",
      enabled: true,
      availability: { status: "available" },
      support: {},
    };
    const item = (
      itemId: string,
      type: string,
      value: unknown,
      quantities = {}
    ) => ({
      ...common,
      id: itemId,
      type,
      quantities,
      state: { value, observed_at, observation_id: `obs_${itemId}` },
    });
    const power = item("power", "hearth.power/v1", true);
    const brightness = item("brightness", "hearth.brightness/v1", 50, {
      "": { unit: "%", label: "%", fraction_digits: 0 },
    });
    const color = item(
      "color",
      "hearth.colorxy/v1",
      { active: true, x: 0.3127, y: 0.329 },
      { "/x": chromaticityQuantity, "/y": chromaticityQuantity }
    );
    const canonicalCoordinate = {
      unit: "10*-4",
      label: "10*-4",
      fraction_digits: 0,
    };
    const canonicalColor = {
      ...color,
      quantities: { "/x": canonicalCoordinate, "/y": canonicalCoordinate },
      state: {
        value: { active: true, x: 3127, y: 3290 },
        observed_at,
        observation_id: "obs_color",
      },
    };
    const display = parseDevice(
      { id: "dev", name: "Light", entities: [power, brightness, color] },
      "dev"
    );
    const canonical = parseDevice(
      {
        id: "dev",
        name: "Light",
        entities: [power, brightness, canonicalColor],
      },
      "dev"
    );
    const light = lightReading(display.entities, canonical.entities);
    if (!light) {
      throw new Error("Expected combined light reading");
    }
    expect(light.colorText).toContain("0.3127");
    expect(light.swatch).toBe("rgb(255 255 255)");
    const displayOnly = lightReading(display.entities);
    if (!displayOnly) {
      throw new Error("Expected display light reading");
    }
    expect(displayOnly.swatch).toBeUndefined();
    const unidentified = {
      ...canonicalColor,
      state: { value: canonicalColor.state.value, observed_at },
    };
    const withoutIdentity = parseDevice(
      { id: "dev", name: "Light", entities: [unidentified] },
      "dev"
    );
    const withoutCanonical = lightReading(
      display.entities,
      withoutIdentity.entities
    );
    if (!withoutCanonical) {
      throw new Error("Expected light reading without canonical color");
    }
    expect(withoutCanonical.swatch).toBeUndefined();
  });

  it("uses the delivered identity-unit HS color without a canonical read", () => {
    const color = entityOf("hearth.colorhs/v1", {
      active: true,
      hue: 0,
      saturation: 100,
    });
    const power = { ...entityOf("hearth.power/v1", true), id: "power" };
    const brightness = {
      ...entityOf("hearth.brightness/v1", 50),
      id: "brightness",
    };
    const light = lightReading(
      parseDevice(
        { id: "dev", name: "Light", entities: [power, brightness, color] },
        "dev"
      ).entities
    );
    if (!light) {
      throw new Error("Expected HS light reading");
    }
    expect(light.swatch).toBe("rgb(255 0 0)");
    expect(light.colorPreviewUnavailable).toBeFalsy();
  });

  it("converts RGB channels directly to swatch colors", () => {
    const color = entityOf("hearth.colorrgb/v1", {
      active: true,
      red: 12,
      green: 128,
      blue: 255,
    });
    expect(swatchColor(color)).toBe("rgb(12 128 255)");
    expect(
      swatchColor({
        ...color,
        state: {
          ...color.state,
          value: { active: true, red: 0, green: 0, blue: 0 },
        },
      })
    ).toBe("rgb(0 0 0)");
    expect(
      swatchColor({
        ...color,
        state: { ...color.state, value: { active: true, red: 12, green: 128 } },
      })
    ).toBeUndefined();
  });

  it("rejects a malformed sibling field before replacing the shared cached response", async () => {
    vi.useFakeTimers();
    const cache = new ReadCache();
    try {
      const type = "hearth.colorhs/v1";
      const good = entityOf(type, { active: true, hue: 210, saturation: 64 });
      const fetch = vi
        .spyOn(globalThis, "fetch")
        .mockResolvedValueOnce(Response.json(pairedEntityResponse(good, good)));
      const hue = readingAdapter.stateRequest(source(type, "/hue"));
      cache.subscribe(hue, vi.fn());
      cache.subscribe(
        readingAdapter.stateRequest(source(type, "/saturation")),
        vi.fn()
      );
      await vi.advanceTimersByTimeAsync(0);
      expect(cache.snapshot(hue.key).data).toMatchObject({
        ...good,
        canonical: {
          state: good.state,
          quantities: { "/hue": { unit: "deg" }, "/saturation": { unit: "%" } },
        },
      });
      expect(fetch).toHaveBeenCalledOnce();
      const bad = entityOf(type, { active: true, hue: 220, saturation: "bad" });
      fetch.mockResolvedValueOnce(
        Response.json(pairedEntityResponse(bad, bad))
      );
      await vi.advanceTimersByTimeAsync(10_000);
      expect(fetch).toHaveBeenCalledTimes(2);
      expect(cache.snapshot(hue.key).data).toMatchObject({
        ...good,
        canonical: {
          state: good.state,
          quantities: { "/hue": { unit: "deg" }, "/saturation": { unit: "%" } },
        },
      });
      expect(cache.snapshot(hue.key).error).toBeInstanceOf(Error);
    } finally {
      cache.dispose();
      vi.restoreAllMocks();
      vi.useRealTimers();
    }
  });

  it.each([
    ["hearth.relativehumidity/v1", "us", 45, "%", "%", 1],
    ["hearth.pressure/v1", "metric", 1013.2, "hPa", "hPa", 1],
    ["hearth.pressure/v1", "us", 29.919866023257644, "[in_i'Hg]", "in Hg", 2],
    ["hearth.speed/v1", "us", 7.605583392984967, "[mi_i]/h", "mi/h", 1],
    ["hearth.temperature/v1", "metric", 21.5, "Cel", "°C", 1],
    ["hearth.temperature/v1", "us", 70.7, "[degF]", "°F", 1],
  ] as const)(
    "formats %s for a %s household",
    (type, _system, value, unit, label, digits) => {
      const entity = {
        ...entityOf(type, value),
        quantities: { "": { unit, label, fraction_digits: digits } },
      };
      const metric = readingAdapter.metric(entity, source(type));
      if (!metric || "absent" in metric) {
        throw new Error("Expected numeric metric");
      }
      expect(metric.value).toBeCloseTo(value, 6);
      expect(metric.format).toStrictEqual({
        unit_label: label,
        fraction_digits: digits,
      });
      expect(entity.state.value).toBe(value);
    }
  );

  it("labels generic readings with the Entity's support unit", () => {
    const type = "hearth.numericsensor/v1";
    const entity = {
      ...entityOf(type, 120, {
        state: { minimum: 0, maximum: 255, unit: "lqi" },
        operations: {},
      }),
      quantities: {
        "": {
          unit: "lqi",
          label: "lqi",
          fraction_digits: 1,
          range: { minimum: 0, maximum: 255 },
        },
      },
    };
    expect(readingAdapter.metric(entity, source(type))).toMatchObject({
      value: 120,
      format: { unit_label: "lqi", fraction_digits: 1 },
      range: { minimum: 0, maximum: 255 },
    });
    expect(() =>
      readingAdapter.metric(entityOf(type, 120), source(type))
    ).toThrow(/.+/u);
  });

  it("uses the projected value and bounds, not canonical support bounds", () => {
    const type = "hearth.numericsensor/v1";
    const entity = {
      ...entityOf(type, 0.1, {
        state: {
          unit: "mm",
          quantity_kind: "precipitation_depth",
          minimum: 0,
          maximum: 25.4,
        },
      }),
      quantities: {
        "": {
          unit: "[in_i]",
          label: "in",
          fraction_digits: 2,
          range: { minimum: 0, maximum: 1 },
        },
      },
    };
    expect(readingAdapter.metric(entity, source(type))).toMatchObject({
      value: 0.1,
      format: { unit_label: "in", fraction_digits: 2 },
      range: { minimum: 0, maximum: 1 },
    });
  });

  it("preserves resource condition and unobserved State", () => {
    const type = "hearth.pressure/v1";
    const entity = {
      ...entityOf(type, 1000),
      enabled: false,
      availability: { status: "unavailable" },
    };
    expect(readingAdapter.metadata(entity, source(type))).toMatchObject({
      enabled: false,
      availability: "unavailable",
    });
    expect(
      readingAdapter.metric({ ...entity, state: null }, source(type))
    ).toBeNull();
    expect(
      readingAdapter.metric(
        { ...entity, state: null, quantities: {} },
        source(type)
      )
    ).toBeNull();
  });

  it("presents structured and percentage fields using returned metadata", () => {
    const examples = [
      [
        "brightness",
        "",
        51,
        { state: {}, operations: { set: {} } },
        51,
        "%",
        0,
      ],
      [
        "brightness",
        "",
        99,
        { state: {}, operations: { set: {} } },
        99,
        "%",
        0,
      ],
      [
        "brightness",
        "",
        100,
        { state: {}, operations: { set: {} } },
        100,
        "%",
        0,
      ],
      [
        "colorhs",
        "/hue",
        { active: false, hue: 210, saturation: 64 },
        {},
        210,
        "°",
        0,
      ],
      [
        "colorhs",
        "/saturation",
        { active: true, hue: 210, saturation: 64 },
        {},
        64,
        "%",
        0,
      ],
      [
        "colortemp",
        "/value",
        { active: true, value: 370 },
        {},
        370,
        "mired",
        0,
      ],
      [
        "colorxy",
        "/x",
        { active: true, x: 0.3127, y: 0.329 },
        {},
        0.3127,
        "",
        4,
      ],
      [
        "colorxy",
        "/y",
        { active: true, x: 0.3127, y: 0.329 },
        {},
        0.329,
        "",
        4,
      ],
      [
        "numericsetting",
        "/value",
        { mode: "value", value: 40 },
        { state: { unit: "%" } },
        40,
        "%",
        1,
      ],
      ["numericsetting", "/value", { mode: "value", value: 0 }, {}, 0, "", 1],
    ] as const;
    for (const [name, field, raw, support, value, label, digits] of examples) {
      const type = `hearth.${name}/v1`;
      const base = entityOf(type, raw, support);
      const unit = (
        { "%": "%", "°": "deg", mired: "/MK", "": "1" } as Record<
          string,
          string
        >
      )[label];
      const entity = {
        ...base,
        quantities: {
          ...base.quantities,
          [field]: { unit, label, fraction_digits: digits },
        },
      };
      const metric = readingAdapter.metric(entity, source(type, field));
      if (!metric || "absent" in metric) {
        throw new Error("Expected numeric metric");
      }
      expect(metric.value).toBeCloseTo(value, 6);
      expect(metric).toMatchObject({
        observed_at,
        format: { unit_label: label, fraction_digits: digits },
      });
    }
  });

  it("distinguishes absent optional values from null State and malformed required data", () => {
    const type = "hearth.numericsetting/v1";
    const selected = source(type, "/value");
    const choice = entityOf(type, { mode: "choice", choice: "auto" });
    expect(readingAdapter.metric(choice, selected)).toStrictEqual({
      absent: true,
      observed_at,
    });
    expect(
      readingAdapter.metric({ ...choice, state: null }, selected)
    ).toBeNull();
    for (const value of [null, "40", true]) {
      expect(() =>
        readingAdapter.metric(
          entityOf(type, { mode: "value", value }),
          selected
        )
      ).toThrow(/.+/u);
    }
    expect(() =>
      readingAdapter.metric(
        entityOf("hearth.colorhs/v1", { active: true, hue: 20 }),
        source("hearth.colorhs/v1", "/hue")
      )
    ).toThrow(/.+/u);
    expect(() => readingAdapter.metric(choice, source(type, "/mode"))).toThrow(
      /.+/u
    );
  });

  it.each(
    [null, 42, [], { state: null }, { state: 42 }, { state: [] }].map(
      (support) => ({ support })
    )
  )("rejects malformed optional-unit support $support", ({ support }) => {
    const type = "hearth.numericsetting/v1";
    expect(() =>
      readingAdapter.metric(
        entityOf(type, { mode: "value", value: 40 }, support),
        source(type, "/value")
      )
    ).toThrow(/.+/u);
  });

  it("rejects mismatched identity and unreadable types", () => {
    const type = "hearth.relativehumidity/v1";
    const entity = entityOf(type, 45);
    expect(() =>
      readingAdapter.metric({ ...entity, id: "other" }, source(type))
    ).toThrow(/.+/u);
    expect(() =>
      readingAdapter.metric(
        { ...entity, type: "hearth.temperature/v1" },
        source(type)
      )
    ).toThrow(/.+/u);
    expect(() =>
      readingAdapter.metric(
        { ...entity, type: "hearth.power/v1" },
        source("hearth.power/v1")
      )
    ).toThrow(/.+/u);
  });

  it("preserves delivered brightness history and gaps", () => {
    const type = "hearth.brightness/v1";
    const from = "2026-11-01T04:00:00Z";
    const buckets = Array.from({ length: seriesBuckets }, (_, index) => ({
      start: from,
      end: observed_at,
      min: index === 0 ? 50 : null,
      max: index === 0 ? 51 : null,
      mean: index === 0 ? 50.5 : null,
    }));
    const series = readingAdapter.series(
      {
        from,
        to: observed_at,
        buckets,
        quantity: { unit: "%", label: "%", fraction_digits: 0 },
      },
      source(type),
      entityOf(type, 51, { state: {}, operations: { set: {} } })
    );
    expect(series.buckets).toStrictEqual(buckets);
    expect(series.format).toStrictEqual({
      unit_label: "%",
      fraction_digits: 0,
    });
  });

  it("preserves delivered series aggregates for the household", () => {
    const series = deliveredTemperatureSeries();
    expect(series.from).toBe("2026-11-01T04:00:00Z");
    expect(series.buckets).toHaveLength(seriesBuckets);
    expect(series.buckets[0].min).toBeCloseTo(69.8, 6);
    expect(series.buckets[0].max).toBeCloseTo(71.6, 6);
    expect(series.buckets[0].mean).toBeCloseTo(70.7, 6);
  });

  it("preserves empty series buckets and the delivered format without a sample count", () => {
    const series = deliveredTemperatureSeries();
    expect(series.buckets[1]).toStrictEqual({
      start: "2026-11-01T04:00:00Z",
      end: "2026-11-01T04:01:15Z",
      min: null,
      max: null,
      mean: null,
    });
    expect(series.format).toStrictEqual({
      unit_label: "°F",
      fraction_digits: 1,
    });
    expect("count" in series.buckets[0]).toBeFalsy();
  });
});
