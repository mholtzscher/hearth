import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useRef } from "react";
import { createMemoryRouter, Link, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import AgentPage from "../../pages/AgentPage";
import { setBaseUrl } from "../../api/client";
import { ViewDataProvider } from "../views/data/provider";
import { Canvas } from "./Canvas";
import { DashboardLibrary } from "./DashboardLibrary";
import { useWorkspace, WorkspaceProvider } from "./Workspace";
import type { Draft } from "./draft";

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
afterEach(() => { cleanup(); setBaseUrl(""); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

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

it("retains streamed evidence when history is unreachable and requires explicit recovery before another send", async () => {
  const artifact = { kind: "hearth.view", artifact_id: "view-1", definition };
  const rows = [
    { message_id: 1, role: "user", content: "show view", created_at: "" },
    { message_id: 2, role: "assistant", content: "", calls: [{ id: "c", name: "present_view", arguments: "{}" }], created_at: "" },
    { message_id: 3, role: "tool", content: "ok", tool_call_id: "c", artifacts: [artifact], created_at: "" },
  ];
  let histories = 0;
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path === "/v1/agent/conversations") return json({ conversations: [] });
    if (path.endsWith("/messages/stream")) return new Response(`data: ${JSON.stringify({ type: "view.presented", call_id: "c", artifact })}\n\n`);
    if (path.endsWith("/messages")) { histories++; if (histories === 2) throw new Error("offline"); return json({ messages: histories === 1 ? [] : rows }); }
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  mount("/conversations/conv-1");
  const input = await screen.findByLabelText("Ask Hearth");
  await waitFor(() => expect(input).not.toHaveProperty("disabled", true));
  fireEvent.change(input, { target: { value: "show view" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  const recover = await screen.findByRole("button", { name: "Retry loading history" });
  await waitFor(() => expect(screen.getAllByText("Generated view")).toHaveLength(1));
  expect(screen.getByRole("button", { name: "Retry message" })).toHaveProperty("disabled", true);
  fireEvent.click(recover);
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry loading history" })).toBeNull());
  expect(screen.getAllByText("Generated view")).toHaveLength(1);
  expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/messages/stream"))).toHaveLength(1);
});

it("requires confirmation and the displayed revision before deleting", async () => {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === "DELETE") return new Response(null, { status: 204 });
    if (String(input) === "/v1/dashboards") return json({ items: [{ id: "dash-1", title: "Warm house", revision: 3, created_at: "2026-01-01", updated_at: "2026-01-01" }] });
    throw new Error(`Unexpected request ${String(input)}`);
  });
  vi.stubGlobal("fetch", fetcher);
  mount("/dashboards");
  fireEvent.click(await screen.findByRole("button", { name: "Delete Warm house" }));
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "DELETE")).toBe(false);
  const dialog = screen.getByRole("dialog");
  fireEvent.click(within(dialog).getByRole("button", { name: "Delete dashboard" }));
  await waitFor(() => expect(fetcher.mock.calls.some(([url, init]) => String(url) === "/v1/dashboards/dash-1?expected_revision=3" && init?.method === "DELETE")).toBe(true));
});

it("preserves unsupported saved JSON and disables editing and copying", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json({ ...dashboard, definition: { schema_version: 2, some_future_field: true } })));
  mount("/dashboards/dash-1");
  expect(await screen.findByText("Download original definition")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Customize" })).toHaveProperty("disabled", true);
  expect(screen.getByRole("button", { name: "Save as new" })).toHaveProperty("disabled", true);
});

it("moves focus into an expanded canvas and restores it to the invoking control", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json(dashboard)));
  mount("/dashboards/dash-1");
  await screen.findByText("Hello home");
  const expand = screen.getByRole("button", { name: "Expand" });
  expand.focus(); fireEvent.click(expand);
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("region", { name: "View canvas" })));
  fireEvent.click(screen.getByRole("button", { name: "Close expanded canvas" }));
  await waitFor(() => expect(document.activeElement).toBe(expand));
});

function DraftRaceHarness() {
  const workspace = useWorkspace();
  const captured = useRef<Draft | null>(null);
  const artifact = { kind: "hearth.view" as const, artifact_id: "view-b", definition };
  return <>
    <button onClick={() => void workspace.openDashboard("dash-1")}>Load A</button>
    <button onClick={() => { captured.current = workspace.draft; }}>Start turn</button>
    <button onClick={() => workspace.selectArtifact({ artifact, base: { dashboard_id: "dash-2", revision: 4 } })}>Select B</button>
    <button onClick={() => workspace.receiveArtifact({ artifact: { ...artifact, artifact_id: "view-result" }, base: { dashboard_id: "dash-1", revision: 1 } }, captured.current)}>Receive result</button>
    <button onClick={() => { if (workspace.context) workspace.edit({ ...workspace.context.definition, title: "Manual edit" }); }}>Edit</button>
    <output>{workspace.draft?.base?.dashboard_id} {workspace.draft?.base?.revision} {workspace.draft?.origin_artifact_id} {workspace.context?.definition.title}</output>
  </>;
}

it("keeps the selected base and manual edits when a turn returns against an older draft", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json(dashboard)));
  const router = createMemoryRouter([{ path: "/", element: <WorkspaceProvider><DraftRaceHarness /></WorkspaceProvider> }]);
  render(<RouterProvider router={router} />);
  fireEvent.click(screen.getByText("Load A"));
  await screen.findByText(/dash-1 1/);
  fireEvent.click(screen.getByText("Start turn"));
  fireEvent.click(screen.getByText("Select B"));
  fireEvent.click(screen.getByText("Receive result"));
  expect(screen.getByText(/dash-2 4 view-b/)).toBeTruthy();
  fireEvent.click(screen.getByText("Start turn"));
  fireEvent.click(screen.getByText("Edit"));
  fireEvent.click(screen.getByText("Receive result"));
  expect(screen.getByText(/dash-2 4 view-b Manual edit/)).toBeTruthy();
});

it("refetches a saved dashboard when reopening the same clean base", async () => {
  let revision = 1;
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path === "/v1/dashboards/dash-1") return json({ ...dashboard, revision });
    if (path === "/v1/agent/conversations") return json({ conversations: [] });
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const router = mount("/dashboards/dash-1");
  await screen.findByText(/revision 1/);
  fireEvent.click(screen.getByRole("button", { name: "Customize with Hearth" }));
  await screen.findByLabelText("Ask Hearth");
  revision = 2;
  await act(async () => { await router.navigate("/dashboards/dash-1"); });
  expect(await screen.findByText(/revision 2/)).toBeTruthy();
  expect(fetcher.mock.calls.filter(([url]) => url === "/v1/dashboards/dash-1")).toHaveLength(2);
});

it("ignores an old turn's history when navigation supersedes reconciliation", async () => {
  let resolveOld!: (value: Response) => void;
  const oldHistory = new Promise<Response>(resolve => { resolveOld = resolve; });
  let oldReads = 0;
  let oldSignal: AbortSignal | null | undefined;
  const artifact = { kind: "hearth.view", artifact_id: "view-old", definition };
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/agent/conversations") return json({ conversations: [] });
    if (path === "/v1/agent/conversations/conv-1/messages/stream") return new Response('data: {"type":"turn.finished"}\n\n');
    if (path === "/v1/agent/conversations/conv-1/messages") {
      if (++oldReads === 1) return json({ messages: [] });
      oldSignal = init?.signal;
      return oldHistory;
    }
    if (path === "/v1/agent/conversations/conv-2/messages") return json({ messages: [{ message_id: 9, role: "user", content: "New conversation", created_at: "" }] });
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const router = mount("/conversations/conv-1");
  const input = await screen.findByLabelText("Ask Hearth");
  await waitFor(() => expect(input).not.toHaveProperty("disabled", true));
  fireEvent.change(input, { target: { value: "old prompt" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  await waitFor(() => expect(oldReads).toBe(2));
  await act(async () => { await router.navigate("/conversations/conv-2"); });
  await screen.findByText("New conversation");
  expect(oldSignal?.aborted).toBe(true);
  await act(async () => { resolveOld(json({ messages: [
    { message_id: 1, role: "user", content: "Old conversation", created_at: "" },
    { message_id: 2, role: "assistant", content: "", calls: [{ id: "c", name: "present_view", arguments: "{}" }], created_at: "" },
    { message_id: 3, role: "tool", content: "ok", tool_call_id: "c", artifacts: [artifact], created_at: "" },
  ] })); await oldHistory; });
  expect(screen.queryByText("Old conversation")).toBeNull();
  expect(screen.queryByText("Generated view")).toBeNull();
  expect(screen.getByText("New conversation")).toBeTruthy();
});

it("does not apply old-backend history after the connection changes", async () => {
  let resolveOld!: (value: Response) => void;
  const oldHistory = new Promise<Response>(resolve => { resolveOld = resolve; });
  let oldReads = 0;
  let oldSignal: AbortSignal | null | undefined;
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/agent/conversations" || path === "http://new-backend/v1/agent/conversations") return json({ conversations: [] });
    if (path === "/v1/agent/conversations/conv-1/messages/stream") return new Response('data: {"type":"turn.finished"}\n\n');
    if (path === "/v1/agent/conversations/conv-1/messages") {
      if (++oldReads === 1) return json({ messages: [] });
      oldSignal = init?.signal;
      return oldHistory;
    }
    if (path === "http://new-backend/v1/agent/conversations/conv-1/messages") return json({ messages: [{ message_id: 9, role: "user", content: "New backend", created_at: "" }] });
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  mount("/conversations/conv-1");
  const input = await screen.findByLabelText("Ask Hearth");
  await waitFor(() => expect(input).not.toHaveProperty("disabled", true));
  fireEvent.change(input, { target: { value: "old backend prompt" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  await waitFor(() => expect(oldReads).toBe(2));
  await act(async () => { setBaseUrl("http://new-backend"); });
  await screen.findByText("New backend");
  expect(oldSignal?.aborted).toBe(true);
  await act(async () => { resolveOld(json({ messages: [{ message_id: 1, role: "user", content: "Old backend", created_at: "" }] })); await oldHistory; });
  expect(screen.queryByText("Old backend")).toBeNull();
  expect(screen.getByText("New backend")).toBeTruthy();
});
