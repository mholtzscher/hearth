import { apiFetch } from "../src/api/client";
import { parseFieldEntity, type FieldEntity } from "../src/features/views/bindings/entity";
import { useRead } from "../src/features/views/data/provider";

/** Keep display/capability args editable while State follows the real read cache's Command refresh. */
export function useObservedEntity(entity: FieldEntity | undefined): FieldEntity | undefined {
  // Controls consume the whole State, including structured colors without a root field binding.
  const read = useRead(entity ? {
    key: `entity-state:${entity.id}:household`, kind: "state",
    load: signal => apiFetch(`/v1/entities/${encodeURIComponent(entity.id)}`, { signal },
      text => parseFieldEntity(JSON.parse(text))),
  } : null);
  if (read.error) throw read.error;
  if (!entity || read.data === undefined) return entity;
  return { ...entity, state: parseFieldEntity(read.data).state };
}
