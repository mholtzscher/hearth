import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import {
  catalogExamples,
  createEntities,
  dashboardDefinition,
  definitionFor,
  entityId,
  weatherDefinition,
} from "../../../../.storybook/fixtures";
import { pairedEntityResponse } from "../../../../.storybook/paired-fixtures";
import { expectNoHorizontalOverflow } from "../../../../.storybook/play";
import type { ViewDefinition } from "../catalog";
import { ViewRenderer } from "./view-renderer";

// Controls choose a valid example instead of editing raw definition JSON.
const examples = Object.fromEntries([
  ["Morning dashboard", dashboardDefinition],
  ...Object.values(catalogExamples).map((element) => [
    element.type,
    definitionFor(element),
  ]),
]);

const meta = {
  title: "Components/ViewRenderer",
  component: ViewRenderer,
  tags: ["ai-generated"],
  args: { definition: dashboardDefinition, headingLevel: 3 },
  argTypes: {
    definition: {
      options: Object.keys(examples),
      mapping: examples,
      control: { type: "select" },
    },
  },
  parameters: {
    docs: {
      description: {
        component:
          "Render a validated hearth.dashboard/v1 graph. Layout and text nodes are catalog elements, not independent React components. Unsupported content remains recoverable.",
      },
    },
  },
} satisfies Meta<typeof ViewRenderer>;
export default meta;
type Story = StoryObj<typeof meta>;

export const MorningDashboard: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("9.4")).toBeVisible();
    await expect(await canvas.findByRole("switch")).toHaveAttribute(
      "aria-checked",
      "true"
    );
  },
};
export const PhoneDashboard: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByText("9.4");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const WeatherRefresh: Story = {
  args: { definition: weatherDefinition },
  play: async ({ canvas, canvasElement, msw }) => {
    await expect(await canvas.findByText("Moderate breeze")).toBeVisible();
    await expect(await canvas.findByText("Falling")).toBeVisible();
    await expect(await canvas.findByText(/24 h high/u)).toBeVisible();
    const outside = canvas.getByRole("article", { name: "Outside" });
    const rowCount = outside.querySelectorAll(
      '[data-slot="reading-row"]'
    ).length;
    const cards = [...canvasElement.querySelectorAll("article")];
    const bounds = () =>
      cards.map((card) => {
        const rect = card.getBoundingClientRect();
        return { top: rect.top, height: rect.height };
      });
    const before = bounds();
    const samples: ReturnType<typeof bounds>[] = [];
    let fallbackAppeared = false;
    const observer = new MutationObserver(() => {
      samples.push(bounds());
      fallbackAppeared ||=
        outside.querySelectorAll('[data-slot="reading-row"]').length !==
          rowCount ||
        !outside.querySelector('[data-slot="weather-conditions"]') ||
        !within(canvasElement)
          .queryByRole("article", { name: "Wind" })
          ?.querySelector('[data-slot="weather-conditions"]');
    });
    observer.observe(canvasElement, {
      childList: true,
      subtree: true,
      characterData: true,
    });
    const speed = createEntities().find(
      (entity) => entity.id === entityId(103)
    );
    if (!speed) {
      throw new Error("Missing fixture Entity 103");
    }
    const { state } = speed;
    if (!state) {
      throw new Error("Fixture Entity 103 has no State");
    }
    const newer = {
      ...speed,
      state: {
        ...state,
        value: 9.4,
        observed_at: "2026-11-09T10:00:31Z",
      },
    };
    msw.use(
      http.get(`/v1/entities/${speed.id}`, async () => {
        await delay(150);
        return HttpResponse.json(pairedEntityResponse(newer, newer));
      })
    );
    try {
      const view = canvasElement.ownerDocument.defaultView;
      if (!view) {
        throw new Error("The story canvas has no window");
      }
      view.dispatchEvent(new Event("focus"));
      await expect(
        within(canvas.getByRole("article", { name: "Wind" })).getByText("6.3")
      ).toBeVisible();
      await expect(await canvas.findByText("Fresh breeze")).toBeVisible();
      await expect(within(outside).getByText("Rain · Windy")).toBeVisible();
      const frames = Promise.withResolvers<undefined>();
      requestAnimationFrame(() =>
        requestAnimationFrame(() => {
          frames.resolve(undefined);
        })
      );
      await frames.promise;
      expect(fallbackAppeared).toBe(false);
      expect(samples.length).toBeGreaterThan(0);
      for (const sample of samples) {
        for (const [index, rect] of sample.entries()) {
          expect(rect.height).toBeCloseTo(before[index].height, 0);
          expect(rect.top).toBeCloseTo(before[index].top, 0);
        }
      }
      await expectNoHorizontalOverflow(canvasElement);
    } finally {
      observer.disconnect();
    }
  },
};

export const PhoneWeatherRefresh: Story = {
  ...WeatherRefresh,
  globals: { viewport: { value: "phone", isRotated: false } },
};
export const Stack: Story = {
  args: { definition: definitionFor(catalogExamples.Stack) },
};
export const Grid: Story = {
  args: { definition: definitionFor(catalogExamples.Grid) },
};
export const CompactReadingGrid: Story = {
  args: {
    definition: {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Readings",
      spec: {
        root: "root",
        elements: {
          root: { type: "Stack", props: { gap: "small" }, children: ["grid"] },
          grid: {
            type: "Grid",
            props: { columns: 2 },
            children: ["metric", "state"],
          },
          metric: catalogExamples.Metric,
          state: catalogExamples.State,
        },
      },
    },
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("94.0")).toBeVisible();
    await expect(await canvas.findByText("Closed")).toBeVisible();
  },
};
export const CompactToggleGrid: Story = {
  args: {
    definition: {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Quick switches",
      spec: {
        root: "root",
        elements: {
          root: {
            type: "Grid",
            props: { columns: 2 },
            children: ["plug", "cover"],
          },
          plug: catalogExamples.ToggleTile,
          cover: {
            type: "Cover",
            props: {
              layout: "tile",
              label: "Garage door",
              source: {
                kind: "entity-state",
                entity_id: entityId(401),
                entity_type: "hearth.cover/v1",
              },
            },
          },
        },
      },
    },
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("switch", { name: "Office plug power" })
    ).toHaveAttribute("aria-checked", "false");
    await expect(await canvas.findByText("Closed")).toBeVisible();
  },
};
export const PhoneMeteredToggleGrid: Story = {
  ...CompactToggleGrid,
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    const metered = await canvas.findByRole("article", { name: "Office plug" });
    await expect(await within(metered).findByText("7.9")).toBeVisible();
    const cover = await canvas.findByRole("article", { name: "Garage door" });
    const grid = metered.parentElement;
    if (!grid) {
      throw new Error("The metered card has no grid parent");
    }
    await expect(metered.getBoundingClientRect().width).toBeCloseTo(
      grid.getBoundingClientRect().width,
      0
    );
    await expect(cover.getBoundingClientRect().width).toBeLessThan(
      metered.getBoundingClientRect().width
    );
    await expectNoHorizontalOverflow(canvasElement);
  },
};

/** Cards per row, top to bottom, so a 3 + 1 wrap is distinguishable from 2 + 2. */
const expectGridRows = async (
  canvasElement: HTMLElement,
  expected: number[]
) => {
  const grid = canvasElement.querySelector('[data-slot="view-grid"]');
  if (!grid) {
    throw new Error("Expected the view grid");
  }
  const rows = new Map<number, number>();
  for (const card of grid.children) {
    const top = Math.round(card.getBoundingClientRect().top);
    rows.set(top, (rows.get(top) ?? 0) + 1);
  }
  await expect([...rows.values()]).toEqual(expected);
};

// Literal definitions without spreads, so the Storybook manifest can extract complete snippets.
const fourCardGrid: ViewDefinition = {
  schema_version: 1,
  catalog_version: "hearth.dashboard/v1",
  title: "Weather",
  spec: {
    root: "root",
    elements: {
      root: {
        type: "Grid",
        props: { columns: 4 },
        children: ["outside", "wind", "rain", "pressure"],
      },
      outside: catalogExamples.OutsideNow,
      wind: catalogExamples.Wind,
      rain: catalogExamples.Rain,
      pressure: catalogExamples.Pressure,
    },
  },
};

const fourTileGrid: ViewDefinition = {
  schema_version: 1,
  catalog_version: "hearth.dashboard/v1",
  title: "Quick switches",
  spec: {
    root: "root",
    elements: {
      root: {
        type: "Grid",
        props: { columns: 4 },
        children: ["plug", "fan", "garage", "purifier"],
      },
      plug: {
        type: "ToggleTile",
        props: {
          label: "Office plug",
          source: catalogExamples.ToggleTile.props.source,
        },
      },
      fan: {
        type: "ToggleTile",
        props: {
          label: "Fan",
          source: catalogExamples.ToggleList.props.rows[0].source,
        },
      },
      garage: {
        type: "Cover",
        props: {
          layout: "tile",
          label: "Garage door",
          source: catalogExamples.Cover.props.source,
        },
      },
      purifier: {
        type: "AirPurifier",
        props: {
          layout: "tile",
          label: "Living room purifier",
          power: catalogExamples.AirPurifier.props.power,
          pm25: catalogExamples.AirPurifier.props.pm25,
        },
      },
    },
  },
};

const awaitWeather = async (canvasElement: HTMLElement) => {
  await expect(
    await within(canvasElement).findByText("Moderate breeze")
  ).toBeVisible();
};

const awaitTiles = async (canvasElement: HTMLElement) => {
  const canvas = within(canvasElement);
  await expect(
    await canvas.findByRole("switch", { name: "Fan power" })
  ).toBeVisible();
  await expect(await canvas.findByText("Closed")).toBeVisible();
};

/** Four regular cards fill one row at exactly 4 × 248px + three gaps. */
export const FourColumnGridFitsFour: Story = {
  args: { definition: fourCardGrid },
  decorators: [
    (Story) => (
      <div className="w-[1028px]">
        <Story />
      </div>
    ),
  ],
  play: async ({ canvasElement }) => {
    await awaitWeather(canvasElement);
    await expectGridRows(canvasElement, [4]);
  },
};

/** One pixel narrower, the grid halves to 2 + 2 instead of wrapping 3 + 1. */
export const FourColumnGridHalves: Story = {
  args: { definition: fourCardGrid },
  decorators: [
    (Story) => (
      <div className="w-[1027px]">
        <Story />
      </div>
    ),
  ],
  play: async ({ canvasElement }) => {
    await awaitWeather(canvasElement);
    await expectGridRows(canvasElement, [2, 2]);
  },
};

/** The canvas docked beside a conversation is about 830px wide. */
export const FourColumnGridDocked: Story = {
  args: { definition: fourCardGrid },
  decorators: [
    (Story) => (
      <div className="w-[828px]">
        <Story />
      </div>
    ),
  ],
  play: async ({ canvasElement }) => {
    await awaitWeather(canvasElement);
    await expectGridRows(canvasElement, [2, 2]);
  },
};

export const PhoneFourColumnGrid: Story = {
  args: { definition: fourCardGrid },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvasElement }) => {
    await awaitWeather(canvasElement);
    await expectGridRows(canvasElement, [1, 1, 1, 1]);
    await expectNoHorizontalOverflow(canvasElement);
  },
};

/** Tiles use a 160px minimum, so four fit from 4 × 160px + three gaps. */
export const FourTileGridFitsFour: Story = {
  args: { definition: fourTileGrid },
  decorators: [
    (Story) => (
      <div className="w-[676px]">
        <Story />
      </div>
    ),
  ],
  play: async ({ canvasElement }) => {
    await awaitTiles(canvasElement);
    await expectGridRows(canvasElement, [4]);
  },
};

export const FourTileGridHalves: Story = {
  args: { definition: fourTileGrid },
  decorators: [
    (Story) => (
      <div className="w-[675px]">
        <Story />
      </div>
    ),
  ],
  play: async ({ canvasElement }) => {
    await awaitTiles(canvasElement);
    await expectGridRows(canvasElement, [2, 2]);
  },
};

export const Heading: Story = {
  args: { definition: definitionFor(catalogExamples.Heading) },
};
export const Note: Story = {
  args: { definition: definitionFor(catalogExamples.Note) },
};
export const Inactive: Story = {
  args: { active: false },
  play: async ({ canvas }) => {
    await expect(
      canvas.getByText("Open view to load live readings")
    ).toBeVisible();
  },
};
export const UnsupportedElement: Story = {
  args: {
    definition: {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Future dashboard",
      spec: {
        root: "root",
        elements: { root: { type: "FutureCard", props: {} } },
      },
    },
  },
  play: async ({ canvas }) => {
    await expect(canvas.getByText(/original content preserved/u)).toBeVisible();
  },
};
export const InvalidDefinition: Story = {
  args: { definition: { schema_version: 999 } },
  play: async ({ canvas }) => {
    await expect(canvas.getByText("Invalid view definition.")).toBeVisible();
  },
};
