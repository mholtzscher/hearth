import { describe, expect, it } from "vitest";
import { draftContext, editDraft, fromArtifact, fromDashboard } from "./draft";
import type { ViewDefinition } from "../views/catalog";

const definition: ViewDefinition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Home", spec: { root: "heading", elements: { heading: { type: "Heading", props: { text: "Hello" } } } } };

describe("dashboard drafts", () => {
  it("keeps a saved base and marks changes without mutating the original", () => {
    const draft = fromDashboard({ id: "dash-1", revision: 3, definition, created_at: "", updated_at: "" });
    const edited = editDraft(draft, { ...definition, title: "New" });
    expect(draft.dirty).toBe(false);
    expect(edited.dirty).toBe(true);
    expect(definition.title).toBe("Home");
    expect(draftContext(edited)).toEqual({ definition: edited.definition, source_dashboard_id: "dash-1", source_revision: 3 });
    expect(editDraft(edited, definition).dirty).toBe(false);
  });
  it("preserves unsupported generated definitions without exposing them as model context", () => {
    const draft = fromArtifact({ artifact: { kind: "hearth.view", artifact_id: "view-1", definition: { schema_version: 2 } }, base: null });
    expect(draft.dirty).toBe(true);
    expect(draft.definition).toEqual({ schema_version: 2 });
    expect(draftContext(draft)).toBeUndefined();
  });
});
