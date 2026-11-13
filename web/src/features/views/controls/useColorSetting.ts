import { useEffect, useRef, useState } from "react";
import type { FieldEntity } from "../bindings/entity";
import { useControl } from "../data/provider";
import { KEYBOARD_SETTLE_MS } from "./BrightnessSlider";
import type { ControlValue } from "./queue";

/** Local drag/keyboard preview over the shared, durable Command control state. */
export function useColorSetting<T extends ControlValue>({ entity, read, fallback }: {
  entity: FieldEntity; read: (value: unknown) => T | undefined; fallback: T;
}) {
  const [state, request] = useControl({ entityId: entity.id, operation: "set" });
  const [draft, setDraft] = useState<T>();
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const dragging = useRef(false);
  const pending = state.phase === "pending" || state.phase === "confirming";
  const observed = read(entity.state?.value);
  const target = (pending ? read(state.requested) : undefined) ?? observed;
  const value = draft ?? target ?? fallback;
  const latest = useRef({ request, enabled: entity.enabled });
  latest.current = { request, enabled: entity.enabled };
  function cancelTimer() { clearTimeout(timer.current); timer.current = undefined; }
  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(() => { if (!entity.enabled) { clearTimeout(timer.current); setDraft(undefined); } }, [entity.enabled]);
  function commit(next: T | undefined) {
    cancelTimer(); setDraft(undefined);
    const current = latest.current;
    // An explicit same-value set can switch back from another mode's pending Command.
    if (next !== undefined && current.enabled) current.request(next);
  }
  return {
    value, observed, pending, failed: state.phase === "failed",
    begin: () => { dragging.current = true; cancelTimer(); },
    change: (next: T) => {
      setDraft(next);
      if (dragging.current) return;
      cancelTimer(); timer.current = setTimeout(() => commit(next), KEYBOARD_SETTLE_MS);
    },
    commit,
    end: (next: T) => { dragging.current = false; commit(next); },
    cancel: () => { dragging.current = false; commit(undefined); },
    blur: () => { if (timer.current !== undefined) commit(draft); },
  };
}
