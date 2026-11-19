import { Minus, Plus } from "lucide-react";

import { Button } from "../../../../components/button";
import { cn } from "../../../../lib/utils";
import type { FieldEntity } from "../../bindings/entity";
import { useControl } from "../../data/provider";

const filledPipClass = (active: boolean, enabled: boolean) => {
  if (!active) {
    return "bg-transparent shadow-[inset_0_0_0_1px_var(--gray-6)]";
  }
  return enabled ? "bg-violet" : "bg-gray-5";
};

export const FanStepper = ({
  entity,
  levels,
  active,
  label,
}: {
  entity: FieldEntity;
  levels: number;
  active: boolean;
  label: string;
}) => {
  const [control, request] = useControl({
    entityId: entity.id,
    operation: "set",
  });
  const working = control.phase === "pending" || control.phase === "confirming";
  const raw = working ? control.requested : entity.state?.value;
  const level =
    typeof raw === "string" && /^\d+$/u.test(raw) ? Number(raw) : undefined;
  return (
    <div className="border-border grid gap-2 border-t py-3">
      <div className="flex justify-between gap-3">
        <span className="text-gray-9">Fan level</span>
        {level !== undefined && (
          <span className="text-muted-foreground text-right tabular-nums">
            {active
              ? `Level ${level} of ${levels}`
              : `Level ${level} kept for Manual`}
          </span>
        )}
      </div>
      <fieldset
        className="m-0 grid min-w-0 grid-cols-[44px_minmax(0,1fr)_44px] items-center gap-2 border-0 p-0"
        aria-label={label}
      >
        <Button
          variant="outline"
          size="icon-touch"
          type="button"
          aria-label="Lower fan level"
          disabled={!entity.enabled || level === undefined || level <= 1}
          onClick={() => {
            if (level !== undefined) {
              request(String(level - 1));
            }
          }}
        >
          <Minus className="size-4" aria-hidden="true" />
        </Button>
        <div className="flex items-center justify-center gap-3">
          <span
            className="text-compact-reading text-foreground data-[value=empty]:text-gray-6 max-w-full text-right tabular-nums"
            data-value={level === undefined ? "empty" : undefined}
          >
            {level ?? "—"}
          </span>
          <span
            data-slot="fan-pips"
            className="flex flex-wrap gap-1"
            data-stored={!active || undefined}
            data-disabled={!entity.enabled || undefined}
            aria-hidden="true"
          >
            {Array.from({ length: levels }, (_, i) => (
              <i
                key={i}
                data-on={i < (level ?? 0) || undefined}
                className={cn(
                  "bg-gray-4 h-1.5 w-3.5 rounded-xs",
                  i < (level ?? 0) && filledPipClass(active, entity.enabled)
                )}
              />
            ))}
          </span>
        </div>
        <Button
          variant="outline"
          size="icon-touch"
          type="button"
          aria-label="Raise fan level"
          disabled={!entity.enabled || level === undefined || level >= levels}
          onClick={() => {
            if (level !== undefined) {
              request(String(level + 1));
            }
          }}
        >
          <Plus className="size-4" aria-hidden="true" />
        </Button>
      </fieldset>
    </div>
  );
};
