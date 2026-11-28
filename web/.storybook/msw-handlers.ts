import { delay, http, HttpResponse } from "msw";
import { z } from "zod";

import type {
  CommandRecord,
  Device,
  Entity,
  EntityStateHistoryEntry,
  HealthTransition,
} from "../src/api/types";
import { isHeldField } from "../src/features/views/bindings/state-fields";
import type { MediaPlayerState } from "../src/features/views/cards/media-player/data";
import {
  createEntities,
  deviceId,
  entityId,
  fixtureNow,
  observedAt,
} from "./fixtures";
import {
  applyMediaPlayerCommand,
  artworkHandler,
} from "./media-player-fixtures";
import { pairedEntityResponse, pairedHistoryResponse } from "./paired-fixtures";
import {
  applyThermostatCommand,
  thermostatCanonicalState,
  thermostatShownState,
} from "./thermostat-fixtures";
import type { ThermostatState } from "./thermostat-fixtures";

export { fixtureNow } from "./fixtures";
const iso = (milliseconds: number) => new Date(milliseconds).toISOString();
// Core prints a bucket length as Go's Duration.String, such as "5m0s" or "3h0m0s".
const goDurationPattern =
  /^(?:(?<hours>\d+)h)?(?:(?<minutes>\d+)m)?(?<seconds>\d+)s$/u;
const goDuration = (text: string): number => {
  const { hours, minutes, seconds } =
    goDurationPattern.exec(text)?.groups ?? {};
  return (
    (Number(hours ?? 0) * 3600 + Number(minutes ?? 0) * 60 + Number(seconds)) *
    1000
  );
};
const rangeSchema = z.object({
  from: z.string().datetime(),
  to: z.string().datetime(),
});
const historyQuery = z.object({
  range: rangeSchema,
  select_field: z.string(),
  result: z.object({
    bucket: z.object({ duration: z.string().regex(goDurationPattern) }),
  }),
});
const changesQuery = z.object({
  range: rangeSchema,
  select_field: z.string(),
  result: z.object({ kind: z.enum(["summary", "timeline"]) }),
});

const quantitiesOf = (entity: Entity) => {
  const { quantities } = entity;
  if (!quantities) {
    throw new Error("Fixture Entity has no quantities");
  }
  return quantities;
};

const canonical = (entity: Entity): Entity => {
  const copy = structuredClone(entity);
  if (copy.state && copy.type === "hearth.thermostat/v1") {
    copy.state.value = thermostatCanonicalState(
      copy.state.value as Record<string, unknown>
    );
    for (const [path, quantity] of Object.entries(quantitiesOf(copy))) {
      if (quantity.unit === "[degF]") {
        quantitiesOf(copy)[path] = {
          unit: "mCel",
          label: "mCel",
          fraction_digits: 1,
        };
      }
    }
  }
  if (copy.state && copy.type === "hearth.temperature/v1") {
    copy.state.value = Number(copy.state.value) * 1000;
    copy.quantities = {
      "": { unit: "mCel", label: "mCel", fraction_digits: 0 },
    };
  }
  if (
    copy.state &&
    (copy.type === "hearth.hourlyforecast/v1" ||
      copy.type === "hearth.dailyforecast/v1")
  ) {
    const value = copy.state.value as { periods: Record<string, unknown>[] };
    const quantities = quantitiesOf(copy);
    for (const [index, period] of value.periods.entries()) {
      for (const [field, reading] of Object.entries(period)) {
        if (
          (field.startsWith("temperature") ||
            field === "apparent_temperature") &&
          typeof reading === "number"
        ) {
          quantities[`/periods/${index}/${field}`] = {
            unit: "Cel",
            label: "Cel",
            fraction_digits: 1,
          };
        }
      }
    }
  }
  return copy;
};

/** The State a satisfied Command leaves, or undefined for an unsupported Operation. */
const commandedValue = (
  entity: Entity,
  operation: string,
  parameters: Record<string, unknown>
): unknown => {
  if (entity.type === "hearth.thermostat/v1") {
    const state = thermostatCanonicalState(
      entity.state?.value as Record<string, unknown>
    ) as unknown as ThermostatState;
    const next = applyThermostatCommand(state, operation, parameters);
    return next && thermostatShownState({ ...next });
  }
  if (operation !== "set") {
    return undefined;
  }
  if (
    entity.type === "hearth.cover/v1" &&
    typeof parameters.position === "number"
  ) {
    return { position: parameters.position, motion: "idle" };
  }
  return entity.type === "hearth.colorhs/v1"
    ? { ...parameters, active: true }
    : parameters.value;
};

export const createCoreHandlers = (
  overrides: Entity[] = [],
  { commandDelayMs }: { commandDelayMs?: number } = {}
) => {
  const entities = new Map(
    [...createEntities(), ...overrides].map((entity) => [entity.id, entity])
  );
  const retainedStates = new Map(
    [...entities].map(([id, entity]) => [id, canonical(entity).state])
  );
  const devices: Device[] = [
    "Living room purifier",
    "Hallway lamp",
    "Weather station",
    "Office plug",
  ].map((name, index) => ({
    id: deviceId(index + 1),
    name,
    kind: index === 1 ? "light" : "device",
    adapter_name: name,
    name_override: null,
  }));
  let commandNumber = 0;
  return [
    artworkHandler,
    http.get("/v1/entities", ({ request }) => {
      const device = new URL(request.url).searchParams.get("device_id");
      const items = [...entities.values()].filter(
        (entity) => !device || entity.device_id === device
      );
      return HttpResponse.json({ items, next_cursor: null });
    }),
    http.get("/v1/devices", () =>
      HttpResponse.json({ items: devices, next_cursor: null })
    ),
    http.get("/v1/entities/:id", ({ params }) => {
      const entity = entities.get(String(params.id));
      if (!entity) {
        return HttpResponse.json(
          { detail: "Entity not found" },
          { status: 404 }
        );
      }
      return HttpResponse.json(pairedEntityResponse(entity, canonical(entity)));
    }),
    http.get("/v1/devices/:id", ({ params, request }) => {
      const device = devices.find((candidate) => candidate.id === params.id);
      if (!device) {
        return HttpResponse.json(
          { detail: "Device not found" },
          { status: 404 }
        );
      }
      const children = [...entities.values()].filter(
        (entity) => entity.device_id === device.id
      );
      return HttpResponse.json({
        ...device,
        entities:
          new URL(request.url).searchParams.get("units") === "canonical"
            ? children.map(canonical)
            : children,
        next_entity_cursor: null,
      });
    }),
    // Detail pages display retained records separately from the cards' aggregate queries.
    http.get("/v1/entities/:id/commands", ({ params }) => {
      const entity = entities.get(String(params.id));
      const state = retainedStates.get(String(params.id));
      const items: CommandRecord[] =
        entity?.type === "hearth.power/v1" && state
          ? [
              {
                id: "cmd_story_previous",
                entity_id: entity.id,
                operation: "set",
                parameters: { value: state.value },
                status: "satisfied",
                requested_at: "2026-11-09T09:59:59Z",
                deadline_at: "2026-11-09T10:00:04Z",
                completed_at: observedAt,
                outcome_observation_id: state.observation_id,
              },
            ]
          : [];
      return HttpResponse.json({ items, next_cursor: null });
    }),
    http.get("/v1/entities/:id/availability/history", () =>
      HttpResponse.json({
        items: [
          { status: "available", source: "adapter", observed_at: observedAt },
        ] satisfies HealthTransition[],
        next_cursor: null,
      })
    ),
    http.get("/v1/entities/:id/state/history", ({ params }) => {
      const state = retainedStates.get(String(params.id));
      const items: EntityStateHistoryEntry[] = state
        ? [
            {
              observation_id: state.observation_id,
              value: state.value,
              disposition: "applied",
              adapter_received_at: state.adapter_received_at,
              observed_at: state.observed_at,
            },
          ]
        : [];
      return HttpResponse.json({ items, next_cursor: null });
    }),
    http.post("/v1/entities/:id/state/history", async ({ params, request }) => {
      const query = historyQuery.parse(await request.json());
      const entity = entities.get(String(params.id));
      if (!entity) {
        return HttpResponse.json(
          { detail: "Entity not found" },
          { status: 404 }
        );
      }
      const selected = entity;
      const quantity = selected.quantities?.[query.select_field];
      if (!quantity) {
        return HttpResponse.json(
          { detail: "No such numeric field" },
          { status: 422 }
        );
      }
      const from = Date.parse(query.range.from);
      const to = Date.parse(query.range.to);
      const step = goDuration(query.result.bucket.duration);
      const raw: unknown = selected.state?.value;
      const base = Number(
        query.select_field && raw !== null && typeof raw === "object"
          ? (raw as Record<string, unknown>)[query.select_field.slice(1)]
          : raw
      );
      // A set value holds, then jumps: flat buckets with a change every 40.
      const held = isHeldField(selected.type, query.select_field);
      const buckets = Array.from(
        { length: Math.ceil((to - from) / step) },
        (_, index) => {
          const start = from + index * step;
          const pressure = selected.type === "hearth.pressure/v1";
          const measured = pressure
            ? base + (Math.min((to - start) / 60_000, 180) / 180) * 3.1
            : base +
              Math.sin(index / 12) * Math.max(Math.abs(base) * 0.04, 0.2);
          const mean = held
            ? base + (Math.floor(index / 40) % 2) * 2
            : measured;
          const spread = held ? 0 : 0.1;
          const gap = index >= 70 && index < 76;
          return {
            range: { from: iso(start), to: iso(Math.min(start + step, to)) },
            metrics: {
              min: gap ? null : mean - spread,
              max: gap ? null : mean + spread,
              mean: gap ? null : mean,
            },
          };
        }
      );
      const summary = {
        kind: "summary",
        entity_id: selected.id,
        range: query.range,
        time_axis: "observed_at",
        coverage: {
          basis: "retained_observations",
          completeness: "unverified",
        },
        quantity,
        bucket: query.result.bucket,
        buckets,
      };
      const working = canonical(entity);
      const factor = entity.type === "hearth.temperature/v1" ? 1000 : 1;
      return HttpResponse.json(
        pairedHistoryResponse(summary, {
          quantity: quantitiesOf(working)[query.select_field],
          buckets: buckets.map((bucket) => ({
            range: bucket.range,
            metrics: Object.fromEntries(
              Object.entries(bucket.metrics).map(([name, value]) => [
                name,
                value === null ? null : value * factor,
              ])
            ),
          })),
        })
      );
    }),
    http.post("/v1/entities/:id/state/changes", async ({ params, request }) => {
      const query = changesQuery.parse(await request.json());
      const entity = entities.get(String(params.id));
      if (!entity) {
        return HttpResponse.json(
          { detail: "Entity not found" },
          { status: 404 }
        );
      }
      const common = {
        entity_id: entity.id,
        select_field: query.select_field,
        range: query.range,
        time_axis: "observed_at",
        coverage: {
          basis: "retained_changes",
          completeness: "unverified",
          seeded: true,
        },
      };
      const from = Date.parse(query.range.from);
      const to = Date.parse(query.range.to);
      if (query.result.kind === "summary") {
        return HttpResponse.json({
          ...common,
          kind: "summary",
          bucket: { duration: "1h0m0s", alignment: "range_start" },
          buckets: Array.from(
            { length: Math.ceil((to - from) / 3_600_000) },
            (_, index) => {
              const start = from + index * 3_600_000;
              const end = Math.min(start + 3_600_000, to);
              const seconds = (end - start) / 1000;
              return {
                range: { from: iso(start), to: iso(end) },
                seconds_true: index % 5 === 0 ? seconds / 4 : 0,
                known_seconds: index % 17 === 0 ? 0 : seconds,
                transitions_into_true:
                  index % 5 === 0 && index % 17 !== 0 ? 1 : 0,
              };
            }
          ).map((bucket) =>
            bucket.known_seconds === 0 ? { ...bucket, seconds_true: 0 } : bucket
          ),
        });
      }
      const value = entity.state?.value ?? null;
      const changed = Math.max(from, to - 40 * 60_000);
      return HttpResponse.json({
        ...common,
        kind: "timeline",
        intervals: [
          {
            kind: "value",
            from: iso(from),
            to: iso(changed),
            value: typeof value === "boolean" ? !value : "heat",
          },
          { kind: "value", from: iso(changed), to: iso(to), value },
        ],
      });
    }),
    http.post("/v1/entities/:id/commands", async ({ params, request }) => {
      const entity = entities.get(String(params.id));
      if (!entity) {
        return HttpResponse.json(
          { detail: "Entity not found" },
          { status: 404 }
        );
      }
      const body = z
        .object({
          operation: z.string(),
          parameters: z.record(z.string(), z.unknown()),
        })
        .parse(await request.json());
      if (entity.type === "hearth.mediaplayer/v1" && entity.state) {
        commandNumber += 1;
        const now = iso(Date.parse(fixtureNow) + commandNumber * 1000);
        const next = applyMediaPlayerCommand(
          entity.state.value as MediaPlayerState,
          body.operation,
          body.parameters,
          now
        );
        if (!next) {
          return HttpResponse.json(
            { detail: "Unsupported Operation" },
            { status: 400 }
          );
        }
        entity.state = { ...entity.state, value: next.state, observed_at: now };
        return HttpResponse.json(
          next.status === "dispatched"
            ? { command_id: `cmd_story_${commandNumber}`, status: "dispatched" }
            : {
                command_id: `cmd_story_${commandNumber}`,
                status: "satisfied",
                observation_id: `obs_story_command_${commandNumber}`,
                value: body.parameters,
              }
        );
      }
      const value = commandedValue(entity, body.operation, body.parameters);
      if (value === undefined) {
        return HttpResponse.json(
          { detail: "Unsupported Operation" },
          { status: 400 }
        );
      }
      if (commandDelayMs !== undefined) {
        await delay(commandDelayMs);
      }
      commandNumber += 1;
      const observation_id = `obs_story_command_${commandNumber}`;
      entity.state = {
        value,
        observation_id,
        observed_at: iso(Date.parse(fixtureNow) + commandNumber * 1000),
        adapter_received_at: fixtureNow,
      };
      return HttpResponse.json({
        command_id: `cmd_story_${commandNumber}`,
        status: "satisfied",
        observation_id,
        value,
      });
    }),
  ];
};

/** Override one real endpoint to explore a named lifecycle state. */
export const entityScenario = (
  number: number,
  state:
    | "loading"
    | "unobserved"
    | "unavailable"
    | "disabled"
    | "error"
    | "missing"
    | "read-only"
) =>
  http.get(`/v1/entities/${entityId(number)}`, async () => {
    if (state === "loading") {
      await delay("infinite");
      return new HttpResponse(null);
    }
    if (state === "error" || state === "missing") {
      return HttpResponse.json(
        {
          detail:
            state === "missing" ? "Entity not found" : "Story API unavailable",
        },
        { status: state === "missing" ? 404 : 503 }
      );
    }
    const entity = createEntities().find(
      (candidate) => candidate.id === entityId(number)
    );
    if (!entity) {
      throw new Error(`Missing fixture Entity ${number}`);
    }
    if (state === "unobserved") {
      entity.state = null;
    }
    if (state === "unavailable") {
      entity.availability.status = "unavailable";
    }
    if (state === "disabled") {
      entity.enabled = false;
    }
    if (state === "read-only") {
      entity.support.operations = {};
    }
    return HttpResponse.json(pairedEntityResponse(entity, canonical(entity)));
  });
