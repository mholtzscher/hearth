import { useReadings } from "../../bindings/registry";
import type { EntitySource, Resolved, TextData, Words } from "../../bindings/types";
import { CardFrame } from "../CardFrame";
import { cardStatus } from "../status";

/**
 * A yes/no or text State field in words at the reading scale, such as Open,
 * Off or Color temperature. The words are the reading: no icon or colored
 * pill stands in for them.
 */
export function StateCard({ label, resource }: { label?: string; resource: Resolved<TextData> }) {
  const status = cardStatus(resource);
  const data = status.value === "empty" ? undefined : resource.data;
  return <CardFrame className="view-state" compactTimeLabel title={label ?? resource.metadata?.label ?? "State"} status={status.entries} observedAt={data?.observed_at}
    arrival={data ? { updatedAt: data.observed_at, shown: data.text, eligible: status.glows } : undefined}
    sources={resource.metadata ? [{ entityId: resource.metadata.id, label: resource.metadata.label, availability: resource.metadata.availability, enabled: resource.metadata.enabled }] : []}>
    <p className="view-metric-value view-state-value" data-value={status.value}>{data ? data.text : "—"}</p>
  </CardFrame>;
}

export function BoundState({ source, label, words }: { source: EntitySource; label?: string; words?: Words }) {
  const [result] = useReadings([{ source, words }]);
  const data = result.data && "text" in result.data ? result.data : undefined;
  return <StateCard label={label} resource={{ ...result, data }} />;
}
