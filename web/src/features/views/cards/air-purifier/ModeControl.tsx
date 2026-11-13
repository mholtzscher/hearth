import type { FieldEntity } from "../../bindings/entity";
import { useControl } from "../../data/provider";
import { choiceWords, choicesOf } from "./category";

export function ModeControl({ entity, label }: { entity: FieldEntity; label: string }) {
  const [control, request] = useControl({ entityId: entity.id, operation: "set" });
  const working = control.phase === "pending" || control.phase === "confirming";
  const shown = working ? control.requested : entity.state?.value;
  return <div className="view-air-purifier-control"><span className="view-air-purifier-control-name">Mode</span>
    <div className="view-air-purifier-segmented" role="group" aria-label={label}>
      {choicesOf(entity.support).map(choice => <button key={choice} type="button" aria-pressed={shown === choice} disabled={!entity.enabled} onClick={() => request(choice)}>{choiceWords(choice)}</button>)}
    </div>
  </div>;
}
