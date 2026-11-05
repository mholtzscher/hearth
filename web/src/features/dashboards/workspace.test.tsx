import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useRef } from "react";
import { createMemoryRouter, Link, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import AgentPage from "../../pages/AgentPage";
import { setBaseUrl } from "../../api/client";
import { ViewDataProvider } from "../views/data/provider";
import { Canvas } from "./Canvas";
import { DashboardLibrary } from "./DashboardLibrary";
import { useWorkspace, WorkspaceProvider } from "./Workspace";
import { ConversationsProvider } from "../conversation/ConversationsProvider";

const definition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Warm house", spec: { root: "heading", elements: { heading: { type: "Heading", props: { text: "Hello home" } } } } };
const dashboard = { id: "dash-1", revision: 1, definition, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" };
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json" } });
function Root() { return <ViewDataProvider><ConversationsProvider><WorkspaceProvider><nav><Link to="/dashboards">Library</Link><Link to="/">Home</Link></nav><Outlet /></WorkspaceProvider></ConversationsProvider></ViewDataProvider>; }
function mount(path: string) {
  const router = createMemoryRouter([{ path: "/", element: <Root />, children: [
    { index: true, element: <AgentPage /> }, { path: "conversations/:id", element: <AgentPage /> },
    { path: "dashboards", element: <DashboardLibrary /> }, { path: "dashboards/:id", element: <Canvas dashboardPage /> },
  ] }], { initialEntries: [path] });
  render(<RouterProvider router={router} />);
  return router;
}
// jsdom has no layout; browser verification exercises actual viewport scrolling.
beforeEach(() => vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }));
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
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
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
  await waitFor(() => expect(screen.getAllByRole("button", { name: /in canvas/ })).toHaveLength(1));
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
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  fireEvent.change(screen.getByLabelText("Dashboard title"), { target: { value: "My draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  expect(await screen.findByText(/changed elsewhere/)).toBeTruthy();
  expect((screen.getByLabelText("Dashboard title") as HTMLInputElement).value).toBe("My draft");
  fireEvent.click(screen.getAllByRole("button", { name: "Save as new" })[0]);
  await waitFor(() => expect(fetcher.mock.calls.some(([url, init]) => url === "/v1/dashboards" && init?.method === "POST")).toBe(true));
  expect(await screen.findByText(/revision 1 · Saved/)).toBeTruthy();
});

it.each([1, 2])("shows %i streamed artifacts in the reply without opening the canvas, then the committed artifact", async count => {
  let stream!: ReadableStreamDefaultController<Uint8Array>;
  const body = new ReadableStream<Uint8Array>({ start(controller) { stream = controller; } });
  const artifact = (id: string, title: string) => ({ kind: "hearth.view", artifact_id: id, definition: { ...definition, title } });
  const final = artifact("view-final", "Committed view");
  let historyReads = 0;
  vi.stubGlobal("fetch", vi.fn(async input => {
    const path = String(input);
    if (path === "/v1/agent/conversations") return json({ conversations: [] });
    if (path.endsWith("/messages/stream")) return new Response(body);
    if (path.endsWith("/messages")) return json({ messages: ++historyReads === 1 ? [] : [
      { message_id: 1, role: "user", content: "make a view", created_at: "" },
      { message_id: 2, role: "assistant", content: "", calls: [{ id: "final", name: "present_view", arguments: "{}" }], created_at: "" },
      { message_id: 3, role: "tool", content: "ok", tool_call_id: "final", artifacts: [final], created_at: "" },
    ] });
    throw new Error(`Unexpected request ${path}`);
  }));
  mount("/conversations/conv-1");
  const input = await screen.findByLabelText("Ask Hearth");
  await waitFor(() => expect(input).not.toHaveProperty("disabled", true));
  fireEvent.change(input, { target: { value: "make a view" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  const log = within(screen.getByRole("log"));
  for (const [id, title] of [["first", "First view"], ["second", "Second view"]].slice(0, count)) {
    await act(async () => { stream.enqueue(new TextEncoder().encode(`data: ${JSON.stringify({ type: "view.presented", call_id: id, artifact: artifact(id, title) })}\n\n`)); });
    expect(await log.findByRole("heading", { name: title })).toBeTruthy();
  }
  expect(screen.queryByRole("region", { name: "View canvas" })).toBeNull();
  await act(async () => { stream.close(); });
  expect(await log.findByRole("heading", { name: "Committed view" })).toBeTruthy();
  expect(screen.queryByRole("region", { name: "View canvas" })).toBeNull();
  fireEvent.click(log.getByRole("button", { name: "Open in canvas" }));
  const canvas = within(await screen.findByRole("region", { name: "View canvas" }));
  expect(canvas.getByRole("heading", { name: "Committed view" })).toBeTruthy();
  expect(canvas.getByRole("button", { name: "Save dashboard" })).not.toHaveProperty("disabled", true);
});

it.each([
  ["initial", "success", "before"], ["initial", "failure", "before"],
  ["pagination", "success", "before"], ["pagination", "failure", "before"],
  ["initial", "success", "after"], ["pagination", "success", "after"],
])("isolates an old backend's %s %s response arriving %s the new list", async (request, outcome, order) => {
  let resolveOld!: (value: Response) => void;
  let rejectOld!: (error: Error) => void;
  let resolveNew!: (value: Response) => void;
  const oldResponse = new Promise<Response>((resolve, reject) => { resolveOld = resolve; rejectOld = reject; });
  const newResponse = new Promise<Response>(resolve => { resolveNew = resolve; });
  const item = (title: string) => ({ id: title, title, revision: 1, created_at: "2026-01-01", updated_at: "2026-01-01" });
  const fetcher = vi.fn(async input => {
    const path = String(input);
    if (path === "http://new-backend/v1/dashboards") return newResponse;
    if (path === "http://new-backend/v1/dashboards?cursor=new-next") return json({ items: [item("New second page")] });
    if (path === "/v1/dashboards" && request === "pagination") return json({ items: [item("Old first page")], next_cursor: "old-next" });
    if (path.startsWith("/v1/dashboards")) return oldResponse;
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  mount("/dashboards");
  if (request === "pagination") fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
  await act(async () => { setBaseUrl("http://new-backend"); });
  const finishNew = async () => { await act(async () => { resolveNew(json({ items: [item("New backend")], next_cursor: "new-next" })); }); };
  if (order === "after") await finishNew();
  await act(async () => {
    if (outcome === "failure") rejectOld(new Error("Old backend failed"));
    else resolveOld(json({ items: [item("Stale response")], next_cursor: "stale-next" }));
  });
  expect(screen.queryByRole("heading", { name: "Old first page" })).toBeNull();
  expect(screen.queryByRole("heading", { name: "Stale response" })).toBeNull();
  expect(screen.queryByRole("alert")).toBeNull();
  if (order === "before") {
    expect(screen.getByRole("status").textContent).toBe("Loading dashboards…");
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
    await finishNew();
  }
  expect(await screen.findByRole("heading", { name: "New backend" })).toBeTruthy();
  expect(screen.queryByRole("status")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Load more" }));
  expect(await screen.findByRole("heading", { name: "New second page" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "New backend" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
});

it("clears the old backend's delete confirmation when switching connections", async () => {
  vi.stubGlobal("fetch", vi.fn(async input => json({ items: String(input).startsWith("http:") ? [] : [{ ...dashboard, title: "Old dashboard" }] })));
  mount("/dashboards");
  fireEvent.click(await screen.findByRole("button", { name: "Delete Old dashboard" }));
  expect(screen.getByRole("dialog", { name: "Remove this dashboard?" })).toBeTruthy();
  await act(async () => { setBaseUrl("http://new-backend"); });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(await screen.findByRole("heading", { name: "Your first dashboard starts with a question." })).toBeTruthy();
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
  fireEvent.click(screen.getByRole("button", { name: "Ask Hearth to change" }));
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
  await waitFor(() => expect(screen.getAllByRole("button", { name: /in canvas/ })).toHaveLength(1));
  expect(screen.getByRole("button", { name: "Retry message" })).toHaveProperty("disabled", true);
  fireEvent.click(recover);
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry loading history" })).toBeNull());
  expect(screen.getAllByRole("button", { name: /in canvas/ })).toHaveLength(1);
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
  expect(screen.getByRole("button", { name: "Edit" })).toHaveProperty("disabled", true);
  expect(screen.getByRole("button", { name: "Save as new" })).toHaveProperty("disabled", true);
});

it("makes the expanded canvas modal and restores focus and navigation after Escape", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json(dashboard)));
  mount("/dashboards/dash-1");
  await screen.findByText("Hello home");
  const expand = screen.getByRole("button", { name: "Expand" });
  expand.focus(); fireEvent.click(expand);
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("dialog", { name: "View canvas" })));
  expect(screen.getByRole("navigation")).toHaveProperty("inert", true);
  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  await waitFor(() => expect(document.activeElement).toBe(expand));
  expect(screen.getByRole("navigation").inert).not.toBe(true);
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("removes an expanded canvas when discarding its unsaved generated view", async () => {
  const artifact = { kind: "hearth.view", artifact_id: "view-discard", definition };
  vi.stubGlobal("fetch", vi.fn(async input => String(input).endsWith("/messages")
    ? json({ messages: [
      { message_id: 1, role: "assistant", content: "", calls: [{ id: "call-1", name: "present_view", arguments: "{}" }], created_at: "" },
      { message_id: 2, role: "tool", content: "", tool_call_id: "call-1", tool_name: "present_view", artifacts: [artifact], created_at: "" },
    ] }) : json({ conversations: [] })));
  mount("/conversations/conv-1");
  fireEvent.click(await screen.findByRole("button", { name: "Open in canvas" }));
  fireEvent.click(screen.getByRole("button", { name: "Expand" }));
  await screen.findByRole("dialog", { name: "View canvas" });
  fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(screen.queryByRole("region", { name: "View canvas" })).toBeNull();
  expect(screen.getByRole("navigation").inert).not.toBe(true);
});

it("focuses the canvas on selection and the composer on customization, including from Home", async () => {
  const artifact = { kind: "hearth.view", artifact_id: "view-focus", definition };
  vi.stubGlobal("fetch", vi.fn(async input => String(input).endsWith("/messages")
    ? json({ messages: [
      { message_id: 1, role: "assistant", content: "", calls: [{ id: "call-1", name: "present_view", arguments: "{}" }], created_at: "" },
      { message_id: 2, role: "tool", content: "", tool_call_id: "call-1", tool_name: "present_view", artifacts: [artifact], created_at: "" },
    ] })
    : json({ conversations: [] })));
  const router = mount("/conversations/conv-1");
  fireEvent.click(await screen.findByRole("button", { name: "Open in canvas" }));
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("region", { name: "View canvas" })));
  fireEvent.click(screen.getByRole("button", { name: "Ask Hearth to change" }));
  await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText("Ask Hearth")));
  expect(router.state.location.pathname).toBe("/");
  expect(screen.getByRole("button", { name: "Conversation" }).getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(screen.getByRole("button", { name: "Canvas" }));
  fireEvent.click(screen.getByRole("button", { name: "Ask Hearth to change" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Conversation" }).getAttribute("aria-pressed")).toBe("true"));
  expect(document.activeElement).toBe(screen.getByLabelText("Ask Hearth"));
});

it("keeps focus on the canvas after discarding unsaved editor values", async () => {
  vi.stubGlobal("fetch", vi.fn(async input => String(input).startsWith("/v1/entities") ? json({ items: [], next_cursor: null }) : json(dashboard)));
  mount("/dashboards/dash-1");
  await screen.findByText("Hello home");
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  fireEvent.change(screen.getByLabelText("Dashboard title"), { target: { value: "Local edit" } });
  const discard = screen.getByRole("button", { name: "Discard changes" });
  discard.focus(); fireEvent.click(discard);
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("region", { name: "View canvas" })));
  expect(screen.queryByRole("button", { name: "Discard changes" })).toBeNull();
});

function DraftRaceHarness() {
  const workspace = useWorkspace();
  const captured = useRef(0);
  const artifact = { kind: "hearth.view" as const, artifact_id: "view-b", definition };
  return <>
    <button onClick={() => void workspace.openDashboard("dash-1")}>Load A</button>
    <button onClick={() => { captured.current = workspace.draftVersion; }}>Start turn</button>
    <button onClick={() => workspace.selectArtifact({ artifact, base: { dashboard_id: "dash-2", revision: 4 } })}>Select B</button>
    <button onClick={() => workspace.receiveArtifact({ artifact: { ...artifact, artifact_id: "view-result" }, base: { dashboard_id: "dash-1", revision: 1 } }, captured.current)}>Receive result</button>
    <button onClick={() => workspace.receiveArtifact({ artifact: { ...artifact, artifact_id: "view-later", definition: { ...definition, title: "Later result" } }, base: { dashboard_id: "dash-1", revision: 1 } }, captured.current)}>Receive later</button>
    <button onClick={() => { if (workspace.context) workspace.edit({ ...workspace.context.definition, title: "Manual edit" }); }}>Edit</button>
    <button onClick={() => void workspace.save()}>Save draft</button>
    <button onClick={() => void workspace.discard()}>Discard draft</button>
    <button onClick={() => workspace.setEditorValid(false)}>Invalidate editor</button>
    <output>{workspace.draft?.base?.dashboard_id} {workspace.draft?.base?.revision} {workspace.draft?.origin_artifact_id} {workspace.context?.definition.title}</output>
    {workspace.error && <p role="alert">{workspace.error}</p>}
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

it.each([
  ["Edit", "dash-1 1 view-result Manual edit"],
  ["Select B", "dash-2 4 view-b Warm house"],
  ["Save draft", "dash-1 2  Warm house"],
  ["Discard draft", "dash-1 1  Warm house"],
  ["Invalidate editor", "dash-1 1 view-result "],
])("preserves %s after the first artifact when more results arrive", async (action, expected) => {
  vi.stubGlobal("fetch", vi.fn(async (_input, init) => json({ ...dashboard, revision: init?.method === "PUT" ? 2 : 1 })));
  const router = createMemoryRouter([{ path: "/", element: <WorkspaceProvider><DraftRaceHarness /></WorkspaceProvider> }]);
  render(<RouterProvider router={router} />);
  fireEvent.click(screen.getByText("Load A"));
  await waitFor(() => expect(screen.getByRole("status").textContent).toBe("dash-1 1  Warm house"));
  fireEvent.click(screen.getByText("Start turn"));
  fireEvent.click(screen.getByText("Receive result"));
  expect(screen.getByRole("status").textContent).toBe("dash-1 1 view-result Warm house");
  fireEvent.click(screen.getByText(action));
  if (action === "Select B") fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Discard" }));
  await waitFor(() => expect(screen.getByRole("status").textContent).toBe(expected));
  fireEvent.click(screen.getByText("Receive later"));
  expect(screen.getByRole("status").textContent).toBe(expected);
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
  fireEvent.click(screen.getByRole("button", { name: "Ask Hearth to change" }));
  await screen.findByLabelText("Ask Hearth");
  revision = 2;
  await act(async () => { await router.navigate("/dashboards/dash-1"); });
  expect(await screen.findByText(/revision 2/)).toBeTruthy();
  expect(fetcher.mock.calls.filter(([url]) => url === "/v1/dashboards/dash-1")).toHaveLength(2);
});

it("retains the draft chosen for saving when the save conflicts and later artifacts arrive", async () => {
  let resolveSave!: (response: Response) => void;
  const saving = new Promise<Response>(resolve => { resolveSave = resolve; });
  vi.stubGlobal("fetch", vi.fn(async (_input, init) => init?.method === "PUT" ? saving : json(dashboard)));
  const router = createMemoryRouter([{ path: "/", element: <WorkspaceProvider><DraftRaceHarness /></WorkspaceProvider> }]);
  render(<RouterProvider router={router} />);
  fireEvent.click(screen.getByText("Load A"));
  await screen.findByText(/dash-1 1/);
  fireEvent.click(screen.getByText("Start turn"));
  fireEvent.click(screen.getByText("Receive result"));
  fireEvent.click(screen.getByText("Save draft"));
  fireEvent.click(screen.getByText("Receive later"));
  expect(screen.getByRole("status").textContent).toBe("dash-1 1 view-result Warm house");
  await act(async () => { resolveSave(json({ title: "Conflict", detail: "Changed" }, 409)); });
  expect((await screen.findByRole("alert")).textContent).toContain("changed elsewhere");
  fireEvent.click(screen.getByText("Receive later"));
  expect(screen.getByRole("status").textContent).toBe("dash-1 1 view-result Warm house");
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
  expect(screen.queryByRole("button", { name: /in canvas/ })).toBeNull();
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
