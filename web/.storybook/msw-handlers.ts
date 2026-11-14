import { delay, http, HttpResponse } from "msw";
import { z } from "zod";
import type { Device, Entity } from "../src/api/types";
import { createEntities, deviceId, entityId, fixtureNow } from "./fixtures";

export { fixtureNow } from "./fixtures";
const iso = (milliseconds: number) => new Date(milliseconds).toISOString();
const rangeSchema = z.object({ from: z.string().datetime(), to: z.string().datetime() });
const historyQuery = z.object({
  range: rangeSchema, select_field: z.string(),
  result: z.object({ bucket: z.object({ duration: z.enum(["1m0s", "5m0s", "30m0s"]) }) }),
});
const changesQuery = z.object({
  range: rangeSchema, select_field: z.string(),
  result: z.object({ kind: z.enum(["summary", "timeline"]) }),
});

function canonical(entity: Entity): Entity {
  const copy = structuredClone(entity);
  if (copy.state && copy.type === "hearth.temperature/v1") {
    copy.state.value = Number(copy.state.value) * 1000;
    copy.quantities = { "": { unit: "mCel", label: "mCel", fraction_digits: 0 } };
  }
  return copy;
}

export function createCoreHandlers() {
  const entities = new Map(createEntities().map(entity => [entity.id, entity]));
  const devices: Device[] = ["Living room purifier", "Hallway lamp", "Weather station", "Office plug"].map((name, index) => ({
    id: deviceId(index + 1), name, kind: index === 1 ? "light" : "device",
    adapter_name: name, name_override: null,
  }));
  let commandNumber = 0;
  return [
    http.get("/v1/entities", () => HttpResponse.json({ items: [...entities.values()], next_cursor: null })),
    http.get("/v1/devices", () => HttpResponse.json({ items: devices, next_cursor: null })),
    http.get("/v1/entities/:id", ({ params, request }) => {
      const entity = entities.get(String(params.id));
      if (!entity) return HttpResponse.json({ detail: "Entity not found" }, { status: 404 });
      return HttpResponse.json(new URL(request.url).searchParams.get("units") === "canonical" ? canonical(entity) : entity);
    }),
    http.get("/v1/devices/:id", ({ params, request }) => {
      const device = devices.find(device => device.id === params.id);
      if (!device) return HttpResponse.json({ detail: "Device not found" }, { status: 404 });
      const children = [...entities.values()].filter(entity => entity.device_id === device.id);
      return HttpResponse.json({ ...device, entities: new URL(request.url).searchParams.get("units") === "canonical" ? children.map(canonical) : children, next_entity_cursor: null });
    }),
    http.post("/v1/entities/:id/state/history", async ({ params, request }) => {
      const query = historyQuery.parse(await request.json());
      const entity = entities.get(String(params.id));
      if (!entity) return HttpResponse.json({ detail: "Entity not found" }, { status: 404 });
      const selected = new URL(request.url).searchParams.get("units") === "canonical" ? canonical(entity) : entity;
      const quantity = selected.quantities?.[query.select_field];
      if (!quantity) return HttpResponse.json({ detail: "No such numeric field" }, { status: 422 });
      const from = Date.parse(query.range.from), to = Date.parse(query.range.to);
      const step = { "1m0s": 60_000, "5m0s": 300_000, "30m0s": 1_800_000 }[query.result.bucket.duration];
      const base = Number(selected.state?.value);
      const buckets = Array.from({ length: Math.ceil((to - from) / step) }, (_, index) => {
        const start = from + index * step;
        const pressure = selected.type === "hearth.pressure/v1";
        const mean = pressure ? base + Math.min((to - start) / 60_000, 180) / 180 * 3.1
          : base + Math.sin(index / 12) * Math.max(Math.abs(base) * .04, .2);
        const gap = index >= 70 && index < 76;
        return {
          range: { from: iso(start), to: iso(Math.min(start + step, to)) },
          metrics: { min: gap ? null : mean - .1, max: gap ? null : mean + .1, mean: gap ? null : mean },
        };
      });
      return HttpResponse.json({ kind: "summary", entity_id: selected.id, range: query.range,
        time_axis: "observed_at", coverage: { basis: "retained_observations", completeness: "unverified" },
        quantity, bucket: query.result.bucket, buckets });
    }),
    http.post("/v1/entities/:id/state/changes", async ({ params, request }) => {
      const query = changesQuery.parse(await request.json());
      const entity = entities.get(String(params.id));
      if (!entity) return HttpResponse.json({ detail: "Entity not found" }, { status: 404 });
      const common = { entity_id: entity.id, select_field: query.select_field, range: query.range,
        time_axis: "observed_at", coverage: { basis: "retained_changes", completeness: "unverified", seeded: true } };
      const from = Date.parse(query.range.from), to = Date.parse(query.range.to);
      if (query.result.kind === "summary") {
        return HttpResponse.json({ ...common, kind: "summary", bucket: { duration: "1h0m0s", alignment: "range_start" },
          buckets: Array.from({ length: Math.ceil((to - from) / 3_600_000) }, (_, index) => {
            const start = from + index * 3_600_000, end = Math.min(start + 3_600_000, to), seconds = (end - start) / 1000;
            return { range: { from: iso(start), to: iso(end) }, seconds_true: index % 5 === 0 ? seconds / 4 : 0,
              known_seconds: index % 17 === 0 ? 0 : seconds, transitions_into_true: index % 5 === 0 && index % 17 !== 0 ? 1 : 0 };
          }).map(bucket => bucket.known_seconds === 0 ? { ...bucket, seconds_true: 0 } : bucket) });
      }
      const value = entity.state?.value ?? null;
      const changed = Math.max(from, to - 40 * 60_000);
      return HttpResponse.json({ ...common, kind: "timeline", intervals: [
        { kind: "value", from: iso(from), to: iso(changed), value: typeof value === "boolean" ? !value : "heat" },
        { kind: "value", from: iso(changed), to: iso(to), value },
      ] });
    }),
    http.post("/v1/entities/:id/commands", async ({ params, request }) => {
      const entity = entities.get(String(params.id));
      if (!entity) return HttpResponse.json({ detail: "Entity not found" }, { status: 404 });
      const body = z.object({ operation: z.literal("set"), parameters: z.record(z.string(), z.unknown()) }).parse(await request.json());
      const value = entity.type === "hearth.colorhs/v1"
        ? { ...body.parameters, active: true }
        : body.parameters.value;
      const observation_id = `obs_story_command_${++commandNumber}`;
      entity.state = { value, observation_id, observed_at: iso(Date.parse(fixtureNow) + commandNumber * 1000),
        adapter_received_at: fixtureNow };
      return HttpResponse.json({ command_id: `cmd_story_${commandNumber}`, status: "satisfied", observation_id, value });
    }),
  ];
}

/** Override one real endpoint to explore a named lifecycle state. */
export function entityScenario(number: number, state: "loading" | "unobserved" | "unavailable" | "disabled" | "error" | "missing" | "read-only") {
  return http.get(`/v1/entities/${entityId(number)}`, async () => {
    if (state === "loading") { await delay("infinite"); return new HttpResponse(null); }
    if (state === "error" || state === "missing") return HttpResponse.json({ detail: state === "missing" ? "Entity not found" : "Story API unavailable" }, { status: state === "missing" ? 404 : 503 });
    const entity = createEntities().find(entity => entity.id === entityId(number))!;
    if (state === "unobserved") entity.state = null;
    if (state === "unavailable") entity.availability.status = "unavailable";
    if (state === "disabled") entity.enabled = false;
    if (state === "read-only") entity.support.operations = {};
    return HttpResponse.json(entity);
  });
}
