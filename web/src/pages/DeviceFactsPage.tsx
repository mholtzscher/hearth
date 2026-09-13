import { cn } from "cn";
import { useEffect, useMemo, useRef, useState } from "react";
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
import { apiFetch } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Collection, Device, Entity } from "../api/types.ts";
import { acquireNatsConnection } from "../api/nats.ts";
import type { NatsConnectionLease } from "../api/nats.ts";
import { ErrorBox, Facts, JsonCode, RawJson, Section, StatusChip } from "../components/common.tsx";
import { readMeasurementReading } from "../lib/measurement.ts";
import { Button } from "../components/ui/button.tsx";
import { Input } from "../components/ui/input.tsx";
import { Label } from "../components/ui/label.tsx";
import { Switch } from "../components/ui/switch.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table.tsx";

/** Newest live deliveries kept in the page buffer; the retained snapshot has its own cap. */
const LIVE_BUFFER_LIMIT = 200;
const ROW_CAP_OPTIONS = [25, 50, 100, 250] as const;
const DEFAULT_ROW_CAP = 50;

type FamilyFilter = "all" | DeviceFactFamily;
type DispositionFilter = "all" | "applied" | "unchanged";
type SnapshotState = "idle" | "reading" | "ready" | "error";
type LiveState = "off" | "connecting" | "live" | "error";

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
  support: Record<string, unknown>;
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
function mergeFactRows(snapshotFacts: DeviceFact[], liveFacts: DeviceFact[]): FactRow[] {
  const byId = new Map<string, FactRow>();
  function merge(fact: DeviceFact, fromSnapshot: boolean, fromLive: boolean) {
    const existing = byId.get(fact.id);
    if (!existing) {
      byId.set(fact.id, { fact, fromSnapshot, fromLive });
      return;
    }
    byId.set(fact.id, {
      fact:
        fact.streamSequence > existing.fact.streamSequence ? fact : existing.fact,
      fromSnapshot: existing.fromSnapshot || fromSnapshot,
      fromLive: existing.fromLive || fromLive,
    });
  }
  for (const fact of snapshotFacts) merge(fact, true, false);
  for (const fact of liveFacts) merge(fact, false, true);
  return [...byId.values()].sort((a, b) => b.fact.streamSequence - a.fact.streamSequence);
}

/** HH:MM:SS.mmm from an ISO timestamp; leaves anything unparseable alone. */
function clockTime(iso: string): string {
  return iso.length >= 23 && iso[10] === "T" ? iso.slice(11, 23) : iso;
}

function formatDuration(ms: number): string {
  const abs = Math.abs(ms);
  if (abs < 1000) return `${Math.round(abs)}ms`;
  if (abs < 60_000) return `${(abs / 1000).toFixed(abs < 10_000 ? 1 : 0)}s`;
  return `${(abs / 60_000).toFixed(1)}m`;
}

/** Signed gap between two payload timestamps, or "" when either is unparseable. */
function deltaLabel(fromIso: string, toIso: string): string {
  const from = Date.parse(fromIso);
  const to = Date.parse(toIso);
  if (Number.isNaN(from) || Number.isNaN(to)) return "";
  const ms = to - from;
  return `${ms >= 0 ? "+" : "-"}${formatDuration(ms)}`;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function compactJson(value: unknown): string {
  try {
    const text = JSON.stringify(value);
    return text === undefined ? String(value) : text;
  } catch {
    return String(value);
  }
}

/** A best-effort, single-line rendering of an Observation value, selected by the
    enriched Entity type. Falls back to compact JSON whenever the shape is not
    the one the type implies: the payload is authoritative, this is only a view. */
function renderObservationValue(
  value: unknown,
  entityType: string | undefined,
  support: Record<string, unknown> | undefined,
): string {
  switch (entityType) {
    case "hearth.power/v1":
      return typeof value === "boolean" ? (value ? "On" : "Off") : compactJson(value);
    case "hearth.binarysensor/v1":
      return typeof value === "boolean" ? (value ? "True" : "False") : compactJson(value);
    case "hearth.measurement/v1": {
      const reading = readMeasurementReading(value, support);
      return reading ? reading.label : compactJson(value);
    }
    case "hearth.numericsensor/v1": {
      if (typeof value !== "number") return compactJson(value);
      const unit = supportStateUnit(support);
      return unit ? `${value} ${unit}` : String(value);
    }
    case "hearth.brightness/v1":
    case "hearth.colormode/v1":
    case "hearth.enumsetting/v1":
      return typeof value === "string" || typeof value === "number" ? String(value) : compactJson(value);
    case "hearth.colortemp/v1": {
      if (!isPlainObject(value)) return compactJson(value);
      const active = value.active === true ? " (active)" : " (inactive)";
      return `${String(value.value)} mireds${active}`;
    }
    case "hearth.colorxy/v1": {
      if (!isPlainObject(value)) return compactJson(value);
      const x = value.x;
      const y = value.y;
      if (typeof x !== "number" || typeof y !== "number") return compactJson(value);
      return `x ${x} (${(x / 10000).toFixed(4)}), y ${y} (${(y / 10000).toFixed(4)})`;
    }
    case "hearth.colorhs/v1": {
      if (!isPlainObject(value)) return compactJson(value);
      const { hue, saturation } = value;
      if (typeof hue !== "number" || typeof saturation !== "number") return compactJson(value);
      return `hue ${hue}°, saturation ${saturation}%`;
    }
    case "hearth.numericsetting/v1": {
      if (!isPlainObject(value)) return compactJson(value);
      if (value.mode === "choice") return String(value.choice);
      if (value.mode === "value") {
        const rendered = typeof value.value === "number" ? String(value.value) : compactJson(value.value);
        const unit = supportStateUnit(support);
        return unit ? `${rendered} ${unit}` : rendered;
      }
      return compactJson(value);
    }
    default:
      return compactJson(value);
  }
}

/** numericsensor and numericsetting support carry a display unit under
    support.state.unit. */
function supportStateUnit(support: Record<string, unknown> | undefined): string | undefined {
  const state = support?.state;
  if (!isPlainObject(state)) return undefined;
  const unit = state.unit;
  return typeof unit === "string" && unit.length > 0 ? unit : undefined;
}

function sourceTimeOf(fact: DeviceFact): string {
  return fact.family === "observation"
    ? (fact.data as ObservationFactData).adapter_received_at
    : (fact.data as EntityEventFactData).reported_at;
}

export default function DeviceFactsPage() {
  const [snapshotFacts, setSnapshotFacts] = useState<DeviceFact[]>([]);
  const [snapshotState, setSnapshotState] = useState<SnapshotState>("idle");
  const [snapshotMeta, setSnapshotMeta] = useState<DeviceFactSnapshot | null>(null);
  const [snapshotError, setSnapshotError] = useState<Error | null>(null);

  const [liveFacts, setLiveFacts] = useState<DeviceFact[]>([]);
  const [liveState, setLiveState] = useState<LiveState>("off");
  const [liveError, setLiveError] = useState<Error | null>(null);
  const [liveConsumer, setLiveConsumer] = useState<string | null>(null);
  const [liveDelivered, setLiveDelivered] = useState(0);
  const [liveMalformed, setLiveMalformed] = useState(0);
  const [frozenAt, setFrozenAt] = useState<string | null>(null);

  const [familyFilter, setFamilyFilter] = useState<FamilyFilter>("all");
  const [dispositionFilter, setDispositionFilter] = useState<DispositionFilter>("all");
  const [textFilter, setTextFilter] = useState("");
  const [rowCap, setRowCap] = useState<number>(DEFAULT_ROW_CAP);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const mountedRef = useRef(true);
  // Generation guards: a superseded snapshot read or live attempt must neither
  // publish state nor leave a consumer behind.
  const snapshotAttemptRef = useRef(0);
  const liveGenerationRef = useRef(0);
  const liveRef = useRef<{ live: DeviceFactLive; lease: NatsConnectionLease } | null>(null);
  const tableViewportRef = useRef<HTMLDivElement | null>(null);

  // Enrichment is dashboard-only and may fail: the fact payload carries the
  // canonical entity id and nothing else, so unresolved rows stay truthful.
  const entities = useApi<Collection<Entity>>("device-facts-entities", () =>
    apiFetch<Collection<Entity>>("/v1/entities?limit=200"),
  );
  const devices = useApi<Collection<Device>>("device-facts-devices", () =>
    apiFetch<Collection<Device>>("/v1/devices?limit=200"),
  );
  const entityById = useMemo(
    () => new Map((entities.data?.items ?? []).map((entity) => [entity.id, entity])),
    [entities.data],
  );
  const deviceById = useMemo(
    () => new Map((devices.data?.items ?? []).map((device) => [device.id, device])),
    [devices.data],
  );

  function enrichmentFor(entityId: string): EntityEnrichment | null {
    const entity = entityById.get(entityId);
    if (!entity) return null;
    const device = deviceById.get(entity.device_id);
    return {
      name: entity.name,
      type: entity.type,
      deviceId: entity.device_id,
      deviceName: device?.name ?? "",
      support: entity.support,
    };
  }

  async function refreshSnapshot() {
    const attempt = ++snapshotAttemptRef.current;
    setSnapshotState("reading");
    setSnapshotError(null);
    let lease: NatsConnectionLease | null = null;
    try {
      lease = await acquireNatsConnection();
      const result = await readDeviceFactSnapshot(lease.connection);
      if (attempt !== snapshotAttemptRef.current || !mountedRef.current) return;
      setSnapshotFacts(result.facts);
      setSnapshotMeta(result);
      setSnapshotState("ready");
    } catch (err) {
      if (attempt !== snapshotAttemptRef.current || !mountedRef.current) return;
      setSnapshotError(err instanceof Error ? err : new Error(String(err)));
      setSnapshotState("error");
    } finally {
      await lease?.release();
    }
  }

  function handleRefresh() {
    void refreshSnapshot();
    void entities.refresh();
    void devices.refresh();
  }

  async function enableLive() {
    const generation = ++liveGenerationRef.current;
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
          if (generation !== liveGenerationRef.current) return;
          setLiveFacts((prev) => dedupeDeviceFacts([fact, ...prev]).slice(0, LIVE_BUFFER_LIMIT));
          setLiveDelivered((count) => count + 1);
        },
        onMalformed: () => {
          if (generation !== liveGenerationRef.current) return;
          setLiveMalformed((count) => count + 1);
        },
        onError: (err) => {
          if (generation !== liveGenerationRef.current) return;
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
    } catch (err) {
      await lease?.release();
      if (generation !== liveGenerationRef.current || !mountedRef.current) return;
      setLiveError(err instanceof Error ? err : new Error(String(err)));
      setLiveState("error");
    }
  }

  async function disableLive() {
    liveGenerationRef.current += 1;
    setLiveState("off");
    setLiveError(null);
    setFrozenAt(new Date().toISOString());
    const current = liveRef.current;
    liveRef.current = null;
    if (current) {
      await current.live.close();
      await current.lease.release();
    }
  }

  useEffect(() => {
    mountedRef.current = true;
    void refreshSnapshot();
    return () => {
      mountedRef.current = false;
      // Invalidate any in-flight attempt, then delete the live consumer and drop
      // its lease. A snapshot read deletes its own consumer in a finally, so a
      // superseded read cannot leak one.
      snapshotAttemptRef.current += 1;
      liveGenerationRef.current += 1;
      const current = liveRef.current;
      liveRef.current = null;
      if (current) void current.live.close().then(() => current.lease.release());
    };
    // Mount-only: refreshes are driven by the Refresh button and the live switch.
  }, []);

  const rows = useMemo(() => mergeFactRows(snapshotFacts, liveFacts), [snapshotFacts, liveFacts]);

  const filtered = useMemo(() => {
    const needle = textFilter.trim().toLowerCase();
    return rows.filter((row) => {
      const fact = row.fact;
      if (familyFilter === "observation" && fact.family !== "observation") return false;
      if (familyFilter === "entity-event" && fact.family !== "entity-event") return false;
      if (dispositionFilter !== "all") {
        if (fact.family !== "observation") return false;
        if ((fact.data as ObservationFactData).disposition !== dispositionFilter) return false;
      }
      if (!needle) return true;
      const enrichment = enrichmentFor(fact.entityId);
      const haystack = [
        enrichment?.name ?? "",
        enrichment?.deviceName ?? "",
        fact.entityId,
        fact.id,
        fact.envelope.causation_id,
        fact.family === "entity-event" ? (fact.data as EntityEventFactData).event_id : "",
        fact.family === "entity-event" ? (fact.data as EntityEventFactData).name : "",
        fact.subject,
      ]
        .join(" ")
        .toLowerCase();
      return haystack.includes(needle);
    });
    // enrichmentFor/familyFilter/dispositionFilter/textFilter all derive from these.
  }, [rows, familyFilter, dispositionFilter, textFilter, entityById, deviceById]); // eslint-disable-line react-hooks/exhaustive-deps

  const display = filtered.slice(0, rowCap);
  const selectedIndex = display.findIndex((row) => row.fact.id === selectedId);
  const selectedRow = selectedId ? (rows.find((row) => row.fact.id === selectedId) ?? null) : null;
  const duplicateCount = rows.filter((row) => row.fromSnapshot && row.fromLive).length;
  const filtersActive =
    familyFilter !== "all" || dispositionFilter !== "all" || textFilter.trim() !== "";
  const dispositionDisabled = familyFilter === "entity-event";

  function clearFilters() {
    setFamilyFilter("all");
    setDispositionFilter("all");
    setTextFilter("");
  }

  function selectFamily(value: FamilyFilter) {
    setFamilyFilter(value);
    // Entity Events carry no disposition; keep the control honest.
    if (value === "entity-event") setDispositionFilter("all");
  }

  function onRowKeyDown(event: KeyboardEvent<HTMLTableRowElement>, factId: string, index: number) {
    const domRows = tableViewportRef.current?.querySelectorAll<HTMLTableRowElement>("tr[data-fact-id]");
    const count = domRows?.length ?? 0;
    if (count === 0) return;
    let next = -1;
    switch (event.key) {
      case "ArrowDown":
        next = Math.min(count - 1, index + 1);
        break;
      case "ArrowUp":
        next = Math.max(0, index - 1);
        break;
      case "Home":
        next = 0;
        break;
      case "End":
        next = count - 1;
        break;
      case "PageDown":
        next = Math.min(count - 1, index + 10);
        break;
      case "PageUp":
        next = Math.max(0, index - 10);
        break;
      case "Enter":
      case " ":
        event.preventDefault();
        setSelectedId(factId);
        return;
      default:
        return;
    }
    event.preventDefault();
    const target = domRows?.[next];
    const targetId = target?.getAttribute("data-fact-id") ?? null;
    if (targetId) setSelectedId(targetId);
    target?.focus();
    target?.scrollIntoView({ block: "nearest" });
  }

  const snapshotChipText =
    snapshotState === "reading"
      ? "reading…"
      : snapshotState === "error"
        ? "error"
        : snapshotMeta
          ? `${clockTime(snapshotMeta.readAt)} · ${snapshotFacts.length} retained`
          : "not read";
  const snapshotTone =
    snapshotState === "error"
      ? "error"
      : snapshotState === "reading"
        ? "warning"
        : snapshotState === "ready"
          ? "success"
          : "default";

  const liveChipText =
    liveState === "live"
      ? "LIVE"
      : liveState === "connecting"
        ? "CONNECTING"
        : liveState === "error"
          ? "ERROR"
          : "OFF";
  const liveTone =
    liveState === "live"
      ? "success"
      : liveState === "connecting"
        ? "warning"
        : liveState === "error"
          ? "error"
          : "default";
  const liveEnabled = liveState === "connecting" || liveState === "live";

  const liveStateLine =
    liveState === "connecting"
      ? `Connecting — creating an ephemeral consumer on ${DEVICE_FACT_STREAM}…`
      : liveState === "live"
        ? `Live — ephemeral DeliverNew tail (${liveConsumer ?? "consumer"}), non-durable.`
        : liveState === "error"
          ? "Live stopped on an error; re-enable to start a new ephemeral tail at the current stream end."
          : frozenAt
            ? `Off — view frozen at ${clockTime(frozenAt)}; retained snapshot only.`
            : "Off — retained snapshot only.";

  const liveExplanation =
    liveState === "connecting"
      ? "A DeliverNew consumer receives only facts published after it is created. Retained history is not replayed to it, so facts published during this moment are not delivered."
      : liveState === "live"
        ? "New facts arrive as the relay publishes them. This consumer keeps no position and is deleted when live is switched off: it offers no recovery, and facts published while live was off are not replayed. A retry stored again past the stream's duplicate window is collapsed here on its stable fct_ id."
        : liveState === "error"
          ? "The ephemeral consumer was deleted when the tail failed. Re-enabling creates a new DeliverNew consumer at the then-current stream end, so nothing published while it was down is replayed to it."
          : "The table is a bounded read of facts already retained in the stream, newest first. Enabling live adds a separate ephemeral DeliverNew consumer; facts published while live is off appear only after a snapshot refresh.";

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-lg font-semibold">Device facts</h1>
        <Button
          size="sm"
          variant="outline"
          disabled={snapshotState === "reading"}
          onClick={handleRefresh}
        >
          Refresh snapshot
        </Button>
        <span className="ms-auto" />
        <StatusChip label="snapshot" status={snapshotChipText} tone={snapshotTone} />
      </div>
      <p className="mt-1 text-sm text-muted-foreground">
        Facts published by Core into <code className="font-mono">{DEVICE_FACT_STREAM}</code> (subject
        filter <code className="font-mono">{DEVICE_FACT_SUBJECT_FILTER}</code>), newest first. The
        snapshot is a bounded retained read of at most {DEVICE_FACT_SNAPSHOT_LIMIT} facts and live is
        off by default.
      </p>
      {snapshotError && <ErrorBox error={snapshotError} />}

      <Section title="Live stream">
        <div className="flex flex-wrap items-center gap-3">
          <Switch
            id="device-facts-live"
            checked={liveEnabled}
            onCheckedChange={(next) => {
              if (next) void enableLive();
              else void disableLive();
            }}
          />
          <Label htmlFor="device-facts-live">Stream live facts</Label>
          <StatusChip status={liveChipText} tone={liveTone} />
          <span className="text-sm text-muted-foreground" role="status">
            {liveStateLine}
          </span>
        </div>
        <p className="mt-1.5 text-xs text-muted-foreground">{liveExplanation}</p>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Button
            size="xs"
            variant="outline"
            disabled={liveFacts.length === 0}
            onClick={() => {
              setLiveFacts([]);
            }}
          >
            Clear live arrivals
          </Button>
          <span className="text-xs text-muted-foreground">
            live arrivals delivered this session: {liveDelivered}
            {liveMalformed > 0 ? ` · ${liveMalformed} malformed skipped` : ""}
          </span>
        </div>
        {liveError && <ErrorBox error={liveError} />}
      </Section>

      <div className="mt-4 flex flex-wrap items-end gap-x-5 gap-y-3">
        <div>
          <span id="device-facts-family-label" className="block text-xs text-muted-foreground">
            Family
          </span>
          <div
            role="group"
            aria-labelledby="device-facts-family-label"
            className="mt-0.5 flex gap-1"
          >
            {FAMILY_OPTIONS.map((option) => (
              <Button
                key={option.value}
                size="xs"
                variant={familyFilter === option.value ? "secondary" : "ghost"}
                aria-pressed={familyFilter === option.value}
                onClick={() => selectFamily(option.value)}
              >
                {option.label}
              </Button>
            ))}
          </div>
        </div>
        <div>
          <span
            id="device-facts-disposition-label"
            className="block text-xs text-muted-foreground"
          >
            Disposition (observations only)
          </span>
          <div
            role="group"
            aria-labelledby="device-facts-disposition-label"
            className="mt-0.5 flex gap-1"
          >
            {DISPOSITION_OPTIONS.map((option) => (
              <Button
                key={option.value}
                size="xs"
                variant={dispositionFilter === option.value ? "secondary" : "ghost"}
                aria-pressed={dispositionFilter === option.value}
                disabled={dispositionDisabled}
                onClick={() => setDispositionFilter(option.value)}
              >
                {option.label}
              </Button>
            ))}
          </div>
        </div>
        <div className="min-w-[16rem] flex-1">
          <Label htmlFor="device-facts-text" className="text-xs text-muted-foreground">
            Filter
          </Label>
          <Input
            id="device-facts-text"
            type="search"
            className="mt-0.5"
            placeholder="entity, device, fact/source id, event name…"
            value={textFilter}
            onChange={(event) => setTextFilter(event.target.value)}
          />
        </div>
        <div>
          <Label htmlFor="device-facts-cap" className="text-xs text-muted-foreground">
            Row cap
          </Label>
          <select
            id="device-facts-cap"
            className="mt-0.5 h-8 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring dark:bg-input/30"
            value={rowCap}
            onChange={(event) => setRowCap(Number(event.target.value))}
          >
            {ROW_CAP_OPTIONS.map((cap) => (
              <option key={cap} value={cap}>
                {cap}
              </option>
            ))}
          </select>
        </div>
        {filtersActive && (
          <Button size="xs" variant="outline" onClick={clearFilters}>
            Clear filters
          </Button>
        )}
      </div>

      <p className="mt-2 text-xs text-muted-foreground">
        Entity, device and type names are dashboard enrichment from the devices read API; the fact
        payload and subject carry only the canonical entity id.
        {(entities.error || devices.error) && " Enrichment is unavailable right now, so rows show raw ids."}
      </p>

      <div className="mt-2 flex flex-wrap items-baseline gap-x-3 text-sm text-muted-foreground">
        <span id="device-facts-summary">
          Newest first · showing {display.length} of {filtered.length} matching · {rows.length} in
          view (snapshot ∪ live)
        </span>
        {duplicateCount > 0 && (
          <span
            className="text-warning"
            title="The same fct_ id arrived from both consumers; rows are keyed by fact id."
          >
            {duplicateCount} duplicate fct_ id collapsed
          </span>
        )}
        {snapshotMeta && snapshotMeta.malformed > 0 && (
          <span className="text-warning">
            {snapshotMeta.malformed} malformed message(s) skipped in the last snapshot
          </span>
        )}
      </div>

      <div className="mt-2" ref={tableViewportRef}>
        <Table
          role="grid"
          aria-label="Device facts, newest first"
          aria-describedby="device-facts-summary"
        >
          <TableHeader>
            <TableRow className="hover:bg-transparent">
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
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={8} className="py-6 text-center text-muted-foreground">
                  {rows.length === 0
                    ? snapshotState === "reading"
                      ? "Reading the retained snapshot…"
                      : "No facts in view. Refresh the snapshot or enable live streaming."
                    : "No facts match the current filters."}
                </TableCell>
              </TableRow>
            )}
            {display.map((row, index) => {
              const fact = row.fact;
              const enrichment = enrichmentFor(fact.entityId);
              const selected = fact.id === selectedId;
              const isGridEntry = selectedIndex === -1 ? index === 0 : index === selectedIndex;
              const value =
                fact.family === "observation"
                  ? renderObservationValue(
                      (fact.data as ObservationFactData).value,
                      enrichment?.type,
                      enrichment?.support,
                    )
                  : "—";
              const source = sourceTimeOf(fact);
              return (
                <TableRow
                  key={fact.id}
                  data-fact-id={fact.id}
                  tabIndex={isGridEntry ? 0 : -1}
                  aria-selected={selected}
                  data-state={selected ? "selected" : undefined}
                  className={cn(
                    "cursor-pointer align-top focus-visible:outline-2 focus-visible:outline-ring",
                    row.fromLive && "border-s-2 border-s-success",
                  )}
                  onClick={() => setSelectedId(fact.id)}
                  onKeyDown={(event) => onRowKeyDown(event, fact.id, index)}
                >
                  <TableCell>
                    <StatusChip
                      status={fact.family}
                      tone={fact.family === "entity-event" ? "info" : "default"}
                    />
                  </TableCell>
                  <TableCell>
                    {fact.family === "observation" ? (
                      <StatusChip
                        status={(fact.data as ObservationFactData).disposition}
                        tone={
                          (fact.data as ObservationFactData).disposition === "applied"
                            ? "success"
                            : "default"
                        }
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
                    <span className="block truncate text-xs text-muted-foreground">
                      {enrichment?.deviceName || fact.entityId}
                    </span>
                  </TableCell>
                  <TableCell className="whitespace-normal">
                    <span
                      className="block max-w-[12rem] truncate"
                      title={
                        fact.family === "observation"
                          ? compactJson((fact.data as ObservationFactData).value)
                          : "Entity Events carry no value"
                      }
                    >
                      {value}
                    </span>
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap items-center gap-1">
                      <StatusChip
                        status={
                          row.fromSnapshot && row.fromLive
                            ? "snapshot + live"
                            : row.fromLive
                              ? "live"
                              : "snapshot"
                        }
                        tone={row.fromLive ? "success" : "default"}
                      />
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
                      className="block truncate font-mono text-xs text-muted-foreground"
                      title={fact.id}
                    >
                      {fact.id}
                    </span>
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    <span title={fact.envelope.emitted_at}>{clockTime(fact.envelope.emitted_at)}</span>
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    <span title={source}>{clockTime(source)}</span>
                    <span className="ms-1 text-[0.65rem]">
                      {deltaLabel(source, fact.envelope.emitted_at)}
                    </span>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>

      <Section title="Fact detail">
        {selectedRow ? (
          <FactDetail
            row={selectedRow}
            enrichment={enrichmentFor(selectedRow.fact.entityId)}
            snapshotReadAt={snapshotMeta?.readAt ?? null}
          />
        ) : (
          <p className="text-sm text-muted-foreground">
            {selectedId
              ? "The selected fact is no longer in the buffer: it was trimmed by the row cap, or it was a live arrival that was cleared. Select another row."
              : "Select a fact row to inspect its envelope, source timing, value, subject, broker metadata and raw JSON."}
          </p>
        )}
      </Section>
    </div>
  );
}

function FactDetail({
  row,
  enrichment,
  snapshotReadAt,
}: {
  row: FactRow;
  enrichment: EntityEnrichment | null;
  snapshotReadAt: string | null;
}) {
  const fact = row.fact;
  const headers = Object.keys(fact.headers);
  const deliveredBy =
    row.fromSnapshot && row.fromLive
      ? "snapshot + live (same fct_ id, collapsed)"
      : row.fromLive
        ? "live (ephemeral DeliverNew tail)"
        : "snapshot (bounded retained read)";

  const metadataRows: [string, ReactNode][] = [
    ["Subject", fact.subject],
    ["Stream", DEVICE_FACT_STREAM],
    ["Stream sequence", String(fact.streamSequence)],
    ["Broker stored", fact.brokerStoredAt || "unknown"],
    ["Delivered by", deliveredBy],
    ["Consumer", `${fact.consumer} (ephemeral)`],
    ["Delivery count", String(fact.deliveryCount)],
    ["Delivered at", `${fact.deliveredAt} (this page's receive time)`],
  ];
  if (row.fromSnapshot && snapshotReadAt) metadataRows.push(["Snapshot read", snapshotReadAt]);
  metadataRows.push([
    "Published headers",
    headers.length > 0 ? (
      <span className="whitespace-pre-wrap">
        {headers.map((key) => `${key}: ${fact.headers[key].join(", ")}`).join("\n")}
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
          tone={fact.family === "entity-event" ? "info" : "default"}
        />
        {enrichment && (
          <span className="text-sm">
            {enrichment.deviceName ? `${enrichment.deviceName} · ` : ""}
            {enrichment.name}
          </span>
        )}
        <span className="font-mono text-xs text-muted-foreground">{fact.id}</span>
      </div>

      {fact.warnings.length > 0 && (
        <div
          role="alert"
          className="mt-2 rounded-lg border border-warning/35 bg-warning/10 px-3 py-2 text-sm text-warning"
        >
          <span className="font-medium">
            Contract mismatch{fact.warnings.length > 1 ? "es" : ""}:
          </span>
          <ul className="mt-1 list-disc ps-5">
            {fact.warnings.map((warning) => (
              <li key={warning}>{warning}</li>
            ))}
          </ul>
        </div>
      )}

      <Section title="Envelope">
        <Facts
          rows={[
            ["Fact id", `${fact.envelope.id} (stable identity, published as Nats-Msg-Id)`],
            ["Schema", fact.envelope.schema],
            ["Emitted (Core commit)", fact.envelope.emitted_at],
            ["Correlation id", fact.envelope.correlation_id],
            [
              "Causation id",
              `${fact.envelope.causation_id} (durable ${
                fact.family === "observation" ? "Observation" : "Entity Event"
              })`,
            ],
            ["Nats-Msg-Id header", fact.msgIdHeader ?? "absent"],
          ]}
        />
      </Section>

      <Section title="Source timing">
        {fact.family === "observation" ? (
          <Facts
            rows={[
              [
                "source_updated_at",
                (fact.data as ObservationFactData).source_updated_at ?? "absent on this fact",
              ],
              ["adapter_received_at", (fact.data as ObservationFactData).adapter_received_at],
              ["observed_at", (fact.data as ObservationFactData).observed_at],
              [
                "adapter_received → emitted",
                deltaLabel(
                  (fact.data as ObservationFactData).adapter_received_at,
                  fact.envelope.emitted_at,
                ),
              ],
              [
                "observed → emitted",
                deltaLabel(
                  (fact.data as ObservationFactData).observed_at,
                  fact.envelope.emitted_at,
                ),
              ],
            ]}
          />
        ) : (
          <Facts
            rows={[
              ["reported_at", `${(fact.data as EntityEventFactData).reported_at} (SDK reported time)`],
              ["received_at", `${(fact.data as EntityEventFactData).received_at} (Core receive time)`],
              [
                "recorded_at",
                `${(fact.data as EntityEventFactData).recorded_at} (Core first-record time; reused as emitted_at)`,
              ],
              [
                "reported → received",
                deltaLabel(
                  (fact.data as EntityEventFactData).reported_at,
                  (fact.data as EntityEventFactData).received_at,
                ),
              ],
              [
                "received → emitted",
                deltaLabel(
                  (fact.data as EntityEventFactData).received_at,
                  fact.envelope.emitted_at,
                ),
              ],
            ]}
          />
        )}
      </Section>

      {fact.family === "observation" ? (
        <Section title="Normalized value">
          <Facts
            rows={[
              [
                "Disposition",
                `${(fact.data as ObservationFactData).disposition}${
                  (fact.data as ObservationFactData).disposition === "applied"
                    ? " (State moved)"
                    : " (same value as the previous accepted Observation)"
                }`,
              ],
              [
                "Rendered",
                renderObservationValue(
                  (fact.data as ObservationFactData).value,
                  enrichment?.type,
                  enrichment?.support,
                ),
              ],
              ["Entity type", enrichment?.type ?? "unknown (no enrichment)"],
              ["Entity id", (fact.data as ObservationFactData).entity_id],
            ]}
          />
          <JsonCode code={JSON.stringify((fact.data as ObservationFactData).value ?? null, null, 2)} />
        </Section>
      ) : (
        <Section title="Entity event">
          <Facts
            rows={[
              [
                "Name",
                `${(fact.data as EntityEventFactData).name} (supported name on an event-source Entity; no State, no Command link)`,
              ],
              ["Entity type", enrichment?.type ?? "unknown (no enrichment)"],
              ["Event id", (fact.data as EntityEventFactData).event_id],
              ["Entity id", (fact.data as EntityEventFactData).entity_id],
            ]}
          />
        </Section>
      )}

      <Section title="Subject and broker metadata">
        <Facts rows={metadataRows} />
      </Section>

      <Section title="Raw fact JSON">
        <p className="text-xs text-muted-foreground">
          The published payload as decoded from the wire: every field the publisher sent, including
          any the typed envelope above does not model, pretty-printed for display only. Stream
          sequence, consumer and delivery time above are broker- and page-side metadata, not
          payload fields.
        </p>
        <JsonCode code={JSON.stringify(fact.rawPayload, null, 2)} />
        <RawJson value={row} title="Raw delivery record JSON" />
      </Section>
    </div>
  );
}
