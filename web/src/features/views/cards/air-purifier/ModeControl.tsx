import type { FieldEntity } from "../../bindings/entity";
import { useControl } from "../../data/provider";
import { choiceWords, choicesOf } from "./category";
import { SegmentedControl } from "../../../../components/ui/segmented-control";

export function ModeControl({ entity, label }: { entity: FieldEntity; label: string }) {
  const [control, request] = useControl({ entityId: entity.id, operation: "set" });
  const working = control.phase === "pending" || control.phase === "confirming";
  const shown = working ? control.requested : entity.state?.value;
  return <div className="grid gap-2 border-t border-border py-3"><span className="text-gray-9">Mode</span>
    <SegmentedControl label={label} size="touch" value={typeof shown === "string" ? shown : ""} disabled={!entity.enabled}
      options={choicesOf(entity.support).map(choice => ({ value: choice, label: choiceWords(choice) }))} onValueChange={request} />
  </div>;
}
