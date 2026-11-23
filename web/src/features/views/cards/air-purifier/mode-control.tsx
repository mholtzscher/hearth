import { SegmentedControl } from "../../../../components/segmented-control";
import { cn } from "../../../../lib/utils";
import type { FieldEntity } from "../../bindings/entity";
import { useRefusal } from "../../controls/command-motion";
import { useControl } from "../../data/provider";
import { choiceWords, choicesOf } from "./category";

export const ModeControl = ({
  entity,
  label,
}: {
  entity: FieldEntity;
  label: string;
}) => {
  const [control, request] = useControl({
    entityId: entity.id,
    operation: "set",
  });
  const working = control.phase === "pending" || control.phase === "confirming";
  const refusal = useRefusal(control);
  const shown = working ? control.requested : entity.state?.value;
  return (
    <div className={cn("border-border grid gap-2 border-t py-3", refusal)}>
      <span className="text-gray-9">Mode</span>
      <SegmentedControl
        label={label}
        size="touch"
        value={typeof shown === "string" ? shown : ""}
        disabled={!entity.enabled}
        options={choicesOf(entity.support).map((choice) => ({
          value: choice,
          label: choiceWords(choice),
        }))}
        onValueChange={request}
      />
    </div>
  );
};
