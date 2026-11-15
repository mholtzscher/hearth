import type { FieldEntity } from "../../bindings/entity";
import { useControl } from "../../data/provider";
import { Minus, Plus } from "lucide-react";
import { Button } from "../../../../components/button";
import { cn } from "../../../../lib/utils";

export function FanStepper({ entity, levels, active, label }: { entity: FieldEntity; levels: number; active: boolean; label: string }) {
  const [control, request] = useControl({ entityId: entity.id, operation: "set" });
  const working = control.phase === "pending" || control.phase === "confirming";
  const raw = working ? control.requested : entity.state?.value;
  const level = typeof raw === "string" && /^\d+$/.test(raw) ? Number(raw) : undefined;
  return <div className="grid gap-2 border-t border-border py-3">
    <div className="flex justify-between gap-3"><span className="text-gray-9">Fan level</span>{level !== undefined && <span className="text-right text-muted-foreground tabular-nums">{active ? `Level ${level} of ${levels}` : `Level ${level} kept for Manual`}</span>}</div>
    <div className="grid grid-cols-[44px_minmax(0,1fr)_44px] items-center gap-2" role="group" aria-label={label}>
      <Button variant="outline" size="icon-touch" type="button" aria-label="Lower fan level" disabled={!entity.enabled || level === undefined || level <= 1} onClick={() => request(String(level! - 1))}><Minus className="size-4" aria-hidden="true" /></Button>
      <div className="flex items-center justify-center gap-3"><span className="max-w-full text-right text-compact-reading leading-[1.3] tracking-[-.02em] text-foreground data-[value=empty]:text-gray-6" data-value={level === undefined ? "empty" : undefined}>{level ?? "—"}</span><span data-slot="fan-pips" className="flex flex-wrap gap-1" data-stored={!active || undefined} data-disabled={!entity.enabled || undefined} aria-hidden="true">{Array.from({ length: levels }, (_, i) => <i key={i} data-on={i < (level ?? 0) || undefined} className={cn("h-1.5 w-3.5 rounded-[3px] bg-gray-4", i < (level ?? 0) && (active ? entity.enabled ? "bg-violet" : "bg-gray-5" : "bg-transparent shadow-[inset_0_0_0_1px_var(--gray-6)]"))} />)}</span></div>
      <Button variant="outline" size="icon-touch" type="button" aria-label="Raise fan level" disabled={!entity.enabled || level === undefined || level >= levels} onClick={() => request(String(level! + 1))}><Plus className="size-4" aria-hidden="true" /></Button>
    </div>
  </div>;
}
