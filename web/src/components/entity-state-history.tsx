import { useState } from "react";
import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Collection, EntityStateHistoryEntry } from "../api/types.ts";
import { ErrorBox, MonoId, StatusChip } from "./common.tsx";
import { Button } from "./ui/button.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table.tsx";

const HISTORY_LIMIT = 50;

const FILTERS: { value: string; label: string }[] = [
  { value: "state-updates", label: "State updates" },
  { value: "all", label: "All" },
  { value: "applied", label: "Applied" },
  { value: "unchanged", label: "Unchanged" },
  { value: "rejected", label: "Rejected" },
];

function isAccepted(entry: EntityStateHistoryEntry): boolean {
  return entry.disposition === "applied" || entry.disposition === "unchanged";
}

interface PlottedValue {
  numeric: number;
  label: string;
}

/** Integer within static schema bounds; otherwise the value is malformed.
    Bounds come from the Entity type's State schema, never from live support:
    support may narrow after an Observation was recorded, and historical values
    outside current support must still plot. */
function isBoundedInteger(value: unknown, min: number, max: number): value is number {
  return (
    typeof value === "number" && Number.isInteger(value) && value >= min && value <= max
  );
}

/** Numeric chart value for known Entity types; null for malformed values.
    Color temperature State is the object form `{active, value}`; the chart
    plots `value` in mireds. XY, HS, and mode States are structured or
    discrete and have no numeric chart value. Numeric-setting States plot
    only `value`-mode numbers with their unit label; `choice`-mode States
    are listed below. */
function isNumericSettingChoiceState(value: unknown): boolean {
  if (typeof value !== "object" || value === null) return false;
  const state = value as { mode?: unknown; choice?: unknown; value?: unknown };
  return state.mode === "choice" && typeof state.choice === "string" && state.value === undefined;
}

function parseHistoryValue(type: string | undefined, value: unknown): PlottedValue | null {
  switch (type) {
    case "hearth.power/v1":
      return typeof value === "boolean" ? { numeric: value ? 1 : 0, label: value ? "On" : "Off" } : null;
    case "hearth.brightness/v1":
      return isBoundedInteger(value, 0, 100) ? { numeric: value, label: String(value) } : null;
    case "hearth.colortemp/v1": {
      const mireds = colorTempHistoryMireds(value);
      return mireds === null ? null : { numeric: mireds, label: `${mireds} mireds` };
    }
    case "hearth.temperature/v1":
      return isBoundedInteger(value, -273150, 1000000)
        ? { numeric: value / 1000, label: `${value / 1000} °C` }
        : null;
    case "hearth.numericsensor/v1":
      return typeof value === "number" && Number.isFinite(value)
        ? { numeric: value, label: String(value) }
        : null;
    case "hearth.numericsetting/v1": {
      if (typeof value !== "object" || value === null) return null;
      const v = value as { mode?: unknown; value?: unknown };
      return v.mode === "value" && typeof v.value === "number" && Number.isFinite(v.value)
        ? { numeric: v.value, label: String(v.value) }
        : null;
    }
    default:
      return null;
  }
}

/** Mired value from an object-form color-temperature State; null unless the
    shape is `{active: boolean, value: bounded integer}`. Bounds come from the
    Entity type's State schema, never from live support. */
function colorTempHistoryMireds(value: unknown): number | null {
  if (typeof value !== "object" || value === null) return null;
  const v = value as { active?: unknown; value?: unknown };
  if (typeof v.active !== "boolean" || !isBoundedInteger(v.value, 100, 1000)) return null;
  return v.value;
}

/** Table display for structured color States with coordinates, units, and
    mode activity; null for malformed values (the caller falls back to raw
    JSON) and for types without a structured form. */
function formatStructuredHistoryValue(type: string | undefined, value: unknown): string | null {
  switch (type) {
    case "hearth.colorxy/v1": {
      if (typeof value !== "object" || value === null) return null;
      const v = value as { active?: unknown; x?: unknown; y?: unknown };
      if (typeof v.active !== "boolean" || typeof v.x !== "number" || typeof v.y !== "number") return null;
      return `x ${v.x} (${(v.x / 10000).toFixed(4)}), y ${v.y} (${(v.y / 10000).toFixed(4)}), ${v.active ? "active" : "inactive"}`;
    }
    case "hearth.colorhs/v1": {
      if (typeof value !== "object" || value === null) return null;
      const v = value as { active?: unknown; hue?: unknown; saturation?: unknown };
      if (typeof v.active !== "boolean" || typeof v.hue !== "number" || typeof v.saturation !== "number") return null;
      return `hue ${v.hue}°, saturation ${v.saturation}%, ${v.active ? "active" : "inactive"}`;
    }
    case "hearth.colormode/v1":
      return typeof value === "string" ? value : null;
    case "hearth.numericsensor/v1": {
      if (typeof value !== "number") return null;
      return Number.isFinite(value) ? String(value) : null;
    }
    case "hearth.enumsetting/v1":
      return typeof value === "string" ? value : null;
    case "hearth.numericsetting/v1": {
      if (typeof value !== "object" || value === null) return null;
      const v = value as { mode?: unknown; value?: unknown; choice?: unknown };
      if (v.mode === "value" && typeof v.value === "number") return String(v.value);
      if (v.mode === "choice" && typeof v.choice === "string") return v.choice;
      return null;
    }
    case "hearth.colortemp/v1": {
      if (typeof value !== "object" || value === null) return null;
      const v = value as { active?: unknown; value?: unknown };
      if (typeof v.active !== "boolean" || typeof v.value !== "number") return null;
      return `${v.value} mireds, ${v.active ? "active" : "inactive"}`;
    }
    default:
      return null;
  }
}

/** Table display value; falls back to raw JSON for unknown types and malformed values. */
function formatHistoryValue(type: string | undefined, value: unknown): string {
  const structured = formatStructuredHistoryValue(type, value);
  if (structured !== null) return structured;
  const parsed = parseHistoryValue(type, value);
  if (parsed) return parsed.label;
  try {
    return JSON.stringify(value) ?? String(value);
  } catch {
    return String(value);
  }
}

/** Current support bounds in chart units, when the Entity type defines them.
    Support fields are validated as integers within their static support-schema
    ranges; malformed support is ignored so it cannot distort the domain. */
function supportRange(
  type: string | undefined,
  support: Record<string, unknown> | undefined,
): [number, number] | null {
  if (type === "hearth.power/v1") return [0, 1];
  const state = support?.state as { maximum?: unknown; minimum?: unknown } | undefined;
  if (type === "hearth.numericsensor/v1" || type === "hearth.numericsetting/v1") {
    return typeof state?.minimum === "number" &&
      typeof state?.maximum === "number" &&
      Number.isFinite(state.minimum) &&
      Number.isFinite(state.maximum) &&
      state.minimum <= state.maximum
      ? [state.minimum, state.maximum]
      : null;
  }
  if (type === "hearth.brightness/v1") {
    return isBoundedInteger(state?.maximum, 1, 100) ? [0, state.maximum] : null;
  }
  if (type === "hearth.colortemp/v1") {
    return isBoundedInteger(state?.minimum, 100, 1000) &&
      isBoundedInteger(state?.maximum, 100, 1000) &&
      state.minimum <= state.maximum
      ? [state.minimum, state.maximum]
      : null;
  }
  return null;
}

/** Expand a collapsed domain so constant values stay visible. Applied only
    after the union of support bounds and plotted values is computed. */
function padCollapsedDomain(type: string | undefined, value: number): [number, number] {
  switch (type) {
    case "hearth.power/v1":
      return [value - 0.25, value + 0.25];
    case "hearth.brightness/v1":
      return [value - 5, value + 5];
    case "hearth.colortemp/v1":
      return [value - 10, value + 10];
    default:
      return [value - 1, value + 1];
  }
}

function tickLabel(type: string | undefined, tick: number, min: number, max: number): string {
  if (type === "hearth.power/v1") {
    if (tick === min) return "Off";
    if (tick === max) return "On";
    return String(tick);
  }
  if (type === "hearth.temperature/v1") return `${tick} °C`;
  return String(tick);
}

interface ChartPoint {
  /** Ordinal accepted-row position (ascending receive order); gaps mark malformed rows. */
  position: number;
  numeric: number;
  label: string;
  observedAt: string;
}

/** Page-local observed_at bounds in chronological order. Server timestamps
    are UTC RFC3339Nano ending in Z; Date.parse keeps only millisecond
    precision and collapses distinct nanosecond stamps, so compare canonical
    fixed-9 fractional keys lexically and return the original strings. */
function observedAtKey(stamp: string): string {
  const match = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d+))?Z$/.exec(stamp);
  if (!match) return stamp;
  return `${match[1]}.${((match[2] ?? "") + "000000000").slice(0, 9)}Z`;
}

function observedAtBounds(stamps: string[]): [string, string] {
  let min = stamps[0];
  let max = stamps[0];
  let minKey = observedAtKey(min);
  let maxKey = minKey;
  for (const stamp of stamps.slice(1)) {
    const key = observedAtKey(stamp);
    if (key < minKey) {
      min = stamp;
      minKey = key;
    }
    if (key > maxKey) {
      max = stamp;
      maxKey = key;
    }
  }
  return [min, max];
}

const CHART_WIDTH = 560;
const CHART_HEIGHT = 200;
const PAD_LEFT = 48;
const PAD_RIGHT = 12;
const PAD_TOP = 12;
const PAD_BOTTOM = 30;

function StateHistoryChart({
  type,
  support,
  items,
  filter,
}: {
  type: string | undefined;
  support: Record<string, unknown> | undefined;
  items: EntityStateHistoryEntry[];
  filter: string;
}) {
  const accepted = items.filter(isAccepted);
  const rejectedSkipped = items.length - accepted.length;
  // Response order is newest-first; the chart plots ascending receive order.
  const ascending = [...accepted].reverse();
  const points: (ChartPoint | null)[] = ascending.map((entry, position) => {
    const parsed = parseHistoryValue(type, entry.value);
    return parsed
      ? { position, numeric: parsed.numeric, label: parsed.label, observedAt: entry.observed_at }
      : null;
  });
  const valid = points.filter((point): point is ChartPoint => point !== null);
  const choiceSkipped = type === "hearth.numericsetting/v1"
    ? ascending.filter((entry) => isNumericSettingChoiceState(entry.value)).length
    : 0;
  const malformedSkipped = points.length - valid.length - choiceSkipped;

  if (accepted.length === 0) {
    return (
      <p className="mt-3 text-sm text-muted-foreground">
        Rejected observations have no canonical State value to chart.
      </p>
    );
  }
  switch (type) {
    case "hearth.power/v1":
    case "hearth.brightness/v1":
    case "hearth.colortemp/v1":
    case "hearth.temperature/v1":
    case "hearth.numericsensor/v1":
    case "hearth.numericsetting/v1":
      break;
    case "hearth.colorxy/v1":
    case "hearth.colorhs/v1":
      return (
        <p className="mt-3 text-sm text-muted-foreground">
          Structured coordinate values are listed below with units and activity; no chart.
        </p>
      );
    case "hearth.colormode/v1":
    case "hearth.enumsetting/v1":
      return (
        <p className="mt-3 text-sm text-muted-foreground">
          Discrete values are listed below; no chart.
        </p>
      );
    case "hearth.enumaction/v1":
      return (
        <p className="mt-3 text-sm text-muted-foreground">
          Stateless actions record no State; triggers appear in command history.
        </p>
      );
    default:
      return (
        <p className="mt-3 text-sm text-muted-foreground">
          Chart unavailable for this Entity type.
        </p>
      );
  }
  if (valid.length === 0) {
    return (
      <p className="mt-3 text-sm text-muted-foreground">
        {choiceSkipped > 0 && malformedSkipped === 0
          ? "Choice values are listed below; no numeric State values to chart."
          : `No valid State values to chart. (${malformedSkipped} malformed skipped)`}
      </p>
    );
  }

  const range = supportRange(type, support);
  const seeds = range ? [range[0], range[1]] : [];
  let min = Math.min(...seeds, ...valid.map((point) => point.numeric));
  let max = Math.max(...seeds, ...valid.map((point) => point.numeric));
  if (min === max) [min, max] = padCollapsedDomain(type, min);

  const innerWidth = CHART_WIDTH - PAD_LEFT - PAD_RIGHT;
  const innerHeight = CHART_HEIGHT - PAD_TOP - PAD_BOTTOM;
  const count = points.length;
  const x = (position: number) =>
    count === 1 ? PAD_LEFT + innerWidth / 2 : PAD_LEFT + (position * innerWidth) / (count - 1);
  const y = (value: number) => PAD_TOP + (1 - (value - min) / (max - min)) * innerHeight;

  // Maximal runs of valid points at contiguous accepted positions. Rejected
  // rows occupy no position; malformed rows hold a position and break runs.
  const runs: ChartPoint[][] = [];
  for (const point of valid) {
    const last = runs[runs.length - 1];
    if (last && point.position === last[last.length - 1].position + 1) last.push(point);
    else runs.push([point]);
  }

  const stepPath = (run: ChartPoint[]): string => {
    let d = `M ${x(run[0].position)} ${y(run[0].numeric)}`;
    for (let i = 1; i < run.length; i++) {
      d += ` H ${x(run[i].position)} V ${y(run[i].numeric)}`;
    }
    return d;
  };

  const [firstObserved, lastObserved] = observedAtBounds(
    items.map((entry) => entry.observed_at),
  );
  const skipped: string[] = [];
  if (rejectedSkipped > 0) skipped.push(`${rejectedSkipped} rejected skipped`);
  if (choiceSkipped > 0) skipped.push(`${choiceSkipped} choice values listed below`);
  if (malformedSkipped > 0) skipped.push(`${malformedSkipped} malformed skipped`);

  return (
    <figure className="mt-3">
      <svg
        viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`}
        className="h-auto w-full max-w-[560px] text-muted-foreground"
        role="img"
        aria-label={`State history chart for ${type}`}
      >
        {[min, max].map((tick) => (
          <g key={tick}>
            <line
              x1={PAD_LEFT}
              x2={CHART_WIDTH - PAD_RIGHT}
              y1={y(tick)}
              y2={y(tick)}
              className="stroke-border"
              strokeWidth={1}
            />
            <text x={PAD_LEFT - 6} y={y(tick) + 3.5} textAnchor="end" fontSize={10} fill="currentColor">
              {tickLabel(type, tick, min, max)}
            </text>
          </g>
        ))}
        {filter === "unchanged"
          ? valid.map((point) => (
              <circle
                key={point.position}
                cx={x(point.position)}
                cy={y(point.numeric)}
                r={3.5}
                className="fill-primary"
              >
                <title>{`${point.label} at ${point.observedAt}`}</title>
              </circle>
            ))
          : runs.map((run, index) =>
              run.length === 1 ? (
                <circle
                  key={index}
                  cx={x(run[0].position)}
                  cy={y(run[0].numeric)}
                  r={3.5}
                  className="fill-primary"
                >
                  <title>{`${run[0].label} at ${run[0].observedAt}`}</title>
                </circle>
              ) : (
                <path
                  key={index}
                  d={stepPath(run)}
                  fill="none"
                  className="stroke-primary"
                  strokeWidth={2}
                  strokeLinejoin="round"
                />
              ),
            )}
        {valid.length === 1 && (
          <text
            x={Math.min(x(valid[0].position) + 8, CHART_WIDTH - PAD_RIGHT - 40)}
            y={Math.max(y(valid[0].numeric) - 8, PAD_TOP)}
            fontSize={11}
            fill="currentColor"
          >
            {valid[0].label}
          </text>
        )}
        <text
          x={PAD_LEFT + innerWidth / 2}
          y={CHART_HEIGHT - 8}
          textAnchor="middle"
          fontSize={10}
          fill="currentColor"
        >
          Observation sequence (not elapsed time)
        </text>
      </svg>
      <figcaption className="mt-1 text-xs text-muted-foreground">
        {`Observed ${firstObserved} to ${lastObserved} · ${valid.length} plotted${skipped.length > 0 ? ` · ${skipped.join(" · ")}` : ""}.`}
        {filter === "unchanged" && " Unchanged observations only; intervening State changes are not shown."}
      </figcaption>
    </figure>
  );
}

export default function EntityStateHistory({
  entityId,
  entityType,
  support,
}: {
  entityId: string;
  entityType: string | undefined;
  support: Record<string, unknown> | undefined;
}) {
  const [filter, setFilter] = useState("state-updates");
  const [nonce, setNonce] = useState(0);
  const baseVersion = useBaseUrlVersion();
  // Remount the scope below on Entity, filter, or server change: the fresh
  // instance starts with no rows and fires exactly one request, so neither
  // stale rows nor an in-flight request from a previous scope can appear
  // under the new scope. The nonce makes Latest / Refresh history refetch
  // the first page even when already on it.
  const scopeKey = `${entityId}|${filter}|${baseVersion}|${nonce}`;
  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <div role="group" aria-label="State history filter" className="flex flex-wrap gap-1.5">
          {FILTERS.map((option) => (
            <Button
              key={option.value}
              size="sm"
              variant={filter === option.value ? "default" : "outline"}
              aria-pressed={filter === option.value}
              onClick={() => setFilter(option.value)}
            >
              {option.label}
            </Button>
          ))}
        </div>
        <Button size="sm" variant="outline" onClick={() => setNonce((n) => n + 1)}>
          Latest / Refresh history
        </Button>
      </div>
      <p className="mt-1.5 text-xs text-muted-foreground">
        Newer observations appear after refreshing. Older entries may expire while paging.
      </p>
      <HistoryScope
        key={scopeKey}
        entityId={entityId}
        entityType={entityType}
        support={support}
        filter={filter}
      />
    </div>
  );
}

/** One Entity/filter/server scope: owns the cursor and the request, so a
    scope change remounts with an empty cursor and no carried-over rows. */
function HistoryScope({
  entityId,
  entityType,
  support,
  filter,
}: {
  entityId: string;
  entityType: string | undefined;
  support: Record<string, unknown> | undefined;
  filter: string;
}) {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const { data, error, loading } = useApi(
    `state-history-${entityId}-${filter}-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<EntityStateHistoryEntry>>(
        `/v1/entities/${entityId}/state/history?limit=${HISTORY_LIMIT}&disposition=${encodeURIComponent(filter)}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
      ),
  );

  return (
    <div>
      {loading && <span className="mt-1.5 block text-sm text-muted-foreground">Loading…</span>}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          {data.items.length === 0 ? (
            <p className="mt-3 text-sm text-muted-foreground">
              No state history recorded. No State updates to chart.
            </p>
          ) : (
            <>
              <StateHistoryChart
                type={entityType}
                support={support}
                items={data.items}
                filter={filter}
              />
              <Table className="mt-3">
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead>Value</TableHead>
                    <TableHead>Disposition</TableHead>
                    <TableHead>Observation ID</TableHead>
                    <TableHead>Observed at</TableHead>
                    <TableHead>Adapter received at</TableHead>
                    <TableHead>Source updated at</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.items.map((entry) => (
                    <TableRow key={entry.observation_id}>
                      <TableCell
                        className="font-mono text-xs"
                        title={entry.value === undefined ? undefined : JSON.stringify(entry.value)}
                      >
                        {entry.disposition === "rejected"
                          ? "—"
                          : formatHistoryValue(entityType, entry.value)}
                      </TableCell>
                      <TableCell>
                        <span className="flex flex-wrap items-center gap-1.5">
                          <StatusChip status={entry.disposition} />
                          {entry.rejection_code && (
                            <span className="font-mono text-xs text-muted-foreground">
                              {entry.rejection_code}
                            </span>
                          )}
                        </span>
                      </TableCell>
                      <TableCell className="max-w-[16rem]">
                        <MonoId value={entry.observation_id} className="text-muted-foreground" />
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {entry.observed_at}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {entry.adapter_received_at}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {entry.source_updated_at ?? "—"}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </>
          )}
          {data.next_cursor && (
            <Button
              size="sm"
              variant="outline"
              className="mt-2"
              onClick={() => setCursor(data.next_cursor)}
            >
              Next page
            </Button>
          )}
        </>
      )}
    </div>
  );
}
