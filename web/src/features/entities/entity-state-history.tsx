import { useState } from "react";
import type { ReactNode } from "react";

import { apiFetch, useBaseUrlVersion } from "../../api/client.ts";
import { useApi } from "../../api/hooks.ts";
import type { Collection, EntityStateHistoryEntry } from "../../api/types.ts";
import { Button } from "../../components/button.tsx";
import { ErrorBox } from "../../components/error-box.tsx";
import { MonoId } from "../../components/mono-id.tsx";
import { SegmentedControl } from "../../components/segmented-control.tsx";
import { StatusChip } from "../../components/status-chip.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../../components/table.tsx";
import { forecastSummary } from "./forecast-summary.ts";

const HISTORY_LIMIT = 50;

const FILTERS: { value: string; label: string }[] = [
  { value: "state-updates", label: "State updates" },
  { value: "all", label: "All" },
  { value: "applied", label: "Applied" },
  { value: "unchanged", label: "Unchanged" },
  { value: "rejected", label: "Rejected" },
];

const isAccepted = (entry: EntityStateHistoryEntry): boolean =>
  entry.disposition === "applied" || entry.disposition === "unchanged";

interface PlottedValue {
  numeric: number;
  label: string;
}

/** Integer within static schema bounds; otherwise the value is malformed.
    Bounds come from the Entity type's State schema, never from live support:
    support may narrow after an Observation was recorded, and historical values
    outside current support must still plot. */
const isBoundedInteger = (
  value: unknown,
  min: number,
  max: number
): value is number =>
  typeof value === "number" &&
  Number.isInteger(value) &&
  value >= min &&
  value <= max;

/** Numeric chart value for known Entity types; null for malformed values.
    Color temperature State is the object form `{active, value}`; the chart
    plots `value` in mireds. XY, HS, and mode States are structured or
    discrete and have no numeric chart value. Numeric-setting States plot
    only `value`-mode numbers with their unit label; `choice`-mode States
    are listed below. */
const isNumericSettingChoiceState = (value: unknown): boolean => {
  if (typeof value !== "object" || value === null) {
    return false;
  }
  const state = value as { mode?: unknown; choice?: unknown; value?: unknown };
  return (
    state.mode === "choice" &&
    typeof state.choice === "string" &&
    state.value === undefined
  );
};

/** Nonempty support.state.unit, or empty when absent or malformed. */
const supportUnit = (support: Record<string, unknown> | undefined): string => {
  const unit = (support?.state as { unit?: unknown } | undefined)?.unit;
  return typeof unit === "string" && unit ? unit : "";
};

/** Mired value from an object-form color-temperature State; null unless the
    shape is `{active: boolean, value: bounded integer}`. Bounds come from the
    Entity type's State schema, never from live support. */
const colorTempHistoryMireds = (value: unknown): number | null => {
  if (typeof value !== "object" || value === null) {
    return null;
  }
  const state = value as { active?: unknown; value?: unknown };
  if (
    typeof state.active !== "boolean" ||
    !isBoundedInteger(state.value, 100, 1000)
  ) {
    return null;
  }
  return state.value;
};

const booleanPlot = (
  value: unknown,
  on: string,
  off: string
): PlottedValue | null => {
  if (typeof value !== "boolean") {
    return null;
  }
  return { numeric: value ? 1 : 0, label: value ? on : off };
};

const plotBrightness = (value: unknown): PlottedValue | null =>
  isBoundedInteger(value, 0, 100)
    ? { numeric: value, label: `${value}%` }
    : null;

const plotTemperature = (value: unknown): PlottedValue | null =>
  isBoundedInteger(value, -273_150, 1_000_000)
    ? { numeric: value, label: `${value} mCel` }
    : null;

const plotNumericSensor = (value: unknown): PlottedValue | null =>
  typeof value === "number" ? { numeric: value, label: String(value) } : null;

const plotNumericSetting = (
  value: unknown,
  support?: Record<string, unknown> | undefined
): PlottedValue | null => {
  if (typeof value !== "object" || value === null) {
    return null;
  }
  const state = value as { mode?: unknown; value?: unknown };
  if (state.mode !== "value" || typeof state.value !== "number") {
    return null;
  }
  const unit = supportUnit(support);
  return {
    numeric: state.value,
    label: unit ? `${state.value} ${unit}` : String(state.value),
  };
};

const plotColorTemp = (value: unknown): PlottedValue | null => {
  const mireds = colorTempHistoryMireds(value);
  return mireds === null
    ? null
    : { numeric: mireds, label: `${mireds} mireds` };
};

type HistoryPlotter = (
  value: unknown,
  support?: Record<string, unknown>
) => PlottedValue | null;

const historyPlotters = new Map<string, HistoryPlotter>([
  ["hearth.power/v1", (value) => booleanPlot(value, "On", "Off")],
  ["hearth.binarysensor/v1", (value) => booleanPlot(value, "True", "False")],
  ["hearth.brightness/v1", plotBrightness],
  ["hearth.colortemp/v1", plotColorTemp],
  ["hearth.temperature/v1", plotTemperature],
  ["hearth.numericsensor/v1", plotNumericSensor],
  ["hearth.numericsetting/v1", plotNumericSetting],
]);

const parseHistoryValue = (
  type: string | undefined,
  value: unknown,
  support?: Record<string, unknown> | undefined
): PlottedValue | null => {
  if (!type) {
    return null;
  }
  const plot = historyPlotters.get(type);
  return plot ? plot(value, support) : null;
};

const formatColorXY = (value: unknown): string | null => {
  if (typeof value !== "object" || value === null) {
    return null;
  }
  const state = value as { active?: unknown; x?: unknown; y?: unknown };
  if (
    typeof state.active !== "boolean" ||
    typeof state.x !== "number" ||
    typeof state.y !== "number"
  ) {
    return null;
  }
  return `x ${state.x}, y ${state.y} (10*-4), ${state.active ? "active" : "inactive"}`;
};

const formatColorHS = (value: unknown): string | null => {
  if (typeof value !== "object" || value === null) {
    return null;
  }
  const state = value as {
    active?: unknown;
    hue?: unknown;
    saturation?: unknown;
  };
  if (
    typeof state.active !== "boolean" ||
    typeof state.hue !== "number" ||
    typeof state.saturation !== "number"
  ) {
    return null;
  }
  return `hue ${state.hue}°, saturation ${state.saturation}%, ${state.active ? "active" : "inactive"}`;
};

const formatColorMode = (value: unknown): string | null =>
  typeof value === "string" ? value : null;

const formatEnumSetting = (value: unknown): string | null =>
  typeof value === "string" ? value : null;

const formatNumericSettingChoice = (value: unknown): string | null => {
  if (typeof value !== "object" || value === null) {
    return null;
  }
  const state = value as { mode?: unknown; choice?: unknown };
  return state.mode === "choice" && typeof state.choice === "string"
    ? state.choice
    : null;
};

const formatColorTemp = (value: unknown): string | null => {
  if (typeof value !== "object" || value === null) {
    return null;
  }
  const state = value as { active?: unknown; value?: unknown };
  if (typeof state.active !== "boolean" || typeof state.value !== "number") {
    return null;
  }
  return `${state.value} mireds, ${state.active ? "active" : "inactive"}`;
};

type StructuredFormatter = (value: unknown) => string | null;

const structuredFormatters = new Map<string, StructuredFormatter>([
  ["hearth.colorxy/v1", formatColorXY],
  ["hearth.colorhs/v1", formatColorHS],
  ["hearth.colormode/v1", formatColorMode],
  ["hearth.enumsetting/v1", formatEnumSetting],
  ["hearth.numericsetting/v1", formatNumericSettingChoice],
  ["hearth.colortemp/v1", formatColorTemp],
]);

/** Table display for structured color States with coordinates, units, and
    mode activity; null for malformed values (the caller falls back to raw
    JSON) and for types without a structured form. */
const formatStructuredHistoryValue = (
  type: string | undefined,
  value: unknown
): string | null => {
  const forecast = forecastSummary(type, value);
  if (forecast !== null) {
    return forecast;
  }
  if (!type) {
    return null;
  }
  const format = structuredFormatters.get(type);
  return format ? format(value) : null;
};

/** Table display value; falls back to raw JSON for unknown types and malformed values. */
const formatHistoryValue = (
  type: string | undefined,
  value: unknown,
  support?: Record<string, unknown> | undefined
): string => {
  const structured = formatStructuredHistoryValue(type, value);
  if (structured !== null) {
    return structured;
  }
  const parsed = parseHistoryValue(type, value, support);
  if (parsed) {
    return parsed.label;
  }
  return JSON.stringify(value) ?? String(value);
};

/** Fixed semantic or control bounds in chart units. Generic numeric sensor
    bounds are validation envelopes rather than operating ranges, so their
    charts scale to the page's observed values instead. */
const supportRange = (
  type: string | undefined,
  support: Record<string, unknown> | undefined
): [number, number] | null => {
  if (type === "hearth.power/v1" || type === "hearth.binarysensor/v1") {
    return [0, 1];
  }
  const state = support?.state as
    | { maximum?: unknown; minimum?: unknown }
    | undefined;
  if (type === "hearth.numericsetting/v1") {
    return typeof state?.minimum === "number" &&
      typeof state?.maximum === "number" &&
      state.minimum <= state.maximum
      ? [state.minimum, state.maximum]
      : null;
  }
  if (type === "hearth.brightness/v1") {
    return [0, 100];
  }
  if (type === "hearth.colortemp/v1") {
    return isBoundedInteger(state?.minimum, 100, 1000) &&
      isBoundedInteger(state?.maximum, 100, 1000) &&
      state.minimum <= state.maximum
      ? [state.minimum, state.maximum]
      : null;
  }
  return null;
};

/** Expand a collapsed domain so constant values stay visible. Applied only
    after the union of support bounds and plotted values is computed. */
const padCollapsedDomain = (
  type: string | undefined,
  value: number
): [number, number] => {
  const pad = type === "hearth.colortemp/v1" ? 10 : 1;
  return [value - pad, value + pad];
};

const tickLabel = (
  type: string | undefined,
  tick: number,
  min: number
): string => {
  // Power and binary sensor charts always span [0, 1], so their ticks are the bounds.
  if (type === "hearth.power/v1") {
    return tick === min ? "Off" : "On";
  }
  if (type === "hearth.binarysensor/v1") {
    return tick === min ? "False" : "True";
  }
  if (type === "hearth.temperature/v1") {
    return `${tick} mCel`;
  }
  if (type === "hearth.brightness/v1") {
    return `${tick}%`;
  }
  return String(tick);
};

interface ChartPoint {
  /** Ordinal accepted-row position (ascending receive order); gaps mark malformed rows. */
  position: number;
  numeric: number;
  label: string;
  observedAt: string;
}

/** Page-local earliest and latest observed_at, as the server's strings. */
const observedAtBounds = (stamps: string[]): [string, string] => {
  const byTime = stamps.toSorted((a, b) => Date.parse(a) - Date.parse(b));
  const [first] = byTime;
  const last = byTime.at(-1);
  if (first === undefined || last === undefined) {
    throw new Error("observed_at bounds require at least one timestamp");
  }
  return [first, last];
};

const CHART_WIDTH = 560;
const CHART_HEIGHT = 200;
const PAD_LEFT = 48;
const PAD_RIGHT = 12;
const PAD_TOP = 12;
const PAD_BOTTOM = 30;

type ChartKind =
  | "chartable"
  | "coordinates"
  | "discrete"
  | "stateless"
  | "forecast"
  | "unsupported";

const historyChartKind = (type: string | undefined): ChartKind => {
  switch (type) {
    case "hearth.power/v1":
    case "hearth.binarysensor/v1":
    case "hearth.brightness/v1":
    case "hearth.colortemp/v1":
    case "hearth.temperature/v1":
    case "hearth.numericsensor/v1":
    case "hearth.numericsetting/v1": {
      return "chartable";
    }
    case "hearth.colorxy/v1":
    case "hearth.colorhs/v1": {
      return "coordinates";
    }
    case "hearth.colormode/v1":
    case "hearth.enumsetting/v1": {
      return "discrete";
    }
    case "hearth.enumaction/v1": {
      return "stateless";
    }
    case "hearth.hourlyforecast/v1":
    case "hearth.dailyforecast/v1": {
      return "forecast";
    }
    default: {
      return "unsupported";
    }
  }
};

const renderChartNotice = (
  kind: ChartKind,
  acceptedCount: number,
  validCount: number,
  choiceSkipped: number,
  malformedSkipped: number
): ReactNode | null => {
  if (acceptedCount === 0) {
    return (
      <p className="text-muted-foreground mt-3 text-sm">
        Rejected observations have no canonical State value to chart.
      </p>
    );
  }
  if (kind === "coordinates") {
    return (
      <p className="text-muted-foreground mt-3 text-sm">
        Structured coordinate values are listed below with units and activity;
        no chart.
      </p>
    );
  }
  if (kind === "discrete") {
    return (
      <p className="text-muted-foreground mt-3 text-sm">
        Discrete values are listed below; no chart.
      </p>
    );
  }
  if (kind === "stateless") {
    return (
      <p className="text-muted-foreground mt-3 text-sm">
        Stateless actions record no State; triggers appear in command history.
      </p>
    );
  }
  if (kind === "forecast") {
    return (
      <p className="text-muted-foreground mt-3 text-sm">
        Forecast snapshots are listed below; no chart.
      </p>
    );
  }
  if (kind === "unsupported") {
    return (
      <p className="text-muted-foreground mt-3 text-sm">
        Chart unavailable for this Entity type.
      </p>
    );
  }
  if (validCount === 0) {
    return (
      <p className="text-muted-foreground mt-3 text-sm">
        {choiceSkipped > 0 && malformedSkipped === 0
          ? "Choice values are listed below; no numeric State values to chart."
          : `No valid State values to chart. (${malformedSkipped} malformed skipped)`}
      </p>
    );
  }
  return null;
};

const skippedParts = (
  rejected: number,
  choice: number,
  malformed: number
): string[] => {
  const parts: string[] = [];
  if (rejected > 0) {
    parts.push(`${rejected} rejected skipped`);
  }
  if (choice > 0) {
    parts.push(`${choice} choice values listed below`);
  }
  if (malformed > 0) {
    parts.push(`${malformed} malformed skipped`);
  }
  return parts;
};

const StateHistoryChart = ({
  type,
  support,
  items,
  filter,
}: {
  type: string | undefined;
  support: Record<string, unknown> | undefined;
  items: EntityStateHistoryEntry[];
  filter: string;
}) => {
  const accepted = items.filter(isAccepted);
  const rejectedSkipped = items.length - accepted.length;
  // Response order is newest-first; the chart plots ascending receive order.
  const ascending = accepted.toReversed();
  const points: (ChartPoint | null)[] = ascending.map((entry, position) => {
    const parsed = parseHistoryValue(type, entry.value, support);
    return parsed
      ? {
          position,
          numeric: parsed.numeric,
          label: parsed.label,
          observedAt: entry.observed_at,
        }
      : null;
  });
  const valid = points.filter((point): point is ChartPoint => point !== null);
  const choiceSkipped =
    type === "hearth.numericsetting/v1"
      ? ascending.filter((entry) => isNumericSettingChoiceState(entry.value))
          .length
      : 0;
  const malformedSkipped = points.length - valid.length - choiceSkipped;

  const notice = renderChartNotice(
    historyChartKind(type),
    accepted.length,
    valid.length,
    choiceSkipped,
    malformedSkipped
  );
  if (notice !== null) {
    return notice;
  }

  const range = supportRange(type, support);
  const seeds = range ? [range[0], range[1]] : [];
  let min = Math.min(...seeds, ...valid.map((point) => point.numeric));
  let max = Math.max(...seeds, ...valid.map((point) => point.numeric));
  if (min === max) {
    [min, max] = padCollapsedDomain(type, min);
  }

  const innerWidth = CHART_WIDTH - PAD_LEFT - PAD_RIGHT;
  const innerHeight = CHART_HEIGHT - PAD_TOP - PAD_BOTTOM;
  const count = points.length;
  const x = (position: number) =>
    count === 1
      ? PAD_LEFT + innerWidth / 2
      : PAD_LEFT + (position * innerWidth) / (count - 1);
  const y = (value: number) =>
    PAD_TOP + (1 - (value - min) / (max - min)) * innerHeight;

  // Maximal runs of valid points at contiguous accepted positions. Rejected
  // rows occupy no position; malformed rows hold a position and break runs.
  const runs: ChartPoint[][] = [];
  for (const point of valid) {
    const last = runs.at(-1);
    const lastPoint = last?.at(-1);
    if (last && lastPoint && point.position === lastPoint.position + 1) {
      last.push(point);
    } else {
      runs.push([point]);
    }
  }

  const stepPath = (run: ChartPoint[]): string => {
    let d = `M ${x(run[0].position)} ${y(run[0].numeric)}`;
    for (let index = 1; index < run.length; index += 1) {
      d += ` H ${x(run[index].position)} V ${y(run[index].numeric)}`;
    }
    return d;
  };

  const [firstObserved, lastObserved] = observedAtBounds(
    items.map((entry) => entry.observed_at)
  );
  const skipped = skippedParts(
    rejectedSkipped,
    choiceSkipped,
    malformedSkipped
  );

  return (
    <figure className="mt-3">
      <svg
        viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`}
        className="text-muted-foreground h-auto w-full max-w-[560px]"
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
            <text
              x={PAD_LEFT - 6}
              y={y(tick) + 3.5}
              textAnchor="end"
              fontSize={10}
              fill="currentColor"
            >
              {tickLabel(type, tick, min)}
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
              )
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
      <figcaption className="text-muted-foreground mt-1 text-xs">
        {`Observed ${firstObserved} to ${lastObserved} · ${valid.length} plotted${skipped.length > 0 ? ` · ${skipped.join(" · ")}` : ""}.`}
        {filter === "unchanged" &&
          " Unchanged observations only; intervening State changes are not shown."}
      </figcaption>
    </figure>
  );
};

/** One Entity/filter/server scope: owns the cursor and the request, so a
    scope change remounts with an empty cursor and no carried-over rows. */
const HistoryScope = ({
  entityId,
  entityType,
  support,
  filter,
}: {
  entityId: string;
  entityType: string | undefined;
  support: Record<string, unknown> | undefined;
  filter: string;
}) => {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const { data, error, loading } = useApi(
    `state-history-${entityId}-${filter}-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<EntityStateHistoryEntry>>(
        `/v1/entities/${entityId}/state/history?limit=${HISTORY_LIMIT}&disposition=${encodeURIComponent(filter)}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`
      )
  );

  return (
    <div>
      {loading && (
        <span className="text-muted-foreground mt-1.5 block text-sm">
          Loading…
        </span>
      )}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          {data.items.length === 0 ? (
            <p className="text-muted-foreground mt-3 text-sm">
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
              <Table className="mt-3" aria-label="State history">
                <TableHeader>
                  <TableRow>
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
                        title={
                          entry.value === undefined ||
                          forecastSummary(entityType, entry.value) !== null
                            ? undefined
                            : JSON.stringify(entry.value)
                        }
                      >
                        {entry.disposition === "rejected"
                          ? "—"
                          : formatHistoryValue(
                              entityType,
                              entry.value,
                              support
                            )}
                      </TableCell>
                      <TableCell>
                        <span className="flex flex-wrap items-center gap-1.5">
                          <StatusChip status={entry.disposition} />
                          {entry.rejection_code && (
                            <span className="text-muted-foreground font-mono text-xs">
                              {entry.rejection_code}
                            </span>
                          )}
                        </span>
                      </TableCell>
                      <TableCell className="max-w-[16rem]">
                        <MonoId
                          value={entry.observation_id}
                          className="text-muted-foreground"
                        />
                      </TableCell>
                      <TableCell className="text-muted-foreground font-mono text-xs">
                        {entry.observed_at}
                      </TableCell>
                      <TableCell className="text-muted-foreground font-mono text-xs">
                        {entry.adapter_received_at}
                      </TableCell>
                      <TableCell className="text-muted-foreground font-mono text-xs">
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
              size="default"
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
};

const EntityStateHistory = ({
  entityId,
  entityType,
  support,
}: {
  entityId: string;
  entityType: string | undefined;
  support: Record<string, unknown> | undefined;
}) => {
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
        <SegmentedControl
          label="State history filter"
          value={filter}
          onValueChange={setFilter}
          options={FILTERS}
        />
        <Button
          size="default"
          variant="outline"
          onClick={() => setNonce((current) => current + 1)}
        >
          Latest / Refresh history
        </Button>
      </div>
      <p className="text-muted-foreground mt-1.5 text-xs">
        Newer observations appear after refreshing. Older entries may expire
        while paging.
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
};

export default EntityStateHistory;
