import type { z } from "zod";

import type { MetricData, Resolved } from "../../bindings/types";
import { DataCard } from "../data-card";
import { Trend, trendMethod } from "../metric/trend";
import {
  listArrivals,
  oldestObservation,
  rowSources,
  rowStates,
} from "../reading-list/reading-rows";
import { cardStatus } from "../status";
import type { CardStatus } from "../status";
import { rainWord } from "../weather/formulas";
import {
  canonicalValue,
  derivedTreatment,
  formatReading,
  shownData,
} from "../weather/readings";
import {
  SlotStatus,
  slotRows,
  useWeatherReads,
  WeatherReading,
  WeatherZones,
  weatherConditions,
  weatherDetail,
  weatherTrendParts,
} from "../weather/weather-parts";
import type { WeatherRead } from "../weather/weather-parts";
import type { RainPeriod, rainSchema } from "./schema";
import { rainPeriods } from "./schema";

type Props = z.infer<typeof rainSchema>;
type Slot = "rate" | "rateCanonical" | "storm" | RainPeriod;
const periodNames: Record<RainPeriod, string> = {
  day: "Today",
  week: "This week",
  month: "This month",
  year: "This year",
};

interface RainTotal {
  period: RainPeriod;
  resource: Resolved<MetricData>;
  data: MetricData;
}

/** The words beside a leading storm total, following the shown rate's own treatment. */
const rateSummary = (
  data: MetricData | undefined,
  mmPerHour: number | undefined,
  word: string | undefined
) => {
  if (!data) {
    return "";
  }
  if (mmPerHour === undefined) {
    return ` · Rate ${formatReading(data)}`;
  }
  if (word) {
    return ` · ${word} now, ${formatReading(data)}`;
  }
  return " · Stopped for now";
};

/** One status line for totals that share their statuses, such as a station that stopped reporting; otherwise one per total. */
const TotalsStatus = ({
  totals,
}: {
  totals: { period: RainPeriod; resource: Resolved<MetricData> }[];
}) => {
  const flagged = totals
    .map((total) => ({
      ...total,
      keys: cardStatus(total.resource)
        .entries.map((entry) => entry.key)
        .join(","),
    }))
    .filter((total) => total.keys);
  if (
    flagged.length > 1 &&
    flagged.length === totals.length &&
    flagged.every((total) => total.keys === flagged[0].keys)
  ) {
    return <SlotStatus name="Rain totals" resource={flagged[0].resource} />;
  }
  return (
    <>
      {flagged.map((total) => (
        <SlotStatus
          key={total.period}
          name={periodNames[total.period]}
          resource={total.resource}
        />
      ))}
    </>
  );
};

/** The leading reading: a storm total while it is above zero, otherwise the rate or Dry. */
const RainPrimary = ({
  stormLeads,
  stormData,
  status,
  stormReading,
  rateReading,
  rateData,
  rateStatus,
  mmPerHour,
  word,
}: {
  stormLeads: boolean;
  stormData?: MetricData;
  status: CardStatus;
  stormReading?: Resolved<MetricData>;
  rateReading: Resolved<MetricData>;
  rateData?: MetricData;
  rateStatus: CardStatus;
  mmPerHour?: number;
  word?: string;
}) => {
  if (stormLeads) {
    return (
      <>
        <WeatherReading data={stormData} status={status} />
        <p
          data-slot="weather-conditions"
          className={weatherConditions}
          data-value={derivedTreatment(stormReading, rateReading)}
        >
          This storm
          {rateSummary(rateData, mmPerHour, word)}
        </p>
      </>
    );
  }
  if (rateData && mmPerHour === 0) {
    return (
      <>
        <WeatherReading status={rateStatus} words="Dry" />
        <p className={weatherDetail} data-value={rateStatus.value}>
          Rate {formatReading(rateData)}
        </p>
      </>
    );
  }
  return (
    <>
      <WeatherReading data={rateData} status={rateStatus} />
      {word && (
        <p
          data-slot="weather-conditions"
          className={weatherConditions}
          data-value={rateStatus.value}
        >
          {word}
        </p>
      )}
    </>
  );
};

/** The station's own period counters in one line, in their shared unit when all agree. */
const RainTotals = ({
  totals,
  sharedUnit,
}: {
  totals: RainTotal[];
  sharedUnit: boolean;
}) => {
  if (totals.length === 0) {
    return null;
  }
  return (
    <p data-slot="rain-totals" className={`mt-1 ${weatherDetail}`}>
      {totals.map((total, index) => (
        <span
          key={total.period}
          data-value={cardStatus(total.resource).value}
          className="data-[value=dimmed]:text-gray-7"
        >
          {index > 0 && " · "}
          {periodNames[total.period]}{" "}
          {sharedUnit
            ? total.data.value.toFixed(total.data.format.fraction_digits)
            : formatReading(total.data)}
        </span>
      ))}
      {sharedUnit && ` ${totals[0].data.format.unit_label}`}
    </p>
  );
};

/**
 * While the station's storm total is above zero it leads, even after the rain
 * stops; otherwise the rain rate does, or Dry at zero. The station's own
 * counters follow in one line, never sums of the rate.
 */
export const BoundRain = ({ label, rate, storm, totals, trend }: Props) => {
  const reads = useWeatherReads<Slot>([
    ["rate", { source: rate, window: trend, canonicalUnit: "mm/h" }],
    [
      "rateCanonical",
      { source: rate, units: "canonical", canonicalUnit: "mm/h" },
    ],
    ["storm", storm && { source: storm, canonicalUnit: "mm" }],
    ...rainPeriods.map((period): [Slot, WeatherRead | undefined] => [
      period,
      totals?.[period] && { source: totals[period], canonicalUnit: "mm" },
    ]),
  ]);
  const rateReading = reads.current("rate");
  if (!rateReading) {
    throw new Error("Rain rate reading is missing");
  }
  const rateStatus = cardStatus(rateReading);
  const rateData = shownData(rateReading);
  const mmPerHour = canonicalValue(rateReading, reads.current("rateCanonical"));
  const stormReading = reads.current("storm");
  const stormData = shownData(stormReading);
  const stormLeads = stormData !== undefined && stormData.value > 0;
  // The header speaks for the leading reading; the other keeps its own status line.
  let status = rateStatus;
  if (stormLeads && stormReading) {
    status = cardStatus(stormReading);
  }
  const shownTotals = rainPeriods.flatMap((period) => {
    const resource = reads.current(period);
    return resource ? [{ period, resource, data: shownData(resource) }] : [];
  });
  const totalsLine = shownTotals.flatMap((total): RainTotal[] =>
    total.data ? [{ ...total, data: total.data }] : []
  );
  // Core can project counters differently, such as classified inches beside an unclassified mm sensor.
  const sharedUnit = totalsLine.every(
    (total) =>
      total.data.format.unit_label === totalsLine[0].data.format.unit_label
  );
  const all = slotRows([
    ["rate", "Rain rate", rateReading],
    ["storm", "This storm", stormReading],
    ...shownTotals.map(
      (total) =>
        [total.period, periodNames[total.period], total.resource] as [
          string,
          string,
          Resolved<MetricData>,
        ]
    ),
  ]);
  const displayed = rowStates(all);
  const history = reads.history("rate");
  const word = mmPerHour === undefined ? undefined : rainWord(mmPerHour);
  return (
    <DataCard
      className="@container"
      classNames={{ body: "flex flex-col gap-1.5" }}
      title={label ?? "Rain"}
      status={status.entries}
      observedAt={oldestObservation(displayed)}
      observedLabel={all.length > 1 ? "Oldest reading" : "Observed"}
      arrivals={listArrivals(displayed)}
      sources={rowSources(all)}
      sourceExtra={
        <>
          <p>
            Rain or Dry comes from the rain rate: light rain is below 2.5 mm/h
            and heavy rain above 7.6 mm/h. The storm total leads while it is
            above zero.
          </p>
          <p>
            Totals are the station&apos;s own counters, which it resets for each
            storm, day, week, month and year.
          </p>
          {trend && trendMethod(trend)}
        </>
      }
    >
      <WeatherZones
        aside={
          trend &&
          history &&
          rateStatus.value !== "empty" && (
            <Trend
              window={trend}
              resource={history}
              dimmed={rateStatus.value === "dimmed"}
              className="@min-[460px]:mt-0"
              bandClassName="fill-chart-4"
              classNames={{
                ...weatherTrendParts,
                mean:
                  rateStatus.value === "dimmed"
                    ? undefined
                    : "stroke-[color:color-mix(in_oklch,var(--chart-4)_50%,var(--gray-9))]",
              }}
            />
          )
        }
      >
        <RainPrimary
          stormLeads={stormLeads}
          stormData={stormData}
          status={status}
          stormReading={stormReading}
          rateReading={rateReading}
          rateData={rateData}
          rateStatus={rateStatus}
          mmPerHour={mmPerHour}
          word={word}
        />
      </WeatherZones>
      <RainTotals totals={totalsLine} sharedUnit={sharedUnit} />
      <SlotStatus
        name={stormLeads ? "Rain rate" : "This storm"}
        resource={stormLeads ? rateReading : stormReading}
      />
      <TotalsStatus totals={shownTotals} />
    </DataCard>
  );
};
