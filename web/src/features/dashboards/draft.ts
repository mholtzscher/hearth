import type { Dashboard } from "../../api/dashboards";
import type { ViewDefinition } from "../views/catalog";
import { readDefinition } from "../views/catalog/compatibility";
import type { Base, PresentedArtifact } from "../conversation/runtime";

export type Draft = { definition: unknown; original: unknown; base: Base; origin_artifact_id: string | null; dirty: boolean };
export function fromDashboard(dashboard: Dashboard): Draft {
  return { definition: dashboard.definition, original: dashboard.definition, base: { dashboard_id: dashboard.id, revision: dashboard.revision }, origin_artifact_id: null, dirty: false };
}
export function fromArtifact(item: PresentedArtifact, previous?: Draft | null): Draft {
  const sameBase = item.base && previous?.base?.dashboard_id === item.base.dashboard_id && previous.base.revision === item.base.revision;
  return { definition: item.artifact.definition, original: sameBase ? previous.original : item.artifact.definition, base: item.base, origin_artifact_id: item.artifact.artifact_id, dirty: true };
}
export function editDraft(draft: Draft, definition: ViewDefinition): Draft {
  return { ...draft, definition, dirty: draft.origin_artifact_id !== null || JSON.stringify(definition) !== JSON.stringify(draft.original) };
}
/** An unedited generated view without a saved base remains recoverable from its conversation. */
export function isUneditedGeneratedView(draft: Draft) {
  return draft.origin_artifact_id !== null && !draft.base && draft.definition === draft.original;
}
export function draftContext(draft: Draft | null) {
  if (!draft) return undefined;
  const read = readDefinition(draft.definition);
  if (read.status !== "supported") return undefined;
  return { definition: read.definition, ...(draft.base ? { source_dashboard_id: draft.base.dashboard_id, source_revision: draft.base.revision } : {}) };
}
