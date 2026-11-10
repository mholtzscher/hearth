import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createMemoryRouter, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { ViewDataProvider } from "../views/data/provider";
import { Canvas } from "./Canvas";
import { WorkspaceProvider } from "./Workspace";

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

const id = "ent_019a3e6a-4111-7000-8000-000000000001";
const entity = { id, type: "hearth.temperature/v1", name: "Temperature", enabled: true, availability: { status: "available" }, support: {},
  quantities: { "": { unit: "Cel", label: "°C", fraction_digits: 1 } }, state: { value: 21.5, observed_at: "2026-11-05T22:00:00Z" } };
const definition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Climate", spec: { root: "metric", elements: {
  metric: { type: "Metric", props: { label: "Living room", source: { kind: "entity-state", entity_id: id, entity_type: entity.type } } },
} } };

function mount() {
  vi.spyOn(globalThis, "fetch").mockImplementation(async input => {
    const path = String(input);
    if (path === "/v1/dashboards/dash-1") return new Response(JSON.stringify({ id: "dash-1", revision: 1, definition, created_at: "", updated_at: "" }));
    if (path === `/v1/entities/${id}`) return new Response(JSON.stringify(entity));
    if (path.startsWith("/v1/entities?")) return new Response(JSON.stringify({ items: [entity] }));
    throw new Error(`Unexpected request ${path}`);
  });
  function Root() {
    return <ViewDataProvider><WorkspaceProvider><Outlet /></WorkspaceProvider></ViewDataProvider>;
  }
  const router = createMemoryRouter([{ path: "/", element: <Root />, children: [
    { path: "dashboards/:id", element: <Canvas dashboardPage /> },
    { path: "entities/:entityId", element: <p>Entity detail route</p> },
  ] }], { initialEntries: ["/dashboards/dash-1"] });
  render(<RouterProvider router={router} />);
  return router;
}

it.each([
  { state: "invalid", phone: false }, { state: "unsaved", phone: false },
  { state: "invalid", phone: true }, { state: "unsaved", phone: true },
])("guards Source navigation with $state input (phone=$phone)", async ({ state, phone }) => {
  vi.stubGlobal("matchMedia", () => ({ matches: phone, addEventListener() {}, removeEventListener() {} }));
  const router = mount();
  const card = await screen.findByRole("article", { name: "Living room" });
  await within(card).findByText("21.5");
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  const text = state === "invalid" ? "" : "Edited climate";
  fireEvent.change(screen.getByLabelText("Dashboard title"), { target: { value: text } });
  fireEvent.click(within(card).getByRole("button", { name: "Source" }));
  fireEvent.click(within(screen.getByRole("dialog", { name: "Reading details" })).getByRole("link", { name: "Temperature" }));
  const dialog = await screen.findByRole("dialog", { name: "Keep your changes?" });
  expect(screen.queryByRole("dialog", { name: "Reading details" })).toBeNull();
  expect(router.state.location.pathname).toBe("/dashboards/dash-1");
  expect(within(dialog).getByRole("button", { name: "Save" })).toHaveProperty("disabled", state === "invalid");
  fireEvent.click(within(dialog).getByRole("button", { name: "Stay" }));
  expect(screen.getByLabelText("Dashboard title")).toHaveProperty("value", text);
  fireEvent.click(within(card).getByRole("button", { name: "Source" }));
  fireEvent.click(within(screen.getByRole("dialog", { name: "Reading details" })).getByRole("link", { name: "Temperature" }));
  fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Discard" }));
  expect(await screen.findByText("Entity detail route")).toBeTruthy();
  expect(router.state.location.pathname).toBe(`/entities/${id}`);
});
