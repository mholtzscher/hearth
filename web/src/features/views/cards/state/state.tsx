import { useReadings } from "../../bindings/registry";
import type {
  EntitySource,
  Resolved,
  TextData,
  Words,
} from "../../bindings/types";
import { DataCard } from "../data-card";
import { Reading } from "../reading";
import { cardStatus } from "../status";

/**
 * A yes/no or text State field in words at the reading scale, such as Open,
 * Off or Color temperature. The words are the reading: no icon or colored
 * pill stands in for them.
 */
export const StateCard = ({
  label,
  resource,
}: {
  label?: string;
  resource: Resolved<TextData>;
}) => {
  const status = cardStatus(resource);
  const data = status.value === "empty" ? undefined : resource.data;
  return (
    <DataCard
      className="@container"
      classNames={{
        timeLabel: "@max-[200px]:hidden",
        footer: "@max-[200px]:gap-2",
        sourceToggle: "@max-[200px]:-me-1 @max-[200px]:px-1",
      }}
      compactTimeLabel
      title={label ?? resource.metadata?.label ?? "State"}
      status={status.entries}
      observedAt={data?.observed_at}
      arrival={
        data
          ? {
              updatedAt: data.observed_at,
              shown: data.text,
              eligible: status.glows,
            }
          : undefined
      }
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
    >
      <Reading
        value={status.value}
        className="@max-[200px]:text-reading-narrow text-balance [overflow-wrap:anywhere]"
      >
        {data ? data.text : "—"}
      </Reading>
    </DataCard>
  );
};

export const BoundState = ({
  source,
  label,
  words,
}: {
  source: EntitySource;
  label?: string;
  words?: Words;
}) => {
  const [result] = useReadings([{ source, words }]);
  const data = result.data && "text" in result.data ? result.data : undefined;
  return <StateCard label={label} resource={{ ...result, data }} />;
};
