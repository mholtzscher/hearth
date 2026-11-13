import { useEffect, useId, useRef, useState, type CSSProperties, type ReactNode } from "react";
import type { FieldEntity } from "../bindings/entity";
import { useControl } from "../data/provider";
import { ControlStatus } from "./ControlStatus";

/** Keyboard steps send once they settle for this long. */
export const KEYBOARD_SETTLE_MS = 500;
const MIN = 1, MAX = 100;
const clamp = (value: number) => Math.min(MAX, Math.max(MIN, value));

/**
 * A light's brightness from 1 to 100 %. Only the power switch turns a light
 * off. The value follows the pointer locally and sends once on release.
 * Keyboard steps send after they settle. While a Command is pending the slider
 * shows the requested level; otherwise it shows observed State, so an observed
 * 0 % reads 0 % with the thumb at the minimum.
 */
export function BrightnessSlider({ entity, label, feedback }: { entity: FieldEntity; label: string; feedback?: ReactNode }) {
  const [state, request] = useControl({ entityId: entity.id, operation: "set" });
  const [draft, setDraft] = useState<number>();
  const dragging = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const id = useId();
  useEffect(() => () => clearTimeout(timer.current), []);

  const working = state.phase === "pending" || state.phase === "confirming";
  const observed = typeof entity.state?.value === "number" ? entity.state.value : undefined;
  const target = working && typeof state.requested === "number" ? state.requested : observed;
  const shown = draft ?? target;
  // A settle timer fires after later renders; it compares against the current target.
  const latest = useRef({ request, target });
  latest.current = { request, target };

  function cancelSettle() {
    clearTimeout(timer.current);
    timer.current = undefined;
  }
  function commit(value: number | undefined) {
    cancelSettle();
    setDraft(undefined);
    if (value !== undefined && value !== latest.current.target) latest.current.request(value);
  }

  const position = clamp(shown ?? MIN);
  return <div className="view-control-slider">
    <div className="view-slider-label">
      <label htmlFor={id}>Brightness</label>
      {/* aria-valuetext carries the level; an <output> would announce every step of a drag. */}
      <span className="view-slider-value" aria-hidden="true">{shown === undefined ? "—" : `${shown} %`}</span>
    </div>
    <input id={id} type="range" className="view-slider" min={MIN} max={MAX} step={1} value={position}
      aria-label={label} aria-valuetext={shown === undefined ? "No value" : `${shown} %`}
      aria-busy={working || undefined} disabled={!entity.enabled}
      style={{ "--fill": `${(position - MIN) / (MAX - MIN) * 100}%` } as CSSProperties}
      onPointerDown={() => { dragging.current = true; cancelSettle(); }}
      onPointerUp={event => { dragging.current = false; commit(Number(event.currentTarget.value)); }}
      onPointerCancel={() => { dragging.current = false; commit(undefined); }}
      onChange={event => {
        const value = Number(event.currentTarget.value);
        setDraft(value);
        if (dragging.current) return;
        cancelSettle();
        timer.current = setTimeout(() => commit(value), KEYBOARD_SETTLE_MS);
      }}
      onBlur={() => { if (timer.current !== undefined) commit(draft); }} />
    {feedback ?? <ControlStatus entityId={entity.id} pending={requested => `Setting brightness to ${String(requested)} %…`} />}
  </div>;
}
