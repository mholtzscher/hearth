import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { StrictMode } from "react";
import { createMemoryRouter, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ConversationsProvider } from "../features/conversation/ConversationsProvider";
import { WorkspaceProvider } from "../features/dashboards/Workspace";
import { ViewDataProvider } from "../features/views/data/provider";
import AgentPage from "./AgentPage";
import { ConversationHistory } from "../features/conversation/ConversationHistory";

// jsdom has no layout; browser verification exercises actual viewport scrolling.
beforeEach(() => vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); localStorage.clear(); });
function mount(path: string) {
  const router = createMemoryRouter([{ path: "/", element: <ViewDataProvider><ConversationsProvider><WorkspaceProvider><ConversationHistory id="recent" open={false} /><Outlet /></WorkspaceProvider></ConversationsProvider></ViewDataProvider>, children: [
    { index: true, element: <AgentPage /> }, { path: "conversations/:id", element: <AgentPage /> },
  ] }], { initialEntries: [path] });
  render(<StrictMode><RouterProvider router={router} /></StrictMode>);
}
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
