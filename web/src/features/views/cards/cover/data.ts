import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "../../../../api/client";
import { entityMetadata, entityStateRequest, readEntity, type FieldEntity } from "../../bindings/entity";
import { readErrorMessage } from "../../bindings/history";
import type { Resolved } from "../../bindings/types";
import { supportsOperation } from "../../controls/support";
import type { ControlState } from "../../controls/queue";
import { useControl, useFastRefresh, useForgetControl, useRead } from "../../data/provider";
import { cardStatus, type CardStatus } from "../status";
import type { SourceItem } from "../CardFrame";
import { confirmsOpen, type CoverProps } from "./schema";
import { coverFailures, pendingWords, type CoverMotion } from "./words";

export type CoverData = { position: number; motion: CoverMotion; observed_at: string };
/** One line under the controls. short is the tile's one-line wording; text is the full sentence. */
export type CoverLine = { tone: "info" | "neutral" | "warn" | "error"; text: string; short: string; alert?: boolean };
/** Who asked for the second press: the card's Open button or the tile's whole-card press. */
export type ConfirmFrom = "button" | "tile";

/** How long a first press waits for the second before opening is no longer armed. */
export const CONFIRM_MS = 4_000;
/** How long after Stop is sent a still-moving cover is worth a warning. */
const STOP_GRACE_MS = 3_000;

const working = (state: ControlState) => state.phase === "pending" || state.phase === "confirming";

function requestedPosition(state: ControlState): number | undefined {
  if (!working(state) || typeof state.requested !== "object" || state.requested === null) return undefined;
  const position = (state.requested as { position?: unknown }).position;
  return typeof position === "number" ? position : undefined;
}

const positionSource = (props: CoverProps) => ({ ...props.source, field: "/position" });

/** The whole cover State is read once; its position field passes the shared Entity check. */
function resolveCover(props: CoverProps, snapshot: ReturnType<typeof useRead>): { entity?: FieldEntity; resource: Resolved<CoverData> } {
  const missing = snapshot.error instanceof ApiError && snapshot.error.status === 404;
  const empty: Resolved<CoverData> = { status: missing ? "missing" : snapshot.error ? "error" : "loading", error: readErrorMessage(snapshot.error) };
  if (snapshot.data === undefined || missing) return { resource: empty };
  try {
    const entity = readEntity(snapshot.data, positionSource(props));
    const value = entity.state?.value as { position: number; motion: CoverMotion } | undefined;
    const data = entity.state && value ? { position: Math.round(value.position), motion: value.motion, observed_at: entity.state.observed_at } : undefined;
    const metadata = entityMetadata(entity, props.source);
    return { entity, resource: { status: data ? "ready" : "unobserved", data, metadata, error: readErrorMessage(snapshot.error), refreshFailed: !!snapshot.error } };
  } catch {
    return { resource: { status: "error", error: "The resource returned incompatible data." } };
  }
}

/**
 * Reads one cover and drives its controls. Commands and their outcomes belong
 * to the Entity and are shared by every card showing it; a confirmation prompt
 * belongs to the card that asked and is scoped to its current Entity.
 */
export function useCover(props: CoverProps) {
  // The read validates one field; the whole State, position and motion, comes back with it.
  const request = entityStateRequest(positionSource(props), "canonical");
  const { entity, resource } = resolveCover(props, useRead(request));
  const status: CardStatus = cardStatus(resource);
  const data = status.value === "empty" ? undefined : resource.data;
  const entityId = props.source.entity_id;
  const setTarget = { entityId, operation: "set" } as const, stopTarget = { entityId, operation: "stop" } as const;
  const [setState, requestSet] = useControl(setTarget, "replace");
  const [stopState, requestStop] = useControl(stopTarget, "replace");
  const forget = useForgetControl();
  const canSet = !!entity && supportsOperation(entity, "set");
  const canStop = !!entity && supportsOperation(entity, "stop");
  const enabled = entity?.enabled ?? false;
  const moving = !!data && data.motion !== "idle";
  const target = requestedPosition(setState);

  const [armed, setConfirm] = useState<{ entityId: string; target: number; from: ConfirmFrom }>();
  const confirm = armed?.entityId === entityId ? armed : undefined;
  const confirmTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const disarm = useCallback(() => { clearTimeout(confirmTimer.current); setConfirm(undefined); }, []);
  useEffect(() => () => clearTimeout(confirmTimer.current), []);

  // Dispatch is terminal evidence, independent of the State refresh that follows.
  const stopSentAt = stopState.phase === "dispatched" ? stopState.at : undefined;
  const [, tick] = useState(0);
  useEffect(() => {
    if (stopSentAt === undefined || !moving) return;
    const wait = stopSentAt + STOP_GRACE_MS - Date.now();
    if (wait <= 0) return;
    const timer = setTimeout(() => tick(value => value + 1), wait);
    return () => clearTimeout(timer);
  }, [stopSentAt, moving]);

  useFastRefresh(request, working(setState) || working(stopState) || moving);

  /** Open asks for a second press even at 100 %: an estimated endpoint may be wrong. Close never asks. */
  const move = useCallback((position: number, from: ConfirmFrom) => {
    const opens = position > 0 && (position === 100 || data === undefined || position > data.position);
    if (confirmsOpen(props) && opens && !(confirm && confirm.target === position && confirm.from === from)) {
      clearTimeout(confirmTimer.current);
      setConfirm({ entityId, target: position, from });
      confirmTimer.current = setTimeout(() => setConfirm(undefined), CONFIRM_MS);
      return;
    }
    disarm();
    forget(stopTarget);
    requestSet({ position });
  }, [data, props, confirm, disarm, requestSet, forget, entityId]);

  /** Stop is sent at once, and the card stops showing the earlier position Command, whose later timeout would mislead. */
  const stop = useCallback(() => {
    disarm();
    forget(setTarget);
    requestStop({});
  }, [disarm, forget, requestStop, entityId]);

  /** The reserved line: a confirmation prompt, then pending requests, then failures, then the Stop note. */
  function line(from: ConfirmFrom): CoverLine | undefined {
    if (confirm?.from === from) return from === "tile"
      ? { tone: "neutral", text: "Press again within 4 s to open.", short: "Press again." }
      : { tone: "neutral", text: "Press Confirm within 4 s to open.", short: "Press Confirm." };
    if (target !== undefined) return { tone: "info", text: pendingWords(target), short: pendingWords(target) };
    if (working(stopState)) return { tone: "info", text: "Sending Stop…", short: "Sending Stop…" };
    if (setState.phase === "failed") return { tone: "error", alert: true, ...coverFailures[setState.failure.kind] };
    if (stopState.phase === "failed") {
      const failure = coverFailures[stopState.failure.kind];
      return {
        tone: "error", alert: true, short: "Stop failed.",
        text: stopState.failure.kind === "unconfirmed"
          ? "Stop wasn't confirmed as sent. It may still apply."
          : `Stop: ${failure.text}`,
      };
    }
    if (stopSentAt !== undefined) {
      if (moving && Date.now() - stopSentAt >= STOP_GRACE_MS) return { tone: "warn", text: `Stop sent, but the cover is still ${data!.motion}.`, short: `Still ${data!.motion}.` };
      return { tone: "neutral", text: "Stop sent.", short: "Stop sent." };
    }
    return undefined;
  }

  const title = props.label ?? resource.metadata?.label ?? "Cover";
  const sources: SourceItem[] = [{ entityId, label: resource.metadata?.label ?? title, availability: resource.metadata?.availability, enabled: resource.metadata?.enabled }];
  return { entity, resource, status, data, title, sources, canSet, canStop, enabled, moving, target, confirm, setPending: working(setState), move, stop, line };
}
