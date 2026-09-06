import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { ApiError, apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Collection, CommandRecord, CommandResult, Entity, HealthTransition } from "../api/types.ts";
import {
  EmptyRow,
  ErrorBox,
  Facts,
  MonoId,
  RawJson,
  Section,
  StatusChip,
} from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import EntityStateHistory from "../components/entity-state-history.tsx";
import { Card, CardContent } from "../components/ui/card.tsx";
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
import { Textarea } from "../components/ui/textarea.tsx";

function presetsFor(type: string | undefined, support?: Record<string, unknown>): { label: string; params: string }[] {
  switch (type) {
    case "hearth.power/v1":
      return [
        { label: "power on", params: '{"value":true}' },
        { label: "power off", params: '{"value":false}' },
      ];
    case "hearth.brightness/v1":
      return brightnessPresets(support);
    case "hearth.colortemp/v1":
      return colorTempPresets(support);
    case "hearth.colorxy/v1":
      return colorXYPresets();
    case "hearth.colorhs/v1":
      return colorHSPresets();
    default:
      return [];
  }
}

function exampleParams(type: string | undefined, support?: Record<string, unknown>): string {
  if (type === "hearth.brightness/v1") {
    const { maximum, step } = brightnessBounds(support);
    return `{"value":${step <= maximum ? step : 0}}`;
  }
  if (type === "hearth.colortemp/v1") {
    return `{"value":${colorTempExample(support)}}`;
  }
  if (type === "hearth.colorxy/v1") {
    return '{"x":3125,"y":3291}';
  }
  if (type === "hearth.colorhs/v1") {
    return '{"hue":120,"saturation":80}';
  }
  return '{"value":true}';
}

/** Per-entity brightness bounds; defaults preserve the old fixed presets. */
function brightnessBounds(support?: Record<string, unknown>): { maximum: number; step: number } {
  const state = support?.state as { maximum?: unknown } | undefined;
  const set = (support?.operations as { set?: { step?: unknown } } | undefined)?.set;
  return {
    maximum: typeof state?.maximum === "number" ? state.maximum : 100,
    step: typeof set?.step === "number" && set.step > 0 ? set.step : 1,
  };
}

/** Color-temperature presets (mireds) that satisfy the entity's own support. */
function colorTempPresets(support?: Record<string, unknown>): { label: string; params: string }[] {
  const { minimum, maximum, step } = colorTempBounds(support);
  const snap = (value: number) => Math.round(value / step) * step;
  return [minimum, (minimum + maximum) / 2, maximum]
    .map(snap)
    .filter((value, index, all) => value >= minimum && value <= maximum && all.indexOf(value) === index)
    .map((value) => ({ label: `color temp ${value}`, params: `{"value":${value}}` }));
}

/** Smallest valid color-temperature set value (mireds): first step multiple at or above minimum. */
function colorTempExample(support?: Record<string, unknown>): number {
  const { minimum, maximum, step } = colorTempBounds(support);
  const first = Math.ceil(minimum / step) * step;
  return first <= maximum ? first : minimum;
}

/** Per-entity color-temperature bounds in mireds; defaults cover the common Zigbee range. */
function colorTempBounds(support?: Record<string, unknown>): { minimum: number; maximum: number; step: number } {
  const state = support?.state as { minimum?: unknown; maximum?: unknown } | undefined;
  const set = (support?.operations as { set?: { step?: unknown } } | undefined)?.set;
  return {
    minimum: typeof state?.minimum === "number" ? state.minimum : 154,
    maximum: typeof state?.maximum === "number" ? state.maximum : 500,
    step: typeof set?.step === "number" && set.step > 0 ? set.step : 1,
  };
}
/** Fixed XY coordinate presets: support carries no gamut bounds, so these are
    raw protocol coordinates, not calibrated swatches. */
function colorXYPresets(): { label: string; params: string }[] {
  return [
    { label: "xy 3125 · 3291", params: '{"x":3125,"y":3291}' },
    { label: "xy 7000 · 3000", params: '{"x":7000,"y":3000}' },
    { label: "xy 1500 · 7000", params: '{"x":1500,"y":7000}' },
  ];
}

/** Fixed HS coordinate presets: both coordinates are always required. */
function colorHSPresets(): { label: string; params: string }[] {
  return [
    { label: "hs 0° · 100%", params: '{"hue":0,"saturation":100}' },
    { label: "hs 120° · 80%", params: '{"hue":120,"saturation":80}' },
    { label: "hs 240° · 50%", params: '{"hue":240,"saturation":50}' },
  ];
}
/** True when the Entity type declares at least one command operation.
    Read-only types (color mode, ambient temperature) carry empty operations. */
function hasOperations(support?: Record<string, unknown>): boolean {
  const operations = support?.operations as Record<string, unknown> | undefined;
  return !!operations && Object.keys(operations).length > 0;
}

/** Human-readable State summary with units and mode activity.
    Returns null for types without a summary or malformed values. */
function formatStateSummary(type: string | undefined, value: unknown): string | null {
  if (value === null || value === undefined) return null;
  switch (type) {
    case "hearth.colorxy/v1": {
      const v = value as { active?: unknown; x?: unknown; y?: unknown };
      if (typeof v.active !== "boolean" || typeof v.x !== "number" || typeof v.y !== "number") return null;
      return `x ${v.x} (${(v.x / 10000).toFixed(4)}), y ${v.y} (${(v.y / 10000).toFixed(4)}), ${v.active ? "active" : "inactive"}`;
    }
    case "hearth.colorhs/v1": {
      const v = value as { active?: unknown; hue?: unknown; saturation?: unknown };
      if (typeof v.active !== "boolean" || typeof v.hue !== "number" || typeof v.saturation !== "number") return null;
      return `hue ${v.hue}°, saturation ${v.saturation}%, ${v.active ? "active" : "inactive"}`;
    }
    case "hearth.colortemp/v1": {
      const v = value as { active?: unknown; value?: unknown };
      if (typeof v.active !== "boolean" || typeof v.value !== "number") return null;
      return `${v.value} mireds, ${v.active ? "active" : "inactive"}`;
    }
    case "hearth.colormode/v1":
      return typeof value === "string" ? value : null;
    default:
      return null;
  }
}
function brightnessPresets(support?: Record<string, unknown>): { label: string; params: string }[] {
  const { maximum, step } = brightnessBounds(support);
  return [0, 50, 100]
    .filter((value) => value <= maximum && value % step === 0)
    .map((value) => ({ label: `brightness ${value}`, params: `{"value":${value}}` }));
}

function CommandHistory({ entityId }: { entityId: string }) {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const baseVersion = useBaseUrlVersion();
  useEffect(() => {
    // Cursors are scoped to one server: restart from the first page there.
    setCursor(undefined);
  }, [baseVersion]);
  const { data, error, loading, refresh } = useApi(
    `cmds-${entityId}-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<CommandRecord>>(
        `/v1/entities/${entityId}/commands?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
      ),
  );
  return (
    <div>
      <div className="flex items-center gap-2">
        <Button size="sm" variant="outline" onClick={() => void refresh()}>
          Refresh history
        </Button>
        {loading && <span className="text-sm text-muted-foreground">Loading…</span>}
      </div>
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Table className="mt-3">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Command id</TableHead>
                <TableHead>Operation</TableHead>
                <TableHead>Params</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Failure</TableHead>
                <TableHead>Requested</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.items.length === 0 && <EmptyRow colSpan={6} message="No commands sent yet." />}
              {data.items.map((c) => (
                <TableRow key={c.id}>
                  <TableCell className="max-w-[16rem]">
                    <MonoId value={c.id} className="text-muted-foreground" />
                  </TableCell>
                  <TableCell>{c.operation}</TableCell>
                  <TableCell className="font-mono text-xs">{JSON.stringify(c.parameters)}</TableCell>
                  <TableCell>
                    <StatusChip status={c.status} />
                  </TableCell>
                  <TableCell className="font-mono text-xs">{c.failure_code ?? "—"}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{c.requested_at}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {data.next_cursor && (
            <Button size="sm" variant="outline" className="mt-2" onClick={() => setCursor(data.next_cursor)}>
              Next page
            </Button>
          )}
        </>
      )}
    </div>
  );
}

function AvailabilityHistory({ entityId }: { entityId: string }) {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const baseVersion = useBaseUrlVersion();
  useEffect(() => {
    // Cursors are scoped to one server: restart from the first page there.
    setCursor(undefined);
  }, [baseVersion]);
  const { data, error, loading } = useApi(
    `avail-${entityId}-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<HealthTransition>>(
        `/v1/entities/${entityId}/availability/history?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
      ),
  );
  if (loading) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (error) return <ErrorBox error={error} />;
  return (
    <>
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>Status</TableHead>
            <TableHead>Source</TableHead>
            <TableHead>Reason</TableHead>
            <TableHead>Observed at</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(data?.items ?? []).length === 0 && (
            <EmptyRow colSpan={4} message="No availability transitions recorded." />
          )}
          {data?.items.map((t, i) => (
            <TableRow key={i}>
              <TableCell>
                <StatusChip status={t.status} />
              </TableCell>
              <TableCell>{t.source}</TableCell>
              <TableCell className="font-mono text-xs">{t.reason?.code ?? "—"}</TableCell>
              <TableCell className="font-mono text-xs text-muted-foreground">{t.observed_at}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {data?.next_cursor && (
        <Button size="sm" variant="outline" className="mt-2" onClick={() => setCursor(data.next_cursor)}>
          Next page
        </Button>
      )}
    </>
  );
}

export default function EntityDetailPage() {
  const { entityId = "" } = useParams();
  const entityIdRef = useRef(entityId);
  entityIdRef.current = entityId;
  const baseVersion = useBaseUrlVersion();
  const baseVersionRef = useRef(baseVersion);
  baseVersionRef.current = baseVersion;
  const { data, error, loading, refresh } = useApi(`entity-${entityId}`, () =>
    apiFetch<Entity>(`/v1/entities/${entityId}`),
  );
  const [operation, setOperation] = useState("set");
  const [paramsText, setParamsText] = useState('{"value":true}');
  const [paramsTouched, setParamsTouched] = useState(false);
  useEffect(() => {
    if (data && !paramsTouched) setParamsText(exampleParams(data.type, data.support));
  }, [data, paramsTouched]);
  useEffect(() => {
    // The route reuses this page across entities, and the toolbar can switch
    // servers under the same entity ID: reset form, outcome, and pending state.
    setOperation("set");
    setParamsText('{"value":true}');
    setParamsTouched(false);
    setParamsError(null);
    setResult(null);
    setSendError(null);
    setSending(false);
    setToggling(false);
  }, [entityId, baseVersion]);
  const [paramsError, setParamsError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const [result, setResult] = useState<CommandResult | null>(null);
  const [sendError, setSendError] = useState<Error | null>(null);
  const [toggling, setToggling] = useState(false);

  async function sendCommand() {
    const target = entityId;
    const targetBase = baseVersionRef.current;
    setParamsError(null);
    setSendError(null);
    setResult(null);
    let params: unknown;
    try {
      params = paramsText.trim() ? JSON.parse(paramsText) : {};
    } catch {
      setParamsError("Parameters must be valid JSON.");
      return;
    }
    setSending(true);
    try {
      const res = await apiFetch<CommandResult>(`/v1/entities/${target}/commands`, {
        method: "POST",
        body: JSON.stringify({ operation, parameters: params }),
      });
      if (entityIdRef.current !== target || baseVersionRef.current !== targetBase) return;
      setResult(res);
      void refresh();
    } catch (e) {
      if (entityIdRef.current !== target || baseVersionRef.current !== targetBase) return;
      setSendError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      if (entityIdRef.current === target && baseVersionRef.current === targetBase) setSending(false);
    }
  }

  async function setEnabled(enabled: boolean) {
    const target = entityId;
    const targetBase = baseVersionRef.current;
    setToggling(true);
    try {
      await apiFetch(`/v1/entities/${target}`, {
        method: "PATCH",
        body: JSON.stringify({ enabled }),
      });
      if (entityIdRef.current !== target || baseVersionRef.current !== targetBase) return;
      void refresh();
    } catch (e) {
      if (entityIdRef.current !== target || baseVersionRef.current !== targetBase) return;
      setSendError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      if (entityIdRef.current === target && baseVersionRef.current === targetBase) setToggling(false);
    }
  }

  const stateSummary = data ? formatStateSummary(data.type, data.state?.value) : null;
  const commandable = hasOperations(data?.support);

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-1 text-lg font-semibold">{data?.name ?? "Entity"}</h1>
        {data && <StatusChip label="availability" status={data.availability.status} />}
        {data && (
          <StatusChip
            label={data.enabled ? "enabled" : "disabled"}
            status={data.enabled ? "ok" : "disabled"}
          />
        )}
        <Button size="sm" variant="outline" onClick={() => void refresh()}>
          Refresh
        </Button>
        {loading && <span className="text-sm text-muted-foreground">Loading…</span>}
      </div>
      <p className="mt-1 font-mono text-xs break-all text-muted-foreground">{entityId}</p>
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <div className="mt-3 grid gap-3 lg:grid-cols-2">
            <Card size="sm">
              <CardContent>
                <Facts
                  rows={[
                    ["Type", data.type],
                    ["Device", data.device_id],
                    ["Adapter", data.adapter_id],
                    [
                      "Availability",
                      `${data.availability.source} since ${data.availability.since}${data.availability.reason ? ` (${data.availability.reason.code})` : ""}`,
                    ],
                    [
                      "State",
                      data.state
                        ? `${JSON.stringify(data.state.value)} obs=${data.state.observation_id} at ${data.state.observed_at}`
                        : "null (never observed)",
                    ],
                    ...(stateSummary !== null
                      ? ([["State summary", stateSummary]] as [string, string][])
                      : []),
                  ]}
                />
                <div className="mt-3 flex items-center gap-2">
                  <Switch
                    id="entity-enabled"
                    checked={data.enabled}
                    disabled={toggling}
                    onCheckedChange={(checked) => void setEnabled(checked)}
                  />
                  <Label htmlFor="entity-enabled" className="text-sm">
                    Enabled
                  </Label>
                  <span className="font-mono text-xs text-muted-foreground">
                    PATCH /v1/entities/{"{id}"}
                  </span>
                </div>
              </CardContent>
            </Card>

            {commandable ? (
            <Card size="sm">
              <CardContent>
                <div className="flex items-center justify-between gap-2">
                  <span className="text-sm font-medium">Send command</span>
                  <span className="font-mono text-xs text-muted-foreground">
                    POST /v1/entities/{"{id}"}/commands
                  </span>
                </div>
                <div className="mt-3 flex flex-col gap-3">
                  {presetsFor(data?.type, data?.support).length > 0 && (
                    <div className="flex flex-wrap gap-1.5">
                      {presetsFor(data?.type, data?.support).map((p) => (
                        <Button
                          key={p.label}
                          size="xs"
                          variant="outline"
                          onClick={() => { setParamsText(p.params); setParamsTouched(true); }}
                        >
                          {p.label}
                        </Button>
                      ))}
                    </div>
                  )}
                  <div className="grid gap-3 sm:grid-cols-[10rem_1fr]">
                    <div className="grid content-start gap-1.5">
                      <Label htmlFor="cmd-operation">operation</Label>
                      <Input
                        id="cmd-operation"
                        className="font-mono text-xs"
                        value={operation}
                        onChange={(e) => setOperation(e.target.value)}
                      />
                    </div>
                    <div className="grid gap-1.5">
                      <Label htmlFor="cmd-params">parameters (JSON)</Label>
                      <Textarea
                        id="cmd-params"
                        className="font-mono text-xs"
                        value={paramsText}
                        rows={2}
                        aria-invalid={!!paramsError}
                        onChange={(e) => { setParamsText(e.target.value); setParamsTouched(true); }}
                      />
                      <p className={`text-xs ${paramsError ? "text-destructive" : "text-muted-foreground"}`}>
                        {paramsError ?? `e.g. ${exampleParams(data?.type, data?.support)}${data ? ` for ${data.type} set` : ""}`}
                      </p>
                    </div>
                  </div>
                  <div>
                    <Button size="sm" disabled={sending} onClick={() => void sendCommand()}>
                      {sending ? "Sending…" : "Send command"}
                    </Button>
                  </div>
                  {sendError && (
                    <ErrorBox error={sendError instanceof ApiError ? sendError : sendError} />
                  )}
                  {result && (
                    <div>
                      <div className="flex items-center gap-2">
                        <StatusChip status={result.status} />
                        <span className="font-mono text-xs text-muted-foreground">
                          obs={result.observation_id} value={JSON.stringify(result.value)}
                        </span>
                      </div>
                      <RawJson value={result} title="Command result JSON" />
                    </div>
                  )}
                </div>
              </CardContent>
            </Card>
            ) : (
            <Card size="sm">
              <CardContent>
                <span className="text-sm font-medium">Read-only entity</span>
                <p className="mt-1.5 text-xs text-muted-foreground">
                  {`This ${data.type} entity declares no command operations.`}
                </p>
              </CardContent>
            </Card>
            )}
          </div>

          <Section title="Command history">
            <CommandHistory key={entityId} entityId={entityId} />
          </Section>

          <Section title="Availability history">
            <AvailabilityHistory key={entityId} entityId={entityId} />
          </Section>

          <Section title="State history">
            <EntityStateHistory
              key={entityId}
              entityId={entityId}
              entityType={data.type}
              support={data.support}
            />
          </Section>

          <Section title="Support">
            <RawJson value={data.support} title="support JSON" />
          </Section>

          <RawJson value={data} title="Raw entity JSON" />
        </>
      )}
    </div>
  );
}
