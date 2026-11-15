import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { http, HttpResponse } from "msw";
import { entityId } from "../../.storybook/fixtures";
import { entityScenario } from "../../.storybook/msw-handlers";
import EntityDetailPage from "./EntityDetailPage";

const meta = {
  title: "Pages/EntityDetailPage",
  component: EntityDetailPage,
  tags: ["ai-generated"],
  parameters: {
    route: { path: "/entities/:entityId", entry: `/entities/${entityId(4)}` },
    docs: { description: { component: "One Entity's debug page, including retained history and the Command form. It takes no props: the route supplies entityId. Historical fixtures and mock Commands are not device-effect evidence. Rename and enablement writes are not mocked in these page-read scenarios." } },
  },
} satisfies Meta<typeof EntityDetailPage>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Loaded: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("heading", { name: "Office plug" })).toBeVisible();
    await expect(await canvas.findByText("cmd_story_previous")).toBeVisible();
    await expect(await canvas.findByText("applied")).toBeVisible();
  },
};
/** A known Entity can have neither observed State nor retained history. */
export const NeverObserved: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      entityScenario(4, "unobserved"),
      http.get("/v1/entities/:id/commands", () => HttpResponse.json({ items: [], next_cursor: null })),
      http.get("/v1/entities/:id/availability/history", () => HttpResponse.json({ items: [], next_cursor: null })),
      http.get("/v1/entities/:id/state/history", () => HttpResponse.json({ items: [], next_cursor: null })),
    );
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("null (never observed)")).toBeVisible();
    await expect(await canvas.findByText("No commands sent yet.")).toBeVisible();
    await expect(await canvas.findByText("No availability transitions recorded.")).toBeVisible();
    await expect(await canvas.findByText("No state history recorded. No State updates to chart.")).toBeVisible();
  },
};
export const ReadFailed: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(4, "error")); },
  play: async ({ canvas }) => { await expect(await canvas.findByRole("alert")).toHaveTextContent("Story API unavailable"); },
};
export const Missing: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(4, "missing")); },
  play: async ({ canvas }) => { await expect(await canvas.findByRole("alert")).toHaveTextContent("Entity not found"); },
};
