import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { StrictMode } from "react";
import { createMemoryRouter, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { WorkspaceProvider } from "../features/dashboards/Workspace";
import { ViewDataProvider } from "../features/views/data/provider";
import AgentPage from "./AgentPage";

// jsdom has no layout; browser verification exercises actual viewport scrolling.
beforeEach(() => vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); localStorage.clear(); });
function mount(path: string) {
  const router = createMemoryRouter([{ path: "/", element: <ViewDataProvider><WorkspaceProvider><Outlet /></WorkspaceProvider></ViewDataProvider>, children: [
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

it("fills and focuses the composer from a starter without sending it", async () => {
  const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ conversations: [] })));
  vi.stubGlobal("fetch", fetcher);
  mount("/");
  fireEvent.click(await screen.findByRole("button", { name: "Show me the temperatures" }));
  const input = screen.getByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(document.activeElement).toBe(input));
  expect(input).toHaveProperty("value", "Show me the temperatures throughout the house.");
  expect(screen.getByRole("button", { name: "Send message" })).toHaveProperty("disabled", false);
  expect(fetcher.mock.calls.every(([, init]) => !init || init.method !== "POST")).toBe(true);
});

it("connects the empty canvas to the composer without expanding or submitting", async () => {
  const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ conversations: [] })));
  vi.stubGlobal("fetch", fetcher);
  mount("/");
  fireEvent.click(await screen.findByRole("button", { name: "Canvas" }));
  expect(screen.getByRole("button", { name: "Canvas" }).getAttribute("aria-pressed")).toBe("true");
  expect(screen.queryByRole("button", { name: "Expand" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Ask for a view" }));
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("textbox", { name: "Ask Hearth" })));
  expect(screen.getByRole("button", { name: "Conversation" }).getAttribute("aria-pressed")).toBe("true");
  expect(screen.getByRole("heading", { level: 1, name: "Home workspace" })).toBeTruthy();
  expect(fetcher.mock.calls.every(([, init]) => !init || init.method !== "POST")).toBe(true);
});

it("disables the canvas prompt action until the conversation can accept focus", async () => {
  let resolveHistory!: (response: Response) => void;
  vi.stubGlobal("fetch", vi.fn(async input => String(input).endsWith("/messages")
    ? new Promise<Response>(resolve => { resolveHistory = resolve; })
    : new Response(JSON.stringify({ conversations: [] }))));
  mount("/conversations/loading");
  expect(screen.getByRole("button", { name: "Ask for a view" })).toHaveProperty("disabled", true);
  await act(async () => resolveHistory(new Response(JSON.stringify({ messages: [] }))));
  expect(screen.getByRole("button", { name: "Ask for a view" })).toHaveProperty("disabled", false);
});
