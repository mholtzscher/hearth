import { useState } from "react";
import type { z } from "zod";

import { Button } from "../../../../components/button";
import { usePowerReadings } from "../../bindings/power";
import type { PowerReading } from "../../bindings/power";
import { ControlStatus } from "../../controls/control-status";
import { PowerSwitch } from "../../controls/power-switch";
import { supportsOperation } from "../../controls/support";
import { useControls } from "../../data/provider";
import { DataCard, StatusList } from "../data-card";
import { useArrival } from "../observation";
import {
  listArrivals,
  oldestObservation,
  rowSources,
  rowStates,
} from "../reading-list/reading-rows";
import type { ReadingRow } from "../reading-list/reading-rows";
import { cardStatus } from "../status";
import type { toggleListSchema } from "./schema";

const readingText = (data: { on: boolean } | undefined) => {
  if (!data) {
    return "—";
  }
  return data.on ? "On" : "Off";
};
const groupText = (
  members: number,
  inProgress: boolean,
  off: number,
  failed: number
) => {
  if (members === 0) {
    return "";
  }
  if (inProgress) {
    return `Updating ${members} switches…`;
  }
  let suffix = "";
  if (failed > 0) {
    suffix = ` · ${failed} failed`;
  } else if (off < members) {
    suffix = " · check rows";
  }
  return `${off} of ${members} Off${suffix}`;
};

const SwitchRow = ({
  power,
  label,
}: {
  power: PowerReading;
  label: string;
}) => {
  const status = cardStatus(power.resource);
  const data = status.value === "empty" ? undefined : power.resource.data;
  const glow = useArrival(
    data
      ? {
          updatedAt: data.observed_at,
          shown: String(data.on),
          eligible: status.glows,
        }
      : undefined
  );
  const canSet = !!power.entity && supportsOperation(power.entity, "set");
  return (
    <li className="not-first:border-border relative grid grid-cols-[minmax(0,1fr)_auto_44px] items-center gap-3 py-3 not-first:border-t">
      {glow !== undefined && (
        <span
          key={glow}
          className="animate-arrival bg-orange pointer-events-none absolute inset-x-0 -bottom-px z-[1] h-0.5"
          aria-hidden="true"
        />
      )}
      <div className="text-label text-gray-9 min-w-0 [overflow-wrap:anywhere]">
        <span>{label}</span>
        <StatusList entries={status.entries} />
        {canSet ? (
          <ControlStatus
            className="text-muted-foreground mt-0.5"
            entityId={power.source.entity_id}
            subject="power"
            pending={(value) =>
              value === true ? "Turning on…" : "Turning off…"
            }
            idle={power.since}
          />
        ) : (
          <p className="text-label text-muted-foreground mt-0.5 h-[21px] truncate">
            {power.entity ? "Read-only" : power.since}
          </p>
        )}
      </div>
      <span
        className="text-compact-reading text-foreground data-[value=dimmed]:text-gray-8 data-[value=empty]:text-gray-6 ms-auto max-w-full flex-none text-right [overflow-wrap:break-word] whitespace-nowrap"
        data-value={status.value}
      >
        {readingText(data)}
      </span>
      {canSet && power.entity ? (
        <PowerSwitch
          className="m-0"
          entity={power.entity}
          label={`${label} power`}
        />
      ) : (
        <span className="w-11" aria-hidden="true" />
      )}
    </li>
  );
};

export const BoundToggleList = ({
  title,
  rows,
  all_off,
}: z.infer<typeof toggleListSchema>) => {
  const powers = usePowerReadings(rows.map((row) => row.source));
  const targets = rows.map((row) => ({
    entityId: row.source.entity_id,
    operation: "set" as const,
  }));
  const [controls, request] = useControls(targets);
  const [attempted, setAttempted] = useState<string[]>([]);
  const identity = JSON.stringify(rows.map((row) => row.source));
  const [attemptedIdentity, setAttemptedIdentity] = useState(identity);
  // A changed row set discards the previous attempt before it renders.
  if (attemptedIdentity !== identity) {
    setAttemptedIdentity(identity);
    setAttempted([]);
  }
  const readingRows: ReadingRow[] = powers.map((power, index) => ({
    key: power.source.entity_id,
    name: rows[index].label ?? power.resource.metadata?.label ?? "Power",
    resource: {
      ...power.resource,
      data: power.resource.data && {
        text: power.resource.data.on ? "On" : "Off",
        observed_at: power.resource.data.observed_at,
      },
    },
  }));
  const displayed = rowStates(readingRows);
  const sources = rowSources(readingRows);
  for (const row of readingRows) {
    if (!sources.some((source) => source.entityId === row.key)) {
      sources.push({ entityId: row.key, label: row.name });
    }
  }
  const working = controls.some(
    (control) => control.phase === "pending" || control.phase === "confirming"
  );
  const eligible = powers.flatMap((power, index) =>
    power.entity?.enabled &&
    supportsOperation(power.entity, "set") &&
    power.resource.data?.on !== false
      ? [index]
      : []
  );
  const canGroup = powers.some(
    (power) => power.entity && supportsOperation(power.entity, "set")
  );
  const members = powers.flatMap((power, index) =>
    attempted.includes(power.source.entity_id) ? [index] : []
  );
  const inProgress = members.some(
    (index) =>
      controls[index].phase === "pending" ||
      controls[index].phase === "confirming"
  );
  const off = members.filter(
    (index) =>
      powers[index].resource.status === "ready" &&
      powers[index].resource.data?.on === false
  ).length;
  const failed = members.filter(
    (index) => controls[index].phase === "failed"
  ).length;
  const groupFeedback = groupText(members.length, inProgress, off, failed);
  return (
    <DataCard
      title={title}
      status={[]}
      sources={sources}
      arrivals={listArrivals(displayed)}
      observedAt={oldestObservation(displayed)}
      observedLabel="Oldest reading"
      sourceExtra={
        <>
          <p>
            Each switch uses its Entity&apos;s current support. Turn all off
            requests separate Commands for enabled, supported rows that are not
            already observed Off.
          </p>
          <p>
            Group feedback counts observed Off readings; it is not an atomic
            multi-Device outcome.
          </p>
          {powers
            .filter((power) => power.historyError)
            .map((power) => (
              <p key={power.source.entity_id}>
                {power.resource.metadata?.label ?? "Power"}: since time
                unavailable.
              </p>
            ))}
        </>
      }
    >
      <ul className="-my-1 grid list-none">
        {powers.map((power, index) => (
          <SwitchRow
            key={power.source.entity_id}
            power={power}
            label={readingRows[index].name}
          />
        ))}
      </ul>
      {all_off && canGroup && (
        <div className="border-border mt-3 flex flex-wrap items-center gap-x-3 gap-y-2 border-t pt-3.5">
          <Button
            variant="outline"
            size="touch"
            type="button"
            disabled={working || eligible.length === 0}
            onClick={() => {
              setAttempted(
                eligible.map((index) => powers[index].source.entity_id)
              );
              for (const index of eligible) {
                request(targets[index], false);
              }
            }}
          >
            Turn all off
          </Button>
          <output
            className="text-label text-muted-foreground data-[failed]:text-destructive h-[21px] min-w-0 flex-[1_1_9em] truncate"
            data-failed={failed > 0 || undefined}
            title={groupFeedback || undefined}
          >
            {groupFeedback}
          </output>
        </div>
      )}
    </DataCard>
  );
};
