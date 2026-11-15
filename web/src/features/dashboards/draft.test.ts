import { describe, expect, it } from "vitest";

import type { ViewDefinition } from "../views/catalog";
import {
  draftContext,
  editDraft,
  fromArtifact,
  fromDashboard,
  isUneditedGeneratedView,
} from "./draft";

const definition: ViewDefinition = {
  schema_version: 1,
  catalog_version: "hearth.dashboard/v1",
  title: "Home",
  spec: {
    root: "heading",
    elements: { heading: { type: "Heading", props: { text: "Hello" } } },
  },
};

describe("dashboard drafts", () => {
  it("keeps a saved base and marks changes without mutating the original", () => {
    const draft = fromDashboard({
      id: "dash-1",
      revision: 3,
      definition,
      created_at: "",
      updated_at: "",
    });
    const edited = editDraft(draft, { ...definition, title: "New" });
    expect(draft.dirty).toBeFalsy();
    expect(edited.dirty).toBeTruthy();
    expect(definition.title).toBe("Home");
    expect(draftContext(edited)).toStrictEqual({
      definition: edited.definition,
      source_dashboard_id: "dash-1",
      source_revision: 3,
    });
    expect(editDraft(edited, definition).dirty).toBeFalsy();
  });

  it("preserves unsupported generated definitions without exposing them as model context", () => {
    const draft = fromArtifact({
      artifact: {
        kind: "hearth.view",
        artifact_id: "view-1",
        definition: { schema_version: 2 },
      },
      base: null,
    });
    expect(draft.dirty).toBeTruthy();
    expect(draft.definition).toStrictEqual({ schema_version: 2 });
    expect(draftContext(draft)).toBeUndefined();
  });

  it("treats only an unedited, unsaved generated view as recoverable from its conversation", () => {
    const generated = fromArtifact({
      artifact: { kind: "hearth.view", artifact_id: "view-1", definition },
      base: null,
    });
    const revision = fromArtifact({
      artifact: { kind: "hearth.view", artifact_id: "view-2", definition },
      base: { dashboard_id: "dash-1", revision: 3 },
    });
    expect(isUneditedGeneratedView(generated)).toBeTruthy();
    expect(
      isUneditedGeneratedView(
        editDraft(generated, { ...definition, title: "Edited" })
      )
    ).toBeFalsy();
    expect(isUneditedGeneratedView(revision)).toBeFalsy();
    expect(
      isUneditedGeneratedView(
        fromDashboard({
          id: "dash-1",
          revision: 3,
          definition,
          created_at: "",
          updated_at: "",
        })
      )
    ).toBeFalsy();
  });
});
