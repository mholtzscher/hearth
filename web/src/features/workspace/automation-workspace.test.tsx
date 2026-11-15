// Router blockers and deferred fetch updates settle across a full event-loop turn.
import { setTimeout as delay } from "node:timers/promises";

import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { useRef } from "react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { AutomationArtifact } from "../../api/agent";
import { parseAutomationResponse } from "../../api/automation-json";
import type { Automation } from "../../api/types";
import { editAutomationDocument } from "../automations/document";
import { Canvas } from "../dashboards/canvas";
import { WorkspaceProvider, useWorkspace } from "./workspace";

const id = "aut_019a5b00-0000-7000-8000-000000000001";
const raw =
  '{"name":"Exact","enabled":false,"triggers":[],"variables":{"n":9007199254740993},"steps":[{"id":"wait","kind":"delay","duration_ms":1000}]}';
const saved = () =>
  parseAutomationResponse<Automation>(
    `{"id":"${id}","revision":3,"definition":${raw},"created_at":"now","updated_at":"now"}`
  );
const committed = () => {
  const base = saved();
  return {
    ...base,
    revision: 4,
    definition: { ...base.definition, name: "Proposed" },
  };
};
const proposal = {
  kind: "hearth.automation",
  artifact_id: "aart_019a5b00-0000-7000-8000-000000000002",
  schema_version: 1,
  mode: "replace",
  automation_id: id,
  expected_revision: 3,
  definition_json: raw.replace('"Exact"', '"Proposed"'),
} satisfies AutomationArtifact;
const json = (value: unknown, status = 200) => Response.json(value, { status });

const Harness = ({ canvas = false }: { canvas?: boolean }) => {
  const workspace = useWorkspace();
  const version = useRef(0);
  return (
    <>
      <button type="button" onClick={() => workspace.selectAutomation(saved())}>
        Open saved
      </button>
      <button
        type="button"
        onClick={() =>
          workspace.selectArtifact({
            artifact: {
              kind: "hearth.automation",
              schema_version: 1,
              artifact_id: "aart_019a5b00-0000-7000-8000-000000000003",
              mode: "reference",
              automation_id: id,
            },
            base: null,
          })
        }
      >
        Open reference
      </button>
      <button type="button" onClick={() => workspace.newAutomation()}>
        New draft
      </button>
      <button
        type="button"
        onClick={() => {
          version.current = workspace.draftVersion;
        }}
      >
        Start turn
      </button>
      <button
        type="button"
        onClick={() =>
          workspace.receiveArtifact(
            { artifact: proposal, base: null },
            version.current
          )
        }
      >
        Receive proposal
      </button>
      <button
        type="button"
        onClick={() =>
          workspace.receiveArtifact(
            {
              artifact: { ...proposal, automation_id: id.replace(/1$/u, "9") },
              base: null,
            },
            version.current
          )
        }
      >
        Receive different target
      </button>
      <button
        type="button"
        onClick={() => {
          if (workspace.automationDraft) {
            workspace.editAutomation(
              editAutomationDocument(
                workspace.automationDraft.document,
                ["name"],
                '"Manual"'
              )
            );
          }
        }}
      >
        Edit name
      </button>
      <button type="button" onClick={() => workspace.setEditorValid(false)}>
        Invalidate
      </button>
      <button
        type="button"
        onClick={() => {
          void workspace.save();
        }}
      >
        Persist
      </button>
      <button
        type="button"
        onClick={() => {
          void workspace.save(true);
        }}
      >
        Copy
      </button>
      <button
        type="button"
        onClick={() => {
          void workspace.reload();
        }}
      >
        Reload
      </button>
      <output>
        {workspace.automationDraft?.document.definition.name} ·{" "}
        {workspace.automationDraft?.base?.revision} ·{" "}
        {workspace.automationDraft?.dirty ? "dirty" : "clean"}
      </output>
      <pre data-testid="context">
        {JSON.stringify(workspace.automationContext)}
      </pre>
      {workspace.error && <p role="alert">{workspace.error}</p>}
      {canvas && workspace.automationDraft && <Canvas />}
    </>
  );
};
const ProposalControls = () => {
  const workspace = useWorkspace();
  return (
    <>
      <button
        type="button"
        onClick={() =>
          workspace.selectArtifact({ artifact: proposal, base: null })
        }
      >
        Open proposal
      </button>
      <button type="button" onClick={() => workspace.closeDraft()}>
        Close draft
      </button>
    </>
  );
};
const mount = (canvas = false) => {
  const router = createMemoryRouter([
    {
      path: "*",
      element: (
        <WorkspaceProvider>
          <ProposalControls />
          <Harness canvas={canvas} />
        </WorkspaceProvider>
      ),
    },
  ]);
  render(<RouterProvider router={router} />);
  return router;
};
const openProposalWithEdit = () => {
  fireEvent.click(screen.getByText("Open proposal"));
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Temporary" },
  });
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Proposed" },
  });
};
const establishSavedControl = (action: string) => {
  if (action === "save") {
    return screen.getByRole("button", { name: "Save changes" });
  }
  if (action === "reload") {
    return screen.getByText("Reload");
  }
  return screen.getByRole("button", { name: "Discard changes" });
};

describe("automation workspace", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it("keeps a replacement proposal unsaved after editing and reverting its proposed value", () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<(url: unknown, init?: RequestInit) => Promise<Response>>(() =>
        Promise.resolve(json({ items: [] }))
      )
    );
    mount(true);
    openProposalWithEdit();
    expect(screen.getByText("· Unsaved changes").textContent).toBe(
      "· Unsaved changes"
    );
    expect(screen.getByRole("button", { name: "Save changes" })).toHaveProperty(
      "disabled",
      false
    );
  });

  it("blocks closing and navigating away from an edited replacement proposal", async () => {
    const fetcher = vi.fn<
      (url: unknown, init?: RequestInit) => Promise<Response>
    >(() => Promise.resolve(json({ items: [] })));
    vi.stubGlobal("fetch", fetcher);
    const router = mount(true);
    openProposalWithEdit();
    fireEvent.click(screen.getByText("Close draft"));
    expect(
      screen.getByRole("dialog", { name: "Keep your changes?" }).textContent
    ).toContain("This draft has unsaved changes.");
    fireEvent.click(screen.getByRole("button", { name: "Stay" }));
    await act(async () => {
      void router.navigate("/elsewhere");
      await delay(0);
    });
    expect(
      screen.getByRole("dialog", { name: "Keep your changes?" }).textContent
    ).toContain("This draft has unsaved changes.");
    expect(router.state.location.pathname).toBe("/");
    expect(
      fetcher.mock.calls.filter(
        ([, init]) => init?.method && init.method !== "GET"
      )
    ).toStrictEqual([]);
  });

  it.each(["save", "reload", "discard"])(
    "clears pending proposal state after %s establishes a saved definition",
    async (action) => {
      const fetcher = vi.fn<
        (url: unknown, init?: RequestInit) => Promise<Response>
      >((url) =>
        Promise.resolve(
          json(url === `/v1/automations/${id}` ? committed() : { items: [] })
        )
      );
      vi.stubGlobal("fetch", fetcher);
      mount(true);
      openProposalWithEdit();
      fireEvent.click(establishSavedControl(action));
      await waitFor(() =>
        expect(screen.getByRole("status").textContent).toBe(
          "Proposed · 4 · clean"
        )
      );
      const requests = fetcher.mock.calls.filter(
        ([url]) => url === `/v1/automations/${id}`
      );
      expect(requests).toHaveLength(1);
      fireEvent.change(screen.getByLabelText("Name"), {
        target: { value: "Temporary" },
      });
      fireEvent.change(screen.getByLabelText("Name"), {
        target: { value: "Proposed" },
      });
      expect(screen.getByText("· Saved").textContent).toBe("· Saved");
      expect(
        screen.getByRole("button", { name: "Save changes" })
      ).toHaveProperty("disabled", true);
    }
  );

  it("sends a replacement save against the selected base revision", async () => {
    const fetcher = vi.fn<
      (url: unknown, init?: RequestInit) => Promise<Response>
    >((url) =>
      Promise.resolve(
        json(url === `/v1/automations/${id}` ? committed() : { items: [] })
      )
    );
    vi.stubGlobal("fetch", fetcher);
    mount(true);
    openProposalWithEdit();
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toBe(
        "Proposed · 4 · clean"
      )
    );
    const requests = fetcher.mock.calls.filter(
      ([url]) => url === `/v1/automations/${id}`
    );
    expect(requests).toHaveLength(1);
    expect(requests[0][1]).toMatchObject({
      method: "PUT",
      body: `{"expected_revision":3,"definition":${proposal.definition_json}}`,
    });
  });

  it("keeps a saved reference clean when an edit is reverted", async () => {
    vi.stubGlobal("fetch", (url: unknown) =>
      Promise.resolve(
        json(url === `/v1/automations/${id}` ? saved() : { items: [] })
      )
    );
    mount(true);
    fireEvent.click(screen.getByText("Open reference"));
    await screen.findByRole("button", { name: "Edit" });
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Temporary" },
    });
    expect(screen.getByText("· Unsaved changes").textContent).toBe(
      "· Unsaved changes"
    );
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Exact" },
    });
    expect(screen.getByText("· Saved").textContent).toBe("· Saved");
    expect(screen.getByRole("button", { name: "Save changes" })).toHaveProperty(
      "disabled",
      true
    );
    fireEvent.click(screen.getByText("Close draft"));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByLabelText("Name")).toBeNull();
  });

  it("uses the exact selected source/base for revisioned Save and never retries a conflict", async () => {
    const requests: RequestInit[] = [];
    vi.stubGlobal("fetch", (_url: unknown, init: RequestInit) => {
      requests.push(init);
      return Promise.resolve(json({ title: "Conflict" }, 409));
    });
    mount();
    fireEvent.click(screen.getByText("Open saved"));
    fireEvent.click(screen.getByText("Edit name"));
    fireEvent.click(screen.getByText("Persist"));
    await screen.findByRole("alert");
    expect(requests).toHaveLength(1);
    expect(requests[0].method).toBe("PUT");
    expect(requests[0].body).toBe(
      `{"expected_revision":3,"definition":${raw.replace('"Exact"', '"Manual"')}}`
    );
    expect(screen.getByRole("status").textContent).toBe("Manual · 3 · dirty");
    expect(screen.getByTestId("context").textContent).toContain(
      "9007199254740993"
    );
  });

  it("serializes rapid Save clicks into one request and adopts the authoritative new revision", async () => {
    const { promise: pending, resolve: finish } =
      Promise.withResolvers<Response>();
    const fetcher = vi.fn<() => Promise<Response>>(() => pending);
    vi.stubGlobal("fetch", fetcher);
    mount();
    fireEvent.click(screen.getByText("Open saved"));
    fireEvent.click(screen.getByText("Edit name"));
    fireEvent.click(screen.getByText("Persist"));
    fireEvent.click(screen.getByText("Persist"));
    expect(fetcher).toHaveBeenCalledOnce();
    await act(async () => {
      finish(
        json({
          ...saved(),
          revision: 4,
          definition: { ...saved().definition, name: "Committed" },
        })
      );
      await delay(0);
    });
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toBe(
        "Committed · 4 · clean"
      )
    );
  });

  it("invalidates automatic replacements after manual edits or a target/base mismatch", () => {
    mount();
    fireEvent.click(screen.getByText("Open saved"));
    fireEvent.click(screen.getByText("Start turn"));
    fireEvent.click(screen.getByText("Receive different target"));
    expect(screen.getByRole("status").textContent).toBe("Exact · 3 · clean");
    fireEvent.click(screen.getByText("Receive proposal"));
    expect(screen.getByRole("status").textContent).toBe("Proposed · 3 · dirty");
    fireEvent.click(screen.getByText("Edit name"));
    fireEvent.click(screen.getByText("Receive proposal"));
    expect(screen.getByRole("status").textContent).toBe("Manual · 3 · dirty");
  });

  it("keeps invalid edits and route navigation under the same blocker", async () => {
    const fetcher = vi.fn<() => Promise<Response>>();
    vi.stubGlobal("fetch", fetcher);
    const router = mount();
    fireEvent.click(screen.getByText("Open saved"));
    fireEvent.click(screen.getByText("Invalidate"));
    fireEvent.click(screen.getByText("Persist"));
    expect(fetcher).not.toHaveBeenCalled();
    await act(async () => {
      void router.navigate("/elsewhere");
      await delay(0);
    });
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("button", { name: "Save" })).toHaveProperty(
      "disabled",
      true
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Stay" }));
    expect(router.state.location.pathname).toBe("/");
    expect(screen.getByRole("status").textContent).toBe("Exact · 3 · clean");
  });

  it("makes an ambiguous create recoverable without automatically sending it again", async () => {
    const fetcher = vi.fn<
      (url: unknown, init?: RequestInit) => Promise<Response>
    >(() => Promise.reject(new Error("Connection lost")));
    vi.stubGlobal("fetch", fetcher);
    mount();
    fireEvent.click(screen.getByText("New draft"));
    fireEvent.click(screen.getByText("Persist"));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Creation may have succeeded");
    expect(fetcher).toHaveBeenCalledOnce();
    expect(screen.getByRole("status").textContent).toContain("New Automation");
  });

  it("does not replace edits made while a saved reference is loading", async () => {
    const { promise: pending, resolve: finish } =
      Promise.withResolvers<Response>();
    vi.stubGlobal("fetch", () => pending);
    mount();
    fireEvent.click(screen.getByText("Open saved"));
    fireEvent.click(screen.getByText("Open reference"));
    fireEvent.click(screen.getByText("Edit name"));
    await act(async () => {
      finish(json(saved()));
      await delay(0);
    });
    expect(screen.getByRole("status").textContent).toBe("Manual · 3 · dirty");
  });

  it("keeps invalid editor text mounted when a selected reference finishes loading", async () => {
    const { promise: pending, resolve: finish } =
      Promise.withResolvers<Response>();
    vi.stubGlobal("fetch", (url: string) =>
      url === `/v1/automations/${id}`
        ? pending
        : Promise.resolve(json({ items: [] }))
    );
    mount(true);
    fireEvent.click(screen.getByText("Open saved"));
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.click(screen.getByText("Open reference"));
    fireEvent.change(screen.getByLabelText("Wait · milliseconds"), {
      target: { value: "0" },
    });
    await act(async () => {
      finish(json(saved()));
      await delay(0);
    });
    expect(screen.getByLabelText("Wait · milliseconds")).toHaveProperty(
      "value",
      "0"
    );
    expect(screen.getByRole("button", { name: "Save changes" })).toHaveProperty(
      "disabled",
      true
    );
  });
});
