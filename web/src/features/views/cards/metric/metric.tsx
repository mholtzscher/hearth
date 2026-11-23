import { useNumericReads } from "../../bindings/registry";
import type {
  NumericSource,
  Resolved,
  MetricData,
  SeriesData,
  SeriesWindow,
} from "../../bindings/types";
import { DataCard } from "../data-card";
import { Reading, ReadingUnit } from "../reading";
import { cardStatus } from "../status";
import { MetricNumber } from "./metric-roll";
import { RangeTrack } from "./range-track";
import { Trend, trendMethod } from "./trend";

const metricArrival = (data: MetricData | undefined, eligible: boolean) =>
  data
    ? {
        updatedAt: data.observed_at,
        shown: `${data.value.toFixed(data.format.fraction_digits)} ${data.format.unit_label}`,
        eligible,
      }
    : undefined;

export const Metric = ({
  label,
  resource,
  trend,
  range = false,
}: {
  label?: string;
  resource: Resolved<MetricData>;
  /** A history line under the reading over this window. */
  trend?: { window: SeriesWindow; resource: Resolved<SeriesData> };
  /** Show the reading against its field's declared range, when it has one. */
  range?: boolean;
}) => {
  const status = cardStatus(resource);
  const data = status.value === "empty" ? undefined : resource.data;
  const title = label ?? resource.metadata?.label ?? "Reading";
  const dimmed = status.value === "dimmed";
  return (
    <DataCard
      className="@container"
      classNames={{
        timeLabel: "@max-[200px]:hidden",
        footer: "@max-[200px]:gap-2",
        sourceToggle: "@max-[200px]:-me-1 @max-[200px]:px-1",
      }}
      title={title}
      status={status.entries}
      observedAt={data?.observed_at}
      compactTimeLabel
      arrival={metricArrival(data, status.glows)}
      sources={
        resource.metadata
          ? [
              {
                entityId: resource.metadata.id,
                label: resource.metadata.label,
                availability: resource.metadata.availability,
                enabled: resource.metadata.enabled,
              },
            ]
          : []
      }
      sourceExtra={trend ? trendMethod(trend.window) : undefined}
    >
      {/* An unavailable or disabled Entity keeps its last value at the reading scale, dimmed, so the status leads. */}
      <Reading value={status.value} className="[overflow-wrap:anywhere]">
        <MetricNumber
          reading={data}
          eligible={status.glows}
          source={resource.metadata?.id}
        />
        {data?.format.unit_label && (
          <ReadingUnit>{data.format.unit_label}</ReadingUnit>
        )}
      </Reading>
      {range && data?.range && (
        <RangeTrack data={{ ...data, range: data.range }} dimmed={dimmed} />
      )}
      {range && data && !data.range && (
        <p className="text-label text-gray-9">Range unavailable.</p>
      )}
      {trend && status.value !== "empty" && (
        <Trend
          window={trend.window}
          resource={trend.resource}
          dimmed={dimmed}
        />
      )}
    </DataCard>
  );
};

export const BoundMetric = ({
  source,
  label,
  trend,
  range,
}: {
  source: NumericSource;
  label?: string;
  trend?: SeriesWindow;
  range?: true;
}) => {
  const [current, history] = useNumericReads(
    trend ? [{ source }, { source, window: trend }] : [{ source }]
  );
  const data =
    current.data && "value" in current.data ? current.data : undefined;
  const series =
    history?.data && "buckets" in history.data ? history.data : undefined;
  return (
    <Metric
      label={label}
      resource={{ ...current, data }}
      range={range}
      trend={
        trend && history
          ? { window: trend, resource: { ...history, data: series } }
          : undefined
      }
    />
  );
};
