import {
  ArrowDownRightIcon,
  ArrowRightIcon,
  ArrowUpRightIcon,
} from "lucide-react";
import type { z } from "zod";

import type { MetricData, Resolved, SeriesData } from "../../bindings/types";
import { DataCard, StatusList } from "../data-card";
import { Trend, trendMethod } from "../metric/trend";
import { useMinuteClock } from "../observation";
import { cardStatus } from "../status";
import type { CardStatus } from "../status";
import { pressureTendency } from "../weather/formulas";
import { shownData, threeHourChange } from "../weather/readings";
import {
  useWeatherReads,
  WeatherReading,
  WeatherZones,
  weatherDetail,
  weatherTrendParts,
} from "../weather/weather-parts";
import type { pressureSchema } from "./schema";

type Props = z.infer<typeof pressureSchema>;
const icons = {
  rising: ArrowUpRightIcon,
  falling: ArrowDownRightIcon,
  steady: ArrowRightIcon,
};

/**
 * The three-hour tendency in words from canonical hPa, with the change in the
 * displayed unit. Never guessed without a reading from three hours ago. Both
 * representations come from one aggregation with shared bucket boundaries.
 */
const TendencyLine = ({
  data,
  history,
  canonical,
  status,
}: {
  data?: MetricData;
  history: Resolved<SeriesData>;
  canonical: Resolved<SeriesData>;
  status: CardStatus;
}) => {
  const clock = useMinuteClock();
  if (!data) {
    return null;
  }
  const historyData = history.data;
  const change = threeHourChange(historyData, clock);
  const hPa = threeHourChange(canonical.data, clock);
  if (
    !change ||
    !hPa ||
    !historyData ||
    change.nowStart !== hPa.nowStart ||
    change.thenStart !== hPa.thenStart
  ) {
    const loading =
      history.status === "loading" || canonical.status === "loading";
    const failed = history.status === "error" || canonical.status === "error";
    let words = "Tendency unavailable · no recent or 3\u00A0h reading";
    if (loading) {
      words = "Loading the last 3 hours…";
    } else if (failed) {
      words = "Couldn't load the last 3 hours.";
    }
    return <p className={weatherDetail}>{words}</p>;
  }
  const tendency = pressureTendency(hPa.now - hPa.before);
  const Icon = icons[tendency.direction];
  const digits = historyData.format.fraction_digits;
  const shown = Number((change.now - change.before).toFixed(digits));
  let sign = "±";
  if (shown > 0) {
    sign = "+";
  } else if (shown < 0) {
    sign = "−";
  }
  return (
    <p
      data-slot="pressure-tendency"
      className="text-label text-gray-9 data-[value=dimmed]:text-gray-7 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 tabular-nums"
      data-value={status.value}
    >
      <Icon className="size-4 shrink-0" aria-hidden="true" />
      {tendency.words}
      <span className="text-muted-foreground before:text-gray-6 before:me-1.5 before:[content:'·'_/'']">
        {sign}
        {Math.abs(shown).toFixed(digits)} {historyData.format.unit_label} in
        3&nbsp;h
      </span>
    </p>
  );
};

/** Barometric pressure with its three-hour tendency in words and an optional history line. */
export const BoundPressure = ({ label, source, trend }: Props) => {
  const reads = useWeatherReads<"pressure" | "trend" | "canonical">([
    ["pressure", { source, window: "6h" }],
    ["trend", trend && trend !== "6h" ? { source, window: trend } : undefined],
    ["canonical", { source, window: "6h", units: "canonical" }],
  ]);
  const reading = reads.current("pressure");
  const recent = reads.history("pressure");
  const canonical = reads.history("canonical");
  if (!reading || !recent || !canonical) {
    throw new Error("Pressure reading bindings are missing");
  }
  const status = cardStatus(reading);
  const data = shownData(reading);
  const history = trend === "6h" ? recent : reads.history("trend");
  const { metadata } = reading;
  return (
    <DataCard
      className="@container"
      classNames={{ body: "flex flex-col gap-1.5" }}
      title={label ?? "Pressure"}
      status={status.entries}
      observedAt={data?.observed_at}
      arrival={
        data
          ? {
              updatedAt: data.observed_at,
              shown: `${data.value.toFixed(data.format.fraction_digits)} ${data.format.unit_label}`,
              eligible: status.glows,
            }
          : undefined
      }
      sources={
        metadata
          ? [
              {
                entityId: metadata.id,
                label: metadata.label,
                availability: metadata.availability,
                enabled: metadata.enabled,
              },
            ]
          : []
      }
      sourceExtra={
        <>
          <p>
            The tendency compares the latest minute with the minute nearest
            three hours earlier, using Met Office thresholds: steady below 0.1
            hPa, slowly to 1.5 hPa, quickly above 3.5 hPa and very rapidly above
            6 hPa. With no reading within ten minutes of three hours ago, there
            is no tendency.
          </p>
          {trend && trendMethod(trend)}
        </>
      }
    >
      <WeatherZones
        aside={
          trend &&
          history &&
          status.value !== "empty" && (
            <Trend
              window={trend}
              resource={history}
              dimmed={status.value === "dimmed"}
              className="@min-[460px]:mt-0"
              bandClassName="fill-chart-5"
              classNames={{
                ...weatherTrendParts,
                mean:
                  status.value === "dimmed"
                    ? undefined
                    : "stroke-[color:color-mix(in_oklch,var(--chart-5)_50%,var(--gray-9))]",
              }}
            />
          )
        }
      >
        <WeatherReading data={data} status={status} />
        <TendencyLine
          data={data}
          history={recent}
          canonical={canonical}
          status={status}
        />
        {(recent.refreshFailed || canonical.refreshFailed) && (
          <StatusList
            entries={[
              {
                key: "refresh-failed",
                tone: "warn",
                text: "Couldn't refresh pressure history",
              },
            ]}
          />
        )}
      </WeatherZones>
    </DataCard>
  );
};
