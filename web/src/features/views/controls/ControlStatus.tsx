import type { CommandFailure } from "../../../api/commands";
import { useControl } from "../data/provider";

const failures: Record<CommandFailure["kind"], string> = {
  disabled: "Couldn't change the light. This Entity is disabled.",
  unreachable: "Couldn't reach the light. Its adapter or the light is offline.",
  rejected: "The light rejected the change.",
  unconfirmed: "The light didn't confirm in time. It may still change.",
  network: "Couldn't reach Hearth. The change may still apply.",
  invalid: "Couldn't change the light.",
  "not-found": "Couldn't change the light.",
  internal: "Couldn't change the light.",
};

/**
 * The line under a control while its Command is pending, or after it failed.
 * Its row is always reserved, so a Command that settles in milliseconds
 * doesn't move the card. Pending text is a polite status; a failure is an
 * alert. Both pair a tone dot with words, and the failure stays until the next
 * request.
 */
export function ControlStatus({ entityId, pending }: { entityId: string; pending: (requested: unknown) => string }) {
  const [state] = useControl({ entityId, operation: "set" });
  const working = state.phase === "pending" || state.phase === "confirming";
  return <div className="view-control-status">
    <p role="status" data-tone="info">
      {working && <><span className="view-status-dot" aria-hidden="true" />{pending(state.requested)}</>}
    </p>
    {state.phase === "failed" && <p role="alert" data-tone="error">
      <span className="view-status-dot" aria-hidden="true" />{failures[state.failure.kind]}
    </p>}
  </div>;
}
