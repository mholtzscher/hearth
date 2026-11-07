import { useRef } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { parseAutomationResponse } from "../../api/automation-json";
import type { Automation } from "../../api/types";
import type { AutomationArtifact } from "../../api/agent";
import { editAutomationDocument } from "../automations/document";
import { WorkspaceProvider, useWorkspace } from "./Workspace";
import { Canvas } from "../dashboards/Canvas";

const id = "aut_019a5b00-0000-7000-8000-000000000001";
const raw = '{"name":"Exact","enabled":false,"triggers":[],"variables":{"n":9007199254740993},"steps":[{"id":"wait","kind":"delay","duration_ms":1000}]}';
const saved = () => parseAutomationResponse<Automation>(`{"id":"${id}","revision":3,"definition":${raw},"created_at":"now","updated_at":"now"}`);
const proposal = { kind: "hearth.automation", artifact_id: "aart_019a5b00-0000-7000-8000-000000000002", schema_version: 1, mode: "replace", automation_id: id, expected_revision: 3, definition_json: raw.replace('"Exact"', '"Proposed"') } satisfies AutomationArtifact;

function Harness({ canvas = false }: { canvas?: boolean }) {
  const workspace = useWorkspace(); const version = useRef(0);
  return <><button onClick={() => workspace.selectAutomation(saved())}>Open saved</button><button onClick={() => workspace.selectArtifact({ artifact: { kind: "hearth.automation", schema_version: 1, artifact_id: "aart_019a5b00-0000-7000-8000-000000000003", mode: "reference", automation_id: id }, base: null })}>Open reference</button><button onClick={workspace.newAutomation}>New draft</button><button onClick={() => { version.current = workspace.draftVersion; }}>Start turn</button><button onClick={() => workspace.receiveArtifact({ artifact: proposal, base: null }, version.current)}>Receive proposal</button><button onClick={() => workspace.receiveArtifact({ artifact: { ...proposal, automation_id: id.replace(/1$/, "9") }, base: null }, version.current)}>Receive different target</button><button onClick={() => { if (workspace.automationDraft) workspace.editAutomation(editAutomationDocument(workspace.automationDraft.document, ["name"], '"Manual"')); }}>Edit name</button><button onClick={() => workspace.setEditorValid(false)}>Invalidate</button><button onClick={() => void workspace.save()}>Persist</button><button onClick={() => void workspace.save(true)}>Copy</button><button onClick={() => void workspace.reload()}>Reload</button><output>{workspace.automationDraft?.document.definition.name} · {workspace.automationDraft?.base?.revision} · {workspace.automationDraft?.dirty ? "dirty" : "clean"}</output><pre data-testid="context">{JSON.stringify(workspace.automationContext)}</pre>{workspace.error && <p role="alert">{workspace.error}</p>}{canvas && workspace.automationDraft && <Canvas />}</>;
}
function mount(canvas = false) {
  const router = createMemoryRouter([{ path: "*", element: <WorkspaceProvider><Harness canvas={canvas} /></WorkspaceProvider> }]);
  render(<RouterProvider router={router} />); return router;
}
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); localStorage.clear(); });

it("uses the exact selected source/base for revisioned Save and never retries a conflict", async () => {
  const requests: RequestInit[] = [];
  vi.stubGlobal("fetch", async (_url: unknown, init: RequestInit) => { requests.push(init); return json({ title: "Conflict" }, 409); });
  mount(); fireEvent.click(screen.getByText("Open saved")); fireEvent.click(screen.getByText("Edit name")); fireEvent.click(screen.getByText("Persist"));
  await screen.findByRole("alert");
  expect(requests).toHaveLength(1);
  expect(requests[0].method).toBe("PUT");
  expect(requests[0].body).toBe(`{"expected_revision":3,"definition":${raw.replace('"Exact"', '"Manual"')}}`);
  expect(screen.getByRole("status").textContent).toBe("Manual · 3 · dirty");
  expect(screen.getByTestId("context").textContent).toContain("9007199254740993");
});

it("serializes rapid Save clicks into one request and adopts the authoritative new revision", async () => {
  let finish!: (response: Response) => void;
  const pending = new Promise<Response>(resolve => { finish = resolve; });
  const fetcher = vi.fn(async () => pending); vi.stubGlobal("fetch", fetcher);
  mount(); fireEvent.click(screen.getByText("Open saved")); fireEvent.click(screen.getByText("Edit name"));
  fireEvent.click(screen.getByText("Persist")); fireEvent.click(screen.getByText("Persist"));
  expect(fetcher).toHaveBeenCalledTimes(1);
  await act(async () => finish(json({ ...saved(), revision: 4, definition: { ...saved().definition, name: "Committed" } })));
  await waitFor(() => expect(screen.getByRole("status").textContent).toBe("Committed · 4 · clean"));
});

it("invalidates automatic replacements after manual edits or a target/base mismatch", () => {
  mount(); fireEvent.click(screen.getByText("Open saved")); fireEvent.click(screen.getByText("Start turn"));
  fireEvent.click(screen.getByText("Receive different target")); expect(screen.getByRole("status").textContent).toBe("Exact · 3 · clean");
  fireEvent.click(screen.getByText("Receive proposal")); expect(screen.getByRole("status").textContent).toBe("Proposed · 3 · dirty");
  fireEvent.click(screen.getByText("Edit name")); fireEvent.click(screen.getByText("Receive proposal")); expect(screen.getByRole("status").textContent).toBe("Manual · 3 · dirty");
});

it("keeps invalid edits and route navigation under the same blocker", async () => {
  const fetcher = vi.fn(); vi.stubGlobal("fetch", fetcher);
  const router = mount(); fireEvent.click(screen.getByText("Open saved")); fireEvent.click(screen.getByText("Invalidate")); fireEvent.click(screen.getByText("Persist"));
  expect(fetcher).not.toHaveBeenCalled();
  await act(async () => { void router.navigate("/elsewhere"); });
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByRole("button", { name: "Save" })).toHaveProperty("disabled", true);
  fireEvent.click(within(dialog).getByRole("button", { name: "Stay" }));
  expect(router.state.location.pathname).toBe("/");
  expect(screen.getByRole("status").textContent).toBe("Exact · 3 · clean");
});

it("makes an ambiguous create recoverable without automatically sending it again", async () => {
  const fetcher = vi.fn(async () => { throw new Error("Connection lost"); }); vi.stubGlobal("fetch", fetcher);
  mount(); fireEvent.click(screen.getByText("New draft")); fireEvent.click(screen.getByText("Persist"));
  expect((await screen.findByRole("alert")).textContent).toContain("Creation may have succeeded");
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("status").textContent).toContain("New Automation");
});

it("does not replace edits made while a saved reference is loading", async () => {
  let finish!: (response: Response) => void;
  vi.stubGlobal("fetch", () => new Promise<Response>(resolve => { finish = resolve; }));
  mount(); fireEvent.click(screen.getByText("Open saved")); fireEvent.click(screen.getByText("Open reference")); fireEvent.click(screen.getByText("Edit name"));
  await act(async () => finish(json(saved())));
  expect(screen.getByRole("status").textContent).toBe("Manual · 3 · dirty");
});

it("keeps invalid editor text mounted when a selected reference finishes loading", async () => {
  let finish!: (response: Response) => void;
  vi.stubGlobal("fetch", (url: string) => url === `/v1/automations/${id}` ? new Promise<Response>(resolve => { finish = resolve; }) : Promise.resolve(json({ items: [] })));
  mount(true); fireEvent.click(screen.getByText("Open saved")); fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  fireEvent.click(screen.getByText("Open reference"));
  fireEvent.change(screen.getByLabelText("Wait · milliseconds"), { target: { value: "0" } });
  await act(async () => finish(json(saved())));
  expect(screen.getByLabelText("Wait · milliseconds")).toHaveProperty("value", "0");
  expect(screen.getByRole("button", { name: "Save changes" })).toHaveProperty("disabled", true);
});
