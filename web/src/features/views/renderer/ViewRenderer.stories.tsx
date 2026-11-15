import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";
import { delay, http, HttpResponse } from "msw";
import { catalogExamples, createEntities, dashboardDefinition, definitionFor, entityId, weatherDefinition } from "../../../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../../../.storybook/play";
import { pairedEntityResponse } from "../../../../.storybook/paired-fixtures";
import { ViewRenderer } from "./ViewRenderer";

// Controls choose a valid example instead of editing raw definition JSON.
const examples = Object.fromEntries([
  ["Morning dashboard", dashboardDefinition],
  ...Object.values(catalogExamples).map(element => [element.type, definitionFor(element)]),
]);

const meta = {
  component: ViewRenderer,
  tags: ["ai-generated"],
  args: { definition: dashboardDefinition, headingLevel: 3 },
  argTypes: { definition: { options: Object.keys(examples), mapping: examples, control: { type: "select" } } },
  parameters: { docs: { description: { component: "Render a validated hearth.dashboard/v1 graph. Layout and text nodes are catalog elements, not independent React components. Unsupported content remains recoverable." } } },
} satisfies Meta<typeof ViewRenderer>;
export default meta;
type Story = StoryObj<typeof meta>;

export const MorningDashboard: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("9.4")).toBeVisible();
    await expect(await canvas.findByRole("switch")).toHaveAttribute("aria-checked", "true");
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
    await expect(await canvas.findByText(/24 h high/)).toBeVisible();
    const outside = canvas.getByRole("article", { name: "Outside" });
    const rowCount = outside.querySelectorAll(".view-row").length;
    const cards = [...canvasElement.querySelectorAll("article")];
    const bounds = () => cards.map(card => {
      const rect = card.getBoundingClientRect();
      return { top: rect.top, height: rect.height };
    });
    const before = bounds(), samples: ReturnType<typeof bounds>[] = [];
    let fallbackAppeared = false;
    const observer = new MutationObserver(() => {
      samples.push(bounds());
      fallbackAppeared ||= outside.querySelectorAll(".view-row").length !== rowCount ||
        !outside.querySelector(".view-weather-conditions") ||
        !canvasElement.querySelector(".view-wind .view-weather-conditions");
    });
    observer.observe(canvasElement, { childList: true, subtree: true, characterData: true });
    const speed = createEntities().find(entity => entity.id === entityId(103))!;
    const newer = { ...speed, state: { ...speed.state!, value: 9.4, observed_at: "2026-11-09T10:00:31Z" } };
    msw.use(http.get(`/v1/entities/${speed.id}`, async () => {
      await delay(150);
      return HttpResponse.json(pairedEntityResponse(newer, newer));
    }));
    try {
      canvasElement.ownerDocument.defaultView!.dispatchEvent(new Event("focus"));
      await expect(within(canvas.getByRole("article", { name: "Wind" })).getByText("6.3")).toBeVisible();
      await expect(await canvas.findByText("Fresh breeze")).toBeVisible();
      await expect(within(outside).getByText("Rain · Windy")).toBeVisible();
      await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
      expect(fallbackAppeared).toBe(false);
      expect(samples.length).toBeGreaterThan(0);
      for (const sample of samples) {
        sample.forEach((rect, index) => {
          expect(rect.height).toBeCloseTo(before[index].height, 0);
          expect(rect.top).toBeCloseTo(before[index].top, 0);
        });
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
export const Stack: Story = { args: { definition: definitionFor(catalogExamples.Stack) } };
export const Grid: Story = { args: { definition: definitionFor(catalogExamples.Grid) } };
export const CompactReadingGrid: Story = {
  args: { definition: {
    schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Readings",
    spec: { root: "root", elements: {
      root: { type: "Stack", props: { gap: "small" }, children: ["grid"] },
      grid: { type: "Grid", props: { columns: 2 }, children: ["metric", "state"] },
      metric: catalogExamples.Metric, state: catalogExamples.State,
    } },
  } },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("94.0")).toBeVisible();
    await expect(await canvas.findByText("Closed")).toBeVisible();
  },
};
export const CompactToggleGrid: Story = {
  args: { definition: {
    schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Quick switches",
    spec: { root: "root", elements: {
      root: { type: "Grid", props: { columns: 2 }, children: ["plug", "cover"] },
      plug: catalogExamples.ToggleTile,
      cover: { type: "Cover", props: { layout: "tile", label: "Garage door",
        source: { kind: "entity-state", entity_id: entityId(401), entity_type: "hearth.cover/v1" } } },
    } },
  } },
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("switch", { name: "Office plug power" })).toHaveAttribute("aria-checked", "false");
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
    const grid = metered.parentElement!;
    await expect(metered.getBoundingClientRect().width).toBeCloseTo(grid.getBoundingClientRect().width, 0);
    await expect(cover.getBoundingClientRect().width).toBeLessThan(metered.getBoundingClientRect().width);
    await expectNoHorizontalOverflow(canvasElement);
  },
};
export const Heading: Story = { args: { definition: definitionFor(catalogExamples.Heading) } };
export const Note: Story = { args: { definition: definitionFor(catalogExamples.Note) } };
export const Inactive: Story = {
  args: { active: false },
  play: async ({ canvas }) => { await expect(canvas.getByText("Open view to load live readings")).toBeVisible(); },
};
export const UnsupportedElement: Story = {
  args: { definition: {
    schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Future dashboard",
    spec: { root: "root", elements: { root: { type: "FutureCard", props: {} } } },
  } },
  play: async ({ canvas }) => { await expect(canvas.getByText(/original content preserved/)).toBeVisible(); },
};
export const InvalidDefinition: Story = {
  args: { definition: { schema_version: 999 } },
  play: async ({ canvas }) => { await expect(canvas.getByText("Invalid view definition.")).toBeVisible(); },
};
