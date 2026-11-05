import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import App from "./App";

beforeEach(() => vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); localStorage.clear(); });

it("keeps connection setup in Development, not the household workspace", async () => {
  vi.stubGlobal("fetch", vi.fn(async input => {
    const path = String(input);
    const body = path === "/readyz" ? { status: "ready" }
      : path === "/healthz" ? { status: "ok" }
      : path === "/v1/agent/conversations" ? { conversations: [] }
      : path.startsWith("/v1/devices?") ? { items: [] } : null;
    if (!body) throw new Error(`Unexpected request ${path}`);
    return new Response(JSON.stringify(body));
  }));
  render(<App />);
  expect(await screen.findByRole("heading", { name: "Home workspace" })).toBeTruthy();
  expect(screen.getByRole("navigation", { name: "Main navigation" }).nextElementSibling).toBe(screen.getByRole("button", { name: "Conversation history" }));
  expect(screen.queryByRole("button", { name: "Connection settings" })).toBeNull();
  expect(await screen.findByText("Ready", { exact: true })).toBeTruthy();
  expect(within(screen.getByRole("complementary", { name: "Hearth navigation" })).getByRole("status").textContent).toBe("Hearth server Ready");
  fireEvent.click(screen.getByRole("link", { name: "Development" }));
  expect(await screen.findByText("No devices.")).toBeTruthy();
  expect(screen.getAllByRole("button", { name: "Connection settings" })).toHaveLength(1);
  fireEvent.click(screen.getByRole("link", { name: "Home" }));
  expect(await screen.findByRole("heading", { name: "Home workspace" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Connection settings" })).toBeNull();
});

it("lists recent conversations in the sidebar, marks the open one, and starts a new conversation", async () => {
  vi.stubGlobal("fetch", vi.fn(async input => {
    const path = String(input);
    const body = path === "/readyz" ? { status: "ready" }
      : path === "/v1/agent/conversations" ? { conversations: [{ id: "c1", created_at: "", message_count: 1, preview: "Kitchen lights" }] }
      : path === "/v1/agent/conversations/c1/messages" ? { messages: [{ message_id: 1, role: "user", content: "Kitchen lights?", created_at: "" }] } : null;
    if (!body) throw new Error(`Unexpected request ${path}`);
    return new Response(JSON.stringify(body));
  }));
  render(<App />);
  const recent = within(await screen.findByRole("region", { name: "Recent" }));
  fireEvent.click(await recent.findByRole("link", { name: "Kitchen lights" }));
  expect(await screen.findByText("Kitchen lights?")).toBeTruthy();
  expect(recent.getByRole("link", { name: "Kitchen lights" }).getAttribute("aria-current")).toBe("page");
  const toggle = screen.getByRole("button", { name: "Conversation history" });
  fireEvent.click(toggle);
  expect(document.activeElement).toBe(recent.getByRole("link", { name: "Kitchen lights" }));
  fireEvent.keyDown(document, { key: "Escape" });
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  expect(document.activeElement).toBe(toggle);
  fireEvent.click(screen.getByRole("button", { name: "New conversation" }));
  expect(await screen.findByText("What would make home feel better?")).toBeTruthy();
  expect(recent.getByRole("link", { name: "Kitchen lights" }).getAttribute("aria-current")).toBeNull();
  const prompt = screen.getByRole("textbox", { name: "Ask Hearth" });
  fireEvent.change(prompt, { target: { value: "Unsent draft" } });
  fireEvent.click(screen.getByRole("button", { name: "New conversation" }));
  expect(prompt).toHaveProperty("value", "");
});

it("lists a new conversation in Recent after leaving Home mid-reply", async () => {
  let created = false;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/agent/conversations" && init?.method === "POST") { created = true; return new Response(JSON.stringify({ id: "c-new" })); }
    // The reply never finishes; leaving Home abandons it.
    if (path.endsWith("/messages/stream")) return new Response(new ReadableStream());
    const body = path === "/readyz" ? { status: "ready" }
      : path === "/v1/agent/conversations" ? { conversations: created ? [{ id: "c-new", created_at: "", message_count: 1, preview: "Abandoned question" }] : [] }
      : path === "/v1/dashboards" ? { items: [] } : null;
    if (!body) throw new Error(`Unexpected request ${path}`);
    return new Response(JSON.stringify(body));
  }));
  render(<App />);
  const prompt = await screen.findByRole("textbox", { name: "Ask Hearth" });
  fireEvent.change(prompt, { target: { value: "Abandoned question" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  expect(await screen.findByText("Looking through your home…")).toBeTruthy();
  fireEvent.click(screen.getByRole("link", { name: "Dashboards" }));
  expect(await within(screen.getByRole("region", { name: "Recent" })).findByRole("link", { name: "Abandoned question" })).toBeTruthy();
});
