import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { StrictMode } from "react";
import { createHashRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import App, { appRoutes } from "./app";

let router: ReturnType<typeof createHashRouter> | undefined;

const renderApp = (strict = false) => {
  router = createHashRouter(appRoutes);
  const app = <App router={router} />;
  return render(strict ? <StrictMode>{app}</StrictMode> : app);
};

/** A window of the given width: every max-width query at or above it matches. */
const stubWidth = (width: number) => {
  vi.stubGlobal("matchMedia", (query: string) => {
    const match = /max-width: (?<width>\d+)px/u.exec(query);
    return {
      matches: match ? Number(match.groups?.width) >= width : false,
      addEventListener() {},
      removeEventListener() {},
    };
  });
};

const stubFetch = (respond: (path: string, init?: RequestInit) => unknown) => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const body = respond(path, init);
      if (body === null || body === undefined) {
        throw new Error(`Unexpected request ${path}`);
      }
      return body instanceof Response ? body : Response.json(body);
    })
  );
};

const stubHouseholdFetch = () => {
  stubFetch((path) => {
    if (path === "/readyz") {
      return { status: "ready" };
    }
    if (path === "/healthz") {
      return { status: "ok" };
    }
    if (path === "/v1/agent/conversations") {
      return { conversations: [] };
    }
    if (path.startsWith("/v1/devices?")) {
      return { items: [] };
    }
    return null;
  });
};

const dashboardDefinition = {
  schema_version: 1,
  catalog_version: "hearth.dashboard/v1",
  title: "Warm house",
  spec: {
    root: "heading",
    elements: { heading: { type: "Heading", props: { text: "Hello home" } } },
  },
};

const stubConversationHistoryFetch = () => {
  stubFetch((path) => {
    if (path === "/readyz") {
      return { status: "ready" };
    }
    if (path === "/v1/agent/conversations") {
      return {
        conversations: [
          {
            id: "c1",
            created_at: "",
            message_count: 1,
            preview: "Kitchen lights",
          },
        ],
      };
    }
    if (path === "/v1/agent/conversations/c1/messages") {
      return {
        messages: [
          {
            message_id: 1,
            role: "user",
            content: "Kitchen lights?",
            created_at: "",
          },
        ],
      };
    }
    return null;
  });
};

const stubBackgroundConversation = () => {
  let created = false;
  let finished = false;
  let signal: AbortSignal | undefined;
  let stream: ReadableStreamDefaultController<Uint8Array> | undefined;

  stubFetch((path, init) => {
    if (path === "/v1/agent/conversations" && init?.method === "POST") {
      created = true;
      return { id: "c-new" };
    }
    if (path.endsWith("/messages/stream")) {
      signal = init?.signal as AbortSignal;
      return new Response(
        new ReadableStream({
          start(controller) {
            stream = controller;
          },
        })
      );
    }
    if (path === "/readyz") {
      return { status: "ready" };
    }
    if (path === "/v1/agent/conversations") {
      return {
        conversations: created
          ? [
              {
                id: "c-new",
                created_at: "",
                message_count: 1,
                preview: "Background question",
              },
            ]
          : [],
      };
    }
    if (path.endsWith("/messages")) {
      return {
        messages: [
          {
            message_id: 1,
            role: "user",
            content: "Background question",
            created_at: "",
          },
          ...(finished
            ? [
                {
                  message_id: 2,
                  role: "assistant",
                  content: "Finished in the background.",
                  created_at: "",
                },
              ]
            : []),
        ],
      };
    }
    if (path === "/v1/dashboards") {
      return { items: [] };
    }
    return null;
  });

  const finish = async () => {
    const controller = stream;
    if (!controller) {
      throw new Error("Background stream was not opened");
    }
    await act(() => {
      finished = true;
      controller.enqueue(
        new TextEncoder().encode(
          'data: {"type":"turn.finished","reply":"Finished in the background."}\n\n'
        )
      );
      controller.close();
    });
  };

  return { finish, getSignal: () => signal };
};

const stubPhoneDashboardFetch = () => {
  stubFetch((path) => {
    if (path === "/readyz") {
      return Response.json({ status: "not_ready" }, { status: 503 });
    }
    if (path === "/v1/agent/conversations") {
      return {
        conversations: [
          {
            id: "c1",
            created_at: new Date().toISOString(),
            message_count: 1,
            preview: "Kitchen lights",
          },
        ],
      };
    }
    if (path === "/v1/dashboards/dash-1") {
      return {
        id: "dash-1",
        revision: 1,
        definition: dashboardDefinition,
        created_at: "",
        updated_at: "",
      };
    }
    return null;
  });
  window.location.hash = "#/dashboards/dash-1";
};

describe(App, () => {
  beforeEach(() =>
    vi.stubGlobal(
      "ResizeObserver",
      class {
        targets = new Set<Element>();

        observe(target: Element) {
          this.targets.add(target);
        }

        unobserve(target: Element) {
          this.targets.delete(target);
        }

        disconnect() {
          this.targets.clear();
        }
      }
    )
  );
  afterEach(() => {
    cleanup();
    router?.dispose();
    router = undefined;
    vi.unstubAllGlobals();
    localStorage.clear();
    window.location.hash = "";
  });

  it("shows the Home workspace with server status in the sidebar", async () => {
    stubWidth(900);
    stubHouseholdFetch();
    renderApp();
    await expect(
      screen.findByRole("heading", { name: "Home workspace" })
    ).resolves.toBeTruthy();
    expect(
      screen
        .getByRole("navigation", { name: "Main navigation" })
        .nextElementSibling?.contains(
          screen.getByRole("button", { name: "Recent conversation history" })
        )
    ).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "Connection settings" })
    ).toBeNull();
    await expect(
      screen.findByText("Ready", { exact: true })
    ).resolves.toBeTruthy();
    expect(
      within(
        screen.getByRole("complementary", { name: "Hearth navigation" })
      ).getByRole("status").textContent
    ).toBe("Hearth server Ready");
  });

  it("keeps connection setup in Development, not the household workspace", async () => {
    stubWidth(900);
    stubHouseholdFetch();
    renderApp();
    await screen.findByRole("heading", { name: "Home workspace" });
    fireEvent.click(screen.getByRole("link", { name: "Development" }));
    await expect(screen.findByText("No devices.")).resolves.toBeTruthy();
    expect(
      screen.getAllByRole("button", { name: "Connection settings" })
    ).toHaveLength(1);
    fireEvent.click(screen.getByRole("link", { name: "Home" }));
    await expect(
      screen.findByRole("heading", { name: "Home workspace" })
    ).resolves.toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "Connection settings" })
    ).toBeNull();
  });

  it("keeps hash navigation and browser Back working after StrictMode effect replay", async () => {
    stubHouseholdFetch();
    renderApp(true);
    await screen.findByRole("heading", { name: "Home workspace" });
    fireEvent.click(screen.getByRole("link", { name: "Development" }));
    await expect(
      screen.findByRole("heading", { name: "Devices" })
    ).resolves.toBeTruthy();
    await act(() => {
      window.history.back();
    });
    await expect(
      screen.findByRole("heading", { name: "Home workspace" })
    ).resolves.toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Devices" })).toBeNull();
  });

  it("narrows the sidebar to a rail beside a canvas below 1280px", async () => {
    // A 1100px window: wider than the 980px rail breakpoint, narrower than 1280px.
    stubWidth(1100);
    stubFetch((path) => {
      if (path === "/readyz") {
        return { status: "ready" };
      }
      if (path === "/v1/agent/conversations") {
        return { conversations: [] };
      }
      if (path === "/v1/dashboards/dash-1") {
        return {
          id: "dash-1",
          revision: 1,
          definition: dashboardDefinition,
          created_at: "",
          updated_at: "",
        };
      }
      return null;
    });
    window.location.hash = "#/dashboards/dash-1";
    renderApp();
    const sidebar = screen.getByRole("complementary", {
      name: "Hearth navigation",
    });
    const ask = await screen.findByRole("button", {
      name: "Ask Hearth to change",
    });
    // A dashboard page has no conversation beside it, so it keeps the full sidebar.
    expect(sidebar.dataset.nav).toBe("full");
    fireEvent.click(ask);
    await expect(
      screen.findByRole("heading", { name: "Home workspace" })
    ).resolves.toBeTruthy();
    expect(sidebar.dataset.nav).toBe("rail");
    fireEvent.click(screen.getByRole("button", { name: "Close view" }));
    await waitFor(() => expect(sidebar.dataset.nav).toBe("full"));
    window.location.hash = "";
  });

  it("lists recent conversations and marks the open one", async () => {
    stubWidth(900);
    stubConversationHistoryFetch();
    renderApp();
    const recent = within(
      await screen.findByRole("region", { name: "Recent" })
    );
    fireEvent.click(
      await recent.findByRole("link", { name: "Kitchen lights" })
    );
    await expect(screen.findByText("Kitchen lights?")).resolves.toBeTruthy();
    expect(
      recent
        .getByRole("link", { name: "Kitchen lights" })
        .getAttribute("aria-current")
    ).toBe("page");
  });

  it("moves focus into the Recent drawer and closes it with Escape", async () => {
    stubWidth(900);
    stubConversationHistoryFetch();
    renderApp();
    const recent = within(
      await screen.findByRole("region", { name: "Recent" })
    );
    const toggle = screen.getByRole("button", {
      name: "Recent conversation history",
    });
    fireEvent.click(toggle);
    expect(document.activeElement).toBe(
      recent.getByRole("link", { name: "Kitchen lights" })
    );
    fireEvent.keyDown(document, { key: "Escape" });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(toggle);
  });

  it("starts a new conversation and clears an unsent draft", async () => {
    stubWidth(900);
    stubConversationHistoryFetch();
    renderApp();
    const recent = within(
      await screen.findByRole("region", { name: "Recent" })
    );
    fireEvent.click(
      await recent.findByRole("link", { name: "Kitchen lights" })
    );
    await screen.findByText("Kitchen lights?");
    fireEvent.click(screen.getByRole("button", { name: "New conversation" }));
    await expect(
      screen.findByText("What would make home feel better?")
    ).resolves.toBeTruthy();
    expect(
      recent
        .getByRole("link", { name: "Kitchen lights" })
        .getAttribute("aria-current")
    ).toBeNull();
    const prompt = screen.getByRole("textbox", { name: "Ask Hearth" });
    fireEvent.change(prompt, { target: { value: "Unsent draft" } });
    fireEvent.click(screen.getByRole("button", { name: "New conversation" }));
    expect(prompt).toHaveProperty("value", "");
  });

  it("publishes a new conversation immediately and lists it in Recent", async () => {
    stubBackgroundConversation();
    renderApp();
    const prompt = await screen.findByRole("textbox", { name: "Ask Hearth" });
    fireEvent.change(prompt, { target: { value: "Background question" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await expect(
      screen.findByText("Looking through your home…")
    ).resolves.toBeTruthy();
    await waitFor(() =>
      expect(window.location.hash).toBe("#/conversations/c-new")
    );
    const recent = within(screen.getByRole("region", { name: "Recent" }));
    await expect(
      recent.findByRole("link", { name: "Background question" })
    ).resolves.toBeTruthy();
    expect(
      screen.getByRole("button", { name: "New conversation" })
    ).toHaveProperty("disabled", false);
  });

  it("keeps a background reply running after leaving Home", async () => {
    const background = stubBackgroundConversation();
    renderApp();
    const prompt = await screen.findByRole("textbox", { name: "Ask Hearth" });
    fireEvent.change(prompt, { target: { value: "Background question" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => expect(background.getSignal()).toBeDefined());
    fireEvent.click(screen.getByRole("link", { name: "Dashboards" }));
    await expect(
      screen.findByRole("heading", { level: 1, name: "Dashboards" })
    ).resolves.toBeTruthy();
    expect(background.getSignal()?.aborted).toBeFalsy();
  });

  it("delivers a background reply after the user leaves Home", async () => {
    const background = stubBackgroundConversation();
    renderApp();
    const prompt = await screen.findByRole("textbox", { name: "Ask Hearth" });
    fireEvent.change(prompt, { target: { value: "Background question" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => expect(background.getSignal()).toBeDefined());
    fireEvent.click(screen.getByRole("link", { name: "Dashboards" }));
    await screen.findByRole("heading", { level: 1, name: "Dashboards" });
    await background.finish();
    expect(window.location.hash).toBe("#/dashboards");
    const recent = within(screen.getByRole("region", { name: "Recent" }));
    fireEvent.click(recent.getByRole("link", { name: "Background question" }));
    await expect(
      within(screen.getByRole("log")).findByText("Finished in the background.")
    ).resolves.toBeTruthy();
    expect(background.getSignal()?.aborted).toBeFalsy();
  });

  it("puts all navigation in one drawer behind a phone bar", async () => {
    stubWidth(390);
    stubPhoneDashboardFetch();
    renderApp();
    const bar = within(screen.getByRole("banner"));
    await expect(bar.findByText("Dashboards")).resolves.toBeTruthy();
    const menu = await bar.findByRole("button", {
      name: "Open navigation, Hearth server Not ready",
    });
    const main = screen.getByRole("main");
    fireEvent.click(menu);
    expect(menu.getAttribute("aria-expanded")).toBe("true");
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Close navigation" })
    );
    expect(main.hasAttribute("inert")).toBeTruthy();
    const drawer = within(
      screen.getByRole("complementary", { name: "Hearth navigation" })
    );
    await expect(
      drawer.findByRole("link", { name: "Kitchen lights" })
    ).resolves.toBeTruthy();
  });

  it("closes the phone drawer with Escape and restores focus", async () => {
    stubWidth(390);
    stubPhoneDashboardFetch();
    renderApp();
    const bar = within(screen.getByRole("banner"));
    const menu = await bar.findByRole("button", {
      name: "Open navigation, Hearth server Not ready",
    });
    const main = screen.getByRole("main");
    fireEvent.click(menu);
    expect(menu.getAttribute("aria-expanded")).toBe("true");
    fireEvent.keyDown(document, { key: "Escape" });
    expect(menu.getAttribute("aria-expanded")).toBe("false");
    expect(main.hasAttribute("inert")).toBeFalsy();
    expect(document.activeElement).toBe(menu);
  });

  it("closes the phone drawer from its Close button", async () => {
    stubWidth(390);
    stubPhoneDashboardFetch();
    renderApp();
    const bar = within(screen.getByRole("banner"));
    const menu = await bar.findByRole("button", {
      name: "Open navigation, Hearth server Not ready",
    });
    fireEvent.click(menu);
    expect(menu.getAttribute("aria-expanded")).toBe("true");
    const drawer = within(
      screen.getByRole("complementary", { name: "Hearth navigation" })
    );
    fireEvent.click(drawer.getByRole("button", { name: "Close navigation" }));
    expect(menu.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(menu);
  });

  it("replaces the page name with the panel switch and closes the drawer when choosing the open page", async () => {
    stubWidth(390);
    stubPhoneDashboardFetch();
    renderApp();
    const bar = within(screen.getByRole("banner"));
    const menu = await bar.findByRole("button", {
      name: "Open navigation, Hearth server Not ready",
    });
    fireEvent.click(
      await screen.findByRole("button", { name: "Ask Hearth to change" })
    );
    const panels = await bar.findByRole("group", { name: "Workspace panels" });
    expect(within(panels).getByRole("button", { name: "Canvas" })).toBeTruthy();
    fireEvent.click(menu);
    const drawer = within(
      screen.getByRole("complementary", { name: "Hearth navigation" })
    );
    fireEvent.click(drawer.getByRole("link", { name: "Home" }));
    expect(menu.getAttribute("aria-expanded")).toBe("false");
  });

  it("closes the rail's Recent drawer from its own Close button", async () => {
    stubWidth(900);
    stubFetch((path) => {
      if (path === "/readyz") {
        return { status: "ready" };
      }
      if (path === "/v1/agent/conversations") {
        return { conversations: [] };
      }
      return null;
    });
    renderApp();
    const toggle = await screen.findByRole("button", {
      name: "Recent conversation history",
    });
    fireEvent.click(toggle);
    fireEvent.click(
      screen.getByRole("button", { name: "Close recent conversations" })
    );
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(toggle);
  });
});
