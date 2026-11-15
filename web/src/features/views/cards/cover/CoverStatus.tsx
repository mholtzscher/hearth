import type { CoverLine } from "./data";
import { controlStatusClasses, controlStatusLineClasses } from "../../controls/ControlStatus";
import { StatusDot } from "../../controls/StatusDot";
import { cn } from "../../../../lib/utils";

/** Position, motion, and what a finished Command does and doesn't establish. */
export const coverSourceNote = <>
  <p>Position and motion come from the cover's reported State. Some covers estimate position from travel time, so 0 % can also mean the position was never set.</p>
  <p>A finished Open or Close means the cover reported that position, not that closure was verified. Stop completes once it is sent: the reading changes when the cover reports. Stop isn't an emergency stop and has no timing guarantee.</p>
</>;

/** The reserved line beneath a cover's controls. Pending words are a polite status; a failure is an alert. */
export function CoverStatus({ line, tile = false, on = false }: { line?: CoverLine; tile?: boolean; on?: boolean }) {
  const words = line && (tile
    ? <><span data-slot="cover-short" aria-hidden="true" className="min-w-0 overflow-hidden text-ellipsis">{line.short}</span><span className="sr-only">{line.text}</span></>
    : line.text);
  return <div data-cover-status data-slot={tile ? "cover-row" : undefined} className={cn(controlStatusClasses, tile && "m-0 h-[21px] min-h-0 overflow-hidden text-ellipsis whitespace-nowrap", on && "text-[color-mix(in_oklch,var(--violet)_14%,var(--gray-9))]")}>
    <p className={cn(controlStatusLineClasses, "data-[tone=warn]:text-warning", tile && "min-w-0")} role="status" data-tone={line && !line.alert ? line.tone : undefined} title={tile && line && !line.alert ? line.text : undefined}>
      {line && !line.alert && <><StatusDot tone={line.tone} />{words}</>}
    </p>
    {line?.alert && <p className={cn(controlStatusLineClasses, tile && "min-w-0")} role="alert" data-tone="error" title={tile ? line.text : undefined}><StatusDot tone="error" />{words}</p>}
  </div>;
}
