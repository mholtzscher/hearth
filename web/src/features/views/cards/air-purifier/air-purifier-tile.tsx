import type { ReactNode } from "react";

import { useRefusal } from "../../controls/command-motion";
import { ControlStatus } from "../../controls/control-status";
import { supportsOperation } from "../../controls/support";
import { useControl } from "../../data/provider";
import { StatusList } from "../data-card";
import { Reading } from "../reading";
import { listArrivals, oldestObservation } from "../reading-list/reading-rows";
import { TileFrame, tileActionClass } from "../tile-frame";
import { choiceWords } from "./category";
import { Category, usePurifier } from "./data";
import type { AirPurifierProps } from "./schema";

type Row = ReturnType<typeof usePurifier>["rows"][number];

const readingBody = ({
  power,
  mode,
  modeWord,
  on,
}: {
  power: Row;
  mode?: Row;
  modeWord?: string;
  on: boolean;
}) => (
  <Reading as="span" value={power.status.value} className="block">
    {power.data && "text" in power.data ? power.data.text : "—"}
    {on && modeWord && (
      <small
        className="text-compact-reading text-gray-9 data-[value=dimmed]:text-gray-8 ms-1.5"
        data-value={mode?.status.value}
      >
        {modeWord}
      </small>
    )}
  </Reading>
);

const pmDisplay = ({ pm }: { pm: Row }) => {
  const value = pm.data && "value" in pm.data ? pm.data : undefined;
  return (
    <div className="text-gray-9 flex flex-wrap items-baseline gap-x-2.5">
      <span
        className="data-[value=dimmed]:text-gray-8"
        data-value={pm.status.value}
      >
        PM2.5{" "}
        {value ? (
          <>
            {value.value.toFixed(value.format.fraction_digits)}{" "}
            <small className="text-muted-foreground">
              {value.format.unit_label}
            </small>
          </>
        ) : (
          "—"
        )}
      </span>
      {value && (
        <Category value={value.value} dimmed={pm.status.value === "dimmed"} />
      )}
    </div>
  );
};

const displayedRows = (rows: Row[], on: boolean) =>
  rows.filter(
    (row) =>
      row.key === "power" || row.key === "pm25" || (on && row.key === "mode")
  );

const renderRoleStatuses = (rows: Row[], allUnavailable: boolean) =>
  rows.map((row) => {
    const entries = allUnavailable
      ? row.status.entries.filter((entry) => entry.key !== "unavailable")
      : row.status.entries;
    return (
      entries.length > 0 && (
        <div
          key={row.key}
          className="text-muted-foreground mb-2 flex flex-wrap gap-x-2 gap-y-0.5"
        >
          <span>{row.name}</span>
          <StatusList entries={entries} />
        </div>
      )
    );
  });

const renderTileAction = ({
  canSet,
  title,
  on,
  working,
  requested,
  enabled,
  request,
  body,
}: {
  canSet: boolean;
  title: string;
  on: boolean;
  working: boolean;
  requested: unknown;
  enabled: boolean;
  request: (value: boolean) => void;
  body: ReactNode;
}) => {
  if (!canSet) {
    return <p>{body}</p>;
  }
  return (
    <button
      type="button"
      role="switch"
      data-slot="tile-action"
      className={tileActionClass}
      aria-label={`${title} power`}
      aria-checked={on}
      aria-busy={working || undefined}
      disabled={!enabled}
      onClick={() => request(!(working ? requested === true : on))}
    >
      {body}
    </button>
  );
};

const renderControlStatus = ({
  canSet,
  entityId,
  hasEntity,
}: {
  canSet: boolean;
  entityId: string;
  hasEntity: boolean;
}) => {
  if (canSet) {
    return (
      <ControlStatus
        className="mt-0 min-h-[21px]"
        entityId={entityId}
        subject="power"
        pending={(requested) =>
          requested === true ? "Turning on…" : "Turning off…"
        }
      />
    );
  }
  return (
    <p className="text-label text-muted-foreground min-h-[21px]">
      {hasEntity ? "Read-only" : ""}
    </p>
  );
};

export const AirPurifierTile = ({
  props,
  title,
}: {
  props: AirPurifierProps;
  title: string;
}) => {
  const { rows, entities, sources, status, allUnavailable, sourceExtra } =
    usePurifier(props);
  const [control, request] = useControl({
    entityId: props.power.entity_id,
    operation: "set",
  });
  const working = control.phase === "pending" || control.phase === "confirming";
  const refusal = useRefusal(control);
  const power = rows.find((row) => row.key === "power");
  const pm = rows.find((row) => row.key === "pm25");
  if (!power || !pm) {
    throw new Error("Air purifier requires power and PM2.5 rows");
  }
  const mode = rows.find((row) => row.key === "mode");
  const on = entities.power?.state?.value === true;
  const canSet = !!entities.power && supportsOperation(entities.power, "set");
  const modeWord =
    mode?.data && "text" in mode.data ? choiceWords(mode.data.text) : undefined;
  const displayed = displayedRows(rows, on);
  const body = readingBody({ power, mode, modeWord, on });
  return (
    <TileFrame
      title={title}
      className={refusal}
      on={on && power.status.value === "live"}
      interactive={canSet}
      working={working}
      stateObservedAt={entities.power?.state?.observed_at}
      status={status}
      observedAt={oldestObservation(displayed)}
      observedLabel="Oldest reading"
      arrivals={listArrivals([pm])}
      sources={sources}
      sourceExtra={sourceExtra}
    >
      {renderTileAction({
        canSet,
        title,
        on,
        working,
        requested: working ? control.requested : undefined,
        enabled: entities.power?.enabled ?? false,
        request,
        body,
      })}
      {pmDisplay({ pm })}
      {renderRoleStatuses(displayed, allUnavailable)}
      {renderControlStatus({
        canSet,
        entityId: props.power.entity_id,
        hasEntity: !!entities.power,
      })}
    </TileFrame>
  );
};
