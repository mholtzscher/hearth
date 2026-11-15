import { ApiError } from "../../../api/client";
import type { ReadSnapshot } from "../data/cache";
import { useReads } from "../data/provider";
import { readTimeline, timelineRequest } from "./changes";
import { entityMetadata, entityStateRequest, readEntity } from "./entity";
import type { FieldEntity } from "./entity";
import { readErrorMessage } from "./history";
import type { PowerSource } from "./schema";
import type { Resolved } from "./types";

export interface PowerData {
  on: boolean;
  observed_at: string;
}
export interface PowerReading {
  source: PowerSource;
  entity?: FieldEntity;
  resource: Resolved<PowerData>;
  since?: string;
  historyError?: string;
}

type PowerStatus = Resolved<PowerData>["status"];

const pendingStatus = (
  missing: boolean,
  error: Error | undefined
): PowerStatus => {
  if (missing) {
    return "missing";
  }
  if (error) {
    return "error";
  }
  return "loading";
};

const loadedStatus = (
  missing: boolean,
  data: PowerData | undefined
): PowerStatus => {
  if (missing) {
    return "missing";
  }
  if (data) {
    return "ready";
  }
  return "unobserved";
};

interface PowerHistory {
  since?: string;
  error?: string;
}

const resolvePowerHistory = (
  data: PowerData,
  history: ReadSnapshot,
  source: PowerSource
): PowerHistory => {
  try {
    const timeline = readTimeline(history.data, source, "24h");
    const last = timeline.intervals.at(-1);
    const previous = timeline.intervals.at(-2);
    if (!history.error && last?.kind === "value" && last.truthy === data.on) {
      // A seed at the window's left edge is coverage, not the time of the change.
      if (last.from === timeline.from) {
        return { since: `${data.on ? "On" : "Off"} throughout last 24h` };
      }
      // Unknown → value establishes observation, not a known switch transition.
      if (previous?.kind === "value" && previous.truthy !== last.truthy) {
        return {
          since: `Since ${new Date(last.from).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}`,
        };
      }
    }
    return {};
  } catch {
    return { error: "Couldn't read State-change history." };
  }
};

const resolvePower = (
  source: PowerSource,
  state: ReadSnapshot,
  history: ReadSnapshot
): PowerReading => {
  const missing = state.error instanceof ApiError && state.error.status === 404;
  const result: PowerReading = {
    source,
    resource: {
      status: pendingStatus(missing, state.error),
      error: readErrorMessage(state.error),
    },
  };
  if (state.data === undefined) {
    return result;
  }
  try {
    const entity = readEntity(state.data, source, "canonical");
    const data = entity.state
      ? {
          on: entity.state.value === true,
          observed_at: entity.state.observed_at,
        }
      : undefined;
    result.entity = missing ? undefined : entity;
    result.resource = {
      status: loadedStatus(missing, data),
      data,
      metadata: entityMetadata(entity, source),
      error: readErrorMessage(state.error),
      refreshFailed: !!state.error,
    };
    result.historyError = readErrorMessage(history.error);
    if (history.data !== undefined && data) {
      const resolved = resolvePowerHistory(data, history, source);
      result.historyError = resolved.error ?? result.historyError;
      if (resolved.since !== undefined) {
        result.since = resolved.since;
      }
    }
    return result;
  } catch {
    return {
      source,
      resource: {
        status: "error",
        error: "The resource returned incompatible data.",
      },
    };
  }
};

/** Current power/support stays usable when the independent change-history read fails. */
export const usePowerReadings = (sources: PowerSource[]): PowerReading[] => {
  const snapshots = useReads(
    sources.flatMap((source) => [
      entityStateRequest(source),
      timelineRequest(source, "24h"),
    ])
  );
  return sources.map((source, index) =>
    resolvePower(source, snapshots[index * 2], snapshots[index * 2 + 1])
  );
};
