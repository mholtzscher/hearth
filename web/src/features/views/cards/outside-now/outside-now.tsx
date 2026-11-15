import type { z } from "zod";

import type { MetricData, Resolved, SeriesData } from "../../bindings/types";
import { DataCard, StatusList } from "../data-card";
import {
  listArrivals,
  oldestObservation,
  ReadingRows,
  rowSources,
  rowStates,
} from "../reading-list/reading-rows";
import type { ReadingRow } from "../reading-list/reading-rows";
import { cardStatus } from "../status";
import {
  beaufort,
  compassPoint,
  rainWord,
  windWord,
} from "../weather/formulas";
import {
  canonicalValue,
  derivedTreatment,
  extremes,
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
} from "../weather/weather-parts";
import type { WeatherRead } from "../weather/weather-parts";
import type { outsideNowSchema } from "./schema";

type Props = z.infer<typeof outsideNowSchema>;
type Slot =
  | "temperature"
  | "humidity"
  | "wind"
  | "windCanonical"
  | "direction"
  | "rate"
  | "rateCanonical"
  | "today";

/** The 24-hour high and low line, or the history's own loading and failure words. */
const rangeWords = (
  range: ReturnType<typeof extremes>,
  history: Resolved<SeriesData>,
  digits: number
) => {
  if (range) {
    return `${history.refreshFailed ? "Last loaded 24 h:" : "24 h"} high ${range.high.toFixed(digits)}, low ${range.low.toFixed(digits)} ${history.data?.format.unit_label ?? ""}`;
  }
  if (history.status === "loading") {
    return "Loading the last 24 hours…";
  }
  if (history.status === "error") {
    return "Couldn't load the last 24 hours.";
  }
  return "No history in the last 24 h.";
};

const OutsideConditions = ({
  data,
  status,
  words,
  wordTreatment,
  range,
  history,
  digits,
}: {
  data?: MetricData;
  status: ReturnType<typeof cardStatus>;
  words: (string | undefined)[];
  wordTreatment: ReturnType<typeof derivedTreatment>;
  range: ReturnType<typeof extremes>;
  history: Resolved<SeriesData>;
  digits: number;
}) => (
  <>
    <WeatherReading data={data} status={status} />
    {words.length > 0 && (
      <p
        data-slot="weather-conditions"
        className={weatherConditions}
        data-value={wordTreatment}
      >
        {words.join(" · ")}
      </p>
    )}
    {data && (
      <p className={weatherDetail}>{rangeWords(range, history, digits)}</p>
    )}
    {history.refreshFailed && (
      <StatusList
        entries={[
          {
            key: "refresh-failed",
            tone: "warn",
            text: "Couldn't refresh temperature history",
          },
        ]}
      />
    )}
  </>
);

const OutsideSource = ({ history }: { history: Resolved<SeriesData> }) => {
  const historyData = history.data;
  return (
    <>
      <p>
        Conditions come from measurements only: rain words from the rain rate,
        wind words from the Beaufort scale.
      </p>
      <p>
        High and low use a 24-hour history interval, not since midnight, from
        5-minute minimums and maximums. If a refresh fails, the card keeps the
        last loaded interval.
      </p>
      {historyData && (
        <dl className="m-0 grid gap-4 tabular-nums">
          {[
            { caption: "Temperature history from", at: historyData.from },
            { caption: "Temperature history to", at: historyData.to },
          ].map(({ caption, at }) => (
            <div className="grid gap-[3px]" key={caption}>
              <dt className="text-label text-gray-8">{caption}</dt>
              <dd className="m-0">
                <time dateTime={at}>
                  {new Date(at).toLocaleString([], {
                    year: "numeric",
                    month: "short",
                    day: "numeric",
                    hour: "numeric",
                    minute: "2-digit",
                    second: "2-digit",
                    timeZoneName: "short",
                  })}
                </time>
              </dd>
            </div>
          ))}
        </dl>
      )}
    </>
  );
};

/** Rows that still carry a measurement words do not already show. */
const isOutsideRow = (
  row: ReadingRow,
  rateRow: boolean,
  directionRow: boolean
) =>
  row.key === "humidity" ||
  row.key === "wind" ||
  row.key === "today" ||
  (row.key === "rate" && rateRow) ||
  (row.key === "direction" && directionRow);

const withWindTreatment = (
  row: ReturnType<typeof rowStates>[number],
  from: ReturnType<typeof compassPoint> | undefined,
  wind: Resolved<MetricData> | undefined,
  direction: Resolved<MetricData> | undefined
) =>
  row.key === "wind" && from
    ? {
        ...row,
        nameTreatment: derivedTreatment(wind, direction),
      }
    : row;

const outsideRows = (
  displayed: ReturnType<typeof rowStates>,
  from: ReturnType<typeof compassPoint> | undefined,
  wind: Resolved<MetricData> | undefined,
  direction: Resolved<MetricData> | undefined,
  rateRow: boolean,
  directionRow: boolean
): ReturnType<typeof rowStates> =>
  displayed
    .filter((row) => isOutsideRow(row, rateRow, directionRow))
    .map((row) => withWindTreatment(row, from, wind, direction));

/** Every slot the card reads, including the optional sources its schema leaves unbound. */
const outsideSlots = ({
  temperature,
  humidity,
  wind,
  wind_direction,
  rain_rate,
  rain_today,
}: Props): [Slot, WeatherRead | undefined][] => [
  ["temperature", { source: temperature, window: "24h" }],
  ["humidity", humidity && { source: humidity }],
  ["wind", wind && { source: wind }],
  ["windCanonical", wind && { source: wind, units: "canonical" }],
  [
    "direction",
    wind_direction && { source: wind_direction, canonicalUnit: "deg" },
  ],
  ["rate", rain_rate && { source: rain_rate, canonicalUnit: "mm/h" }],
  [
    "rateCanonical",
    rain_rate && {
      source: rain_rate,
      units: "canonical",
      canonicalUnit: "mm/h",
    },
  ],
  ["today", rain_today && { source: rain_today, canonicalUnit: "mm" }],
];

/** Canonical speed and rate, the words they produce, and the compass bearing they justify. */
const outsideConditions = (
  rainReading: Resolved<MetricData> | undefined,
  rateCanonical: Resolved<MetricData> | undefined,
  windReading: Resolved<MetricData> | undefined,
  windCanonical: Resolved<MetricData> | undefined,
  directionReading: Resolved<MetricData> | undefined
) => {
  const speed = canonicalValue(windReading, windCanonical);
  const rate = canonicalValue(rainReading, rateCanonical);
  const bearing = shownData(directionReading)?.value;
  const rain = rate === undefined ? undefined : rainWord(rate);
  const windy = speed === undefined ? undefined : windWord(speed);
  const words = [rain, windy].filter(Boolean);
  // Words dim with the readings that produced them, not with a dry rate that added none.
  const wordTreatment = derivedTreatment(
    rain ? rainReading : undefined,
    windy ? windReading : undefined
  );
  const from =
    bearing !== undefined && speed !== undefined && beaufort(speed) > 0
      ? compassPoint(bearing)
      : undefined;
  return { speed, rate, words, wordTreatment, from };
};

/**
 * Outdoor temperature with conditions in words, the last 24 hours' extremes,
 * and the other readings as rows.
 */
export const BoundOutsideNow = (props: Props) => {
  const { label } = props;
  const reads = useWeatherReads<Slot>(outsideSlots(props));
  const reading = reads.current("temperature");
  if (!reading) {
    throw new Error("Outside temperature reading is missing");
  }
  const status = cardStatus(reading);
  const data = shownData(reading);
  const { speed, rate, words, wordTreatment, from } = outsideConditions(
    reads.current("rate"),
    reads.current("rateCanonical"),
    reads.current("wind"),
    reads.current("windCanonical"),
    reads.current("direction")
  );
  const history = reads.history("temperature");
  if (!history) {
    throw new Error("Outside temperature history is missing");
  }
  const range = extremes(history.data);
  const all = slotRows([
    ["temperature", "Temperature", reading],
    ["humidity", "Humidity", reads.current("humidity")],
    ["wind", from ? `Wind from ${from.short}` : "Wind", reads.current("wind")],
    ["direction", "Wind direction", reads.current("direction")],
    ["rate", "Rain rate", reads.current("rate")],
    ["today", "Rain today", reads.current("today")],
  ]);
  const displayed = rowStates(all);
  // Rate and direction normally speak through words. Without the canonical
  // reading those words need, they show as rows so no measurement disappears.
  const rateRow = rate === undefined;
  const directionRow = speed === undefined;
  const rows = outsideRows(
    displayed,
    from,
    reads.current("wind"),
    reads.current("direction"),
    rateRow,
    directionRow
  );
  const title = label ?? "Outside";
  const digits = history.data?.format.fraction_digits ?? 1;
  return (
    <DataCard
      className="@container"
      classNames={{ body: "flex flex-col gap-1.5" }}
      title={title}
      status={status.entries}
      observedAt={oldestObservation(displayed)}
      observedLabel={all.length > 1 ? "Oldest reading" : "Observed"}
      arrivals={listArrivals(displayed)}
      sources={rowSources(all)}
      sourceExtra={<OutsideSource history={history} />}
    >
      <WeatherZones
        aside={
          rows.length > 0 && (
            <div
              data-slot="weather-rows"
              className="border-border mt-2.5 border-t @min-[460px]:mt-0 @min-[460px]:border-t-0"
            >
              <ReadingRows className="mt-0" rows={rows} />
            </div>
          )
        }
      >
        <OutsideConditions
          data={data}
          status={status}
          words={words}
          wordTreatment={wordTreatment}
          range={range}
          history={history}
          digits={digits}
        />
      </WeatherZones>
      {!directionRow && (
        <SlotStatus
          name="Wind direction"
          resource={reads.current("direction")}
        />
      )}
      {!rateRow && (
        <SlotStatus name="Rain rate" resource={reads.current("rate")} />
      )}
    </DataCard>
  );
};
