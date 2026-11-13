import { useRef } from "react";
import { useControls } from "../data/provider";
import type { ControlState, ControlTarget } from "./queue";
import { powerFailures } from "./ControlStatus";

export type StatusControl = { target: ControlTarget; name: string; pending: (requested: unknown) => string };

/** One reserved line for the latest changed non-idle control. */
export function SharedControlStatus({ controls }: { controls: StatusControl[] }) {
  const [states] = useControls(controls.map(control => control.target));
  const seen = useRef(new Map<string, { state: ControlState; order: number }>());
  const sequence = useRef(0);
  const changed = controls.some((control, index) => seen.current.get(`${control.target.entityId}|${control.target.operation}`)?.state !== states[index]);
  if (changed) sequence.current++;
  controls.forEach((control, index) => {
    const key = `${control.target.entityId}|${control.target.operation}`;
    if (seen.current.get(key)?.state !== states[index]) seen.current.set(key, { state: states[index], order: sequence.current });
  });
  let selected = -1, order = -1;
  controls.forEach((control, index) => {
    const current = seen.current.get(`${control.target.entityId}|${control.target.operation}`)!;
    if (states[index].phase !== "idle" && current.order > order) { selected = index; order = current.order; }
  });
  const state = states[selected], control = controls[selected];
  return <div className="view-control-status">
    <p role="status" data-tone="info">{state && (state.phase === "pending" || state.phase === "confirming") && <><span className="view-status-dot" aria-hidden="true" />{control.pending(state.requested)}</>}</p>
    {state?.phase === "failed" && <p role="alert" data-tone="error"><span className="view-status-dot" aria-hidden="true" />{control.name}: {powerFailures[state.failure.kind]}</p>}
  </div>;
}
