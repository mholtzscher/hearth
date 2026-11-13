import { useId, type PointerEvent } from "react";
import type { HS } from "./light-color";

export function ColorWheel({ value, disabled, pending, onBegin, onChange, onEnd, onCancel, onBlur }: {
  value: HS; disabled: boolean; pending: boolean;
  onBegin: () => void; onChange: (value: HS) => void; onEnd: (value: HS) => void; onCancel: () => void; onBlur: () => void;
}) {
  const hintId = useId();
  const angle = value.hue * Math.PI / 180;
  const position = { left: `${50 + Math.cos(angle) * value.saturation / 2}%`, top: `${50 + Math.sin(angle) * value.saturation / 2}%` };
  const point = (event: PointerEvent<HTMLDivElement>): HS => {
    const box = event.currentTarget.getBoundingClientRect();
    const x = (event.clientX - box.left - box.width / 2) / (box.width / 2), y = (event.clientY - box.top - box.height / 2) / (box.height / 2);
    return { hue: Math.round((Math.atan2(y, x) * 180 / Math.PI + 360) % 360) % 360, saturation: Math.min(100, Math.round(Math.hypot(x, y) * 100)) };
  };
  return <>
    <div className="view-color-wheel" role="slider" tabIndex={disabled ? -1 : 0} aria-label="Color wheel"
      aria-valuemin={0} aria-valuemax={359} aria-valuenow={value.hue}
      aria-valuetext={`Hue ${value.hue} degrees, saturation ${value.saturation} percent`}
      aria-describedby={hintId} aria-disabled={disabled} aria-busy={pending || undefined}
      onPointerDown={event => {
        if (disabled) return;
        event.currentTarget.setPointerCapture(event.pointerId); event.currentTarget.focus();
        onBegin(); onChange(point(event));
      }}
      onPointerMove={event => { if (!disabled && event.currentTarget.hasPointerCapture(event.pointerId)) onChange(point(event)); }}
      onPointerUp={event => {
        if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
        event.currentTarget.releasePointerCapture(event.pointerId);
        if (!disabled) onEnd(point(event)); else onCancel();
      }}
      onPointerCancel={onCancel}
      onKeyDown={event => {
        if (disabled) return;
        const step = event.shiftKey ? 10 : 1;
        let next: HS;
        if (event.key === "ArrowLeft") next = { ...value, hue: (value.hue - step + 360) % 360 };
        else if (event.key === "ArrowRight") next = { ...value, hue: (value.hue + step) % 360 };
        else if (event.key === "ArrowUp") next = { ...value, saturation: Math.min(100, value.saturation + step) };
        else if (event.key === "ArrowDown") next = { ...value, saturation: Math.max(0, value.saturation - step) };
        else return;
        event.preventDefault(); onChange(next);
      }} onBlur={onBlur}>
      <span className="view-color-wheel-marker" style={position} />
    </div>
    <p id={hintId} className="view-color-hint">Left/right: hue. Up/down: saturation. Shift for larger steps.</p>
  </>;
}
