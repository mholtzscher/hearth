import type { CommandFailure } from "../../../api/commands";
import { useControls } from "../data/provider";
import { controlStatusClasses, controlStatusLineClasses, lightFailures } from "./ControlStatus";
import { StatusDot } from "./StatusDot";
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
  if (detailed) return <div>
    {active.map(({ id, name, pending, state }) => <div data-slot="control-status" className={controlStatusClasses} key={id}>
      <p className={controlStatusLineClasses} role={state.phase === "failed" ? "alert" : "status"} data-tone={state.phase === "failed" ? "error" : "info"}>
        <StatusDot tone={state.phase === "failed" ? "error" : "info"} />
        {state.phase === "failed" ? `${name}: ${lightFailures[state.failure.kind]}` : pending(state.requested)}
      </p>
    </div>)}
  </div>;
  const single = active[0];
  const summary = active.length > 1
    ? [failed.length ? `${failed.length} failed` : "", pending ? `${pending} pending` : ""].filter(Boolean).join(" · ")
    : single?.state.phase === "failed" ? `${single.name} ${shortFailures[single.state.failure.kind]}`
    : single ? single.name === "Power" ? single.pending(single.state.requested) : `Setting ${single.name.toLowerCase()}…` : "";
  if (inline) return <span data-slot="color-feedback-indicator" className="whitespace-nowrap text-[13px] text-info data-[tone=error]:text-destructive" aria-hidden={hidden || undefined} data-tone={failed.length ? "error" : "info"}>
    <span id={descriptionId} className="sr-only" role={failed.length ? "alert" : "status"}>{summary}</span>
    {summary && <span aria-hidden="true">{failed.length ? "Failed" : "Pending"}</span>}
  </span>;
  return <div data-slot="control-status" data-light-feedback className={controlStatusClasses} aria-hidden={hidden || undefined} inert={hidden || undefined}>
    <p className={`${controlStatusLineClasses} whitespace-nowrap`} role={failed.length ? "alert" : "status"} data-tone={failed.length ? "error" : "info"}>
      {summary && <button type="button" className="relative flex max-w-full cursor-pointer items-baseline gap-1.5 p-0 text-start font-[inherit] text-inherit before:absolute before:-inset-y-[11.5px] before:inset-x-0" onClick={onDetails}>
        <StatusDot tone={failed.length ? "error" : "info"} /><span className="overflow-hidden text-ellipsis">{summary} · Details</span>
      </button>}
    </p>
  </div>;
}
