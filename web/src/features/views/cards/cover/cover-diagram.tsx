import { useId } from "react";

import { cn } from "../../../../lib/utils";
import type { ValueTreatment } from "../status";

const H = 44;
const W = 52;
const X = 14;
const Y = 14;

/**
 * A slatted cover in its opening, drawn at the reported position: the panel
 * rolls up as the cover opens. While a position Command is pending, a dashed
 * violet line marks the requested position. Decorative; the reading and its
 * position carry the same facts in words.
 */
export const CoverDiagram = ({
  position,
  target,
  value,
}: {
  position?: number;
  target?: number;
  value: ValueTreatment;
}) => {
  const clip = useId();
  const shown =
    value === "empty" || position === undefined ? undefined : position;
  const offset = shown === undefined ? H : (H * shown) / 100;
  // Endpoint marks sit outside the frame/roller so the target cannot disappear into their edges.
  let targetY: number | undefined;
  if (target !== undefined) {
    if (target === 100) {
      targetY = Y - 8;
    } else if (target === 0) {
      targetY = Y + H + 4;
    } else {
      targetY = Y + H - (H * target) / 100;
    }
  }
  return (
    <svg
      className="block h-auto w-[88px] overflow-visible @max-[250px]:w-16"
      viewBox={`0 0 80 ${Y + H + 6}`}
      aria-hidden="true"
      data-value={value}
    >
      <defs>
        <clipPath id={clip}>
          <rect x={X} y={Y} width={W} height={H} />
        </clipPath>
      </defs>
      <line
        className="stroke-gray-5 [stroke-width:1.5] [stroke-linecap:round]"
        x1={X - 8}
        x2={X + W + 8}
        y1={Y + H}
        y2={Y + H}
      />
      <rect className="fill-gray-0" x={X} y={Y} width={W} height={H} />
      {shown !== undefined && (
        <g clipPath={`url(#${clip})`}>
          <g
            className="transition-transform duration-[900ms] ease-linear"
            style={{ transform: `translateY(${(-offset).toFixed(2)}px)` }}
          >
            <rect
              className={cn("fill-gray-4", value === "dimmed" && "fill-gray-3")}
              x={X}
              y={Y}
              width={W}
              height={H}
            />
            {[11, 22, 33].map((y) => (
              <line
                key={y}
                className={cn(
                  "stroke-gray-6 [stroke-width:1]",
                  value === "dimmed" && "stroke-gray-4"
                )}
                x1={X}
                x2={X + W}
                y1={Y + y}
                y2={Y + y}
              />
            ))}
            <rect
              className={cn("fill-gray-8", value === "dimmed" && "fill-gray-6")}
              x={X}
              y={Y + H - 2}
              width={W}
              height={2}
            />
          </g>
        </g>
      )}
      <rect
        className={cn(
          "stroke-gray-6 fill-none [stroke-width:1.5]",
          value !== "live" && "[stroke-dasharray:3_2]"
        )}
        x={X}
        y={Y}
        width={W}
        height={H}
      />
      <rect
        className="fill-gray-5"
        x={X - 3}
        y={Y - 5}
        width={W + 6}
        height={5}
        rx={1}
      />
      {targetY !== undefined && (
        <line
          className="stroke-violet [stroke-width:1.5] [stroke-dasharray:3_2]"
          x1={X - 5}
          x2={X + W + 5}
          y1={targetY}
          y2={targetY}
        />
      )}
    </svg>
  );
};
