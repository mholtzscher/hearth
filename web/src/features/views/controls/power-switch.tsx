import { Switch } from "../../../components/switch";
import type { FieldEntity } from "../bindings/entity";
import { useControl } from "../data/provider";

/**
 * A power Entity's switch. While a Command is pending it shows the requested
 * position; otherwise it shows observed State. A never-observed Entity reads as
 * off, and pressing it turns the Device on.
 */
export const PowerSwitch = ({
  entity,
  label,
  className,
}: {
  entity: FieldEntity;
  label: string;
  className?: string;
}) => {
  const [state, request] = useControl({
    entityId: entity.id,
    operation: "set",
  });
  const working = state.phase === "pending" || state.phase === "confirming";
  const on = working ? state.requested === true : entity.state?.value === true;
  return (
    <Switch
      size="touch"
      className={className ?? "ms-auto"}
      checked={on}
      aria-label={label}
      disabled={!entity.enabled}
      aria-busy={working || undefined}
      onCheckedChange={request}
    />
  );
};
