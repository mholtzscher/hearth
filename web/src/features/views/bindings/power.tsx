import { ApiError } from "../../../api/client";
import { useReads } from "../data/provider";
import type { ReadSnapshot } from "../data/cache";
import { entityMetadata, entityStateRequest, readEntity, type FieldEntity } from "./entity";
import { readTimeline, timelineRequest } from "./changes";
import { readErrorMessage } from "./history";
import type { PowerSource } from "./schema";
import type { Resolved } from "./types";

export type PowerData = { on: boolean; observed_at: string };
export type PowerReading = {
  source: PowerSource;
  entity?: FieldEntity;
  resource: Resolved<PowerData>;
  since?: string;
  historyError?: string;
};

/** Current power/support stays usable when the independent change-history read fails. */
export function usePowerReadings(sources: PowerSource[]): PowerReading[] {
  const snapshots = useReads(sources.flatMap(source => [entityStateRequest(source), timelineRequest(source, "24h")]));
  return sources.map((source, index) => resolvePower(source, snapshots[index * 2], snapshots[index * 2 + 1]));
}

function resolvePower(source: PowerSource, state: ReadSnapshot, history: ReadSnapshot): PowerReading {
  const missing = state.error instanceof ApiError && state.error.status === 404;
  const result: PowerReading = { source, resource: { status: missing ? "missing" : state.error ? "error" : "loading", error: readErrorMessage(state.error) } };
  if (state.data === undefined) return result;
  try {
    const entity = readEntity(state.data, source);
    const data = entity.state ? { on: entity.state.value === true, observed_at: entity.state.observed_at } : undefined;
    result.entity = missing ? undefined : entity;
    result.resource = { status: missing ? "missing" : data ? "ready" : "unobserved", data, metadata: entityMetadata(entity, source), error: readErrorMessage(state.error), refreshFailed: !!state.error };
    result.historyError = readErrorMessage(history.error);
    if (history.data !== undefined && data) {
      try {
        const timeline = readTimeline(history.data, source, "24h");
        const last = timeline.intervals[timeline.intervals.length - 1];
        const previous = timeline.intervals[timeline.intervals.length - 2];
        if (!history.error && last?.kind === "value" && last.truthy === data.on) {
          // A seed at the window's left edge is coverage, not the time of the change.
          if (last.from === timeline.from) result.since = `${data.on ? "On" : "Off"} throughout last 24h`;
          // Unknown → value establishes observation, not a known switch transition.
          else if (previous?.kind === "value" && previous.truthy !== last.truthy) result.since = `Since ${new Date(last.from).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}`;
        }
      } catch { result.historyError = "Couldn't read State-change history."; }
    }
    return result;
  } catch {
    return { source, resource: { status: "error", error: "The resource returned incompatible data." } };
  }
}
