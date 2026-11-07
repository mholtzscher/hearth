import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { WorkspaceProvider } from "../features/workspace/Workspace";
import AutomationDetailPage from "./AutomationDetailPage";

const id = "aut_019a5b00-0000-7000-8000-000000000001";
const definition = { name: "Function Automation", enabled: false, triggers: [], steps: [{ id: "refresh", kind: "function", adapter_id: "sim-healthy", function_key: "inspect", contract_fingerprint: `sha256-v1:${"a".repeat(64)}`, arguments: {} }] };
const saved = { id, revision: 1, definition, created_at: "now", updated_at: "now" };
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
function mount() { render(<RouterProvider router={createMemoryRouter([{ path: "/automations/:automationId", element: <WorkspaceProvider><AutomationDetailPage /></WorkspaceProvider> }], { initialEntries: [`/automations/${id}`] })} />); }
async function openEditor() { fireEvent.click(await screen.findByRole("button", { name: "Edit Automation" })); return screen.findByLabelText("Name"); }

it("keeps the draft editor and Save-as-new recovery after the saved definition is deleted", async () => {
  let gone = false;
  vi.stubGlobal("fetch", async (url: string) => url === `/v1/automations/${id}` ? gone ? json({ title: "Not Found", code: "not_found", detail: "automation resource not found" }, 404) : json(saved) : json({ items: [] }));
  mount(); await openEditor();
  fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "My retained draft" } });
  gone = true; fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await screen.findByText("automation resource not found");
  expect(screen.getByLabelText("Name")).toHaveProperty("value", "My retained draft");
  const canvas = screen.getByRole("region", { name: "Automation canvas" });
  expect(within(canvas).getByRole("button", { name: "Save as new" })).toHaveProperty("disabled", false);
});

it("shows an actionable Step diagnostic for a rejected Function reference", async () => {
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => url === `/v1/automations/${id}` ? init?.method === "PUT" ? json({ title: "Unprocessable Entity", detail: "invalid automation Function reference", diagnostics: [{ code: "function_contract_changed", path: "/definition/steps/0", step_id: "refresh" }], errors: [{ location: "body.definition.steps[0]", message: "step refresh: function_contract_changed" }] }, 422) : json(saved) : json({ items: [] }));
  mount(); await openEditor();
  fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "Changed" } });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(screen.getByRole("region", { name: "Automation canvas" }).textContent).toContain("function_contract_changed"));
  expect(screen.getByRole("button", { name: "Show Step refresh" })).toHaveProperty("disabled", false);
  fireEvent.click(screen.getByRole("button", { name: "Show Step refresh" }));
  await waitFor(() => expect((document.activeElement?.closest("[data-automation-step-id]") as HTMLElement | null)?.dataset.automationStepId).toBe("refresh"));
  expect(screen.getByLabelText("Name")).toHaveProperty("value", "Changed");
});
