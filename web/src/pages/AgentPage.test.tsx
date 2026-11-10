import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { StrictMode } from "react";
import { createMemoryRouter, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ConversationsProvider } from "../features/conversation/ConversationsProvider";
import { WorkspaceProvider } from "../features/dashboards/Workspace";
import { ViewDataProvider } from "../features/views/data/provider";
import AgentPage from "./AgentPage";
import { ConversationHistory } from "../features/conversation/ConversationHistory";
import { setBaseUrl } from "../api/client";

// jsdom has no layout; browser verification exercises actual viewport scrolling.
beforeEach(() => vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); localStorage.clear(); });
function mount(path: string) {
  const router = createMemoryRouter([{ path: "/", element: <ViewDataProvider><ConversationsProvider><WorkspaceProvider><ConversationHistory id="recent" open={false} /><Outlet /></WorkspaceProvider></ConversationsProvider></ViewDataProvider>, children: [
    { index: true, element: <AgentPage /> }, { path: "conversations/:id", element: <AgentPage /> },
    { path: "dashboards", element: <h1>Dashboard library</h1> },
  ] }], { initialEntries: [path] });
  render(<StrictMode><RouterProvider router={router} /></StrictMode>);
  return router;
}

it("keeps a running conversation alive across route changes and restores its live activity on return", async () => {
  let signal: AbortSignal | undefined;
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  let finished = false;
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path.endsWith("/messages/stream")) {
      signal = init?.signal as AbortSignal;
      return new Response(new ReadableStream({ start(value) { controller = value; } }));
    }
    return new Response(JSON.stringify(path.endsWith("/messages") ? { messages: finished ? [
      { message_id: 1, role: "user", content: "Read the temperature", created_at: "" },
      { message_id: 2, role: "assistant", content: "A finished reply.", created_at: "" },
    ] : [] } : { conversations: [] }));
  });
  vi.stubGlobal("fetch", fetcher);
  const router = mount("/conversations/c-running");
  const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(input).toHaveProperty("disabled", false));
  fireEvent.change(input, { target: { value: "Read the temperature" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  await waitFor(() => expect(signal).toBeDefined());
  await act(async () => { await router.navigate("/dashboards"); });
  expect(signal?.aborted).toBe(false);
  await act(async () => {
    controller.enqueue(new TextEncoder().encode('data: {"type":"tool.started","call_id":"lookup","name":"list_entities","arguments":"{}"}\n\n'));
    await router.navigate("/conversations/c-running");
  });
  expect(await screen.findByText("Used 1 tool · 1 pending or unknown")).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "Ask Hearth" })).toHaveProperty("disabled", true);
  await act(async () => {
    finished = true;
    controller.enqueue(new TextEncoder().encode('data: {"type":"tool.finished","call_id":"lookup","result":"[]"}\n\ndata: {"type":"turn.finished","reply":"A finished reply."}\n\n'));
    controller.close();
  });
  expect(await screen.findByText("A finished reply.")).toBeTruthy();
  expect(fetcher.mock.calls.filter(([path]) => String(path).endsWith("/messages/stream"))).toHaveLength(1);
});

it("keeps simultaneous conversations isolated and does not navigate back when a background turn finishes", async () => {
  const streams = new Map<string, ReadableStreamDefaultController<Uint8Array>>();
  const signals = new Map<string, AbortSignal>();
  const finished = new Set<string>();
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    const id = path.includes("/c-a/") ? "c-a" : "c-b";
    if (path.endsWith("/messages/stream")) {
      signals.set(id, init?.signal as AbortSignal);
      return new Response(new ReadableStream({ start(value) { streams.set(id, value); } }));
    }
    return new Response(JSON.stringify(path.endsWith("/messages") ? { messages: finished.has(id) ? [
      { message_id: 1, role: "assistant", content: `Reply for ${id}`, created_at: "" },
    ] : [] } : { conversations: [] }));
  }));
  const router = mount("/conversations/c-a");
  async function send(text: string) {
    const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
    await waitFor(() => expect(input).toHaveProperty("disabled", false));
    fireEvent.change(input, { target: { value: text } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => expect(streams.size).toBe(text === "Question A" ? 1 : 2));
  }
  await send("Question A");
  await act(async () => { await router.navigate("/conversations/c-b"); });
  await send("Question B");
  await act(async () => {
    finished.add("c-a");
    streams.get("c-a")!.enqueue(new TextEncoder().encode('data: {"type":"turn.finished"}\n\n'));
    streams.get("c-a")!.close();
  });
  expect(router.state.location.pathname).toBe("/conversations/c-b");
  expect(within(screen.getByRole("log")).queryByText("Reply for c-a")).toBeNull();
  expect(screen.getByRole("textbox", { name: "Ask Hearth" })).toHaveProperty("disabled", true);
  await act(async () => { await router.navigate("/conversations/c-a"); });
  expect(await screen.findByText("Reply for c-a")).toBeTruthy();
  expect(signals.get("c-b")?.aborted).toBe(false);
  await act(async () => {
    finished.add("c-b");
    streams.get("c-b")!.enqueue(new TextEncoder().encode('data: {"type":"turn.finished"}\n\n'));
    streams.get("c-b")!.close();
    await router.navigate("/conversations/c-b");
  });
  expect(await screen.findByText("Reply for c-b")).toBeTruthy();
});

it("finishes creating a conversation after leaving Home without stealing navigation", async () => {
  let create!: (value: Response) => void;
  let stream!: ReadableStreamDefaultController<Uint8Array>;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/agent/conversations" && init?.method === "POST") return new Promise<Response>(resolve => { create = resolve; });
    if (path.endsWith("/messages/stream")) return new Response(new ReadableStream({ start(value) { stream = value; } }));
    return new Response(JSON.stringify(path.endsWith("/messages") ? { messages: [] } : { conversations: [] }));
  }));
  const router = mount("/");
  fireEvent.click(await screen.findByRole("button", { name: "Show me the temperatures" }));
  await waitFor(() => expect(create).toBeDefined());
  await act(async () => { await router.navigate("/dashboards"); create(new Response('{"id":"c-late"}')); });
  const recent = within(screen.getByRole("region", { name: "Recent" }));
  expect(await recent.findByRole("link", { name: "Show me the temperatures throughout the house." })).toBeTruthy();
  expect(router.state.location.pathname).toBe("/dashboards");
  await act(async () => { stream.enqueue(new TextEncoder().encode('data: {"type":"turn.finished"}\n\n')); stream.close(); });
  expect(router.state.location.pathname).toBe("/dashboards");
});

it("restores a failed creation's prompt when returning Home", async () => {
  let rejectCreation!: (error: Error) => void;
  vi.stubGlobal("fetch", vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === "POST") return new Promise<Response>((_resolve, reject) => { rejectCreation = reject; });
    return new Response('{"conversations":[]}');
  }));
  const router = mount("/");
  fireEvent.click(await screen.findByRole("button", { name: "Show me the temperatures" }));
  await waitFor(() => expect(rejectCreation).toBeDefined());
  await act(async () => { await router.navigate("/dashboards"); rejectCreation(new Error("Could not create conversation")); });
  expect(router.state.location.pathname).toBe("/dashboards");
  await act(async () => { await router.navigate("/"); });
  expect(await screen.findByRole("alert")).toHaveProperty("textContent", expect.stringContaining("Could not create conversation"));
  expect(screen.getByRole("textbox", { name: "Ask Hearth" })).toHaveProperty("value", "Show me the temperatures throughout the house.");
  expect(screen.getByRole("button", { name: "Retry message" })).toHaveProperty("disabled", false);
});

it("hands a delayed creation to the existing owner and waits for its active reply", async () => {
  let create!: (value: Response) => void;
  let list!: (value: Response) => void;
  const streams: ReadableStreamDefaultController<Uint8Array>[] = [];
  const sent: string[] = [];
  let completed = 0;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/agent/conversations" && init?.method === "POST") return new Promise<Response>(resolve => { create = resolve; });
    if (path === "/v1/agent/conversations") return new Promise<Response>(resolve => { list = resolve; });
    if (path.endsWith("/messages/stream")) {
      sent.push(JSON.parse(String(init?.body)).text);
      return new Response(new ReadableStream({ start(value) { streams.push(value); } }));
    }
    return new Response(JSON.stringify({ messages: [
      ...(completed >= 1 ? [
        { message_id: 1, role: "user", content: "An already running prompt", created_at: "" },
        { message_id: 2, role: "assistant", content: "First reply", created_at: "" },
      ] : []),
      ...(completed >= 2 ? [
        { message_id: 3, role: "user", content: "Show me the temperatures throughout the house.", created_at: "" },
        { message_id: 4, role: "assistant", content: "Original reply", created_at: "" },
      ] : []),
    ] }));
  }));
  const router = mount("/");
  fireEvent.click(await screen.findByRole("button", { name: "Show me the temperatures" }));
  await waitFor(() => expect(create).toBeDefined());
  await act(async () => { list(new Response(JSON.stringify({ conversations: [{ id: "c-collision", created_at: "", message_count: 0, preview: "" }] }))); });
  fireEvent.click(await screen.findByRole("link", { name: "Empty conversation" }));
  const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(input).toHaveProperty("disabled", false));
  fireEvent.change(input, { target: { value: "An already running prompt" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  await waitFor(() => expect(sent).toEqual(["An already running prompt"]));
  await act(async () => { create(new Response('{"id":"c-collision"}')); });
  expect(router.state.location.pathname).toBe("/conversations/c-collision");
  expect(sent).toEqual(["An already running prompt"]);
  expect(await screen.findByText("Message queued until the current reply finishes.")).toBeTruthy();
  await act(async () => { completed = 1; streams[0].enqueue(new TextEncoder().encode('data: {"type":"turn.finished"}\n\n')); streams[0].close(); });
  await waitFor(() => expect(sent).toEqual(["An already running prompt", "Show me the temperatures throughout the house."]));
  await act(async () => { completed = 2; streams[1].enqueue(new TextEncoder().encode('data: {"type":"turn.finished"}\n\n')); streams[1].close(); });
  expect(await screen.findByText(/Original reply/)).toBeTruthy();
  expect(within(screen.getByRole("log")).getByText(/First reply/)).toBeTruthy();
});

it("retains failed background activity and the prompt until history recovery succeeds", async () => {
  let stream!: ReadableStreamDefaultController<Uint8Array>;
  let historyUnavailable = false;
  let recover = false;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path.endsWith("/messages/stream")) return new Response(new ReadableStream({ start(value) { stream = value; } }));
    if (path.endsWith("/messages")) {
      if (historyUnavailable && !recover) return new Response('{"detail":"History unavailable"}', { status: 503 });
      return new Response(JSON.stringify({ messages: [] }));
    }
    return new Response('{"conversations":[]}');
  }));
  const router = mount("/conversations/c-failed");
  const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(input).toHaveProperty("disabled", false));
  fireEvent.change(input, { target: { value: "Keep this prompt" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  await waitFor(() => expect(stream).toBeDefined());
  await act(async () => { await router.navigate("/dashboards"); historyUnavailable = true; stream.error(new Error("Connection lost")); });
  await act(async () => { await router.navigate("/conversations/c-failed"); });
  expect(await screen.findByRole("button", { name: "Retry loading history" })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "Ask Hearth" })).toHaveProperty("value", "Keep this prompt");
  expect(screen.getByRole("button", { name: "Retry message" })).toHaveProperty("disabled", true);
  recover = true;
  fireEvent.click(screen.getByRole("button", { name: "Retry loading history" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Retry message" })).toHaveProperty("disabled", false));
});

it("cancels owned streams when the backend changes and ignores their late results", async () => {
  let signal: AbortSignal | undefined;
  let stream!: ReadableStreamDefaultController<Uint8Array>;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path.endsWith("/messages/stream")) {
      signal = init?.signal as AbortSignal;
      return new Response(new ReadableStream({ start(value) { stream = value; } }));
    }
    return new Response(JSON.stringify(path.endsWith("/messages") ? { messages: [] } : { conversations: [] }));
  }));
  mount("/conversations/c-backend");
  const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(input).toHaveProperty("disabled", false));
  fireEvent.change(input, { target: { value: "Old server prompt" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  await waitFor(() => expect(signal).toBeDefined());
  await act(async () => { setBaseUrl("http://other-core"); });
  expect(signal?.aborted).toBe(true);
  await act(async () => { stream.enqueue(new TextEncoder().encode('data: {"type":"turn.finished","reply":"Old server reply"}\n\n')); stream.close(); });
  expect(within(screen.getByRole("log")).queryByText("Old server prompt")).toBeNull();
  expect(screen.queryByText("Old server reply")).toBeNull();
});
it("does not create empty conversations during StrictMode initialization", async () => {
  const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ conversations: [] })));
  vi.stubGlobal("fetch", fetcher);
  mount("/");
  expect(await screen.findByText("What would make", { exact: false })).toBeTruthy();
  expect(fetcher.mock.calls.every(([, init]) => !init || init.method !== "POST")).toBe(true);
});
it("opens the conversation selected by its route, not a stale browser-local ID", async () => {
  localStorage.setItem("hearth.agentConversationId", "stale");
  const fetcher = vi.fn(async (input: RequestInfo | URL) => new Response(JSON.stringify(String(input).endsWith("/messages") ? {
    messages: [{ message_id: 1, role: "user", content: "Kitchen lights", tool_calls: null, created_at: "" }],
  } : { conversations: [{ id: "selected", created_at: "", message_count: 1, preview: "Kitchen lights" }] })));
  vi.stubGlobal("fetch", fetcher);
  mount("/conversations/selected");
  expect(await within(screen.getByRole("log")).findByText("Kitchen lights")).toBeTruthy();
  expect(fetcher.mock.calls.some(([url]) => String(url).includes("/conversations/selected/messages"))).toBe(true);
  expect(fetcher.mock.calls.some(([url]) => String(url).includes("/conversations/stale/messages"))).toBe(false);
});

it("sends a starter immediately and lists the new conversation once the turn completes", async () => {
  let streamed: { text: string } | undefined;
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/agent/conversations" && init?.method === "POST") return new Response(JSON.stringify({ id: "conv-new" }));
    if (path === "/v1/agent/conversations") return new Response(JSON.stringify({ conversations: streamed ? [{ id: "conv-new", created_at: "", message_count: 2, preview: "Temperatures" }] : [] }));
    if (path.endsWith("/messages/stream")) { streamed = JSON.parse(String(init?.body)); return new Response('data: {"type":"turn.finished"}\n\n'); }
    if (path.endsWith("/messages")) return new Response(JSON.stringify({ messages: [] }));
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  mount("/");
  fireEvent.click(await screen.findByRole("button", { name: "Show me the temperatures" }));
  await waitFor(() => expect(streamed).toEqual({ text: "Show me the temperatures throughout the house." }));
  expect(await within(screen.getByRole("region", { name: "Recent" })).findByRole("link", { name: "Temperatures" })).toBeTruthy();
});

const artifact = { kind: "hearth.view", artifact_id: "view-1", definition: { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Warm house", spec: { root: "heading", elements: { heading: { type: "Heading", props: { text: "Hello home" } } } } } };
const historyWithView = { messages: [
  { message_id: 1, role: "assistant", content: "", calls: [{ id: "call-1", name: "present_view", arguments: "{}" }], created_at: "" },
  { message_id: 2, role: "tool", content: "", tool_call_id: "call-1", tool_name: "present_view", artifacts: [artifact], created_at: "" },
] };

it.each([
  { reply: "The view is ready for your decision.", historyFails: false },
  { reply: "The view is ready for your decision.", historyFails: true },
  { reply: "", historyFails: false },
  { reply: "", historyFails: true },
])("shows the committed reply before stream closure and history reconciliation ($reply, history failure: $historyFails)", async ({ reply, historyFails }) => {
  let stream!: ReadableStreamDefaultController<Uint8Array>;
  let reconcile!: (response: Response) => void;
  let recovered = false;
  const history = { messages: [
    { message_id: 1, role: "user", content: "Show a view", created_at: "" },
    { message_id: 2, role: "assistant", content: "Hidden tool narration", calls: [{ id: "call-1", name: "present_view", arguments: "{}" }], created_at: "" },
    { message_id: 3, role: "tool", content: JSON.stringify(artifact), tool_call_id: "call-1", tool_name: "present_view", artifacts: [artifact], created_at: "" },
    { message_id: 4, role: "assistant", content: reply, created_at: "" },
  ] };
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path.endsWith("/messages/stream")) return new Response(new ReadableStream({ start(value) { stream = value; } }));
    if (path.endsWith("/messages")) {
      if (recovered) return new Response(JSON.stringify(history));
      if (stream) return new Promise<Response>(resolve => { reconcile = resolve; });
      return new Response('{"messages":[]}');
    }
    return new Response('{"conversations":[]}');
  });
  vi.stubGlobal("fetch", fetcher);
  mount("/conversations/committed-reply");
  const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(input).toHaveProperty("disabled", false));
  fireEvent.change(input, { target: { value: "Show a view" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  await waitFor(() => expect(stream).toBeDefined());
  await act(async () => {
    for (const event of [
      { type: "tool.started", call_id: "call-1", name: "present_view", arguments: "{}" },
      { type: "tool.finished", call_id: "call-1", result: JSON.stringify(artifact) },
      { type: "view.presented", call_id: "call-1", artifact },
      { type: "turn.finished", reply },
    ]) stream.enqueue(new TextEncoder().encode(`data: ${JSON.stringify(event)}\n\n`));
  });
  const log = within(screen.getByRole("log"));
  if (reply) expect(await log.findByText(reply)).toHaveProperty("textContent", reply);
  expect(log.getAllByText("Hello home")).toHaveLength(1);
  expect(input).toHaveProperty("disabled", true);
  await act(async () => stream.close());
  await waitFor(() => expect(reconcile).toBeDefined());
  expect(input).toHaveProperty("disabled", true);
  expect(screen.getByRole("button", { name: "Send message" })).toHaveProperty("disabled", true);
  if (reply) expect(log.getAllByText(reply)).toHaveLength(1);
  expect(log.queryByText("Hidden tool narration")).toBeNull();
  await act(async () => reconcile(historyFails ? new Response("Unavailable", { status: 503 }) : new Response(JSON.stringify(history))));
  if (historyFails) {
    expect(await screen.findByRole("alert")).toHaveProperty("textContent", expect.stringContaining("Could not reconcile history"));
    if (reply) expect(log.getAllByText(reply)).toHaveLength(1);
    expect(log.getAllByText("Hello home")).toHaveLength(1);
    expect(screen.getByRole("button", { name: "Send message" })).toHaveProperty("disabled", true);
    recovered = true;
    fireEvent.click(screen.getByRole("button", { name: "Retry loading history" }));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  }
  await waitFor(() => expect(input).toHaveProperty("disabled", false));
  expect(log.getAllByText("Hello home")).toHaveLength(1);
  expect(log.getAllByText("Hearth")).toHaveLength(1);
  expect(log.queryByText("Hidden tool narration")).toBeNull();
  if (reply) expect(log.getAllByText(reply)).toHaveLength(1);
  else expect(screen.getByRole("log").querySelectorAll(".message-markdown")).toHaveLength(0);
  expect(fetcher.mock.calls.filter(([path]) => String(path).endsWith("/messages/stream"))).toHaveLength(1);
});

it("renders views in the reply, opens the canvas on demand, and closes an unedited generated view without asking", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => new Response(JSON.stringify(String(input).endsWith("/messages") ? historyWithView : { conversations: [] }))));
  mount("/conversations/conv-1");
  const open = await screen.findByRole("button", { name: "Open in canvas" });
  expect(screen.queryByRole("region", { name: "View canvas" })).toBeNull();
  expect(screen.queryByRole("group", { name: "Workspace panels" })).toBeNull();
  expect(within(screen.getByRole("log")).getByText("Hello home")).toBeTruthy();
  fireEvent.click(open);
  const canvas = await screen.findByRole("region", { name: "View canvas" });
  expect(within(canvas).getByText("Hello home")).toBeTruthy();
  // The open view is drawn once, in the canvas.
  expect(within(screen.getByRole("log")).queryByText("Hello home")).toBeNull();
  expect(screen.getByRole("group", { name: "Workspace panels" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Showing in canvas" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(within(canvas).getByRole("button", { name: "Close view" }));
  await waitFor(() => expect(screen.queryByRole("region", { name: "View canvas" })).toBeNull());
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByRole("button", { name: "Open in canvas" })).toBeTruthy();
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("textbox", { name: "Ask Hearth" })));
});

it("switches a view card's readings a screen away from view and keeps its height when they stop", async () => {
  const observers: { root?: Element | Document | null; rootMargin?: string; notify: (isIntersecting: boolean) => void }[] = [];
  vi.stubGlobal("IntersectionObserver", class {
    constructor(private callback: IntersectionObserverCallback, private options?: IntersectionObserverInit) {}
    observe(target: Element) {
      observers.push({ root: this.options?.root, rootMargin: this.options?.rootMargin, notify: isIntersecting => this.callback([{ isIntersecting, target } as IntersectionObserverEntry], this as unknown as IntersectionObserver) });
    }
    unobserve() {} disconnect() {}
  });
  const latest = () => observers[observers.length - 1];
  const resizes: ResizeObserverCallback[] = [];
  vi.stubGlobal("ResizeObserver", class {
    constructor(callback: ResizeObserverCallback) { resizes.push(callback); }
    observe() {} unobserve() {} disconnect() {}
  });
  vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(868);
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => new Response(JSON.stringify(String(input).endsWith("/messages") ? historyWithView : { conversations: [] }))));
  mount("/conversations/conv-1");
  const log = await screen.findByRole("log");
  await waitFor(() => expect(latest()?.root).toBe(log));
  // Observing within the conversation, with margin, switches cards before they reach the visible area.
  expect(latest()?.rootMargin).toMatch(/[1-9]/);
  act(() => latest().notify(true));
  expect(within(log).getByText("Hello home")).toBeTruthy();
  act(() => resizes.forEach(callback => callback([], {} as ResizeObserver)));
  act(() => latest().notify(false));
  const summary = within(log).getByText("Open view to load live readings");
  expect(summary.closest<HTMLElement>(".artifact-body")?.style.minHeight).toBe("868px");
});

it("asks before closing a view with manual edits", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => new Response(JSON.stringify(String(input).endsWith("/messages") ? historyWithView : String(input).startsWith("/v1/entities") ? { items: [] } : { conversations: [] }))));
  mount("/conversations/conv-1");
  fireEvent.click(await screen.findByRole("button", { name: "Open in canvas" }));
  const canvas = await screen.findByRole("region", { name: "View canvas" });
  fireEvent.click(within(canvas).getByRole("button", { name: "Edit" }));
  fireEvent.change(within(canvas).getByLabelText("Dashboard title"), { target: { value: "Edited" } });
  fireEvent.click(within(canvas).getByRole("button", { name: "Close view" }));
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByText("Keep your changes?")).toBeTruthy();
  fireEvent.click(within(dialog).getByRole("button", { name: "Stay" }));
  expect(screen.getByRole("region", { name: "View canvas" })).toBeTruthy();
  fireEvent.click(within(canvas).getByRole("button", { name: "Close view" }));
  fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Discard" }));
  await waitFor(() => expect(screen.queryByRole("region", { name: "View canvas" })).toBeNull());
});

it("shows one reply per turn with its tool calls collapsed into a summary", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => new Response(JSON.stringify(String(input).endsWith("/messages") ? { messages: [
    { message_id: 1, role: "user", content: "Temperatures?", created_at: "" },
    { message_id: 2, role: "assistant", content: "", calls: [{ id: "c1", name: "list_devices", arguments: "{}" }], created_at: "" },
    { message_id: 3, role: "tool", content: "[]", tool_call_id: "c1", created_at: "" },
    { message_id: 4, role: "assistant", content: "", calls: [{ id: "c2", name: "get_device", arguments: "{}" }], created_at: "" },
    { message_id: 5, role: "tool", content: "{}", tool_call_id: "c2", created_at: "" },
    { message_id: 6, role: "assistant", content: "It is 21 degrees.", created_at: "" },
  ] } : { conversations: [] }))));
  mount("/conversations/conv-1");
  const log = within(await screen.findByRole("log"));
  expect(await log.findByText("It is 21 degrees.")).toBeTruthy();
  expect(log.getAllByText("Hearth")).toHaveLength(1);
  const summary = log.getByText("Used 2 tools");
  expect(summary.closest("details")?.open).toBe(false);
  fireEvent.click(summary);
  expect(log.getByText(/list_devices/)).toBeTruthy();
  expect(log.getByText(/get_device/)).toBeTruthy();
});

it("keeps an expanded tool list open while streamed results update its pending count", async () => {
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  const body = new ReadableStream<Uint8Array>({ start(value) { controller = value; } });
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path.endsWith("/messages/stream")) return new Response(body);
    return new Response(JSON.stringify(path.endsWith("/messages") ? { messages: [] } : { conversations: [] }));
  }));
  mount("/conversations/conv-1");
  const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(input).toHaveProperty("disabled", false));
  fireEvent.change(input, { target: { value: "Read the temperature" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  const event = async (value: unknown) => act(async () => {
    controller.enqueue(new TextEncoder().encode(`data: ${JSON.stringify(value)}\n\n`));
  });
  await event({ type: "tool.started", call_id: "c1", name: "get_device", arguments: "{}" });
  const summary = await screen.findByText("Used 1 tool · 1 pending or unknown");
  fireEvent.click(summary);
  const list = summary.closest("details")!;
  expect(list.open).toBe(true);
  await event({ type: "tool.started", call_id: "c2", name: "list_entities", arguments: "{}" });
  expect(await screen.findByText("Used 2 tools · 2 pending or unknown")).toBeTruthy();
  await event({ type: "tool.finished", call_id: "c1", result: "21 degrees" });
  expect(await screen.findByText("Used 2 tools · 1 pending or unknown")).toBeTruthy();
  expect(list.open).toBe(true);
  const tool = within(list).getByText(/get_device/).closest("details")!;
  fireEvent.click(within(tool).getByText(/get_device/));
  expect(within(tool).getByText("21 degrees")).toBeTruthy();
  await event({ type: "tool.finished", call_id: "c2", result: "[]" });
  expect(await screen.findByText("Used 2 tools", { exact: true })).toBeTruthy();
  expect(list.open).toBe(true);
  await act(async () => controller.close());
});

it("holds a view's place from the moment the model starts writing it until Core accepts or rejects it", async () => {
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  const body = new ReadableStream<Uint8Array>({ start(value) { controller = value; } });
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path.endsWith("/messages/stream")) return new Response(body);
    return new Response(JSON.stringify(path.endsWith("/messages") ? { messages: [] } : { conversations: [] }));
  }));
  mount("/conversations/conv-1");
  const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(input).toHaveProperty("disabled", false));
  fireEvent.change(input, { target: { value: "Show the temperatures" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  const event = async (value: unknown) => act(async () => {
    controller.enqueue(new TextEncoder().encode(`data: ${JSON.stringify(value)}\n\n`));
  });
  const log = within(screen.getByRole("log"));
  // The model announces the call while it is still writing the definition.
  await event({ type: "tool.preparing", call_id: "rejected", name: "present_view" });
  expect(await log.findByText("Preparing a view")).toBeTruthy();
  await event({ type: "tool.started", call_id: "rejected", name: "present_view", arguments: "{}" });
  expect(log.getAllByText("Preparing a view")).toHaveLength(1);
  await event({ type: "tool.finished", call_id: "rejected", result: "invalid view definition" });
  await waitFor(() => expect(log.queryByText("Preparing a view")).toBeNull());
  await event({ type: "tool.preparing", call_id: "lookup", name: "list_entities" });
  expect(log.queryByText("Preparing a view")).toBeNull();
  await event({ type: "tool.preparing", call_id: "accepted", name: "present_view" });
  expect(await log.findByText("Preparing a view")).toBeTruthy();
  await event({ type: "tool.started", call_id: "accepted", name: "present_view", arguments: "{}" });
  await event({ type: "tool.finished", call_id: "accepted", result: JSON.stringify(artifact) });
  await event({ type: "view.presented", call_id: "accepted", artifact });
  expect(await log.findByRole("heading", { name: "Warm house" })).toBeTruthy();
  expect(log.queryByText("Preparing a view")).toBeNull();
  await act(async () => controller.close());
});

it("renders assistant Markdown and copies its original text with native copied feedback", async () => {
  const text = "**Room readings**\n\n- Kitchen: 21°C\n- Bedroom: 20°C\n\n| Room | Temperature |\n| --- | --- |\n| Kitchen | 21°C |\n\n```json\n{\"temperature\":21}\n```\n\n[Details](https://example.com)\n\n<script>unsafe()</script>";
  const writeText = vi.fn(async () => {});
  vi.stubGlobal("navigator", { userAgent: "jsdom", clipboard: { writeText } });
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => new Response(JSON.stringify(String(input).endsWith("/messages") ? { messages: [
    { message_id: 1, role: "assistant", content: "", calls: [{ id: "c1", name: "get_device", arguments: "{}" }], created_at: "" },
    { message_id: 2, role: "tool", content: "Raw tool result is not reply text", tool_call_id: "c1", created_at: "" },
    { message_id: 3, role: "assistant", content: text, created_at: "" },
  ] } : { conversations: [] }))));
  mount("/conversations/conv-1");
  const log = within(screen.getByRole("log"));
  expect((await log.findByText("Room readings")).tagName).toBe("STRONG");
  const markdown = within(log.getByText("Room readings").closest(".message-markdown")!);
  expect(markdown.getAllByRole("listitem")).toHaveLength(2);
  expect(log.getByRole("table")).toBeTruthy();
  expect(log.getByRole("link", { name: "Details" }).getAttribute("href")).toBe("https://example.com");
  expect(log.getByText('{"temperature":21}').closest("pre")).toBeTruthy();
  expect(log.queryByText("unsafe()")).toBeNull();
  fireEvent.click(log.getByRole("button", { name: "Copy response" }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(text));
  expect(await log.findByText("Copied")).toBeTruthy();
  expect(log.getByRole("button", { name: "Response copied" })).toBeTruthy();
});

it("does not submit an IME commit or Shift+Enter, and sends ordinary Enter once", async () => {
  let sent: string | undefined;
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/agent/conversations" && init?.method === "POST") return new Response(JSON.stringify({ id: "conv-new" }));
    if (path.endsWith("/messages/stream")) { sent = JSON.parse(String(init?.body)).text; return new Response('data: {"type":"turn.finished"}\n\n'); }
    return new Response(JSON.stringify(path.endsWith("/messages") ? { messages: [] } : { conversations: [] }));
  });
  vi.stubGlobal("fetch", fetcher);
  mount("/");
  const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
  fireEvent.change(input, { target: { value: "Read the temperature" } });
  fireEvent.keyDown(input, { key: "Enter", isComposing: true, keyCode: 229 });
  fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
  fireEvent.keyDown(input, { key: "Enter" });
  await waitFor(() => expect(sent).toBe("Read the temperature"));
  expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/messages/stream"))).toHaveLength(1);
});

it("does not present a retained tool call without a result as complete", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => new Response(JSON.stringify(String(input).endsWith("/messages") ? { messages: [
    { message_id: 1, role: "assistant", content: "", calls: [{ id: "c1", name: "get_device", arguments: "{}" }], created_at: "" },
  ] } : { conversations: [] }))));
  mount("/conversations/conv-1");
  const summary = await screen.findByText("Used 1 tool · 1 pending or unknown");
  fireEvent.click(summary);
  expect(screen.getByText(/get_device · outcome unknown or pending/)).toBeTruthy();
  expect(screen.queryByText(/get_device · complete/)).toBeNull();
});

it("blocks native composer submission while view edits are invalid and keeps the prompt on discard", async () => {
  const fetcher = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify(String(input).endsWith("/messages") ? historyWithView : String(input).startsWith("/v1/entities") ? { items: [] } : { conversations: [] })));
  vi.stubGlobal("fetch", fetcher);
  mount("/conversations/conv-1");
  fireEvent.click(await screen.findByRole("button", { name: "Open in canvas" }));
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  fireEvent.change(screen.getByLabelText("Dashboard title"), { target: { value: "" } });
  const input = screen.getByRole("textbox", { name: "Ask Hearth" });
  fireEvent.change(input, { target: { value: "Adjust this view" } });
  expect(screen.getByRole("button", { name: "Send message" })).toHaveProperty("disabled", true);
  fireEvent.keyDown(input, { key: "Enter" });
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
  await waitFor(() => expect(screen.queryByRole("region", { name: "View canvas" })).toBeNull());
  expect(input).toHaveProperty("value", "Adjust this view");
  expect(screen.getByRole("button", { name: "Send message" })).toHaveProperty("disabled", false);
});
