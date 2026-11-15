import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { StrictMode } from "react";
import { createHashRouter } from "react-router-dom";
import App, { appRoutes } from "./App";

let router: ReturnType<typeof createHashRouter> | undefined;
function renderApp(strict = false) {
  router = createHashRouter(appRoutes);
  const app = <App router={router} />;
  return render(strict ? <StrictMode>{app}</StrictMode> : app);
}

beforeEach(() => vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }));
afterEach(() => { cleanup(); router?.dispose(); router = undefined; vi.unstubAllGlobals(); localStorage.clear(); window.location.hash = ""; });

it("keeps connection setup in Development, not the household workspace", async () => {
  stubWidth(900);
  vi.stubGlobal("fetch", vi.fn(async input => {
    const path = String(input);
    const body = path === "/readyz" ? { status: "ready" }
      : path === "/healthz" ? { status: "ok" }
      : path === "/v1/agent/conversations" ? { conversations: [] }
      : path.startsWith("/v1/devices?") ? { items: [] } : null;
    if (!body) throw new Error(`Unexpected request ${path}`);
    return new Response(JSON.stringify(body));
  }));
  renderApp();
  expect(await screen.findByRole("heading", { name: "Home workspace" })).toBeTruthy();
  expect(screen.getByRole("navigation", { name: "Main navigation" }).nextElementSibling?.contains(screen.getByRole("button", { name: "Recent conversation history" }))).toBe(true);
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

it("keeps hash navigation and browser Back working after StrictMode effect replay", async () => {
  vi.stubGlobal("fetch", vi.fn(async input => {
    const path = String(input);
    const body = path === "/readyz" ? { status: "ready" }
      : path === "/healthz" ? { status: "ok" }
      : path === "/v1/agent/conversations" ? { conversations: [] }
      : path.startsWith("/v1/devices?") ? { items: [] } : null;
    if (!body) throw new Error(`Unexpected request ${path}`);
    return new Response(JSON.stringify(body));
  }));
  renderApp(true);
  await screen.findByRole("heading", { name: "Home workspace" });
  fireEvent.click(screen.getByRole("link", { name: "Development" }));
  expect(await screen.findByRole("heading", { name: "Devices" })).toBeTruthy();
  await act(async () => { window.history.back(); });
  expect(await screen.findByRole("heading", { name: "Home workspace" })).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "Devices" })).toBeNull();
});

it("narrows the sidebar to a rail beside a canvas below 1280px", async () => {
  // A 1100px window: wider than the 980px rail breakpoint, narrower than 1280px.
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: Number(/max-width: (\d+)px/.exec(query)?.[1]) >= 1100,
    addEventListener() {}, removeEventListener() {},
  }));
  const definition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Warm house", spec: { root: "heading", elements: { heading: { type: "Heading", props: { text: "Hello home" } } } } };
  vi.stubGlobal("fetch", vi.fn(async input => {
    const path = String(input);
    const body = path === "/readyz" ? { status: "ready" }
      : path === "/v1/agent/conversations" ? { conversations: [] }
      : path === "/v1/dashboards/dash-1" ? { id: "dash-1", revision: 1, definition, created_at: "", updated_at: "" } : null;
    if (!body) throw new Error(`Unexpected request ${path}`);
    return new Response(JSON.stringify(body));
  }));
  window.location.hash = "#/dashboards/dash-1";
  renderApp();
  const sidebar = screen.getByRole("complementary", { name: "Hearth navigation" });
  const ask = await screen.findByRole("button", { name: "Ask Hearth to change" });
  // A dashboard page has no conversation beside it, so it keeps the full sidebar.
  expect(sidebar.getAttribute("data-nav")).toBe("full");
  fireEvent.click(ask);
  expect(await screen.findByRole("heading", { name: "Home workspace" })).toBeTruthy();
  expect(sidebar.getAttribute("data-nav")).toBe("rail");
  fireEvent.click(screen.getByRole("button", { name: "Close view" }));
  await waitFor(() => expect(sidebar.getAttribute("data-nav")).toBe("full"));
  window.location.hash = "";
});

it("lists recent conversations in the sidebar, marks the open one, and starts a new conversation", async () => {
  stubWidth(900);
  vi.stubGlobal("fetch", vi.fn(async input => {
    const path = String(input);
    const body = path === "/readyz" ? { status: "ready" }
      : path === "/v1/agent/conversations" ? { conversations: [{ id: "c1", created_at: "", message_count: 1, preview: "Kitchen lights" }] }
      : path === "/v1/agent/conversations/c1/messages" ? { messages: [{ message_id: 1, role: "user", content: "Kitchen lights?", created_at: "" }] } : null;
    if (!body) throw new Error(`Unexpected request ${path}`);
    return new Response(JSON.stringify(body));
  }));
  renderApp();
  const recent = within(await screen.findByRole("region", { name: "Recent" }));
  fireEvent.click(await recent.findByRole("link", { name: "Kitchen lights" }));
  expect(await screen.findByText("Kitchen lights?")).toBeTruthy();
  expect(recent.getByRole("link", { name: "Kitchen lights" }).getAttribute("aria-current")).toBe("page");
  const toggle = screen.getByRole("button", { name: "Recent conversation history" });
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

it("publishes a new conversation immediately and lets its reply finish after leaving Home", async () => {
  let created = false;
  let finished = false;
  let signal: AbortSignal | undefined;
  let stream!: ReadableStreamDefaultController<Uint8Array>;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/agent/conversations" && init?.method === "POST") { created = true; return new Response(JSON.stringify({ id: "c-new" })); }
    if (path.endsWith("/messages/stream")) {
      signal = init?.signal as AbortSignal;
      return new Response(new ReadableStream({ start(controller) { stream = controller; } }));
    }
    const body = path === "/readyz" ? { status: "ready" }
      : path === "/v1/agent/conversations" ? { conversations: created ? [{ id: "c-new", created_at: "", message_count: 1, preview: "Background question" }] : [] }
      : path.endsWith("/messages") ? { messages: [
        { message_id: 1, role: "user", content: "Background question", created_at: "" },
        ...(finished ? [{ message_id: 2, role: "assistant", content: "Finished in the background.", created_at: "" }] : []),
      ] }
      : path === "/v1/dashboards" ? { items: [] } : null;
    if (!body) throw new Error(`Unexpected request ${path}`);
    return new Response(JSON.stringify(body));
  }));
  renderApp();
  const prompt = await screen.findByRole("textbox", { name: "Ask Hearth" });
  fireEvent.change(prompt, { target: { value: "Background question" } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  expect(await screen.findByText("Looking through your home…")).toBeTruthy();
  await waitFor(() => expect(signal).toBeDefined());
  await waitFor(() => expect(window.location.hash).toBe("#/conversations/c-new"));
  const recent = within(screen.getByRole("region", { name: "Recent" }));
  expect(await recent.findByRole("link", { name: "Background question" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "New conversation" })).toHaveProperty("disabled", false);
  fireEvent.click(screen.getByRole("link", { name: "Dashboards" }));
  expect(await screen.findByRole("heading", { name: "Made for you. Here to stay." })).toBeTruthy();
  expect(signal?.aborted).toBe(false);
  await act(async () => {
    finished = true;
    stream.enqueue(new TextEncoder().encode('data: {"type":"turn.finished","reply":"Finished in the background."}\n\n'));
    stream.close();
  });
  expect(window.location.hash).toBe("#/dashboards");
  fireEvent.click(recent.getByRole("link", { name: "Background question" }));
  expect(await within(screen.getByRole("log")).findByText("Finished in the background.")).toBeTruthy();
  expect(signal?.aborted).toBe(false);
});

/** A window of the given width: every max-width query at or above it matches. */
function stubWidth(width: number) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: Number(/max-width: (\d+)px/.exec(query)?.[1]) >= width,
    addEventListener() {}, removeEventListener() {},
  }));
}

it("puts all navigation in one drawer behind a phone bar, and the panel switch in the bar", async () => {
  stubWidth(390);
  const definition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Warm house", spec: { root: "heading", elements: { heading: { type: "Heading", props: { text: "Hello home" } } } } };
  vi.stubGlobal("fetch", vi.fn(async input => {
    const path = String(input);
    if (path === "/readyz") return new Response(JSON.stringify({ status: "not_ready" }), { status: 503 });
    const body = path === "/v1/agent/conversations" ? { conversations: [{ id: "c1", created_at: new Date().toISOString(), message_count: 1, preview: "Kitchen lights" }] }
      : path === "/v1/dashboards/dash-1" ? { id: "dash-1", revision: 1, definition, created_at: "", updated_at: "" } : null;
    if (!body) throw new Error(`Unexpected request ${path}`);
    return new Response(JSON.stringify(body));
  }));
  window.location.hash = "#/dashboards/dash-1";
  renderApp();
  const bar = within(screen.getByRole("banner"));
  expect(await bar.findByText("Dashboards")).toBeTruthy();
  const menu = await bar.findByRole("button", { name: "Open navigation, Hearth server Not ready" });
  const main = screen.getByRole("main");
  fireEvent.click(menu);
  expect(menu.getAttribute("aria-expanded")).toBe("true");
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Close navigation" }));
  expect(main.hasAttribute("inert")).toBe(true);
  const drawer = within(screen.getByRole("complementary", { name: "Hearth navigation" }));
  expect(await drawer.findByRole("link", { name: "Kitchen lights" })).toBeTruthy();
  fireEvent.keyDown(document, { key: "Escape" });
  expect(menu.getAttribute("aria-expanded")).toBe("false");
  expect(main.hasAttribute("inert")).toBe(false);
  expect(document.activeElement).toBe(menu);
  fireEvent.click(menu);
  fireEvent.click(drawer.getByRole("button", { name: "Close navigation" }));
  expect(menu.getAttribute("aria-expanded")).toBe("false");
  expect(document.activeElement).toBe(menu);
  // On Home with a view open, the Conversation/Canvas switch replaces the page name.
  fireEvent.click(await screen.findByRole("button", { name: "Ask Hearth to change" }));
  const panels = await bar.findByRole("group", { name: "Workspace panels" });
  expect(within(panels).getByRole("button", { name: "Canvas" })).toBeTruthy();
  // Choosing the open page from the drawer still closes it.
  fireEvent.click(menu);
  fireEvent.click(drawer.getByRole("link", { name: "Home" }));
  expect(menu.getAttribute("aria-expanded")).toBe("false");
  window.location.hash = "";
});

it("closes the rail's Recent drawer from its own Close button", async () => {
  stubWidth(900);
  vi.stubGlobal("fetch", vi.fn(async input => {
    const path = String(input);
    const body = path === "/readyz" ? { status: "ready" } : path === "/v1/agent/conversations" ? { conversations: [] } : null;
    if (!body) throw new Error(`Unexpected request ${path}`);
    return new Response(JSON.stringify(body));
  }));
  renderApp();
  const toggle = await screen.findByRole("button", { name: "Recent conversation history" });
  fireEvent.click(toggle);
  fireEvent.click(screen.getByRole("button", { name: "Close recent conversations" }));
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  expect(document.activeElement).toBe(toggle);
});
