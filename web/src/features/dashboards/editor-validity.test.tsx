import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import {
  createMemoryRouter,
  Link,
  Outlet,
  RouterProvider,
} from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ViewDataProvider } from "../views/data/provider";
import { Canvas } from "./canvas";
import { WorkspaceProvider } from "./workspace";

// Simulates D5's optional validity callback while keeping its implementation in D5.
vi.mock(import("../views/editor/view-editor"), () => ({
  ViewEditor: ({
    onValidityChange,
  }: {
    onValidityChange?: (valid: boolean) => void;
  }) => (
    <label>
      Intermediate title
      <input
        defaultValue="Home"
        onChange={(event) =>
          onValidityChange?.(event.target.value.trim() !== "")
        }
      />
    </label>
  ),
}));

const definition = {
  schema_version: 1,
  catalog_version: "hearth.dashboard/v1",
  title: "Home",
  spec: {
    root: "heading",
    elements: { heading: { type: "Heading", props: { text: "Home view" } } },
  },
};
const dashboard = {
  id: "dash-1",
  revision: 1,
  definition,
  created_at: "",
  updated_at: "",
};
const Root = () => (
  <ViewDataProvider>
    <WorkspaceProvider>
      <Link to="/other">Other page</Link>
      <Outlet />
    </WorkspaceProvider>
  </ViewDataProvider>
);
const mount = () => {
  const router = createMemoryRouter(
    [
      {
        path: "/",
        element: <Root />,
        children: [
          { path: "dashboards/:id", element: <Canvas dashboardPage /> },
          { path: "other", element: <p>Other page content</p> },
        ],
      },
    ],
    { initialEntries: ["/dashboards/dash-1"] }
  );
  render(<RouterProvider router={router} />);
  return router;
};

describe("Editor validity", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("blocks writes, customization and unload until invalid local text is fixed", async () => {
    const fetcher = vi.fn<typeof fetch>(() =>
      Promise.resolve(Response.json(dashboard))
    );
    vi.stubGlobal("fetch", fetcher);
    mount();
    await screen.findByText("Home view");
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Intermediate title"), {
      target: { value: "" },
    });
    expect(screen.getByRole("button", { name: "Save changes" })).toHaveProperty(
      "disabled",
      true
    );
    expect(screen.getByRole("button", { name: "Save as new" })).toHaveProperty(
      "disabled",
      true
    );
    expect(
      screen.getByRole("button", { name: "Ask Hearth to change" })
    ).toHaveProperty("disabled", true);
    expect(
      screen.getByText(
        /Fix or discard the invalid editor values before saving or asking Hearth/u
      )
    ).toBeTruthy();
    const unload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(unload);
    expect(unload.defaultPrevented).toBeTruthy();
  });

  it("blocks navigation with a dialog until invalid local text is fixed", async () => {
    const fetcher = vi.fn<typeof fetch>(() =>
      Promise.resolve(Response.json(dashboard))
    );
    vi.stubGlobal("fetch", fetcher);
    const router = mount();
    await screen.findByText("Home view");
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Intermediate title"), {
      target: { value: "" },
    });
    fireEvent.click(screen.getByText("Other page"));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("button", { name: "Save" })).toHaveProperty(
      "disabled",
      true
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Stay" }));
    expect(router.state.location.pathname).toBe("/dashboards/dash-1");
    fireEvent.change(screen.getByLabelText("Intermediate title"), {
      target: { value: "Home" },
    });
    expect(screen.getByRole("button", { name: "Save as new" })).toHaveProperty(
      "disabled",
      false
    );
    expect(fetcher).toHaveBeenCalledOnce();
  });

  it("discards invalid local text and remounts the editor with the saved value", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>(() => Promise.resolve(Response.json(dashboard)))
    );
    mount();
    await screen.findByText("Home view");
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Intermediate title"), {
      target: { value: "" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
    await waitFor(() =>
      expect(
        (screen.getByLabelText("Intermediate title") as HTMLInputElement).value
      ).toBe("Home")
    );
    expect(screen.getByRole("button", { name: "Save as new" })).toHaveProperty(
      "disabled",
      false
    );
  });
});
