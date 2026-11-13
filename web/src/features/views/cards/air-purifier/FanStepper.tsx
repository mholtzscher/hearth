import type { FieldEntity } from "../../bindings/entity";
import { useControl } from "../../data/provider";
import { Minus, Plus } from "lucide-react";

export function FanStepper({ entity, levels, active, label }: { entity: FieldEntity; levels: number; active: boolean; label: string }) {
  const [control, request] = useControl({ entityId: entity.id, operation: "set" });
  const working = control.phase === "pending" || control.phase === "confirming";
  const raw = working ? control.requested : entity.state?.value;
  const level = typeof raw === "string" && /^\d+$/.test(raw) ? Number(raw) : undefined;
  return <div className="view-air-purifier-control">
    <div className="view-air-purifier-control-head"><span className="view-air-purifier-control-name">Fan level</span>{level !== undefined && <span className="view-air-purifier-aside">{active ? `Level ${level} of ${levels}` : `Level ${level} kept for Manual`}</span>}</div>
    <div className="view-air-purifier-stepper" role="group" aria-label={label}>
      <button type="button" aria-label="Lower fan level" disabled={!entity.enabled || level === undefined || level <= 1} onClick={() => request(String(level! - 1))}><Minus size={16} aria-hidden="true" /></button>
      <div className="view-air-purifier-stepper-mid"><span className="view-row-value" data-value={level === undefined ? "empty" : undefined}>{level ?? "—"}</span><span className="view-air-purifier-pips" data-stored={!active || undefined} data-disabled={!entity.enabled || undefined} aria-hidden="true">{Array.from({ length: levels }, (_, i) => <i key={i} data-on={i < (level ?? 0) || undefined} />)}</span></div>
      <button type="button" aria-label="Raise fan level" disabled={!entity.enabled || level === undefined || level >= levels} onClick={() => request(String(level! + 1))}><Plus size={16} aria-hidden="true" /></button>
    </div>
  </div>;
}
