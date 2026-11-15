import { useState } from "react";

import { useControls } from "../data/provider";
import {
  controlStatusClasses,
  controlStatusLineClasses,
  powerFailures,
} from "./control-status";
import type { ControlState, ControlTarget } from "./queue";
import { StatusDot } from "./status-dot";

export interface StatusControl {
  target: ControlTarget;
  name: string;
  pending: (requested: unknown) => string;
}

/** One reserved line for the latest changed non-idle control. */
export const SharedControlStatus = ({
  controls,
}: {
  controls: StatusControl[];
}) => {
  const [states] = useControls(controls.map((control) => control.target));
  const [history, setHistory] = useState(() => ({
    seen: new Map<string, { state: ControlState; order: number }>(),
    sequence: 0,
  }));
  const { seen, sequence } = history;
  const changed = controls.some((control, index) => {
    const key = `${control.target.entityId}|${control.target.operation}`;
    return seen.get(key)?.state !== states[index];
  });
  const nextSequence = changed ? sequence + 1 : sequence;
  const nextSeen = changed ? new Map(seen) : seen;
  if (changed) {
    for (const [index, control] of controls.entries()) {
      const key = `${control.target.entityId}|${control.target.operation}`;
      if (nextSeen.get(key)?.state !== states[index]) {
        nextSeen.set(key, { state: states[index], order: nextSequence });
      }
    }
    setHistory({ seen: nextSeen, sequence: nextSequence });
  }
  let selected = -1;
  let selectedOrder = -1;
  for (const [index, control] of controls.entries()) {
    const current = nextSeen.get(
      `${control.target.entityId}|${control.target.operation}`
    );
    if (!current) {
      continue;
    }
    const { order } = current;
    if (states[index].phase !== "idle" && order > selectedOrder) {
      selected = index;
      selectedOrder = order;
    }
  }
  const state = states[selected];
  const control = controls[selected];
  return (
    <div data-slot="control-status" className={controlStatusClasses}>
      <output data-tone="info" className={controlStatusLineClasses}>
        {state &&
          (state.phase === "pending" || state.phase === "confirming") && (
            <>
              <StatusDot tone="info" />
              {control.pending(state.requested)}
            </>
          )}
      </output>
      {state?.phase === "failed" && (
        <p role="alert" data-tone="error" className={controlStatusLineClasses}>
          <StatusDot tone="error" />
          {control.name}: {powerFailures[state.failure.kind]}
        </p>
      )}
    </div>
  );
};
