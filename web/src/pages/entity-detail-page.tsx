import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";

import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { decodeEntityDetail, entityDetailSchema } from "../api/entity-detail";
import { useApi } from "../api/hooks.ts";
import type {
  Collection,
  CommandRecord,
  CommandResult,
  Entity,
  HealthTransition,
} from "../api/types.ts";
import { Button } from "../components/button.tsx";
import { Card } from "../components/card.tsx";
import { EmptyRow } from "../components/empty-row.tsx";
import { ErrorBox } from "../components/error-box.tsx";
import { Facts } from "../components/facts.tsx";
import { Input } from "../components/input.tsx";
import { Label } from "../components/label.tsx";
import { MonoId } from "../components/mono-id.tsx";
import { PageHeader } from "../components/page-header.tsx";
import { RawJson } from "../components/raw-json.tsx";
import { Section } from "../components/section.tsx";
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
import { Textarea } from "../components/textarea.tsx";
import EntityEventHistory from "../features/entities/entity-event-history.tsx";
import EntityStateHistory from "../features/entities/entity-state-history.tsx";
import { stateCellText } from "../features/entities/forecast-summary.ts";
import NameEditor from "../features/naming/name-editor.tsx";

/** String list from support.state; malformed entries dropped. */
const stringChoices = (
  support?: Record<string, unknown>,
  key = "choices"
): string[] => {
  const state = support?.state as Record<string, unknown> | undefined;
  const list = state?.[key];
  return Array.isArray(list)
    ? list.filter((choice): choice is string => typeof choice === "string")
    : [];
};

/** Enum-action values belong to trigger support because they constrain the command. */
const enumActionValues = (support?: Record<string, unknown>): string[] => {
  const operations = support?.operations as
    | { trigger?: { values?: unknown } }
    | undefined;
  const values = operations?.trigger?.values;
  return Array.isArray(values)
    ? values.filter((value): value is string => typeof value === "string")
    : [];
};

/** Per-entity numeric-setting bounds, unit, and named choices. */
const numericSettingBounds = (
  support?: Record<string, unknown>
): {
  minimum: number;
  maximum: number;
  unit: string;
  choices: string[];
} => {
  const state = support?.state as
    | { minimum?: unknown; maximum?: unknown; unit?: unknown }
    | undefined;
  return {
    minimum: typeof state?.minimum === "number" ? state.minimum : 0,
    maximum: typeof state?.maximum === "number" ? state.maximum : 100,
    unit: typeof state?.unit === "string" ? state.unit : "",
    choices: stringChoices(support, "choices"),
  };
};

/** Numeric-setting presets: one in-range value plus each named choice. */
const numericSettingPresets = (
  support?: Record<string, unknown>
): { label: string; params: string }[] => {
  const { minimum, maximum, unit, choices } = numericSettingBounds(support);
  const suffix = unit ? ` ${unit}` : "";
  const numeric =
    minimum <= maximum
      ? [
          {
            label: `set ${minimum}${suffix}`,
            params: `{"mode":"value","value":${minimum}}`,
          },
        ]
      : [];
  return [
    ...numeric,
    ...choices.map((choice) => ({
      label: choice,
      params: `{"mode":"choice","choice":${JSON.stringify(choice)}}`,
    })),
  ];
};

const brightnessPresets = (): { label: string; params: string }[] =>
  [0, 50, 100].map((value) => ({
    label: `brightness ${value}%`,
    params: `{"value":${value}}`,
  }));

/** Per-entity color-temperature bounds in mireds; defaults cover the common Zigbee range. */
const colorTempBounds = (
  support?: Record<string, unknown>
): {
  minimum: number;
  maximum: number;
  step: number;
} => {
  const state = support?.state as
    | { minimum?: unknown; maximum?: unknown }
    | undefined;
  const set = (support?.operations as { set?: { step?: unknown } } | undefined)
    ?.set;
  return {
    minimum: typeof state?.minimum === "number" ? state.minimum : 154,
    maximum: typeof state?.maximum === "number" ? state.maximum : 500,
    step: typeof set?.step === "number" && set.step > 0 ? set.step : 1,
  };
};

/** Color-temperature presets (mireds) that satisfy the entity's own support. */
const colorTempPresets = (
  support?: Record<string, unknown>
): { label: string; params: string }[] => {
  const { minimum, maximum, step } = colorTempBounds(support);
  const snap = (value: number) => Math.round(value / step) * step;
  return [minimum, (minimum + maximum) / 2, maximum]
    .map(snap)
    .filter(
      (value, index, all) =>
        value >= minimum && value <= maximum && all.indexOf(value) === index
    )
    .map((value) => ({
      label: `color temp ${value}`,
      params: `{"value":${value}}`,
    }));
};

/** Smallest valid color-temperature set value (mireds): first step multiple at or above minimum. */
const colorTempExample = (support?: Record<string, unknown>): number => {
  const { minimum, maximum, step } = colorTempBounds(support);
  const first = Math.ceil(minimum / step) * step;
  return first <= maximum ? first : minimum;
};

/** Fixed XY coordinate presets: support carries no gamut bounds, so these are
    raw protocol coordinates, not calibrated swatches. */
const colorXYPresets = (): { label: string; params: string }[] => [
  { label: "xy 3125 · 3291", params: '{"x":3125,"y":3291}' },
  { label: "xy 7000 · 3000", params: '{"x":7000,"y":3000}' },
  { label: "xy 1500 · 7000", params: '{"x":1500,"y":7000}' },
];

/** Fixed HS coordinate presets: both coordinates are always required. */
const colorHSPresets = (): { label: string; params: string }[] => [
  { label: "hs 0° · 100%", params: '{"hue":0,"saturation":100}' },
  { label: "hs 120° · 80%", params: '{"hue":120,"saturation":80}' },
  { label: "hs 240° · 50%", params: '{"hue":240,"saturation":50}' },
];

const presetsFor = (
  type: string | undefined,
  support?: Record<string, unknown>
): { label: string; params: string }[] => {
  switch (type) {
    case "hearth.power/v1": {
      return [
        { label: "power on", params: '{"value":true}' },
        { label: "power off", params: '{"value":false}' },
      ];
    }
    case "hearth.lock/v1": {
      return [
        { label: "lock", params: '{"value":"locked"}' },
        { label: "unlock", params: '{"value":"unlocked"}' },
      ];
    }
    case "hearth.brightness/v1": {
      return brightnessPresets();
    }
    case "hearth.colortemp/v1": {
      return colorTempPresets(support);
    }
    case "hearth.colorxy/v1": {
      return colorXYPresets();
    }
    case "hearth.colorhs/v1": {
      return colorHSPresets();
    }
    case "hearth.colorrgb/v1": {
      return [
        { label: "red", params: '{"red":255,"green":0,"blue":0}' },
        { label: "green", params: '{"red":0,"green":255,"blue":0}' },
        { label: "blue", params: '{"red":0,"green":0,"blue":255}' },
        { label: "white", params: '{"red":255,"green":255,"blue":255}' },
      ];
    }
    case "hearth.enumsetting/v1": {
      return stringChoices(support, "choices").map((choice) => ({
        label: choice,
        params: `{"value":${JSON.stringify(choice)}}`,
      }));
    }
    case "hearth.numericsetting/v1": {
      return numericSettingPresets(support);
    }
    case "hearth.enumaction/v1": {
      return enumActionValues(support).map((value) => ({
        label: value,
        params: `{"name":${JSON.stringify(value)}}`,
      }));
    }
    default: {
      return [];
    }
  }
};

/** Default command operation per Entity type (stateless actions trigger). */
const defaultOperation = (type: string | undefined): string =>
  type === "hearth.enumaction/v1" ? "trigger" : "set";

const exampleParams = (
  type: string | undefined,
  support?: Record<string, unknown>
): string => {
  if (type === "hearth.lock/v1") {
    return '{"value":"locked"}';
  }
  if (type === "hearth.brightness/v1") {
    return '{"value":50}';
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
  if (type === "hearth.colorrgb/v1") {
    return '{"red":255,"green":0,"blue":0}';
  }
  if (type === "hearth.enumsetting/v1") {
    return `{"value":${JSON.stringify(stringChoices(support, "choices")[0] ?? "")}}`;
  }
  if (type === "hearth.numericsetting/v1") {
    return `{"mode":"value","value":${numericSettingBounds(support).minimum}}`;
  }
  if (type === "hearth.enumaction/v1") {
    return `{"name":${JSON.stringify(enumActionValues(support)[0] ?? "")}}`;
  }
  return '{"value":true}';
};

/** True when the Entity type declares at least one command operation.
    Read-only types such as color mode and sensors carry empty operations. */
const hasOperations = (support?: Record<string, unknown>): boolean => {
  const operations = support?.operations as Record<string, unknown> | undefined;
  return !!operations && Object.keys(operations).length > 0;
};

/** Supported Entity Event names from support.events.names with malformed values
    dropped. A non-empty result identifies an event-source Entity: it sources
    Entity Events and therefore declares no State and no command Operations. */
const eventSourceNames = (support?: Record<string, unknown>): string[] => {
  const names = (support?.events as { names?: unknown } | undefined)?.names;
  return Array.isArray(names)
    ? names.filter(
        (name): name is string => typeof name === "string" && name !== ""
      )
    : [];
};

type StateQuantities = Entity["quantities"];
type StateSummarizer = (
  value: unknown,
  quantities?: StateQuantities
) => string | null;

const numericText = (
  amount: number,
  path: string,
  quantities?: StateQuantities
): string => {
  const quantity = quantities?.[path];
  return quantity
    ? `${amount.toFixed(quantity.fraction_digits)}${quantity.label ? ` ${quantity.label}` : ""}`
    : String(amount);
};

const activeWord = (active: boolean): string =>
  active ? "active" : "inactive";

const summarizeColorXY: StateSummarizer = (value, quantities) => {
  const state = value as { active?: unknown; x?: unknown; y?: unknown };
  if (
    typeof state.active !== "boolean" ||
    typeof state.x !== "number" ||
    typeof state.y !== "number"
  ) {
    return null;
  }
  return `x ${numericText(state.x, "/x", quantities)}, y ${numericText(state.y, "/y", quantities)}, ${activeWord(state.active)}`;
};

const summarizeColorHS: StateSummarizer = (value, quantities) => {
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
  return `hue ${numericText(state.hue, "/hue", quantities)}, saturation ${numericText(state.saturation, "/saturation", quantities)}, ${activeWord(state.active)}`;
};

const summarizeColorRGB: StateSummarizer = (value, quantities) => {
  const state = value as {
    active?: unknown;
    red?: unknown;
    green?: unknown;
    blue?: unknown;
  };
  if (
    typeof state.active !== "boolean" ||
    typeof state.red !== "number" ||
    typeof state.green !== "number" ||
    typeof state.blue !== "number"
  ) {
    return null;
  }
  return `RGB ${numericText(state.red, "/red", quantities)}, ${numericText(state.green, "/green", quantities)}, ${numericText(state.blue, "/blue", quantities)}, ${activeWord(state.active)}`;
};

const summarizeColorTemp: StateSummarizer = (value, quantities) => {
  const state = value as { active?: unknown; value?: unknown };
  if (typeof state.active !== "boolean" || typeof state.value !== "number") {
    return null;
  }
  return `${numericText(state.value, "/value", quantities)}, ${activeWord(state.active)}`;
};

const summarizeColorMode: StateSummarizer = (value) =>
  typeof value === "string" ? value : null;

const summarizeBinary: StateSummarizer = (value) => {
  if (typeof value !== "boolean") {
    return null;
  }
  return value ? "True" : "False";
};

const summarizeNumericSensor: StateSummarizer = (value, quantities) =>
  typeof value === "number" ? numericText(value, "", quantities) : null;

const summarizeEnumSetting: StateSummarizer = (value) =>
  typeof value === "string" ? value : null;

const summarizeNumericSetting: StateSummarizer = (value, quantities) => {
  if (typeof value !== "object" || value === null) {
    return null;
  }
  const setting = value as {
    mode?: unknown;
    value?: unknown;
    choice?: unknown;
  };
  if (setting.mode === "value" && typeof setting.value === "number") {
    return numericText(setting.value, "/value", quantities);
  }
  if (setting.mode === "choice" && typeof setting.choice === "string") {
    return setting.choice;
  }
  return null;
};

/** Stateless actions and forecast snapshots never render a State summary. */
const summarizeNever: StateSummarizer = () => null;

const summarizeDefault: StateSummarizer = (value, quantities) =>
  typeof value === "number" ? numericText(value, "", quantities) : null;

const stateSummarizers = new Map<string, StateSummarizer>([
  ["hearth.colorxy/v1", summarizeColorXY],
  ["hearth.colorhs/v1", summarizeColorHS],
  ["hearth.colorrgb/v1", summarizeColorRGB],
  ["hearth.colortemp/v1", summarizeColorTemp],
  ["hearth.colormode/v1", summarizeColorMode],
  ["hearth.binarysensor/v1", summarizeBinary],
  ["hearth.numericsensor/v1", summarizeNumericSensor],
  ["hearth.enumsetting/v1", summarizeEnumSetting],
  ["hearth.numericsetting/v1", summarizeNumericSetting],
  ["hearth.enumaction/v1", summarizeNever],
  ["hearth.hourlyforecast/v1", summarizeNever],
  ["hearth.dailyforecast/v1", summarizeNever],
]);

/** Human-readable State summary with units and mode activity.
    Returns null for types without a summary, malformed values, or stateless
    actions (an effect trigger has no reported State to summarize). */
const formatStateSummary = (
  type: string | undefined,
  value: unknown,
  quantities?: StateQuantities
): string | null => {
  if (value === null || value === undefined) {
    return null;
  }
  const summarize = type ? stateSummarizers.get(type) : undefined;
  return summarize
    ? summarize(value, quantities)
    : summarizeDefault(value, quantities);
};

const CommandHistory = ({ entityId }: { entityId: string }) => {
  const baseVersion = useBaseUrlVersion();
  const [page, setPage] = useState<{ version: number; cursor?: string }>(
    () => ({ version: baseVersion })
  );
  const cursor = page.version === baseVersion ? page.cursor : undefined;
  const { data, error, loading, refresh } = useApi(
    `cmds-${entityId}-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<CommandRecord>>(
        `/v1/entities/${entityId}/commands?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`
      )
  );
  return (
    <div>
      <div className="flex items-center gap-2">
        <Button
          size="default"
          variant="outline"
          onClick={() => {
            refresh();
          }}
        >
          Refresh history
        </Button>
        {loading && (
          <span className="text-muted-foreground text-sm">Loading…</span>
        )}
      </div>
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Table className="mt-3" aria-label="Command history">
            <TableHeader>
              <TableRow>
                <TableHead>Command id</TableHead>
                <TableHead>Operation</TableHead>
                <TableHead>Params</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Failure</TableHead>
                <TableHead>Requested</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.items.length === 0 && (
                <EmptyRow colSpan={6} message="No commands sent yet." />
              )}
              {data.items.map((command) => (
                <TableRow key={command.id}>
                  <TableCell className="max-w-[16rem]">
                    <MonoId
                      value={command.id}
                      className="text-muted-foreground"
                    />
                  </TableCell>
                  <TableCell>{command.operation}</TableCell>
                  <TableCell className="font-mono text-xs">
                    {JSON.stringify(command.parameters)}
                  </TableCell>
                  <TableCell>
                    <StatusChip status={command.status} />
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {command.failure_code ?? "—"}
                  </TableCell>
                  <TableCell className="text-muted-foreground font-mono text-xs">
                    {command.requested_at}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {data.next_cursor && (
            <Button
              size="default"
              variant="outline"
              className="mt-2"
              onClick={() =>
                setPage({ version: baseVersion, cursor: data.next_cursor })
              }
            >
              Next page
            </Button>
          )}
        </>
      )}
    </div>
  );
};

const AvailabilityHistory = ({ entityId }: { entityId: string }) => {
  const baseVersion = useBaseUrlVersion();
  const [page, setPage] = useState<{ version: number; cursor?: string }>(
    () => ({ version: baseVersion })
  );
  const cursor = page.version === baseVersion ? page.cursor : undefined;
  const { data, error, loading } = useApi(
    `avail-${entityId}-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<HealthTransition>>(
        `/v1/entities/${entityId}/availability/history?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`
      )
  );
  if (loading) {
    return <p className="text-muted-foreground text-sm">Loading…</p>;
  }
  if (error) {
    return <ErrorBox error={error} />;
  }
  return (
    <>
      <Table aria-label="Availability history">
        <TableHeader>
          <TableRow>
            <TableHead>Status</TableHead>
            <TableHead>Source</TableHead>
            <TableHead>Reason</TableHead>
            <TableHead>Observed at</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(data?.items ?? []).length === 0 && (
            <EmptyRow
              colSpan={4}
              message="No availability transitions recorded."
            />
          )}
          {data?.items.map((transition, index) => (
            <TableRow key={index}>
              <TableCell>
                <StatusChip status={transition.status} />
              </TableCell>
              <TableCell>{transition.source}</TableCell>
              <TableCell className="font-mono text-xs">
                {transition.reason?.code ?? "—"}
              </TableCell>
              <TableCell className="text-muted-foreground font-mono text-xs">
                {transition.observed_at}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {data?.next_cursor && (
        <Button
          size="default"
          variant="outline"
          className="mt-2"
          onClick={() =>
            setPage({ version: baseVersion, cursor: data.next_cursor })
          }
        >
          Next page
        </Button>
      )}
    </>
  );
};

interface CommandPanelProps {
  type: string | undefined;
  support?: Record<string, unknown>;
  operation: string;
  paramsText: string;
  paramsError: string | null;
  sending: boolean;
  result: CommandResult | null;
  sendError: Error | null;
  onOperationChange: (value: string) => void;
  onParamsChange: (value: string) => void;
  onPreset: (params: string) => void;
  onSend: () => void;
}

const CommandPanel = ({
  type,
  support,
  operation,
  paramsText,
  paramsError,
  sending,
  result,
  sendError,
  onOperationChange,
  onParamsChange,
  onPreset,
  onSend,
}: CommandPanelProps) => {
  const presets = presetsFor(type, support);
  return (
    <Card size="compact">
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-medium">Send command</span>
        <span className="text-muted-foreground font-mono text-xs">
          POST /v1/entities/{"{id}"}/commands
        </span>
      </div>
      <div className="mt-3 flex flex-col gap-3">
        {presets.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {presets.map((preset) => (
              <Button
                key={preset.label}
                size="default"
                variant="outline"
                onClick={() => {
                  onPreset(preset.params);
                }}
              >
                {preset.label}
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
              onChange={(event) => {
                onOperationChange(event.target.value);
              }}
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
              onChange={(event) => {
                onParamsChange(event.target.value);
              }}
            />
            <p
              className={`text-xs ${paramsError ? "text-destructive" : "text-muted-foreground"}`}
            >
              {paramsError ??
                `e.g. ${exampleParams(type, support)}${type ? ` for ${type} ${defaultOperation(type)}` : ""}`}
            </p>
          </div>
        </div>
        <div>
          <Button size="default" disabled={sending} onClick={onSend}>
            {sending ? "Sending…" : "Send command"}
          </Button>
        </div>
        {sendError && <ErrorBox error={sendError} />}
        {result && (
          <div>
            <div className="flex items-center gap-2">
              <StatusChip status={result.status} />
              {result.status === "satisfied" ? (
                <span className="text-muted-foreground font-mono text-xs">
                  obs={result.observation_id} value=
                  {JSON.stringify(result.value)}
                </span>
              ) : (
                <span className="text-muted-foreground text-xs">
                  dispatched — accepted with no observation; the effect was not
                  verified.
                </span>
              )}
            </div>
            <RawJson value={result} title="Command result JSON" />
          </div>
        )}
      </div>
    </Card>
  );
};

const ReadOnlyPanel = ({
  eventNames,
  type,
}: {
  eventNames: string[];
  type: string;
}) => {
  if (eventNames.length === 0) {
    return (
      <Card size="compact">
        <span className="text-sm font-medium">Read-only entity</span>
        <p className="text-muted-foreground mt-1.5 text-xs">
          {`This ${type} entity declares no command operations.`}
        </p>
      </Card>
    );
  }
  return (
    <Card size="compact">
      <span className="text-sm font-medium">Event source</span>
      <p className="text-muted-foreground mt-1.5 text-xs">
        {`This ${type} entity sources Entity Events and declares no command operations. Supported event names:`}
      </p>
      <ul className="mt-1.5 flex flex-wrap gap-1.5">
        {eventNames.map((name, index) => (
          <li
            key={`${index}-${name}`}
            className="rounded-md border px-1.5 py-0.5 font-mono text-xs"
          >
            {name}
          </li>
        ))}
      </ul>
    </Card>
  );
};

const EntityFactsCard = ({
  data,
  stateSummary,
  toggling,
  onEnabledChange,
}: {
  data: Entity;
  stateSummary: string | null;
  toggling: boolean;
  onEnabledChange: (enabled: boolean) => void;
}) => (
  <Card size="compact">
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
            ? `${stateCellText(data.type, data.state.value)} obs=${data.state.observation_id} at ${data.state.observed_at}`
            : "null (never observed)",
        ],
        ...(stateSummary === null
          ? []
          : ([["State summary", stateSummary]] as [string, string][])),
      ]}
    />
    <div className="mt-3 flex items-center gap-2">
      <Switch
        id="entity-enabled"
        checked={data.enabled}
        disabled={toggling}
        onCheckedChange={onEnabledChange}
      />
      <Label htmlFor="entity-enabled" className="text-sm">
        Enabled
      </Label>
      <span className="text-muted-foreground font-mono text-xs">
        PATCH /v1/entities/{"{id}"}
      </span>
    </div>
  </Card>
);

const EntityHeader = ({
  data,
  entityId,
  loading,
  onRefresh,
}: {
  data: Entity | undefined;
  entityId: string;
  loading: boolean;
  onRefresh: () => void;
}) => (
  <PageHeader
    title={data?.name ?? "Entity"}
    description={
      <span className="text-caption font-mono break-all">{entityId}</span>
    }
  >
    {data && (
      <StatusChip label="availability" status={data.availability.status} />
    )}
    {data && <StatusChip status={data.enabled ? "enabled" : "disabled"} />}
    <Button size="default" variant="outline" onClick={onRefresh}>
      Refresh
    </Button>
    {loading && <span className="text-muted-foreground text-sm">Loading…</span>}
  </PageHeader>
);

const EntityHistorySections = ({
  entityId,
  type,
  support,
  raw,
  eventNames,
}: {
  entityId: string;
  type: string;
  support: Record<string, unknown>;
  raw: unknown;
  eventNames: string[];
}) => {
  const eventSource = eventNames.length > 0;
  return (
    <>
      {eventSource ? (
        <Section title="Entity events">
          <EntityEventHistory
            key={entityId}
            entityId={entityId}
            supportedNames={eventNames}
          />
        </Section>
      ) : (
        <Section title="Command history">
          <CommandHistory key={entityId} entityId={entityId} />
        </Section>
      )}

      <Section title="Availability history">
        <AvailabilityHistory key={entityId} entityId={entityId} />
      </Section>

      {!eventSource && (
        <Section title="State history">
          <EntityStateHistory
            key={entityId}
            entityId={entityId}
            entityType={type}
            support={support}
          />
        </Section>
      )}

      <Section title="Support">
        <RawJson value={support} title="support JSON" />
      </Section>

      <RawJson value={raw} title="Raw entity JSON" />
    </>
  );
};

/** Owns the reset-worthy form and mutation state. The route reuses this page
    across entities, and the toolbar can switch servers under the same entity
    ID, so this scope is keyed on both: its form, pending, and outcome state
    start fresh there. History filters and raw JSON disclosures are siblings
    and keep their own state. */
const EntityFormScope = ({
  data,
  entityId,
  refresh,
}: {
  data: Entity;
  entityId: string;
  refresh: () => Promise<void>;
}) => {
  const [operationDraft, setOperationDraft] = useState<string | null>(null);
  const [paramsDraft, setParamsDraft] = useState<string | null>(null);
  const [paramsError, setParamsError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const [result, setResult] = useState<CommandResult | null>(null);
  const [sendError, setSendError] = useState<Error | null>(null);
  const [toggling, setToggling] = useState(false);
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);
  const operation = operationDraft ?? defaultOperation(data.type);
  const paramsText = paramsDraft ?? exampleParams(data.type, data.support);

  const sendCommand = async () => {
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
    let nextResult: CommandResult | null = null;
    let nextError: Error | null = null;
    try {
      nextResult = await apiFetch<CommandResult>(
        `/v1/entities/${entityId}/commands`,
        {
          method: "POST",
          body: JSON.stringify({ operation, parameters: params }),
        }
      );
    } catch (error_) {
      nextError = error_ instanceof Error ? error_ : new Error(String(error_));
    }
    if (!mountedRef.current) {
      return;
    }
    if (nextError) {
      setSendError(nextError);
    }
    if (nextResult) {
      setResult(nextResult);
      refresh();
    }
    setSending(false);
  };

  const setEnabled = async (enabled: boolean) => {
    setToggling(true);
    let nextError: Error | null = null;
    try {
      await apiFetch(`/v1/entities/${entityId}`, {
        method: "PATCH",
        headers: { "content-type": "application/merge-patch+json" },
        body: JSON.stringify({ enabled }),
      });
    } catch (error_) {
      nextError = error_ instanceof Error ? error_ : new Error(String(error_));
    }
    if (!mountedRef.current) {
      return;
    }
    if (nextError) {
      setSendError(nextError);
    } else {
      refresh();
    }
    setToggling(false);
  };

  const stateSummary = formatStateSummary(
    data.type,
    data.state?.value,
    data.quantities
  );
  const commandable = hasOperations(data.support);
  const eventNames = eventSourceNames(data.support);

  return (
    <>
      <EntityFactsCard
        data={data}
        stateSummary={stateSummary}
        toggling={toggling}
        onEnabledChange={(checked) => {
          setEnabled(checked);
        }}
      />

      {commandable ? (
        <CommandPanel
          type={data.type}
          support={data.support}
          operation={operation}
          paramsText={paramsText}
          paramsError={paramsError}
          sending={sending}
          result={result}
          sendError={sendError}
          onOperationChange={setOperationDraft}
          onParamsChange={setParamsDraft}
          onPreset={setParamsDraft}
          onSend={() => {
            sendCommand();
          }}
        />
      ) : (
        <ReadOnlyPanel eventNames={eventNames} type={data.type} />
      )}
    </>
  );
};

const EntityDetailView = ({
  entityId,
  baseVersion,
}: {
  entityId: string;
  baseVersion: number;
}) => {
  const {
    data: response,
    error,
    loading,
    refresh,
    setData,
  } = useApi(`entity-${entityId}`, () =>
    apiFetch(`/v1/entities/${entityId}`, {}, (text) => {
      const raw = entityDetailSchema.parse(JSON.parse(text));
      return { raw, entity: decodeEntityDetail(raw) };
    })
  );
  const data = response?.entity;
  const eventNames = eventSourceNames(data?.support);

  return (
    <div>
      <EntityHeader
        data={data}
        entityId={entityId}
        loading={loading}
        onRefresh={() => {
          refresh();
        }}
      />
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <NameEditor
            objectId={entityId}
            kind="entity"
            metadata={data}
            onUpdated={(metadata) => {
              setData((current) =>
                current
                  ? {
                      raw: { ...current.raw, ...metadata },
                      entity: { ...current.entity, ...metadata },
                    }
                  : current
              );
              refresh();
            }}
          />
          <div className="mt-3 grid gap-3 lg:grid-cols-2">
            <EntityFormScope
              key={`${entityId}:${baseVersion}`}
              data={data}
              entityId={entityId}
              refresh={refresh}
            />
          </div>

          <EntityHistorySections
            entityId={entityId}
            type={data.type}
            support={data.support}
            raw={response?.raw}
            eventNames={eventNames}
          />
        </>
      )}
    </div>
  );
};

/** Only the form/mutation scope keys on Entity or server change; histories,
    their selected filters, and raw JSON disclosures keep their UI state. */
const EntityDetailPage = () => {
  const { entityId = "" } = useParams();
  const baseVersion = useBaseUrlVersion();
  return <EntityDetailView entityId={entityId} baseVersion={baseVersion} />;
};

export default EntityDetailPage;
