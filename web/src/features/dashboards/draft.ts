import type { Dashboard } from "../../api/dashboards";
import type { Base, PresentedArtifact } from "../conversation/runtime";
import type { ViewDefinition } from "../views/catalog";
import { readDefinition } from "../views/catalog/compatibility";

export interface Draft {
  definition: unknown;
  original: unknown;
  base: Base;
  origin_artifact_id: string | null;
  dirty: boolean;
}
export const fromDashboard = (dashboard: Dashboard): Draft => ({
  definition: dashboard.definition,
  original: dashboard.definition,
  base: { dashboard_id: dashboard.id, revision: dashboard.revision },
  origin_artifact_id: null,
  dirty: false,
});
export const fromArtifact = (
  item: PresentedArtifact,
  previous?: Draft | null
): Draft => {
  if (item.artifact.kind !== "hearth.view") {
    throw new Error("Select a dashboard view artifact.");
  }
  const sameBase =
    item.base &&
    previous?.base?.dashboard_id === item.base.dashboard_id &&
    previous.base.revision === item.base.revision;
  return {
    definition: item.artifact.definition,
    original: sameBase ? previous.original : item.artifact.definition,
    base: item.base,
    origin_artifact_id: item.artifact.artifact_id,
    dirty: true,
  };
};
export const editDraft = (draft: Draft, definition: ViewDefinition): Draft => ({
  ...draft,
  definition,
  dirty:
    draft.origin_artifact_id !== null ||
    JSON.stringify(definition) !== JSON.stringify(draft.original),
});
/** An unedited generated view without a saved base remains recoverable from its conversation. */
export const isUneditedGeneratedView = (draft: Draft) =>
  draft.origin_artifact_id !== null &&
  !draft.base &&
  draft.definition === draft.original;
export const draftContext = (draft: Draft | null) => {
  if (!draft) {
    return;
  }
  const read = readDefinition(draft.definition);
  if (read.status !== "supported") {
    return;
  }
  return {
    definition: read.definition,
    ...(draft.base
      ? {
          source_dashboard_id: draft.base.dashboard_id,
          source_revision: draft.base.revision,
        }
      : {}),
  };
};
