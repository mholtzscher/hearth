import { Minus, Plus } from "lucide-react";

import { Button } from "../../../../components/button";
import { cn } from "../../../../lib/utils";
import { Reading } from "../reading";
import type { ValueTreatment } from "../status";
import type { ThermostatMode } from "./state";
import type { StepResult, Target } from "./targets";
import { formatTarget } from "./units";
import { modeWords } from "./words";

/**
 * Every slot fills the same three rows (label, value, and controls) in the
 * same two columns, so a mode or target change never moves the card.
 */
const slotClass =
  "grid min-w-0 grid-rows-[1.3125rem_2.875rem_3rem] items-center justify-items-start gap-1";

const TargetSlot = ({
  target,
  digits,
  value,
  requested,
  refusal,
  enabled,
  step,
  onStep,
}: {
  target: Target;
  digits: number;
  value: ValueTreatment;
  requested: boolean;
  refusal?: string;
  enabled: boolean;
  step: (direction: 1 | -1) => StepResult | undefined;
  onStep: (result: StepResult) => void;
}) => {
  // "Heat to" names the heat target; a single "Target" names just the target.
  const name =
    target.label === "Target"
      ? "target"
      : `${target.label.replace(" to", "").toLowerCase()} target`;
  const shownText =
    target.value === undefined ? undefined : formatTarget(target.value, digits);
  const now = shownText === undefined ? "unknown" : `${shownText}°`;
  const lower = enabled ? step(-1) : undefined;
  const raise = enabled ? step(1) : undefined;
  return (
    <div className={slotClass} data-slot="thermostat-target">
      <span className="text-label text-gray-9">{target.label}</span>
      <Reading
        value={shownText === undefined ? "empty" : value}
        className={cn(
          "decoration-violet underline-offset-[6px]",
          requested &&
            "text-violet-text underline decoration-dashed decoration-[1.5px]"
        )}
      >
        {shownText ?? "—"}
        <span className="text-muted-foreground">°</span>
      </Reading>
      {target.operation && (
        <div className={cn("flex gap-1.5 self-end", refusal)}>
          <Button
            variant="outline"
            size="icon-touch"
            type="button"
            aria-label={`Lower ${name}, now ${now}`}
            disabled={!lower}
            onClick={() => lower && onStep(lower)}
          >
            <Minus className="size-4" aria-hidden="true" />
          </Button>
          <Button
            variant="outline"
            size="icon-touch"
            type="button"
            aria-label={`Raise ${name}, now ${now}`}
            disabled={!raise}
            onClick={() => raise && onStep(raise)}
          >
            <Plus className="size-4" aria-hidden="true" />
          </Button>
        </div>
      )}
    </div>
  );
};

/** A mode without a target, or no State yet, names the mode where targets go. */
const ModeSlot = ({
  mode,
  value,
}: {
  mode: ThermostatMode | undefined;
  value: ValueTreatment;
}) => (
  <div className={cn(slotClass, "col-span-2")} data-slot="thermostat-target">
    <span className="text-label text-gray-9">Mode</span>
    <Reading value={mode ? value : "empty"}>
      {mode ? modeWords[mode] : "—"}
    </Reading>
    {mode && (
      <p className="text-label text-muted-foreground">
        No target in this mode.
      </p>
    )}
  </div>
);

export const TargetBlock = ({
  mode,
  targets,
  digits,
  value,
  enabled,
  requested,
  refusal,
  step,
  onStep,
}: {
  mode: ThermostatMode | undefined;
  targets: Target[];
  digits: number;
  value: ValueTreatment;
  enabled: boolean;
  requested: (target: Target) => boolean;
  refusal: (target: Target) => string | undefined;
  step: (target: Target, direction: 1 | -1) => StepResult | undefined;
  onStep: (result: StepResult) => void;
}) => (
  <div className="grid grid-cols-2 gap-x-4" data-slot="thermostat-targets">
    {targets.length === 0 ? (
      <ModeSlot mode={mode} value={value} />
    ) : (
      targets.map((target) => (
        <TargetSlot
          key={target.key}
          target={target}
          digits={digits}
          value={value}
          requested={requested(target)}
          refusal={refusal(target)}
          enabled={enabled}
          step={(direction) => step(target, direction)}
          onStep={onStep}
        />
      ))
    )}
  </div>
);
