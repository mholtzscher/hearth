import {
  entityStateRequest,
  parseFieldEntity,
} from "../src/features/views/bindings/entity";
import type { FieldEntity } from "../src/features/views/bindings/entity";
import { stateFields } from "../src/features/views/bindings/state-fields";
import { useRead } from "../src/features/views/data/provider";

/** Keep display/capability args editable while State follows the real read cache's Command refresh. */
export const useObservedEntity = (
  entity: FieldEntity | undefined
): FieldEntity | undefined => {
  // Controls consume the whole State, including structured colors without a root field binding.
  const read = useRead(
    entity
      ? entityStateRequest({
          entity_id: entity.id,
          entity_type: entity.type,
          field: Object.keys(stateFields[entity.type] ?? {})[0],
        })
      : null
  );
  if (read.error) {
    throw read.error;
  }
  if (!entity || read.data === undefined) {
    return entity;
  }
  return { ...entity, state: parseFieldEntity(read.data).state };
};
