import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { pairedEntityResponse } from "../../../../.storybook/paired-fixtures";
import { ViewDataProvider } from "../data/provider";
import { ViewRenderer } from "./view-renderer";

const id = "ent_019a3e6a-4111-7000-8000-000000000001";
const source = {
  kind: "entity-state",
  entity_id: id,
  entity_type: "hearth.temperature/v1",
};
const view = {
  schema_version: 1,
  catalog_version: "hearth.dashboard/v1",
  title: "Garage",
  spec: {
    root: "root",
    elements: {
      root: {
        type: "Stack",
        props: { gap: "large" },
        children: ["heading", "metric"],
      },
      heading: { type: "Heading", props: { text: "Climate" } },
      metric: {
        type: "Metric",
        props: { label: "Garage temperature", source },
      },
    },
  },
};
const respond = (availability: string) => {
  const entity = {
    id,
    type: "hearth.temperature/v1",
    name: "Temperature",
    enabled: true,
    availability: { status: availability },
    support: {},
    quantities: { "": { unit: "Cel", label: "°C", fraction_digits: 1 } },
    state: { value: 20, observed_at: new Date().toISOString() },
  };
  const canonical = {
    ...entity,
    quantities: { "": { unit: "mCel", label: "mCel", fraction_digits: 0 } },
    state: { ...entity.state, value: 20_000 },
  };
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    Response.json(pairedEntityResponse(entity, canonical))
  );
};

describe("view card structure", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("takes heading levels from the host: view headings, then card titles one level below", async () => {
    respond("available");
    await act(() =>
      render(
        <ViewDataProvider>
          <ViewRenderer headingLevel={3} definition={view} />
        </ViewDataProvider>,
        { wrapper: MemoryRouter }
      )
    );
    expect(
      screen.getByRole("heading", { level: 3, name: "Climate" })
    ).toBeTruthy();
    expect(
      screen.getByRole("heading", { level: 4, name: "Garage temperature" })
    ).toBeTruthy();
    cleanup();
    await act(() =>
      render(
        <ViewDataProvider>
          <ViewRenderer headingLevel={4} definition={view} />
        </ViewDataProvider>,
        { wrapper: MemoryRouter }
      )
    );
    expect(
      screen.getByRole("heading", { level: 4, name: "Climate" })
    ).toBeTruthy();
    expect(
      screen.getByRole("heading", { level: 5, name: "Garage temperature" })
    ).toBeTruthy();
    // Cards are named articles, not landmarks.
    expect(screen.queryAllByRole("region")).toHaveLength(0);
  });

  it("dims an unavailable reading and leads with its status", async () => {
    respond("unavailable");
    await act(() =>
      render(
        <ViewDataProvider>
          <ViewRenderer headingLevel={3} definition={view} />
        </ViewDataProvider>,
        { wrapper: MemoryRouter }
      )
    );
    const card = await screen.findByRole("article", {
      name: "Garage temperature",
    });
    const status = await within(card).findByText("Unavailable");
    expect(status.closest("li")?.dataset.tone).toBe("warn");
    expect(within(card).getByText("20.0").dataset.value).toBe("dimmed");
    expect(card.querySelector('[data-slot="card-arrival"]')).toBeNull();
  });

  it("links Source to the Entity while expanded", async () => {
    respond("unavailable");
    await act(() =>
      render(
        <ViewDataProvider>
          <ViewRenderer headingLevel={3} definition={view} />
        </ViewDataProvider>,
        { wrapper: MemoryRouter }
      )
    );
    const card = await screen.findByRole("article", {
      name: "Garage temperature",
    });
    const toggle = within(card).getByRole("button", { name: "Source" });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    const details = within(
      screen.getByRole("dialog", { name: "Reading details" })
    );
    expect(
      details.getByRole("link", { name: "Temperature" }).getAttribute("href")
    ).toBe(`/entities/${id}`);
    expect(details.getByText("Unavailable")).toBeTruthy();
    expect(details.getByText("Enabled")).toBeTruthy();
  });

  it("separates combined statuses into distinct list items", async () => {
    const entity = {
      id,
      type: "hearth.temperature/v1",
      name: "Temperature",
      enabled: false,
      availability: { status: "unavailable" },
      support: {},
      quantities: { "": { unit: "Cel", label: "°C", fraction_digits: 1 } },
      state: { value: 20, observed_at: new Date().toISOString() },
    };
    const canonical = {
      ...entity,
      quantities: { "": { unit: "mCel", label: "mCel", fraction_digits: 0 } },
      state: { ...entity.state, value: 20_000 },
    };
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      Response.json(pairedEntityResponse(entity, canonical))
    );
    await act(() =>
      render(
        <ViewDataProvider>
          <ViewRenderer headingLevel={3} definition={view} />
        </ViewDataProvider>,
        { wrapper: MemoryRouter }
      )
    );
    const card = await screen.findByRole("article", {
      name: "Garage temperature",
    });
    await within(card).findByText("Disabled");
    expect(
      within(within(card).getByRole("list"))
        .getAllByRole("listitem")
        .map((li) => li.textContent)
    ).toStrictEqual(["Unavailable", "Disabled"]);
  });

  it.each([
    ["single readings", ["Metric", "State"], "reading"],
    ["quick switches", ["ToggleTile", "ToggleTile"], "toggle"],
    ["purifier tiles", ["AirPurifierTile", "AirPurifierTile"], "toggle"],
    [
      "purifier tiles and switches",
      ["AirPurifierTile", "ToggleTile"],
      "toggle",
    ],
    [
      "purifier card mixed with tiles",
      ["AirPurifierTile", "AirPurifierCard"],
      null,
    ],
    ["mixed content", ["Metric", "Heading"], null],
  ])(
    "selects the compact grid layout only for %s",
    async (_name, types, expected) => {
      respond("available");
      const power = { ...source, entity_type: "hearth.power/v1" };
      const elements = Object.fromEntries(
        types.map((type, i) => {
          if (type.startsWith("AirPurifier")) {
            return [
              `child${i}`,
              {
                type: "AirPurifier",
                props: {
                  layout: type === "AirPurifierTile" ? "tile" : "card",
                  power,
                  pm25: {
                    ...source,
                    entity_id: "ent_019a3e6a-4111-7000-8000-000000000002",
                    entity_type: "hearth.numericsensor/v1",
                  },
                },
              },
            ];
          }
          if (type === "Heading") {
            return [`child${i}`, { type, props: { text: "Heading" } }];
          }
          return [
            `child${i}`,
            { type, props: { source: type === "Metric" ? source : power } },
          ];
        })
      );
      const definition = {
        ...view,
        spec: {
          root: "root",
          elements: {
            ...elements,
            root: {
              type: "Grid",
              props: { columns: 3 },
              children: types.map((_, i) => `child${i}`),
            },
          },
        },
      };
      let container: HTMLElement | undefined;
      await act(() => {
        ({ container } = render(
          <ViewDataProvider>
            <ViewRenderer headingLevel={3} definition={definition} />
          </ViewDataProvider>,
          { wrapper: MemoryRouter }
        ));
      });
      if (!container) {
        throw new Error("Expected the rendered view container");
      }
      const grid = container.querySelector<HTMLElement>(
        '[data-slot="view-grid"]'
      );
      if (!grid) {
        throw new Error("Expected the view grid");
      }
      expect(grid.dataset.gridKind ?? null).toBe(expected);
      expect(grid.style.getPropertyValue("--view-columns")).toBe("3");
    }
  );
});
