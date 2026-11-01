import { cleanup, render, screen, within } from "@testing-library/react";
import { StrictMode } from "react";
import { createMemoryRouter, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { WorkspaceProvider } from "../features/dashboards/Workspace";
import { ViewDataProvider } from "../features/views/data/provider";
import AgentPage from "./AgentPage";

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
