import type { FieldEntity } from "../../bindings/entity";
import { PowerSwitch } from "../../controls/power-switch";
import type { ControlState } from "../../controls/queue";
import { SharedControlStatus } from "../../controls/shared-control-status";
import type { StatusControl } from "../../controls/shared-control-status";
import { supportsOperation } from "../../controls/support";
import { useControls } from "../../data/provider";
import { DataCard, StatusList } from "../data-card";
import { useArrival } from "../observation";
import { Reading, ReadingRow, ReadingUnit } from "../reading";
import {
  listArrivals,
  oldestObservation,
  shownReading,
} from "../reading-list/reading-rows";
import type { StatusEntry } from "../status";
import { AirPurifierTile } from "./air-purifier-tile";
import { choiceWords, choicesOf } from "./category";
import { Category, usePurifier } from "./data";
import { FanStepper } from "./fan-stepper";
import { ModeControl } from "./mode-control";
import type { AirPurifierProps, AirPurifierRole } from "./schema";

type Row = ReturnType<typeof usePurifier>["rows"][number];
type PurifierEntities = ReturnType<typeof usePurifier>["entities"];

const visibleEntries = (entries: StatusEntry[], hideUnavailable: boolean) =>
  hideUnavailable
    ? entries.filter((entry) => entry.key !== "unavailable")
    : entries;

const isControllable = (entities: PurifierEntities, role: AirPurifierRole) => {
  const entity = entities[role];
  return entity ? supportsOperation(entity, "set") : false;
};

const textValue = (row: Row | undefined) => {
  if (!row?.data || !("text" in row.data)) {
    return;
  }
  return row.data.text;
};

const shownModeFor = (
  modeState: ControlState | undefined,
  entity: FieldEntity | undefined
) => {
  if (!modeState) {
    return entity?.state?.value;
  }
  if (modeState.phase === "pending" || modeState.phase === "confirming") {
    return modeState.requested;
  }
  return entity?.state?.value;
};

const buildControls = (
  props: AirPurifierProps,
  entities: PurifierEntities
): StatusControl[] => {
  const controls: StatusControl[] = [];
  if (isControllable(entities, "power")) {
    controls.push({
      target: { entityId: props.power.entity_id, operation: "set" },
      name: "Power",
      pending: (value) => (value === true ? "Turning on…" : "Turning off…"),
    });
  }
  if (props.mode && isControllable(entities, "mode")) {
    controls.push({
      target: { entityId: props.mode.entity_id, operation: "set" },
      name: "Mode",
      pending: (value) => `Switching to ${choiceWords(String(value))}…`,
    });
  }
  if (props.fan_level && isControllable(entities, "fan_level")) {
    controls.push({
      target: { entityId: props.fan_level.entity_id, operation: "set" },
      name: "Fan level",
      pending: (value) => `Setting fan level to ${value}…`,
    });
  }
  if (props.display && isControllable(entities, "display")) {
    controls.push({
      target: { entityId: props.display.entity_id, operation: "set" },
      name: "Display",
      pending: (value) =>
        `Turning the display ${value === true ? "on" : "off"}…`,
    });
  }
  if (props.child_lock && isControllable(entities, "child_lock")) {
    controls.push({
      target: { entityId: props.child_lock.entity_id, operation: "set" },
      name: "Child lock",
      pending: (value) =>
        value === true ? "Locking the controls…" : "Unlocking the controls…",
    });
  }
  return controls;
};

const isShownRow = (
  row: Row,
  observedMode: string | undefined,
  entities: PurifierEntities
) => {
  if (row.key === "power" || row.key === "pm25" || row.key === "filter_life") {
    return true;
  }
  if (row.key === "mode") {
    return observedMode !== undefined;
  }
  if (row.key === "fan_level") {
    return isControllable(entities, "fan_level") || observedMode === "manual";
  }
  if (row.key === "display") {
    return isControllable(entities, "display");
  }
  if (row.key === "child_lock") {
    return isControllable(entities, "child_lock");
  }
  return false;
};

const renderPowerReading = (power: Row | undefined) => {
  if (!power?.data || !("text" in power.data)) {
    return <Reading value={power?.status.value}>—</Reading>;
  }
  return <Reading value={power.status.value}>{power.data.text}</Reading>;
};

const renderObservedDetail = ({
  observedMode,
  observedFan,
  modeValue,
}: {
  observedMode?: string;
  observedFan?: string;
  modeValue: Row["status"]["value"] | undefined;
}) => {
  if (!observedMode) {
    return null;
  }
  return (
    <p
      data-slot="purifier-detail"
      className="text-gray-9 data-[value=dimmed]:text-gray-8 tabular-nums"
      data-value={modeValue}
    >
      {choiceWords(observedMode)}
      {observedMode === "manual" && observedFan && ` · fan ${observedFan}`}
    </p>
  );
};

const renderPowerSwitch = ({
  entities,
  title,
}: {
  entities: PurifierEntities;
  title: string;
}) => {
  const entity = entities.power;
  if (!entity || !supportsOperation(entity, "set")) {
    return null;
  }
  return <PowerSwitch entity={entity} label={`${title} power`} />;
};

const renderModeControl = (entities: PurifierEntities) => {
  const entity = entities.mode;
  if (!entity || !supportsOperation(entity, "set")) {
    return null;
  }
  return <ModeControl entity={entity} label="Mode" />;
};

const renderFanStepper = ({
  entities,
  active,
}: {
  entities: PurifierEntities;
  active: boolean;
}) => {
  const entity = entities.fan_level;
  if (!entity || !supportsOperation(entity, "set")) {
    return null;
  }
  return (
    <FanStepper
      entity={entity}
      levels={choicesOf(entity.support).length}
      active={active}
      label="Fan level"
    />
  );
};

const renderDisplayControls = (entities: PurifierEntities) => {
  const roles = ["display", "child_lock"] as const;
  const shown = roles.filter((role) => isControllable(entities, role));
  if (shown.length === 0) {
    return null;
  }
  return (
    <div className="border-border grid grid-cols-2 gap-x-5 gap-y-1 border-t pt-3 pb-0.5 @max-[260px]:grid-cols-1">
      {shown.map((role) => {
        const entity = entities[role];
        if (!entity) {
          return null;
        }
        const label = role === "display" ? "Display" : "Child lock";
        return (
          <div
            className="text-gray-9 flex min-h-11 items-center justify-between gap-2.5"
            key={role}
          >
            <span>{label}</span>
            <PowerSwitch entity={entity} label={label} />
          </div>
        );
      })}
    </div>
  );
};

const NumericRow = ({
  row,
  hideUnavailable,
}: {
  row: Row;
  hideUnavailable: boolean;
}) => {
  const data = row.data && "value" in row.data ? row.data : undefined;
  const glow = useArrival(
    data
      ? {
          updatedAt: data.observed_at,
          shown: shownReading(data),
          eligible: row.status.glows,
        }
      : undefined
  );
  return (
    <ReadingRow
      name={row.name}
      value={row.status.value}
      arrival={glow}
      className="border-border border-t"
      beforeValue={
        row.key === "pm25" &&
        data && (
          <Category value={data.value} dimmed={row.status.value === "dimmed"} />
        )
      }
      afterValue={
        row.key === "filter_life" &&
        data &&
        data.value <= 10 && (
          <span className="text-warning basis-full">
            Replace the filter soon
          </span>
        )
      }
      status={visibleEntries(row.status.entries, hideUnavailable)}
    >
      {data ? (
        <>
          {data.value.toFixed(data.format.fraction_digits)}
          <ReadingUnit className="ms-[3px]">
            {data.format.unit_label}
          </ReadingUnit>
        </>
      ) : (
        "—"
      )}
    </ReadingRow>
  );
};

const RoleStatus = ({
  row,
  hideUnavailable,
}: {
  row: Row;
  hideUnavailable: boolean;
}) => {
  const entries = visibleEntries(row.status.entries, hideUnavailable);
  return entries.length > 0 ? (
    <div className="text-muted-foreground mb-2 flex flex-wrap gap-x-2 gap-y-0.5">
      <span>{row.name}</span>
      <StatusList entries={entries} />
    </div>
  ) : null;
};

const renderRoleStatuses = (
  rows: Row[],
  roles: readonly AirPurifierRole[],
  allUnavailable: boolean
) =>
  roles.map((role) => {
    const item = rows.find((row) => row.key === role);
    return item ? (
      <RoleStatus key={role} row={item} hideUnavailable={allUnavailable} />
    ) : null;
  });

export const AirPurifierCard = ({
  props,
  title,
}: {
  props: AirPurifierProps;
  title: string;
}) => {
  const { rows, entities, sources, status, allUnavailable, sourceExtra } =
    usePurifier(props);
  const row = (role: AirPurifierRole) => rows.find((item) => item.key === role);
  const power = row("power");
  const mode = row("mode");
  const fan = row("fan_level");
  const controls = buildControls(props, entities);
  const [modeStates] = useControls(
    props.mode ? [{ entityId: props.mode.entity_id, operation: "set" }] : []
  );
  const [modeState] = modeStates;
  const shownMode = shownModeFor(modeState, entities.mode);
  const active =
    !props.mode ||
    !choicesOf(entities.mode?.support).includes("manual") ||
    shownMode === "manual";
  const observedMode = textValue(mode);
  const observedFan = textValue(fan);
  const numeric = rows.filter(
    (item) => item.key === "pm25" || item.key === "filter_life"
  );
  // The footer states the oldest reading the card shows; a read-only setting it hides doesn't count.
  const shown = rows.filter((item) => isShownRow(item, observedMode, entities));
  return (
    <DataCard
      title={title}
      className="@container"
      status={status}
      observedAt={oldestObservation(shown)}
      observedLabel="Oldest reading"
      arrivals={listArrivals(numeric)}
      sources={sources}
      sourceExtra={sourceExtra}
    >
      <div className="flex items-center gap-3.5 pb-3">
        <div className="min-w-0 flex-1">
          {renderPowerReading(power)}
          {renderObservedDetail({
            observedMode,
            observedFan,
            modeValue: mode?.status.value,
          })}
        </div>
        {renderPowerSwitch({ entities, title })}
      </div>
      {power && (
        <StatusList
          entries={visibleEntries(power.status.entries, allUnavailable)}
        />
      )}
      <ul className="my-[-4px] grid list-none">
        {numeric.map((item) => (
          <NumericRow
            key={item.key}
            row={item}
            hideUnavailable={allUnavailable}
          />
        ))}
      </ul>
      {renderModeControl(entities)}
      {renderFanStepper({ entities, active })}
      {renderRoleStatuses(rows, ["mode", "fan_level"], allUnavailable)}
      {renderDisplayControls(entities)}
      {renderRoleStatuses(rows, ["display", "child_lock"], allUnavailable)}
      <SharedControlStatus controls={controls} />
    </DataCard>
  );
};

export const BoundAirPurifier = (props: AirPurifierProps) => {
  const title = props.label ?? "Air purifier";
  return props.layout === "tile" ? (
    <AirPurifierTile props={props} title={title} />
  ) : (
    <AirPurifierCard props={props} title={title} />
  );
};
