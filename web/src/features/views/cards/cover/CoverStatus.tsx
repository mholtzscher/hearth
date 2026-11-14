import type { CoverLine } from "./data";

/** Position, motion, and what a finished Command does and doesn't establish. */
export const coverSourceNote = <>
  <p>Position and motion come from the cover's reported State. Some covers estimate position from travel time, so 0 % can also mean the position was never set.</p>
  <p>A finished Open or Close means the cover reported that position, not that closure was verified. Stop completes once it is sent: the reading changes when the cover reports. Stop isn't an emergency stop and has no timing guarantee.</p>
</>;

/** The reserved line beneath a cover's controls. Pending words are a polite status; a failure is an alert. */
export function CoverStatus({ line, tile = false }: { line?: CoverLine; tile?: boolean }) {
  const words = line && (tile
    ? <><span aria-hidden="true" className="view-cover-short">{line.short}</span><span className="sr-only">{line.text}</span></>
    : line.text);
  return <div className={`view-control-status view-cover-status${tile ? " view-cover-row" : ""}`}>
    <p role="status" data-tone={line && !line.alert ? line.tone : undefined} title={tile && line && !line.alert ? line.text : undefined}>
      {line && !line.alert && <><span className="view-status-dot" aria-hidden="true" />{words}</>}
    </p>
    {line?.alert && <p role="alert" data-tone="error" title={tile ? line.text : undefined}><span className="view-status-dot" aria-hidden="true" />{words}</p>}
  </div>;
}
