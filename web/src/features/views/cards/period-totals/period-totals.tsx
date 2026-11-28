import type { z } from "zod";

import { durationWords } from "../../bindings/changes";
import { useHourTotals, useWeekPattern } from "../../bindings/registry";
import type { Resolved, PeriodTotalsData } from "../../bindings/types";
import { DataCard } from "../data-card";
import { cardStatus } from "../status";
import { Columns } from "./columns";
import { Ledger } from "./ledger";
import { Plot, periodSentence } from "./parts";
import { compactDuration, dayTotals, summarize } from "./periods";
import type { PeriodSummary } from "./periods";
import type { periodTotalsSchema, PeriodTotalsPeriod } from "./schema";

type Props = z.infer<typeof periodTotalsSchema>;

const methodWords: Record<PeriodTotalsPeriod, string> = {
  day: "Each bar is the time the field was true between local midnights, summed from hourly totals. Today counts up to now. A day with no history has no total, which is not zero.",
  hour: "Each bar is the time the field was true in one local hour, over the last 24 hours. The current hour counts up to now. An hour with no history has no total, which is not zero.",
};

const answer = (
  kind: PeriodTotalsPeriod,
  data: PeriodTotalsData,
  trueWord: string
): { value: string; text: string } => {
  const word = trueWord.toLowerCase();
  const summary = summarize(data.periods);
  if (kind === "day") {
    const average =
      summary.average === null
        ? ""
        : ` · daily average ${compactDuration(summary.average)}`;
    return summary.current
      ? {
          value: compactDuration(summary.current.seconds),
          text: `${word} today so far${average}`,
        }
      : {
          value: "—",
          text: `no history today${average}`,
        };
  }
  const total = data.periods.reduce(
    (sum, period) => sum + (period.seconds ?? 0),
    0
  );
  if (!summary.longest) {
    return { value: "—", text: "no history in the last 24 hours" };
  }
  const hour = new Date(summary.longest.start).toLocaleTimeString([], {
    hour: "numeric",
  });
  return {
    value: compactDuration(total),
    text: `${word} in the last 24 hours · longest hour ${hour}, ${compactDuration(summary.longest.seconds ?? 0)}`,
  };
};

const summaryOf = (
  data: PeriodTotalsData,
  summary: PeriodSummary,
  kind: PeriodTotalsPeriod,
  trueWord: string
): string[] => {
  const sentences = data.periods.map((period) =>
    periodSentence(period, kind, trueWord)
  );
  if (kind === "day" && summary.average !== null) {
    const days = summary.averageCount === 1 ? "day" : "days";
    sentences.push(
      `Daily average ${durationWords(summary.average)} over ${summary.averageCount} complete ${days}.`
    );
  }
  return sentences;
};

const Body = ({
  label,
  kind,
  style,
  trueWord,
  data,
}: {
  label: string;
  kind: PeriodTotalsPeriod;
  style: Props["style"];
  trueWord: string;
  data: PeriodTotalsData;
}) => {
  const summary = summarize(data.periods);
  const lead = answer(kind, data, trueWord);
  return (
    <>
      <p className="text-label text-muted-foreground mb-4 flex flex-wrap items-baseline gap-x-2">
        <span className="text-compact-reading text-foreground">
          {lead.value}
        </span>
        <span>{lead.text}</span>
      </p>
      <Plot
        label={`${label}, ${kind === "day" ? "last 7 days" : "last 24 hours"}`}
        summary={summaryOf(data, summary, kind, trueWord)}
        className={
          style === "ledger"
            ? "text-label grid grid-cols-[2.75rem_minmax(0,1fr)_auto] items-center gap-x-3"
            : "text-label"
        }
      >
        {style === "ledger" ? (
          <Ledger kind={kind} periods={data.periods} summary={summary} />
        ) : (
          <Columns kind={kind} periods={data.periods} summary={summary} />
        )}
      </Plot>
    </>
  );
};

export const PeriodTotals = ({
  label,
  entityId,
  kind,
  style,
  trueWord,
  resource,
}: {
  label: string;
  entityId: string;
  kind: PeriodTotalsPeriod;
  style: Props["style"];
  trueWord: string;
  resource: Resolved<PeriodTotalsData>;
}) => {
  const status = cardStatus(resource);
  const { data, metadata } = resource;
  const signature =
    data &&
    JSON.stringify(
      data.periods.map((period) =>
        period.seconds === null ? null : durationWords(period.seconds)
      )
    );
  return (
    <DataCard
      className="@container"
      title={label}
      unit={kind === "day" ? "Last 7 days" : "Last 24 hours"}
      status={status.entries}
      sources={[
        {
          entityId,
          label: metadata?.label ?? label,
          enabled: metadata?.enabled,
          availability: metadata?.availability,
        },
      ]}
      arrivals={
        data && signature
          ? [
              {
                key: entityId,
                updatedAt: new Date(data.to).toISOString(),
                shown: signature,
                eligible: status.glows,
              },
            ]
          : []
      }
      sourceExtra={
        <>
          <p>{methodWords[kind]}</p>
          {data && (
            <p>
              {new Date(data.from).toLocaleString()} to{" "}
              {new Date(data.to).toLocaleString()}
            </p>
          )}
        </>
      }
    >
      {data ? (
        <Body
          label={label}
          kind={kind}
          style={style}
          trueWord={trueWord}
          data={data}
        />
      ) : (
        <p className="text-label text-muted-foreground py-6">
          Historical readings will appear here.
        </p>
      )}
    </DataCard>
  );
};

const wordsOf = ({ source, words }: Props): string =>
  words?.true ?? (source.field === "/active" ? "Active" : "On");

const DayTotals = (props: Props) => {
  const resource = useWeekPattern({
    source: props.source,
    words: props.words,
  });
  const week = resource.data?.week;
  return (
    <PeriodTotals
      label={props.label ?? resource.metadata?.label ?? "State"}
      entityId={props.source.entity_id}
      kind="day"
      style={props.style}
      trueWord={wordsOf(props)}
      resource={{ ...resource, data: week ? dayTotals(week) : undefined }}
    />
  );
};

const HourTotals = (props: Props) => {
  const resource = useHourTotals({
    source: props.source,
    words: props.words,
  });
  return (
    <PeriodTotals
      label={props.label ?? resource.metadata?.label ?? "State"}
      entityId={props.source.entity_id}
      kind="hour"
      style={props.style}
      trueWord={wordsOf(props)}
      resource={{ ...resource, data: resource.data?.hours }}
    />
  );
};

export const BoundPeriodTotals = (props: Props) =>
  props.period === "day" ? <DayTotals {...props} /> : <HourTotals {...props} />;
