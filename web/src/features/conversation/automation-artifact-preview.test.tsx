import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { AutomationArtifact } from "../../api/agent";
import { AutomationArtifactPreview } from "./automation-artifact-preview";

describe("Automation artifact preview", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("shows an inline error when a saved reference has an invalid definition", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>().mockResolvedValue(
        Response.json(
          {
            automation_id: "aut_01920000-0000-7000-8000-000000000001",
            revision: 1,
            definition: {
              name: "Invalid saved definition",
              enabled: true,
              triggers: [],
              steps: [],
            },
          },
          { status: 200 }
        )
      )
    );
    const artifact: AutomationArtifact = {
      kind: "hearth.automation",
      artifact_id: "reference",
      schema_version: 1,
      mode: "reference",
      automation_id: "aut_01920000-0000-7000-8000-000000000001",
    };
    render(
      <AutomationArtifactPreview
        artifact={artifact}
        active
        open={false}
        onSelect={vi.fn<() => void>()}
      />
    );
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe(
      "Could not load this Automation. Open it in the canvas to try again."
    );
    expect(screen.queryByText("Loading Automation…")).toBeNull();
    expect(screen.getByRole("button", { name: "Open in canvas" })).toBeTruthy();
  });
});
