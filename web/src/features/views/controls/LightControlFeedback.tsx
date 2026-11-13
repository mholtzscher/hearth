import type { CommandFailure } from "../../../api/commands";
import { useControls } from "../data/provider";
import { lightFailures } from "./ControlStatus";
import { colorHex, readColor, type LightColorControls } from "./light-color";

const shortFailures: Record<CommandFailure["kind"], string> = {
  disabled: "disabled", unreachable: "unreachable", rejected: "rejected",
  unconfirmed: "unconfirmed", network: "not confirmed", invalid: "failed",
  "not-found": "not found", internal: "failed",
};

/** Summarize in an existing card row; keep every Entity's outcome in the picker. */
export function LightControlFeedback({ controls, brightnessId, powerId, detailed = false, hidden = false, inline = false, descriptionId, onDetails }: {
  controls: LightColorControls; brightnessId?: string; powerId?: string;
  detailed?: boolean; hidden?: boolean; inline?: boolean; onDetails?: () => void;
  descriptionId?: string;
}) {
  const entries = [
    ...(powerId ? [{ id: powerId, name: "Power", pending: (value: unknown) => value ? "Turning on…" : "Turning off…" }] : []),
    ...(brightnessId ? [{ id: brightnessId, name: "Brightness", pending: (value: unknown) => `Setting brightness to ${String(value)} %…` }] : []),
    ...(controls.temperature ? [{ id: controls.temperature.entity.id, name: "White", pending: (value: unknown) => `Setting white to ${String(value)} mired…` }] : []),
    ...(controls.color ? [{ id: controls.color.entity.id, name: "Color", pending: (value: unknown) => {
      const color = readColor(value, controls.color!.kind);
      return color ? `Setting color to ${colorHex(color)}…` : "Setting color…";
    } }] : []),
  ];
  const [states] = useControls(entries.map(entry => ({ entityId: entry.id, operation: "set" })));
  const active = entries.flatMap((entry, index) => states[index].phase === "idle" ? [] : [{ ...entry, state: states[index] }]);
  const failed = active.filter(entry => entry.state.phase === "failed");
  const pending = active.length - failed.length;
  if (detailed) return <div className="view-color-feedback">
    {active.map(({ id, name, pending, state }) => <div className="view-control-status" key={id}>
      <p role={state.phase === "failed" ? "alert" : "status"} data-tone={state.phase === "failed" ? "error" : "info"}>
        <span className="view-status-dot" aria-hidden="true" />
        {state.phase === "failed" ? `${name}: ${lightFailures[state.failure.kind]}` : pending(state.requested)}
      </p>
    </div>)}
  </div>;
  const single = active[0];
  const summary = active.length > 1
    ? [failed.length ? `${failed.length} failed` : "", pending ? `${pending} pending` : ""].filter(Boolean).join(" · ")
    : single?.state.phase === "failed" ? `${single.name} ${shortFailures[single.state.failure.kind]}`
    : single ? single.name === "Power" ? single.pending(single.state.requested) : `Setting ${single.name.toLowerCase()}…` : "";
  if (inline) return <span className="view-color-feedback-indicator" aria-hidden={hidden || undefined} data-tone={failed.length ? "error" : "info"}>
    <span id={descriptionId} className="sr-only" role={failed.length ? "alert" : "status"}>{summary}</span>
    {summary && <span aria-hidden="true">{failed.length ? "Failed" : "Pending"}</span>}
  </span>;
  return <div className="view-control-status view-light-feedback" aria-hidden={hidden || undefined} inert={hidden || undefined}>
    <p role={failed.length ? "alert" : "status"} data-tone={failed.length ? "error" : "info"}>
      {summary && <button type="button" onClick={onDetails}>
        <span className="view-status-dot" aria-hidden="true" /><span>{summary} · Details</span>
      </button>}
    </p>
  </div>;
}
