import { useId } from "react";
import type { ValueTreatment } from "../status";

const X = 14, Y = 14, W = 52, H = 44;

/**
 * A slatted cover in its opening, drawn at the reported position: the panel
 * rolls up as the cover opens. While a position Command is pending, a dashed
 * violet line marks the requested position. Decorative; the reading and its
 * position carry the same facts in words.
 */
export function CoverDiagram({ position, target, value }: { position?: number; target?: number; value: ValueTreatment }) {
  const clip = useId();
  const shown = value === "empty" || position === undefined ? undefined : position;
  const offset = shown === undefined ? H : H * shown / 100;
  // Endpoint marks sit outside the frame/roller so the target cannot disappear into their edges.
  const targetY = target === undefined ? undefined : target === 100 ? Y - 8 : target === 0 ? Y + H + 4 : Y + H - H * target / 100;
  return <svg className="view-cover-diagram" viewBox={`0 0 80 ${Y + H + 6}`} aria-hidden="true" data-value={value}>
    <defs><clipPath id={clip}><rect x={X} y={Y} width={W} height={H} /></clipPath></defs>
    <line className="view-cover-floor" x1={X - 8} x2={X + W + 8} y1={Y + H} y2={Y + H} />
    <rect className="view-cover-void" x={X} y={Y} width={W} height={H} />
    {shown !== undefined && <g clipPath={`url(#${clip})`}>
      <g className="view-cover-panel" style={{ transform: `translateY(${(-offset).toFixed(2)}px)` }}>
        <rect className="view-cover-face" x={X} y={Y} width={W} height={H} />
        {[11, 22, 33].map(y => <line key={y} className="view-cover-slat" x1={X} x2={X + W} y1={Y + y} y2={Y + y} />)}
        <rect className="view-cover-seal" x={X} y={Y + H - 2} width={W} height={2} />
      </g>
    </g>}
    <rect className="view-cover-frame" x={X} y={Y} width={W} height={H} />
    <rect className="view-cover-roller" x={X - 3} y={Y - 5} width={W + 6} height={5} rx={1} />
    {targetY !== undefined && <line className="view-cover-target" x1={X - 5} x2={X + W + 5} y1={targetY} y2={targetY} />}
  </svg>;
}
