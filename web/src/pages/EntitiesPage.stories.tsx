import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor } from "storybook/test";
import { http, HttpResponse } from "msw";
import { deviceId, entityId } from "../../.storybook/fixtures";
import EntitiesPage from "./EntitiesPage";

const meta = {
  component: EntitiesPage,
  tags: ["ai-generated"],
  parameters: {
    layout: "fullscreen",
    route: { path: "/entities", entry: "/entities" },
    docs: { description: { component: "The debug dashboard's Entity list. It takes no props; routing and synthetic Core HTTP responses supply its context." } },
  },
} satisfies Meta<typeof EntitiesPage>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Loaded: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("link", { name: "Office plug" })).toHaveAttribute("href", `/entities/${entityId(4)}`);
    await expect(canvas.getByRole("link", { name: "Front door" })).toBeVisible();
  },
};
/** Apply a real device_id filter instead of replacing the table in the story. */
export const FilterByDevice: Story = {
  play: async ({ canvas, userEvent }) => {
    await canvas.findByRole("link", { name: "Front door" });
    await userEvent.type(canvas.getByRole("textbox", { name: "device_id filter" }), deviceId(2));
    await userEvent.click(canvas.getByRole("button", { name: "Apply" }));
    await waitFor(() => {
      expect(canvas.getByRole("link", { name: "Lamp brightness" })).toBeVisible();
      expect(canvas.queryByRole("link", { name: "Front door" })).not.toBeInTheDocument();
    });
  },
};
export const Empty: Story = {
  beforeEach: ({ msw }) => { msw.use(http.get("/v1/entities", () => HttpResponse.json({ items: [], next_cursor: null }))); },
  play: async ({ canvas }) => { await expect(await canvas.findByText("No entities.")).toBeVisible(); },
};
export const ReadFailed: Story = {
  beforeEach: ({ msw }) => { msw.use(http.get("/v1/entities", () => HttpResponse.json({ detail: "Entity list unavailable" }, { status: 503 }))); },
  play: async ({ canvas }) => { await expect(await canvas.findByRole("alert")).toHaveTextContent("Entity list unavailable"); },
};
