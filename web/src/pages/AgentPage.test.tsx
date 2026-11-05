import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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

it("shows the canvas only while a view is open and closes an unedited generated view without asking", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => new Response(JSON.stringify(String(input).endsWith("/messages") ? historyWithView : { conversations: [] }))));
  mount("/conversations/conv-1");
  const open = await screen.findByRole("button", { name: "Open in canvas" });
  expect(screen.queryByRole("region", { name: "View canvas" })).toBeNull();
  expect(screen.queryByRole("group", { name: "Workspace panels" })).toBeNull();
  expect(screen.queryByText("Hello home")).toBeNull();
  fireEvent.click(open);
  const canvas = await screen.findByRole("region", { name: "View canvas" });
  expect(within(canvas).getByText("Hello home")).toBeTruthy();
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
