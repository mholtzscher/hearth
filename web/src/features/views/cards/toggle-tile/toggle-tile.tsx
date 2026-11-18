import type { ReactNode } from "react";
import type { z } from "zod";

import { deviceRequest } from "../../bindings/device";
import type { FieldDevice } from "../../bindings/device";
import { usePowerReadings } from "../../bindings/power";
import type { PowerData, PowerReading } from "../../bindings/power";
import { useNumericReads } from "../../bindings/registry";
import type { MetricData, Resolved, SeriesData } from "../../bindings/types";
import { ControlStatus } from "../../controls/control-status";
import { supportsOperation } from "../../controls/support";
import { useControl, useRead } from "../../data/provider";
import { StatusList } from "../data-card";
import { Trend, trendMethod } from "../metric/trend";
import { Reading } from "../reading";
import {
  listArrivals,
  oldestObservation,
  ReadingRows,
  rowSources,
  rowStates,
} from "../reading-list/reading-rows";
import type { ReadingRow } from "../reading-list/reading-rows";
import { cardStatus } from "../status";
import type { CardStatus } from "../status";
import { TileFrame, tileActionClass } from "../tile-frame";
import type { toggleTileSchema } from "./schema";

type ToggleTileProps = z.infer<typeof toggleTileSchema>;
type NumericRead = Resolved<MetricData | SeriesData>;

const powerValue = (data: Resolved<PowerData>["data"]) => {
  if (!data) {
    return "—";
  }
  return data.on ? "On" : "Off";
};

const metricData = (
  resource: NumericRead | undefined
): Resolved<MetricData> | undefined => {
  if (!resource) {
    return undefined;
  }
  return {
    ...resource,
    data: resource.data && "value" in resource.data ? resource.data : undefined,
  };
};

const seriesData = (
  resource: NumericRead | undefined
): Resolved<SeriesData> | undefined => {
  if (!resource) {
    return undefined;
  }
  return {
    ...resource,
    data:
      resource.data && "buckets" in resource.data ? resource.data : undefined,
  };
};

const powerRow = ({
  source,
  title,
  power,
  data,
}: {
  source: ToggleTileProps["source"];
  title: string;
  power: PowerReading;
  data: Resolved<PowerData>["data"];
}): ReadingRow => ({
  key: source.entity_id,
  name: title,
  resource: {
    ...power.resource,
    data: data && {
      text: data.on ? "On" : "Off",
      observed_at: data.observed_at,
    },
  },
});

const metricRow = (
  metric: ToggleTileProps["metric"],
  reading: Resolved<MetricData>
): ReadingRow => ({
  key: `metric:${metric?.source.entity_id ?? ""}:${metric?.source.field ?? ""}`,
  name: reading.metadata?.label ?? "Metering reading",
  resource: reading,
});

const detailRow = (
  metric: ToggleTileProps["metric"],
  detail: NumericRead
): ReadingRow => ({
  key: `detail:${metric?.detail?.source.entity_id ?? ""}:${metric?.detail?.source.field ?? ""}`,
  name: metric?.detail?.label ?? detail.metadata?.label ?? "Reading",
  resource: {
    ...detail,
    data: detail.data && "value" in detail.data ? detail.data : undefined,
  },
});

const buildRows = ({
  source,
  title,
  power,
  data,
  reading,
  detail,
  metric,
}: {
  source: ToggleTileProps["source"];
  title: string;
  power: PowerReading;
  data: Resolved<PowerData>["data"];
  reading?: Resolved<MetricData>;
  detail?: NumericRead;
  metric?: ToggleTileProps["metric"];
}): ReadingRow[] => {
  const rows: ReadingRow[] = [powerRow({ source, title, power, data })];
  if (reading) {
    rows.push(metricRow(metric, reading));
  }
  if (detail) {
    rows.push(detailRow(metric, detail));
  }
  return rows;
};

const numericBindings = (
  metric: ToggleTileProps["metric"]
): Parameters<typeof useNumericReads>[0] => {
  if (!metric) {
    return [];
  }
  return [
    { source: metric.source },
    { source: metric.source, window: metric.window },
    ...(metric.detail ? [{ source: metric.detail.source }] : []),
  ];
};

const referencedSources = (
  source: ToggleTileProps["source"],
  metric: ToggleTileProps["metric"]
) => {
  if (!metric) {
    return [source];
  }
  return [
    source,
    metric.source,
    ...(metric.detail ? [metric.detail.source] : []),
  ];
};

const addMissingSources = (
  rows: ReadingRow[],
  sources: ReturnType<typeof rowSources>,
  referenced: { entity_id: string }[]
) => {
  for (const [index, row] of rows.entries()) {
    const id = referenced[index].entity_id;
    if (!sources.some((item) => item.entityId === id)) {
      sources.push({ entityId: id, label: row.name });
    }
  }
};

const renderReading = ({
  statusValue,
  data,
  metric,
  metricStatus,
  measured,
}: {
  statusValue: CardStatus["value"];
  data: Resolved<PowerData>["data"];
  metric?: ToggleTileProps["metric"];
  metricStatus?: CardStatus;
  measured?: Resolved<MetricData>["data"];
}) => (
  <Reading
    as="span"
    value={statusValue}
    className={
      metric
        ? "max-tablet:flex max-tablet:flex-wrap max-tablet:items-baseline max-tablet:justify-between max-tablet:gap-x-3 max-tablet:gap-y-1 block"
        : "block"
    }
  >
    {powerValue(data)}
    {metric && (
      <small
        data-slot="power-metric"
        className="text-label text-muted-foreground data-[value=empty]:text-gray-6 max-tablet:ms-0 max-tablet:max-w-full max-tablet:whitespace-normal max-tablet:[overflow-wrap:anywhere] ms-1.5 inline-block tracking-normal whitespace-nowrap"
        data-value={metricStatus?.value}
      >
        {measured ? (
          <>
            <span className="max-tablet:text-compact-reading max-tablet:font-normal max-tablet:tracking-[-.02em]">
              {measured.value.toFixed(measured.format.fraction_digits)}
            </span>
            {measured.format.unit_label && ` ${measured.format.unit_label}`}
          </>
        ) : (
          "—"
        )}
      </small>
    )}
  </Reading>
);

const renderSourceExtra = ({
  power,
  metric,
  reading,
}: {
  power: PowerReading;
  metric?: ToggleTileProps["metric"];
  reading?: Resolved<MetricData>;
}) => (
  <>
    <p>
      On/Off comes from observed power State. Pressing the tile requests a
      change; the reading and surface wait for observed State.
    </p>
    <p>
      Since times use recorded changes over the last 24 hours, not observation
      age.
    </p>
    {power.historyError && (
      <p>Couldn&apos;t load the since time. {power.historyError}</p>
    )}
    {metric && (
      <>
        <p>
          Metering source: {reading?.metadata?.label ?? metric.source.entity_id}
          . The number and sparkline share this source.
        </p>
        {trendMethod(metric.window)}
      </>
    )}
  </>
);

const renderAction = ({
  canSet,
  title,
  on,
  working,
  requested,
  enabled,
  request,
  readingBody,
}: {
  canSet: boolean;
  title: string;
  on?: boolean;
  working: boolean;
  requested: unknown;
  enabled: boolean;
  request: (value: boolean) => void;
  readingBody: ReactNode;
}) => {
  if (!canSet) {
    return <p>{readingBody}</p>;
  }
  return (
    <button
      type="button"
      role="switch"
      data-slot="tile-action"
      className={tileActionClass}
      aria-label={`${title} power`}
      aria-checked={on ?? false}
      aria-busy={working || undefined}
      disabled={!enabled}
      onClick={() => request(!(working ? requested === true : on === true))}
    >
      {readingBody}
    </button>
  );
};

const renderControlStatus = ({
  canSet,
  entityId,
  power,
}: {
  canSet: boolean;
  entityId: string;
  power: PowerReading;
}) => {
  if (canSet) {
    return (
      <ControlStatus
        className="mt-0 min-h-[21px]"
        entityId={entityId}
        subject="power"
        pending={(value) => (value === true ? "Turning on…" : "Turning off…")}
        idle={power.since}
      />
    );
  }
  return (
    <p className="text-label text-muted-foreground mt-0 min-h-[21px] leading-normal">
      {power.entity ? "Read-only" : power.since}
    </p>
  );
};

const renderMetricDetails = ({
  metric,
  metricStatus,
  reading,
  series,
}: {
  metric?: ToggleTileProps["metric"];
  metricStatus?: CardStatus;
  reading?: Resolved<MetricData>;
  series?: Resolved<SeriesData>;
}) => {
  if (!metric) {
    return null;
  }
  return (
    <>
      {metricStatus && metricStatus.entries.length > 0 && (
        <div className="text-label text-muted-foreground grid gap-0.5">
          <span>{reading?.metadata?.label ?? "Metering reading"}</span>
          <StatusList entries={metricStatus.entries} />
        </div>
      )}
      {series && (
        <Trend
          className="mt-0.5"
          bandClassName="[fill-opacity:.05]"
          window={metric.window}
          resource={series}
          dimmed={metricStatus?.value === "dimmed"}
        />
      )}
    </>
  );
};

const useTileTitle = (label: string | undefined, power: PowerReading) => {
  const device = useRead(
    label === undefined && power.entity?.device_id
      ? deviceRequest(power.entity.device_id)
      : null
  ).data as FieldDevice | undefined;
  return label ?? device?.name ?? power.resource.metadata?.label ?? "Power";
};

export const BoundToggleTile = ({ source, label, metric }: ToggleTileProps) => {
  const [power] = usePowerReadings([source]);
  const title = useTileTitle(label, power);
  const numeric = useNumericReads(numericBindings(metric));
  const [current, history, detail] = numeric;
  const reading = metricData(current);
  const series = seriesData(history);
  const [control, request] = useControl({
    entityId: source.entity_id,
    operation: "set",
  });
  const working = control.phase === "pending" || control.phase === "confirming";
  const status = cardStatus(power.resource);
  const metricStatus = reading && cardStatus(reading);
  const data = status.value === "empty" ? undefined : power.resource.data;
  const measured = metricStatus?.value === "empty" ? undefined : reading?.data;
  const canSet = !!power.entity && supportsOperation(power.entity, "set");
  const on = data?.on;
  const rows = buildRows({
    source,
    title,
    power,
    data,
    reading,
    detail,
    metric,
  });
  const displayed = rowStates(rows);
  const sources = rowSources(rows);
  const referenced = referencedSources(source, metric);
  addMissingSources(rows, sources, referenced);
  const readingBody = renderReading({
    statusValue: status.value,
    data,
    metric,
    metricStatus,
    measured,
  });
  return (
    <TileFrame
      title={title}
      on={!!on && status.value === "live"}
      interactive={canSet}
      metered={!!metric}
      status={status.entries}
      observedAt={oldestObservation(displayed)}
      observedLabel={metric ? "Oldest reading" : "Observed"}
      arrivals={listArrivals(displayed)}
      sources={sources}
      sourceExtra={renderSourceExtra({ power, metric, reading })}
    >
      {renderAction({
        canSet,
        title,
        on,
        working,
        requested: working ? control.requested : undefined,
        enabled: power.entity?.enabled ?? false,
        request,
        readingBody,
      })}
      {renderControlStatus({
        canSet,
        entityId: source.entity_id,
        power,
      })}
      {renderMetricDetails({ metric, metricStatus, reading, series })}
      {detail && (
        <ReadingRows
          className="border-border mt-2 border-t"
          rows={displayed.slice(2)}
        />
      )}
    </TileFrame>
  );
};
