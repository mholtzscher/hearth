import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { createMemoryRouter, Link, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import AgentPage from "../../pages/AgentPage";
import { ViewDataProvider } from "../views/data/provider";
import { Canvas } from "./Canvas";
import { DashboardLibrary } from "./DashboardLibrary";
import { WorkspaceProvider } from "./Workspace";

const definition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Warm house", spec: { root: "heading", elements: { heading: { type: "Heading", props: { text: "Hello home" } } } } };
const dashboard = { id: "dash-1", revision: 1, definition, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" };
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json" } });
function Root() { return <ViewDataProvider><WorkspaceProvider><nav><Link to="/dashboards">Library</Link><Link to="/">Home</Link></nav><Outlet /></WorkspaceProvider></ViewDataProvider>; }
function mount(path: string) {
  const router = createMemoryRouter([{ path: "/", element: <Root />, children: [
    { index: true, element: <AgentPage /> }, { path: "conversations/:id", element: <AgentPage /> },
    { path: "dashboards", element: <DashboardLibrary /> }, { path: "dashboards/:id", element: <Canvas dashboardPage /> },
  ] }], { initialEntries: [path] });
  render(<RouterProvider router={router} />);
  return router;
}
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it("renders saved dashboards independently, guards edits, and sends a revision-checked save", async () => {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/dashboards/dash-1" && init?.method === "PUT") return json({ ...dashboard, revision: 2, definition: JSON.parse(String(init.body)).definition });
    if (path === "/v1/dashboards/dash-1") return json(dashboard);
    if (path.startsWith("/v1/entities")) return json({ items: [] });
    if (path === "/v1/dashboards") return json({ items: [] });
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const router = mount("/dashboards/dash-1");
  expect(await screen.findByText("Hello home")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Customize" }));
  fireEvent.change(screen.getByLabelText("Dashboard title"), { target: { value: "Edited" } });
  fireEvent.click(screen.getByText("Library"));
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByText("Keep your changes?")).toBeTruthy();
  fireEvent.click(within(dialog).getByRole("button", { name: "Stay" }));
  expect(router.state.location.pathname).toBe("/dashboards/dash-1");
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(fetcher.mock.calls.some(([url, init]) => url === "/v1/dashboards/dash-1" && init?.method === "PUT" && JSON.parse(String(init.body)).expected_revision === 1)).toBe(true));
  expect(await screen.findByText(/revision 2/)).toBeTruthy();
});

it("uses streamed artifacts and reconciles committed history after failure without replay", async () => {
  const artifact = { kind: "hearth.view", artifact_id: "view-1", definition };
  const rows = [
    { message_id: 1, role: "user", content: "show temperatures", tool_calls: null, created_at: "" },
    { message_id: 2, role: "assistant", content: "", calls: [{ id: "call-1", name: "present_view", arguments: "{}" }], tool_calls: ["present_view"], created_at: "" },
    { message_id: 3, role: "tool", content: "ok", tool_call_id: "call-1", tool_name: "present_view", artifacts: [artifact], created_at: "" },
  ];
  let historyReads = 0;
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path === "/v1/agent/conversations") return json({ conversations: [] });
    if (path.endsWith("/messages/stream")) return new Response(`data: ${JSON.stringify({ type: "view.presented", call_id: "call-1", artifact })}\n\ndata: ${JSON.stringify({ type: "turn.failed", error: "Model failed" })}\n\n`, { status: 200 });
    if (path.endsWith("/messages")) return json({ messages: ++historyReads === 1 ? [] : rows });
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  mount("/conversations/conv-1");
  const input = await screen.findByLabelText("Ask Hearth");
  fireEvent.change(input, { target: { value: "show temperatures" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  expect(await screen.findByText("Model failed")).toBeTruthy();
  await waitFor(() => expect(screen.getAllByText("Generated view")).toHaveLength(1));
  expect((input as HTMLTextAreaElement).value).toBe("show temperatures");
  expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/messages/stream"))).toHaveLength(1);
});

it("keeps a conflicting draft and allows saving it as a new dashboard", async () => {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/dashboards/dash-1" && init?.method === "PUT") return json({ title: "Conflict", detail: "Changed" }, 409);
    if (path === "/v1/dashboards/dash-1") return json(dashboard);
    if (path === "/v1/dashboards" && init?.method === "POST") return json({ ...dashboard, id: "dash-2", definition: JSON.parse(String(init.body)).definition });
    if (path.startsWith("/v1/entities")) return json({ items: [] });
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  mount("/dashboards/dash-1");
  await screen.findByText("Hello home");
  fireEvent.click(screen.getByRole("button", { name: "Customize" }));
  fireEvent.change(screen.getByLabelText("Dashboard title"), { target: { value: "My draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  expect(await screen.findByText(/changed elsewhere/)).toBeTruthy();
  expect((screen.getByLabelText("Dashboard title") as HTMLInputElement).value).toBe("My draft");
  fireEvent.click(screen.getAllByRole("button", { name: "Save as new" })[0]);
  await waitFor(() => expect(fetcher.mock.calls.some(([url, init]) => url === "/v1/dashboards" && init?.method === "POST")).toBe(true));
  expect(await screen.findByText(/revision 1 · Saved/)).toBeTruthy();
});

it("attaches the selected draft as context and keeps its revision as the generated view's save base", async () => {
  const updated = { ...definition, title: "Warmer view" };
  const artifact = { kind: "hearth.view", artifact_id: "view-revised", definition: updated };
  let sentBody: { text: string; view_context: unknown } | undefined;
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/dashboards/dash-1" && init?.method === "PUT") return json({ ...dashboard, revision: 2, definition: JSON.parse(String(init.body)).definition });
    if (path === "/v1/dashboards/dash-1") return json(dashboard);
    if (path === "/v1/agent/conversations" && init?.method === "POST") return json({ id: "conv-new" });
    if (path === "/v1/agent/conversations") return json({ conversations: [] });
    if (path.endsWith("/messages/stream")) {
      sentBody = JSON.parse(String(init?.body));
      return new Response(`data: ${JSON.stringify({ type: "view.presented", call_id: "c", artifact })}\n\ndata: {"type":"turn.finished"}\n\n`);
    }
    if (path.endsWith("/messages")) return json({ messages: [
      { message_id: 1, role: "user", content: "make warmer", view_context: sentBody?.view_context, created_at: "" },
      { message_id: 2, role: "assistant", content: "", calls: [{ id: "c", name: "present_view", arguments: "{}" }], created_at: "" },
      { message_id: 3, role: "tool", content: "ok", tool_call_id: "c", artifacts: [artifact], created_at: "" },
    ] });
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  mount("/dashboards/dash-1");
  await screen.findByText("Hello home");
  fireEvent.click(screen.getByRole("button", { name: "Customize with Hearth" }));
  const input = await screen.findByLabelText("Ask Hearth");
  fireEvent.change(input, { target: { value: "make warmer" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  await waitFor(() => expect(sentBody).toEqual({ text: "make warmer", view_context: { definition, source_dashboard_id: "dash-1", source_revision: 1 } }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Save changes" })).not.toHaveProperty("disabled", true));
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(fetcher.mock.calls.some(([url, init]) => url === "/v1/dashboards/dash-1" && init?.method === "PUT" && JSON.parse(String(init.body)).expected_revision === 1)).toBe(true));
});
