import { useEffect, useLayoutEffect, useRef, useState } from "react";

import type { FieldEntity } from "../bindings/entity";
import { useControl } from "../data/provider";
import { KEYBOARD_SETTLE_MS } from "./brightness-slider";
import { useRefusal } from "./command-motion";
import type { ControlValue } from "./queue";

/** Local drag/keyboard preview over the shared, durable Command control state. */
export const useColorSetting = <T extends ControlValue>({
  entity,
  read,
  fallback,
}: {
  entity: FieldEntity;
  read: (value: unknown) => T | undefined;
  fallback: T;
}) => {
  const [state, request] = useControl({
    entityId: entity.id,
    operation: "set",
  });
  const [draft, setDraft] = useState<T>();
  const [wasEnabled, setWasEnabled] = useState(entity.enabled);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const dragging = useRef(false);
  const pending = state.phase === "pending" || state.phase === "confirming";
  const refusal = useRefusal(state);
  const observed = read(entity.state?.value);
  const target = (pending ? read(state.requested) : undefined) ?? observed;
  const value = draft ?? target ?? fallback;
  const latest = useRef({ request, enabled: entity.enabled });
  useLayoutEffect(() => {
    latest.current = { request, enabled: entity.enabled };
  });
  const cancelTimer = () => {
    clearTimeout(timer.current);
    timer.current = undefined;
  };
  useEffect(() => () => clearTimeout(timer.current), []);
  // A disabled Entity discards the local preview, and the effect below stops a
  // pending keyboard settle so it cannot send once control returns.
  if (wasEnabled !== entity.enabled) {
    setWasEnabled(entity.enabled);
    if (!entity.enabled) {
      setDraft(undefined);
    }
  }
  useEffect(() => {
    if (!entity.enabled) {
      clearTimeout(timer.current);
    }
  }, [entity.enabled]);
  const commit = (next: T | undefined) => {
    cancelTimer();
    setDraft(undefined);
    const { current } = latest;
    // An explicit same-value set can switch back from another mode's pending Command.
    if (next !== undefined && current.enabled) {
      current.request(next);
    }
  };
  return {
    value,
    observed,
    pending,
    failed: state.phase === "failed",
    refusal,
    begin: () => {
      dragging.current = true;
      cancelTimer();
    },
    change: (next: T) => {
      setDraft(next);
      if (dragging.current) {
        return;
      }
      cancelTimer();
      timer.current = setTimeout(() => commit(next), KEYBOARD_SETTLE_MS);
    },
    commit,
    end: (next: T) => {
      dragging.current = false;
      commit(next);
    },
    cancel: () => {
      dragging.current = false;
      commit(undefined);
    },
    blur: () => {
      if (timer.current !== undefined) {
        commit(draft);
      }
    },
  };
};
