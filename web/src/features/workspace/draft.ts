import type { AutomationArtifact, AutomationContext } from "../../api/agent";
import type { Draft } from "../dashboards/draft";
import { readAutomationDocument, type AutomationDraft, type AutomationDocument } from "../automations/document";

export type CanvasDraft = Draft | AutomationDraft;
export function isAutomationDraft(draft: CanvasDraft | null): draft is AutomationDraft { return !!draft && "kind" in draft && draft.kind === "automation"; }
export function automationDraftContext(draft: AutomationDraft): AutomationContext {
  return draft.mode === "replace" ? { mode: "replace", automation_id: draft.base.automation_id, expected_revision: draft.base.revision, definition_json: draft.document.source } : { mode: "create", definition_json: draft.document.source };
}
export function fromAutomationArtifact(artifact: Exclude<AutomationArtifact, { mode: "reference" }>, previous?: AutomationDraft | null): AutomationDraft {
  const document = readAutomationDocument(artifact.definition_json);
  const sameBase = artifact.mode === "replace" && previous?.mode === "replace" && previous.base.automation_id === artifact.automation_id && previous.base.revision === artifact.expected_revision;
  const common = { kind: "automation" as const, document, original: sameBase ? previous.original : document, origin_artifact_id: artifact.artifact_id, pendingProposal: true, dirty: true };
  return artifact.mode === "create" ? { ...common, mode: "create", base: null } : { ...common, mode: "replace", base: { automation_id: artifact.automation_id, revision: artifact.expected_revision } };
}
export function editAutomationDraft(draft: AutomationDraft, document: AutomationDocument): AutomationDraft {
  return { ...draft, document, dirty: draft.mode === "create" || draft.pendingProposal || document.source !== draft.original.source };
}
