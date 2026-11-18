import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent, ReactNode } from "react";

import {
  DEVICE_FACT_SNAPSHOT_LIMIT,
  DEVICE_FACT_STREAM,
  DEVICE_FACT_SUBJECT_FILTER,
  dedupeDeviceFacts,
  readDeviceFactSnapshot,
  startDeviceFactLive,
} from "../api/device-facts.ts";
import type {
  DeviceFact,
  DeviceFactFamily,
  DeviceFactLive,
  DeviceFactSnapshot,
  EntityEventFactData,
  ObservationFactData,
} from "../api/device-facts.ts";
import { useApi } from "../api/hooks.ts";
import { acquireNatsConnection } from "../api/nats.ts";
import type { NatsConnectionLease } from "../api/nats.ts";
import { fetchAllCollectionPages } from "../api/pagination.ts";
import type { Collection, Device, Entity } from "../api/types.ts";
import { Button } from "../components/button.tsx";
import { EmptyRow } from "../components/empty-row.tsx";
import { ErrorBox } from "../components/error-box.tsx";
import { Facts } from "../components/facts.tsx";
import { Input } from "../components/input.tsx";
import { JsonCode } from "../components/json-code.tsx";
import { Label } from "../components/label.tsx";
import { NativeSelect } from "../components/native-select.tsx";
import { PageHeader } from "../components/page-header.tsx";
import { RawJson } from "../components/raw-json.tsx";
import { Section } from "../components/section.tsx";
import { SegmentedControl } from "../components/segmented-control.tsx";
import { StatusChip } from "../components/status-chip.tsx";
import { Switch } from "../components/switch.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/table.tsx";
import { cn } from "../lib/utils.ts";

/** Newest live deliveries kept in the page buffer; the retained snapshot has its own cap. */
const LIVE_BUFFER_LIMIT = 200;
const ROW_CAP_OPTIONS = [25, 50, 100, 250] as const;
const DEFAULT_ROW_CAP = 50;

type FamilyFilter = "all" | DeviceFactFamily;
type DispositionFilter = "all" | "applied" | "unchanged";
type SnapshotState = "idle" | "reading" | "ready" | "error";
type LiveState = "off" | "connecting" | "live" | "error";
type StatusTone = "default" | "success" | "warning" | "error" | "info";

interface FactRow {
  fact: DeviceFact;
  fromSnapshot: boolean;
  fromLive: boolean;
}

interface EntityEnrichment {
  name: string;
  type: string;
  deviceId: string;
  deviceName: string;
  unit?: string;
}

const FAMILY_OPTIONS: { value: FamilyFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "observation", label: "Observations" },
  { value: "entity-event", label: "Entity events" },
];

const DISPOSITION_OPTIONS: { value: DispositionFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "applied", label: "Applied" },
  { value: "unchanged", label: "Unchanged" },
];

/** Merge snapshot ∪ live, keyed by stable fct_ id, newest stream sequence first.
    Origins are sticky: once a fact arrives on a path it stays marked with that
    path. Across paths the delivery with the highest stream sequence wins, so a
    fact tailed live and then republished past the duplicate window (arriving in
    a refreshed snapshot) keeps the republish's metadata, not the older one. */
const mergeFactRows = (
  snapshotFacts: DeviceFact[],
  liveFacts: DeviceFact[]
): FactRow[] => {
  const byId = new Map<string, FactRow>();
  const merge = (
    fact: DeviceFact,
    fromSnapshot: boolean,
    fromLive: boolean
  ) => {
    const existing = byId.get(fact.id);
    if (!existing) {
      byId.set(fact.id, { fact, fromSnapshot, fromLive });
      return;
    }
    byId.set(fact.id, {
      fact:
        fact.streamSequence > existing.fact.streamSequence
          ? fact
          : existing.fact,
      fromSnapshot: existing.fromSnapshot || fromSnapshot,
      fromLive: existing.fromLive || fromLive,
    });
  };
  for (const fact of snapshotFacts) {
    merge(fact, true, false);
  }
  for (const fact of liveFacts) {
    merge(fact, false, true);
  }
  return [...byId.values()].toSorted(
    (a, b) => b.fact.streamSequence - a.fact.streamSequence
  );
};

/** HH:MM:SS.mmm from an ISO timestamp; leaves anything unparseable alone. */
const clockTime = (iso: string): string =>
  iso.length >= 23 && iso[10] === "T" ? iso.slice(11, 23) : iso;

const formatDuration = (ms: number): string => {
  const abs = Math.abs(ms);
  if (abs < 1000) {
    return `${Math.round(abs)}ms`;
  }
  if (abs < 60_000) {
    return `${(abs / 1000).toFixed(abs < 10_000 ? 1 : 0)}s`;
  }
  return `${(abs / 60_000).toFixed(1)}m`;
};

/** Signed gap between two payload timestamps, or "" when either is unparseable. */
const deltaLabel = (fromIso: string, toIso: string): string => {
  const from = Date.parse(fromIso);
  const to = Date.parse(toIso);
  if (Number.isNaN(from) || Number.isNaN(to)) {
    return "";
  }
  const ms = to - from;
  return `${ms >= 0 ? "+" : "-"}${formatDuration(ms)}`;
};

const isPlainObject = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value);

const compactJson = (value: unknown): string => {
  try {
    const text = JSON.stringify(value);
    return text === undefined ? String(value) : text;
  } catch {
    return String(value);
  }
};

const renderBooleanValue = (
  value: unknown,
  whenTrue: string,
  whenFalse: string
): string => {
  if (typeof value !== "boolean") {
    return compactJson(value);
  }
  return value ? whenTrue : whenFalse;
};

const renderNumericSensor = (
  value: unknown,
  unit: string | undefined
): string => {
  if (typeof value !== "number") {
    return compactJson(value);
  }
  return unit ? `${value} ${unit}` : String(value);
};

const renderTextValue = (value: unknown): string => {
  if (typeof value === "string" || typeof value === "number") {
    return String(value);
  }
  return compactJson(value);
};

const renderColorTemperature = (value: unknown): string => {
  if (!isPlainObject(value)) {
    return compactJson(value);
  }
  const active = value.active === true ? " (active)" : " (inactive)";
  return `${String(value.value)} mireds${active}`;
};

const renderColorXy = (value: unknown): string => {
  if (!isPlainObject(value)) {
    return compactJson(value);
  }
  const { x, y } = value;
  if (typeof x !== "number" || typeof y !== "number") {
    return compactJson(value);
  }
  return `x ${x}, y ${y} (10*-4)`;
};

const renderColorHs = (value: unknown): string => {
  if (!isPlainObject(value)) {
    return compactJson(value);
  }
  const { hue, saturation } = value;
  if (typeof hue !== "number" || typeof saturation !== "number") {
    return compactJson(value);
  }
  return `hue ${hue}°, saturation ${saturation}%`;
};

const renderNumericSetting = (
  value: unknown,
  unit: string | undefined
): string => {
  if (!isPlainObject(value)) {
    return compactJson(value);
  }
  if (value.mode === "choice") {
    return String(value.choice);
  }
  if (value.mode !== "value") {
    return compactJson(value);
  }
  const rendered =
    typeof value.value === "number"
      ? String(value.value)
      : compactJson(value.value);
  return unit ? `${rendered} ${unit}` : rendered;
};

/** A best-effort, single-line rendering of an Observation value, selected by the
    enriched Entity type. Falls back to compact JSON whenever the shape is not
    the one the type implies: the payload is authoritative, this is only a view. */
const OBSERVATION_RENDERERS: Record<
  string,
  (value: unknown, unit: string | undefined) => string
> = {
  "hearth.power/v1": (value) => renderBooleanValue(value, "On", "Off"),
  "hearth.binarysensor/v1": (value) =>
    renderBooleanValue(value, "True", "False"),
  "hearth.temperature/v1": (value) =>
    typeof value === "number" ? `${value} mCel` : compactJson(value),
  "hearth.numericsensor/v1": (value, unit) => renderNumericSensor(value, unit),
  "hearth.brightness/v1": (value) =>
    typeof value === "number" ? `${value}%` : compactJson(value),
  "hearth.colormode/v1": (value) => renderTextValue(value),
  "hearth.enumsetting/v1": (value) => renderTextValue(value),
  "hearth.colortemp/v1": (value) => renderColorTemperature(value),
  "hearth.colorxy/v1": (value) => renderColorXy(value),
  "hearth.colorhs/v1": (value) => renderColorHs(value),
  "hearth.numericsetting/v1": (value, unit) =>
    renderNumericSetting(value, unit),
};

const renderObservationValue = (
  value: unknown,
  entityType: string | undefined,
  unit: string | undefined
): string => {
  const renderer = entityType ? OBSERVATION_RENDERERS[entityType] : undefined;
  if (!renderer) {
    return compactJson(value);
  }
  return renderer(value, unit);
};

/** numericsensor support carries a display unit under support.state.unit. */
const entityUnit = (entity: Entity): string | undefined => {
  const { support } = entity;
  if (!isPlainObject(support)) {
    return undefined;
  }
  const { state } = support;
  if (!isPlainObject(state)) {
    return undefined;
  }
  const { unit } = state;
  return typeof unit === "string" && unit.length > 0 ? unit : undefined;
};

const sourceTimeOf = (fact: DeviceFact): string =>
  fact.family === "observation"
    ? (fact.data as ObservationFactData).adapter_received_at
    : (fact.data as EntityEventFactData).reported_at;

const snapshotChipText = (
  state: SnapshotState,
  meta: DeviceFactSnapshot | null,
  retained: number
): string => {
  if (state === "reading") {
    return "reading…";
  }
  if (state === "error") {
    return "error";
  }
  if (!meta) {
    return "not read";
  }
  return `${clockTime(meta.readAt)} · ${retained} retained`;
};

const snapshotTone = (state: SnapshotState): StatusTone => {
  if (state === "error") {
    return "error";
  }
  if (state === "reading") {
    return "warning";
  }
  if (state === "ready") {
    return "success";
  }
  return "default";
};

const liveChipText = (state: LiveState): string => {
  if (state === "live") {
    return "LIVE";
  }
  if (state === "connecting") {
    return "CONNECTING";
  }
  if (state === "error") {
    return "ERROR";
  }
  return "OFF";
};

const liveTone = (state: LiveState): StatusTone => {
  if (state === "live") {
    return "success";
  }
  if (state === "connecting") {
    return "warning";
  }
  if (state === "error") {
    return "error";
  }
  return "default";
};

const liveStateLine = (
  state: LiveState,
  consumer: string | null,
  frozenAt: string | null
): string => {
  if (state === "connecting") {
    return `Connecting — creating an ephemeral consumer on ${DEVICE_FACT_STREAM}…`;
  }
  if (state === "live") {
    return `Live — ephemeral DeliverNew tail (${consumer ?? "consumer"}), non-durable.`;
  }
  if (state === "error") {
    return "Live stopped on an error; re-enable to start a new ephemeral tail at the current stream end.";
  }
  if (frozenAt) {
    return `Off — view frozen at ${clockTime(frozenAt)}; retained snapshot only.`;
  }
  return "Off — retained snapshot only.";
};

const liveExplanation = (state: LiveState): string => {
  if (state === "connecting") {
    return "A DeliverNew consumer receives only facts published after it is created. Retained history is not replayed to it, so facts published during this moment are not delivered.";
  }
  if (state === "live") {
    return "New facts arrive as the relay publishes them. This consumer keeps no position and is deleted when live is switched off: it offers no recovery, and facts published while live was off are not replayed. A retry stored again past the stream's duplicate window is collapsed here on its stable fct_ id.";
  }
  if (state === "error") {
    return "The ephemeral consumer was deleted when the tail failed. Re-enabling creates a new DeliverNew consumer at the then-current stream end, so nothing published while it was down is replayed to it.";
  }
  return "The table is a bounded read of facts already retained in the stream, newest first. Enabling live adds a separate ephemeral DeliverNew consumer; facts published while live is off appear only after a snapshot refresh.";
};

const originLabel = (row: FactRow): string => {
  if (row.fromSnapshot && row.fromLive) {
    return "snapshot + live";
  }
  return row.fromLive ? "live" : "snapshot";
};

const originTone = (row: FactRow): StatusTone =>
  row.fromLive ? "success" : "default";

const familyTone = (family: DeviceFactFamily): StatusTone =>
  family === "entity-event" ? "info" : "default";

const dispositionTone = (data: ObservationFactData): StatusTone =>
  data.disposition === "applied" ? "success" : "default";

const emptyFactsMessage = (
  rowsLength: number,
  snapshotState: SnapshotState
): string => {
  if (rowsLength > 0) {
    return "No facts match the current filters.";
  }
  if (snapshotState === "reading") {
    return "Reading the retained snapshot…";
  }
  return "No facts in view. Refresh the snapshot or enable live streaming.";
};

const deliveredByLabel = (row: FactRow): string => {
  if (row.fromSnapshot && row.fromLive) {
    return "snapshot + live (same fct_ id, collapsed)";
  }
  if (row.fromLive) {
    return "live (ephemeral DeliverNew tail)";
  }
  return "snapshot (bounded retained read)";
};

const dispositionLabel = (data: ObservationFactData): string => {
  const suffix =
    data.disposition === "applied"
      ? " (State moved)"
      : " (same value as the previous accepted Observation)";
  return `${data.disposition}${suffix}`;
};

type SnapshotReadOutcome =
  | { lease: NatsConnectionLease | null; result: DeviceFactSnapshot }
  | { lease: NatsConnectionLease | null; error: unknown };

/** One snapshot attempt: acquire the ephemeral consumer, read, and hand back the
    lease so the caller owns releasing it after publishing (or discarding) the
    result. The lease is returned even on failure so nothing is left behind. */
const performSnapshotRead = async (): Promise<SnapshotReadOutcome> => {
  let lease: NatsConnectionLease | null = null;
  try {
    lease = await acquireNatsConnection();
    const result = await readDeviceFactSnapshot(lease.connection);
    return { lease, result };
  } catch (error) {
    return { lease, error };
  }
};

interface LiveSectionProps {
  enabled: boolean;
  chipText: string;
  tone: StatusTone;
  stateLine: string;
  explanation: string;
  liveCount: number;
  delivered: number;
  malformed: number;
  error: Error | null;
  onEnabledChange: (next: boolean) => void;
  onClear: () => void;
}

const LiveSection = ({
  enabled,
  chipText,
  tone,
  stateLine,
  explanation,
  liveCount,
  delivered,
  malformed,
  error,
  onEnabledChange,
  onClear,
}: LiveSectionProps) => (
  <Section title="Live stream">
    <div className="flex flex-wrap items-center gap-3">
      <Switch
        id="device-facts-live"
        checked={enabled}
        onCheckedChange={onEnabledChange}
      />
      <Label htmlFor="device-facts-live">Stream live facts</Label>
      <StatusChip status={chipText} tone={tone} />
      <output className="text-muted-foreground text-sm">{stateLine}</output>
    </div>
    <p className="text-muted-foreground mt-1.5 text-xs">{explanation}</p>
    <div className="mt-2 flex flex-wrap items-center gap-2">
      <Button
        size="default"
        variant="outline"
        disabled={liveCount === 0}
        onClick={onClear}
      >
        Clear live arrivals
      </Button>
      <span className="text-muted-foreground text-xs">
        live arrivals delivered this session: {delivered}
        {malformed > 0 ? ` · ${malformed} malformed skipped` : ""}
      </span>
    </div>
    {error && <ErrorBox error={error} />}
  </Section>
);

interface FactFiltersProps {
  familyFilter: FamilyFilter;
  dispositionFilter: DispositionFilter;
  textFilter: string;
  rowCap: number;
  filtersActive: boolean;
  dispositionDisabled: boolean;
  onFamilyChange: (value: FamilyFilter) => void;
  onDispositionChange: (value: DispositionFilter) => void;
  onTextChange: (value: string) => void;
  onRowCapChange: (value: number) => void;
  onClear: () => void;
}

const FactFilters = ({
  familyFilter,
  dispositionFilter,
  textFilter,
  rowCap,
  filtersActive,
  dispositionDisabled,
  onFamilyChange,
  onDispositionChange,
  onTextChange,
  onRowCapChange,
  onClear,
}: FactFiltersProps) => (
  <div className="mt-4 flex flex-wrap items-end gap-x-5 gap-y-3">
    <div>
      <span
        id="device-facts-family-label"
        className="text-muted-foreground block text-xs"
      >
        Family
      </span>
      <div className="mt-0.5">
        <SegmentedControl
          label="Family"
          value={familyFilter}
          onValueChange={onFamilyChange}
          options={FAMILY_OPTIONS}
        />
      </div>
    </div>
    <div>
      <span
        id="device-facts-disposition-label"
        className="text-muted-foreground block text-xs"
      >
        Disposition (observations only)
      </span>
      <div className="mt-0.5">
        <SegmentedControl
          label="Disposition (observations only)"
          value={dispositionFilter}
          onValueChange={onDispositionChange}
          options={DISPOSITION_OPTIONS}
          disabled={dispositionDisabled}
        />
      </div>
    </div>
    <div className="min-w-[16rem] flex-1">
      <Label
        htmlFor="device-facts-text"
        className="text-muted-foreground text-xs"
      >
        Filter
      </Label>
      <Input
        id="device-facts-text"
        type="search"
        className="mt-0.5"
        placeholder="entity, device, fact/source id, event name…"
        value={textFilter}
        onChange={(event) => onTextChange(event.target.value)}
      />
    </div>
    <div>
      <Label
        htmlFor="device-facts-cap"
        className="text-muted-foreground text-xs"
      >
        Row cap
      </Label>
      <NativeSelect
        id="device-facts-cap"
        className="mt-0.5"
        value={rowCap}
        onChange={(event) => onRowCapChange(Number(event.target.value))}
      >
        {ROW_CAP_OPTIONS.map((cap) => (
          <option key={cap} value={cap}>
            {cap}
          </option>
        ))}
      </NativeSelect>
    </div>
    {filtersActive && (
      <Button size="default" variant="outline" onClick={onClear}>
        Clear filters
      </Button>
    )}
  </div>
);

const FactSummary = ({
  displayCount,
  filteredCount,
  rowCount,
  duplicateCount,
  malformedCount,
}: {
  displayCount: number;
  filteredCount: number;
  rowCount: number;
  duplicateCount: number;
  malformedCount: number;
}) => (
  <div className="text-muted-foreground mt-2 flex flex-wrap items-baseline gap-x-3 text-sm">
    <span id="device-facts-summary">
      Newest first · showing {displayCount} of {filteredCount} matching ·{" "}
      {rowCount} in view (snapshot ∪ live)
    </span>
    {duplicateCount > 0 && (
      <span
        className="text-warning"
        title="The same fct_ id arrived from both consumers; rows are keyed by fact id."
      >
        {duplicateCount} duplicate fct_ id collapsed
      </span>
    )}
    {malformedCount > 0 && (
      <span className="text-warning">
        {malformedCount} malformed message(s) skipped in the last snapshot
      </span>
    )}
  </div>
);

interface FactRowViewProps {
  row: FactRow;
  index: number;
  selected: boolean;
  selectedIndex: number;
  enrichmentFor: (entityId: string) => EntityEnrichment | null;
  onSelect: (factId: string) => void;
  onKeyDown: (
    event: KeyboardEvent<HTMLTableRowElement>,
    factId: string,
    index: number
  ) => void;
}

const FactRowView = ({
  row,
  index,
  selected,
  selectedIndex,
  enrichmentFor,
  onSelect,
  onKeyDown,
}: FactRowViewProps) => {
  const { fact } = row;
  const enrichment = enrichmentFor(fact.entityId);
  const isGridEntry =
    selectedIndex === -1 ? index === 0 : index === selectedIndex;
  const value =
    fact.family === "observation"
      ? renderObservationValue(
          (fact.data as ObservationFactData).value,
          enrichment?.type,
          enrichment?.unit
        )
      : "—";
  const source = sourceTimeOf(fact);
  const valueTitle =
    fact.family === "observation"
      ? compactJson((fact.data as ObservationFactData).value)
      : "Entity Events carry no value";
  return (
    <TableRow
      data-fact-id={fact.id}
      tabIndex={isGridEntry ? 0 : -1}
      aria-selected={selected}
      data-state={selected ? "selected" : undefined}
      className={cn(
        "focus-visible:outline-ring cursor-pointer align-top focus-visible:outline-2",
        row.fromLive && "border-s-success border-s-2"
      )}
      onClick={() => onSelect(fact.id)}
      onKeyDown={(event) => onKeyDown(event, fact.id, index)}
    >
      <TableCell>
        <StatusChip status={fact.family} tone={familyTone(fact.family)} />
      </TableCell>
      <TableCell>
        {fact.family === "observation" ? (
          <StatusChip
            status={(fact.data as ObservationFactData).disposition}
            tone={dispositionTone(fact.data as ObservationFactData)}
          />
        ) : (
          <span className="font-medium">
            {(fact.data as EntityEventFactData).name}
          </span>
        )}
      </TableCell>
      <TableCell className="whitespace-normal">
        <span className="block truncate" title={fact.entityId}>
          {enrichment?.name ?? "(unresolved entity)"}
        </span>
        <span className="text-muted-foreground block truncate text-xs">
          {enrichment?.deviceName || fact.entityId}
        </span>
      </TableCell>
      <TableCell className="whitespace-normal">
        <span className="block max-w-[12rem] truncate" title={valueTitle}>
          {value}
        </span>
      </TableCell>
      <TableCell>
        <div className="flex flex-wrap items-center gap-1">
          <StatusChip status={originLabel(row)} tone={originTone(row)} />
          {fact.warnings.length > 0 && (
            <span title={fact.warnings.join("\n")}>
              <StatusChip
                status={`${fact.warnings.length} mismatch`}
                tone="warning"
              />
            </span>
          )}
        </div>
      </TableCell>
      <TableCell className="max-w-[13rem]">
        <span
          className="text-muted-foreground block truncate font-mono text-xs"
          title={fact.id}
        >
          {fact.id}
        </span>
      </TableCell>
      <TableCell className="text-muted-foreground font-mono text-xs">
        <span title={fact.envelope.emitted_at}>
          {clockTime(fact.envelope.emitted_at)}
        </span>
      </TableCell>
      <TableCell className="text-muted-foreground font-mono text-xs">
        <span title={source}>{clockTime(source)}</span>
        <span className="ms-1 text-[0.65rem]">
          {deltaLabel(source, fact.envelope.emitted_at)}
        </span>
      </TableCell>
    </TableRow>
  );
};

interface FactsTableProps {
  display: FactRow[];
  rowsLength: number;
  selectedId: string | null;
  selectedIndex: number;
  snapshotState: SnapshotState;
  enrichmentFor: (entityId: string) => EntityEnrichment | null;
  onSelect: (factId: string) => void;
}

const FactsTable = ({
  display,
  rowsLength,
  selectedId,
  selectedIndex,
  snapshotState,
  enrichmentFor,
  onSelect,
}: FactsTableProps) => {
  const tableViewportRef = useRef<HTMLDivElement | null>(null);

  const onRowKeyDown = (
    event: KeyboardEvent<HTMLTableRowElement>,
    factId: string,
    index: number
  ) => {
    const domRows =
      tableViewportRef.current?.querySelectorAll<HTMLTableRowElement>(
        "tr[data-fact-id]"
      );
    const count = domRows?.length ?? 0;
    if (count === 0) {
      return;
    }
    let next = -1;
    switch (event.key) {
      case "ArrowDown": {
        next = Math.min(count - 1, index + 1);
        break;
      }
      case "ArrowUp": {
        next = Math.max(0, index - 1);
        break;
      }
      case "Home": {
        next = 0;
        break;
      }
      case "End": {
        next = count - 1;
        break;
      }
      case "PageDown": {
        next = Math.min(count - 1, index + 10);
        break;
      }
      case "PageUp": {
        next = Math.max(0, index - 10);
        break;
      }
      case "Enter":
      case " ": {
        event.preventDefault();
        onSelect(factId);
        return;
      }
      default: {
        return;
      }
    }
    event.preventDefault();
    const target = domRows?.[next];
    const targetId = target?.dataset.factId ?? null;
    if (targetId) {
      onSelect(targetId);
    }
    target?.focus();
    target?.scrollIntoView({ block: "nearest" });
  };

  return (
    <div className="mt-2" ref={tableViewportRef}>
      <Table
        role="grid"
        aria-label="Device facts, newest first"
        aria-describedby="device-facts-summary"
      >
        <TableHeader>
          <TableRow>
            <TableHead className="w-[6.5rem]">Family</TableHead>
            <TableHead className="w-[7.5rem]">Outcome / event</TableHead>
            <TableHead>Entity / device</TableHead>
            <TableHead className="w-[12rem]">Value</TableHead>
            <TableHead className="w-[7.5rem]">Origin</TableHead>
            <TableHead className="w-[13rem]">Fact id</TableHead>
            <TableHead className="w-[7.5rem]">Emitted (UTC)</TableHead>
            <TableHead className="w-[7.5rem]">Source (UTC)</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {display.length === 0 && (
            <EmptyRow
              colSpan={8}
              message={emptyFactsMessage(rowsLength, snapshotState)}
            />
          )}
          {display.map((row, index) => (
            <FactRowView
              key={row.fact.id}
              row={row}
              index={index}
              selected={row.fact.id === selectedId}
              selectedIndex={selectedIndex}
              enrichmentFor={enrichmentFor}
              onSelect={onSelect}
              onKeyDown={onRowKeyDown}
            />
          ))}
        </TableBody>
      </Table>
    </div>
  );
};

const FactWarnings = ({ warnings }: { warnings: string[] }) => {
  if (warnings.length === 0) {
    return null;
  }
  return (
    <div
      role="alert"
      className="border-warning/35 bg-warning/10 text-warning mt-2 rounded-md border px-3 py-2 text-sm"
    >
      <span className="font-medium">
        Contract mismatch{warnings.length > 1 ? "es" : ""}:
      </span>
      <ul className="mt-1 list-disc ps-5">
        {warnings.map((warning) => (
          <li key={warning}>{warning}</li>
        ))}
      </ul>
    </div>
  );
};

const ObservationTiming = ({
  data,
  emittedAt,
}: {
  data: ObservationFactData;
  emittedAt: string;
}) => (
  <Section title="Source timing">
    <Facts
      rows={[
        ["source_updated_at", data.source_updated_at ?? "absent on this fact"],
        ["adapter_received_at", data.adapter_received_at],
        ["observed_at", data.observed_at],
        [
          "adapter_received → emitted",
          deltaLabel(data.adapter_received_at, emittedAt),
        ],
        ["observed → emitted", deltaLabel(data.observed_at, emittedAt)],
      ]}
    />
  </Section>
);

const EntityEventTiming = ({
  data,
  emittedAt,
}: {
  data: EntityEventFactData;
  emittedAt: string;
}) => (
  <Section title="Source timing">
    <Facts
      rows={[
        ["reported_at", `${data.reported_at} (SDK reported time)`],
        ["received_at", `${data.received_at} (Core receive time)`],
        [
          "recorded_at",
          `${data.recorded_at} (Core first-record time; reused as emitted_at)`,
        ],
        ["reported → received", deltaLabel(data.reported_at, data.received_at)],
        ["received → emitted", deltaLabel(data.received_at, emittedAt)],
      ]}
    />
  </Section>
);

const ObservationValueSection = ({
  data,
  enrichment,
}: {
  data: ObservationFactData;
  enrichment: EntityEnrichment | null;
}) => (
  <Section title="Normalized value">
    <Facts
      rows={[
        ["Disposition", dispositionLabel(data)],
        [
          "Rendered",
          renderObservationValue(
            data.value,
            enrichment?.type,
            enrichment?.unit
          ),
        ],
        ["Entity type", enrichment?.type ?? "unknown (no enrichment)"],
        ["Entity id", data.entity_id],
      ]}
    />
    <JsonCode code={JSON.stringify(data.value ?? null, null, 2)} />
  </Section>
);

const EntityEventSection = ({
  data,
  enrichment,
}: {
  data: EntityEventFactData;
  enrichment: EntityEnrichment | null;
}) => (
  <Section title="Entity event">
    <Facts
      rows={[
        [
          "Name",
          `${data.name} (supported name on an event-source Entity; no State, no Command link)`,
        ],
        ["Entity type", enrichment?.type ?? "unknown (no enrichment)"],
        ["Event id", data.event_id],
        ["Entity id", data.entity_id],
      ]}
    />
  </Section>
);

const EnvelopeSection = ({ fact }: { fact: DeviceFact }) => {
  const causationTarget =
    fact.family === "observation" ? "Observation" : "Entity Event";
  return (
    <Section title="Envelope">
      <Facts
        rows={[
          [
            "Fact id",
            `${fact.envelope.id} (stable identity, published as Nats-Msg-Id)`,
          ],
          ["Schema", fact.envelope.schema],
          ["Emitted (Core commit)", fact.envelope.emitted_at],
          ["Correlation id", fact.envelope.correlation_id],
          [
            "Causation id",
            `${fact.envelope.causation_id} (durable ${causationTarget})`,
          ],
          ["Nats-Msg-Id header", fact.msgIdHeader ?? "absent"],
        ]}
      />
    </Section>
  );
};

const MetadataSection = ({ rows }: { rows: [string, ReactNode][] }) => (
  <Section title="Subject and broker metadata">
    <Facts rows={rows} />
  </Section>
);

const RawFactSection = ({ row }: { row: FactRow }) => (
  <Section title="Raw fact JSON">
    <p className="text-muted-foreground text-xs">
      The published payload as decoded from the wire: every field the publisher
      sent, including any the typed envelope above does not model,
      pretty-printed for display only. Stream sequence, consumer and delivery
      time above are broker- and page-side metadata, not payload fields.
    </p>
    <JsonCode code={JSON.stringify(row.fact.rawPayload, null, 2)} />
    <RawJson value={row} title="Raw delivery record JSON" />
  </Section>
);

const FactDetail = ({
  row,
  enrichment,
  snapshotReadAt,
}: {
  row: FactRow;
  enrichment: EntityEnrichment | null;
  snapshotReadAt: string | null;
}) => {
  const { fact } = row;
  const headers = Object.keys(fact.headers);
  const metadataRows: [string, ReactNode][] = [
    ["Subject", fact.subject],
    ["Stream", DEVICE_FACT_STREAM],
    ["Stream sequence", String(fact.streamSequence)],
    ["Broker stored", fact.brokerStoredAt || "unknown"],
    ["Delivered by", deliveredByLabel(row)],
    ["Consumer", `${fact.consumer} (ephemeral)`],
    ["Delivery count", String(fact.deliveryCount)],
    ["Delivered at", `${fact.deliveredAt} (this page's receive time)`],
  ];
  if (row.fromSnapshot && snapshotReadAt) {
    metadataRows.push(["Snapshot read", snapshotReadAt]);
  }
  metadataRows.push([
    "Published headers",
    headers.length > 0 ? (
      <span key="published-headers" className="whitespace-pre-wrap">
        {headers
          .map((key) => `${key}: ${fact.headers[key].join(", ")}`)
          .join("\n")}
      </span>
    ) : (
      "none"
    ),
  ]);

  return (
    <div>
      <div className="flex flex-wrap items-baseline gap-2">
        <StatusChip
          status={`${fact.family}.${fact.variant}`}
          tone={familyTone(fact.family)}
        />
        {enrichment && (
          <span className="text-sm">
            {enrichment.deviceName ? `${enrichment.deviceName} · ` : ""}
            {enrichment.name}
          </span>
        )}
        <span className="text-muted-foreground font-mono text-xs">
          {fact.id}
        </span>
      </div>

      <FactWarnings warnings={fact.warnings} />

      <EnvelopeSection fact={fact} />

      {fact.family === "observation" ? (
        <ObservationTiming
          data={fact.data as ObservationFactData}
          emittedAt={fact.envelope.emitted_at}
        />
      ) : (
        <EntityEventTiming
          data={fact.data as EntityEventFactData}
          emittedAt={fact.envelope.emitted_at}
        />
      )}

      {fact.family === "observation" ? (
        <ObservationValueSection
          data={fact.data as ObservationFactData}
          enrichment={enrichment}
        />
      ) : (
        <EntityEventSection
          data={fact.data as EntityEventFactData}
          enrichment={enrichment}
        />
      )}

      <MetadataSection rows={metadataRows} />
      <RawFactSection row={row} />
    </div>
  );
};

export default function DeviceFactsPage() {
  const [snapshotFacts, setSnapshotFacts] = useState<DeviceFact[]>([]);
  const [snapshotState, setSnapshotState] = useState<SnapshotState>("reading");
  const [snapshotMeta, setSnapshotMeta] = useState<DeviceFactSnapshot | null>(
    null
  );
  const [snapshotError, setSnapshotError] = useState<Error | null>(null);

  const [liveFacts, setLiveFacts] = useState<DeviceFact[]>([]);
  const [liveState, setLiveState] = useState<LiveState>("off");
  const [liveError, setLiveError] = useState<Error | null>(null);
  const [liveConsumer, setLiveConsumer] = useState<string | null>(null);
  const [liveDelivered, setLiveDelivered] = useState(0);
  const [liveMalformed, setLiveMalformed] = useState(0);
  const [frozenAt, setFrozenAt] = useState<string | null>(null);

  const [familyFilter, setFamilyFilter] = useState<FamilyFilter>("all");
  const [dispositionFilter, setDispositionFilter] =
    useState<DispositionFilter>("all");
  const [textFilter, setTextFilter] = useState("");
  const [rowCap, setRowCap] = useState<number>(DEFAULT_ROW_CAP);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [snapshotRequest, setSnapshotRequest] = useState(1);

  const mountedRef = useRef(true);
  // Generation guards: a superseded snapshot read or live attempt must neither
  // publish state nor leave a consumer behind.
  const snapshotAttemptRef = useRef<{ request: number } | null>(null);
  const liveGenerationRef = useRef(0);
  const liveRef = useRef<{
    live: DeviceFactLive;
    lease: NatsConnectionLease;
  } | null>(null);

  // Enrichment is dashboard-only and may fail: the fact payload carries the
  // canonical entity id and nothing else, so unresolved rows stay truthful.
  // Both reads must be complete, not one bounded page: an entity or device that
  // landed on a later page would otherwise render as "(unresolved entity)" and
  // lose its name. fetchAllCollectionPages follows next_cursor to the end and
  // fails loudly rather than returning a partial map.
  const entities = useApi<Collection<Entity>>("device-facts-entities", () =>
    fetchAllCollectionPages<Entity>("/v1/entities")
  );
  const devices = useApi<Collection<Device>>("device-facts-devices", () =>
    fetchAllCollectionPages<Device>("/v1/devices")
  );
  const entityById = useMemo(
    () =>
      new Map(
        (entities.data?.items ?? []).map((entity) => [entity.id, entity])
      ),
    [entities.data]
  );
  const deviceById = useMemo(
    () =>
      new Map((devices.data?.items ?? []).map((device) => [device.id, device])),
    [devices.data]
  );

  const enrichmentFor = useCallback(
    (entityId: string): EntityEnrichment | null => {
      const entity = entityById.get(entityId);
      if (!entity) {
        return null;
      }
      const device = deviceById.get(entity.device_id);
      return {
        name: entity.name,
        type: entity.type,
        deviceId: entity.device_id,
        deviceName: device?.name ?? "",
        unit: entityUnit(entity),
      };
    },
    [entityById, deviceById]
  );

  const handleRefresh = () => {
    // Invalidate any in-flight read before starting the replacement attempt.
    snapshotAttemptRef.current = null;
    setSnapshotState("reading");
    setSnapshotError(null);
    setSnapshotRequest((request) => request + 1);
    void entities.refresh();
    void devices.refresh();
  };

  const enableLive = async () => {
    liveGenerationRef.current += 1;
    const generation = liveGenerationRef.current;
    setLiveState("connecting");
    setLiveError(null);
    setLiveDelivered(0);
    setLiveMalformed(0);
    let lease: NatsConnectionLease | null = null;
    try {
      lease = await acquireNatsConnection();
      if (generation !== liveGenerationRef.current || !mountedRef.current) {
        await lease.release();
        return;
      }
      const live = await startDeviceFactLive(lease.connection, {
        onFact: (fact) => {
          if (generation !== liveGenerationRef.current) {
            return;
          }
          setLiveFacts((prev) =>
            dedupeDeviceFacts([fact, ...prev]).slice(0, LIVE_BUFFER_LIMIT)
          );
          setLiveDelivered((count) => count + 1);
        },
        onMalformed: () => {
          if (generation !== liveGenerationRef.current) {
            return;
          }
          setLiveMalformed((count) => count + 1);
        },
        onError: (err) => {
          if (generation !== liveGenerationRef.current) {
            return;
          }
          // The transport already deleted its consumer; drop the lease too.
          setLiveError(err);
          setLiveState("error");
          liveRef.current = null;
          void lease?.release();
        },
      });
      if (generation !== liveGenerationRef.current || !mountedRef.current) {
        await live.close();
        await lease.release();
        return;
      }
      liveRef.current = { live, lease };
      setLiveConsumer(live.consumer);
      setLiveState("live");
    } catch (error) {
      await lease?.release();
      if (generation !== liveGenerationRef.current || !mountedRef.current) {
        return;
      }
      setLiveError(error instanceof Error ? error : new Error(String(error)));
      setLiveState("error");
    }
  };

  const disableLive = async () => {
    liveGenerationRef.current += 1;
    setLiveState("off");
    setLiveError(null);
    setFrozenAt(new Date().toISOString());
    const { current } = liveRef;
    liveRef.current = null;
    if (current) {
      await current.live.close();
      await current.lease.release();
    }
  };

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      // Invalidate the live attempt, then delete the live consumer and drop its
      // lease. Snapshot reads release their own lease when the attempt settles,
      // so a superseded read cannot leak one either.
      liveGenerationRef.current += 1;
      const { current } = liveRef;
      liveRef.current = null;
      if (current) {
        void current.live.close().then(() => current.lease.release());
      }
    };
  }, []);

  useEffect(() => {
    // Every effect setup owns a distinct attempt, including StrictMode replay.
    const attempt = { request: snapshotRequest };
    snapshotAttemptRef.current = attempt;
    void (async () => {
      const outcome = await performSnapshotRead();
      if (attempt !== snapshotAttemptRef.current || !mountedRef.current) {
        await outcome.lease?.release();
        return;
      }
      if ("error" in outcome) {
        setSnapshotError(
          outcome.error instanceof Error
            ? outcome.error
            : new Error(String(outcome.error))
        );
        setSnapshotState("error");
      } else {
        setSnapshotFacts(outcome.result.facts);
        setSnapshotMeta(outcome.result);
        setSnapshotState("ready");
      }
      await outcome.lease?.release();
    })();
    return () => {
      snapshotAttemptRef.current = null;
    };
  }, [snapshotRequest]);

  const rows = useMemo(
    () => mergeFactRows(snapshotFacts, liveFacts),
    [snapshotFacts, liveFacts]
  );

  const filtered = useMemo(() => {
    const needle = textFilter.trim().toLowerCase();
    return rows.filter((row) => {
      const { fact } = row;
      if (familyFilter === "observation" && fact.family !== "observation") {
        return false;
      }
      if (familyFilter === "entity-event" && fact.family !== "entity-event") {
        return false;
      }
      if (dispositionFilter !== "all") {
        if (fact.family !== "observation") {
          return false;
        }
        if (
          (fact.data as ObservationFactData).disposition !== dispositionFilter
        ) {
          return false;
        }
      }
      if (!needle) {
        return true;
      }
      const enrichment = enrichmentFor(fact.entityId);
      const haystack = [
        enrichment?.name ?? "",
        enrichment?.deviceName ?? "",
        fact.entityId,
        fact.id,
        fact.envelope.causation_id,
        fact.family === "entity-event"
          ? (fact.data as EntityEventFactData).event_id
          : "",
        fact.family === "entity-event"
          ? (fact.data as EntityEventFactData).name
          : "",
        fact.subject,
      ]
        .join(" ")
        .toLowerCase();
      return haystack.includes(needle);
    });
  }, [rows, familyFilter, dispositionFilter, textFilter, enrichmentFor]);

  const display = filtered.slice(0, rowCap);
  const selectedIndex = display.findIndex((row) => row.fact.id === selectedId);
  const selectedRow = selectedId
    ? (rows.find((row) => row.fact.id === selectedId) ?? null)
    : null;
  const duplicateCount = rows.filter(
    (row) => row.fromSnapshot && row.fromLive
  ).length;
  const filtersActive =
    familyFilter !== "all" ||
    dispositionFilter !== "all" ||
    textFilter.trim() !== "";
  const dispositionDisabled = familyFilter === "entity-event";
  const liveEnabled = liveState === "connecting" || liveState === "live";

  const clearFilters = () => {
    setFamilyFilter("all");
    setDispositionFilter("all");
    setTextFilter("");
  };

  const selectFamily = (value: FamilyFilter) => {
    setFamilyFilter(value);
    // Entity Events carry no disposition; keep the control honest.
    if (value === "entity-event") {
      setDispositionFilter("all");
    }
  };

  return (
    <div>
      <PageHeader
        title="Device facts"
        description={
          <>
            Facts published by Core into{" "}
            <code className="font-mono">{DEVICE_FACT_STREAM}</code> (subject
            filter{" "}
            <code className="font-mono">{DEVICE_FACT_SUBJECT_FILTER}</code>),
            newest first. The snapshot is a bounded retained read of at most{" "}
            {DEVICE_FACT_SNAPSHOT_LIMIT} facts and live is off by default.
          </>
        }
      >
        <Button
          size="default"
          variant="outline"
          disabled={snapshotState === "reading"}
          onClick={handleRefresh}
        >
          Refresh snapshot
        </Button>
        <StatusChip
          label="snapshot"
          status={snapshotChipText(
            snapshotState,
            snapshotMeta,
            snapshotFacts.length
          )}
          tone={snapshotTone(snapshotState)}
        />
      </PageHeader>
      {snapshotError && <ErrorBox error={snapshotError} />}

      <LiveSection
        enabled={liveEnabled}
        chipText={liveChipText(liveState)}
        tone={liveTone(liveState)}
        stateLine={liveStateLine(liveState, liveConsumer, frozenAt)}
        explanation={liveExplanation(liveState)}
        liveCount={liveFacts.length}
        delivered={liveDelivered}
        malformed={liveMalformed}
        error={liveError}
        onEnabledChange={(next) => {
          if (next) {
            void enableLive();
          } else {
            void disableLive();
          }
        }}
        onClear={() => {
          setLiveFacts([]);
        }}
      />

      <FactFilters
        familyFilter={familyFilter}
        dispositionFilter={dispositionFilter}
        textFilter={textFilter}
        rowCap={rowCap}
        filtersActive={filtersActive}
        dispositionDisabled={dispositionDisabled}
        onFamilyChange={selectFamily}
        onDispositionChange={setDispositionFilter}
        onTextChange={setTextFilter}
        onRowCapChange={setRowCap}
        onClear={clearFilters}
      />

      <p className="text-muted-foreground mt-2 text-xs">
        Entity, device and type names are dashboard enrichment from the devices
        read API; the fact payload and subject carry only the canonical entity
        id.
        {(entities.error || devices.error) &&
          " Enrichment is unavailable right now, so rows show raw ids."}
      </p>

      <FactSummary
        displayCount={display.length}
        filteredCount={filtered.length}
        rowCount={rows.length}
        duplicateCount={duplicateCount}
        malformedCount={snapshotMeta?.malformed ?? 0}
      />

      <FactsTable
        display={display}
        rowsLength={rows.length}
        selectedId={selectedId}
        selectedIndex={selectedIndex}
        snapshotState={snapshotState}
        enrichmentFor={enrichmentFor}
        onSelect={setSelectedId}
      />

      <Section title="Fact detail">
        {selectedRow ? (
          <FactDetail
            row={selectedRow}
            enrichment={enrichmentFor(selectedRow.fact.entityId)}
            snapshotReadAt={snapshotMeta?.readAt ?? null}
          />
        ) : (
          <p className="text-muted-foreground text-sm">
            {selectedId
              ? "The selected fact is no longer in the buffer: it was trimmed by the row cap, or it was a live arrival that was cleared. Select another row."
              : "Select a fact row to inspect its envelope, source timing, value, subject, broker metadata and raw JSON."}
          </p>
        )}
      </Section>
    </div>
  );
}
