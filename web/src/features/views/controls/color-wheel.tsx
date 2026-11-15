import { useId } from "react";
import type { PointerEvent } from "react";

import { Input } from "../../../components/input";
import type { HS } from "./light-color";

const point = (event: PointerEvent<HTMLInputElement>): HS => {
  const box = event.currentTarget.getBoundingClientRect();
  const x = (event.clientX - box.left - box.width / 2) / (box.width / 2);
  const y = (event.clientY - box.top - box.height / 2) / (box.height / 2);
  return {
    hue: Math.round(((Math.atan2(y, x) * 180) / Math.PI + 360) % 360) % 360,
    saturation: Math.min(100, Math.round(Math.hypot(x, y) * 100)),
  };
};

const blocksNativeRangeKeys = (key: string) =>
  key === "Home" || key === "End" || key === "PageUp" || key === "PageDown";

export const ColorWheel = ({
  value,
  disabled,
  pending,
  onBegin,
  onChange,
  onEnd,
  onCancel,
  onBlur,
}: {
  value: HS;
  disabled: boolean;
  pending: boolean;
  onBegin: () => void;
  onChange: (value: HS) => void;
  onEnd: (value: HS) => void;
  onCancel: () => void;
  onBlur: () => void;
}) => {
  const hintId = useId();
  const angle = (value.hue * Math.PI) / 180;
  const position = {
    left: `${50 + (Math.cos(angle) * value.saturation) / 2}%`,
    top: `${50 + (Math.sin(angle) * value.saturation) / 2}%`,
  };
  return (
    <>
      <div
        data-disabled={disabled || undefined}
        className="relative mx-auto my-4 size-[220px] max-w-full touch-none rounded-full [background:radial-gradient(circle,#fff_0%,transparent_70.7%),conic-gradient(from_90deg,#f00,#ff0,#0f0,#0ff,#00f,#f0f,#f00)] data-[disabled]:cursor-not-allowed data-[disabled]:grayscale"
      >
        <span
          className="pointer-events-none absolute size-[18px] -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-[#fff] shadow-[0_1px_4px_#000]"
          style={position}
        />
        <Input
          type="range"
          min={0}
          max={359}
          step={1}
          value={value.hue}
          aria-label="Color wheel"
          aria-valuetext={`Hue ${value.hue} degrees, saturation ${value.saturation} percent`}
          aria-describedby={hintId}
          aria-busy={pending || undefined}
          disabled={disabled}
          className="peer absolute inset-0 size-full cursor-crosshair touch-none appearance-none rounded-full border-0 bg-transparent p-0 opacity-0 disabled:cursor-not-allowed"
          onChange={(event) => {
            onChange({ ...value, hue: Number(event.currentTarget.value) });
          }}
          onPointerDown={(event) => {
            if (disabled) {
              return;
            }
            // Keep the wheel's two-axis geometry: the native range must not
            // start its own horizontal drag.
            event.preventDefault();
            event.currentTarget.setPointerCapture(event.pointerId);
            event.currentTarget.focus();
            onBegin();
            onChange(point(event));
          }}
          onPointerMove={(event) => {
            if (
              !disabled &&
              event.currentTarget.hasPointerCapture(event.pointerId)
            ) {
              onChange(point(event));
            }
          }}
          onPointerUp={(event) => {
            if (!event.currentTarget.hasPointerCapture(event.pointerId)) {
              return;
            }
            event.currentTarget.releasePointerCapture(event.pointerId);
            if (disabled) {
              onCancel();
            } else {
              onEnd(point(event));
            }
          }}
          onPointerCancel={onCancel}
          onKeyDown={(event) => {
            if (disabled) {
              return;
            }
            const step = event.shiftKey ? 10 : 1;
            let next: HS;
            if (event.key === "ArrowLeft") {
              next = { ...value, hue: (value.hue - step + 360) % 360 };
            } else if (event.key === "ArrowRight") {
              next = { ...value, hue: (value.hue + step) % 360 };
            } else if (event.key === "ArrowUp") {
              next = {
                ...value,
                saturation: Math.min(100, value.saturation + step),
              };
            } else if (event.key === "ArrowDown") {
              next = {
                ...value,
                saturation: Math.max(0, value.saturation - step),
              };
            } else {
              if (blocksNativeRangeKeys(event.key)) {
                event.preventDefault();
              }
              return;
            }
            event.preventDefault();
            onChange(next);
          }}
          onBlur={onBlur}
        />
        <span
          aria-hidden="true"
          className="peer-focus-visible:outline-ring pointer-events-none absolute inset-0 rounded-full peer-focus-visible:outline-2 peer-focus-visible:outline-offset-3"
        />
      </div>
      <p id={hintId} className="text-label text-gray-8 mt-3 leading-normal">
        Left/right: hue. Up/down: saturation. Shift for larger steps.
      </p>
    </>
  );
};
