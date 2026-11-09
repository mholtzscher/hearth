import type { FieldEntity } from "../bindings/entity";
import { useControl } from "../data/provider";

/**
 * A power Entity's switch. While a Command is pending it shows the requested
 * position; otherwise it shows observed State. A never-observed Entity reads as
 * off, and pressing it turns the Device on.
 */
export function PowerSwitch({ entity, label }: { entity: FieldEntity; label: string }) {
  const [state, request] = useControl({ entityId: entity.id, operation: "set" });
  const working = state.phase === "pending" || state.phase === "confirming";
  const on = working ? state.requested === true : entity.state?.value === true;
  return <button type="button" role="switch" className="view-switch" aria-checked={on} aria-label={label}
    disabled={!entity.enabled} aria-busy={working || undefined} onClick={() => request(!on)}>
    <span className="view-switch-thumb" aria-hidden="true" />
  </button>;
}
