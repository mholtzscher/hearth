import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect } from "storybook/test";

import DevicesPage from "./devices-page";

const meta = {
  title: "Pages/DevicesPage",
  component: DevicesPage,
  tags: ["ai-generated"],
  parameters: {
    route: { path: "/devices", entry: "/devices" },
    docs: {
      description: {
        component:
          "The debug dashboard's Device list and inline detail. It takes no props. Select a Device to read its Entities. Rename writes are not mocked in these page-read scenarios.",
      },
    },
  },
} satisfies Meta<typeof DevicesPage>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Loaded: Story = {
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("button", { name: "Hallway lamp" })
    ).toBeVisible();
  },
};
export const SelectDevice: Story = {
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(
      await canvas.findByRole("button", { name: "Hallway lamp" })
    );
    await expect(
      await canvas.findByRole("link", { name: "Lamp brightness" })
    ).toBeVisible();
    await expect(
      canvas.getByRole("heading", { name: "Device detail" })
    ).toBeVisible();
  },
};
export const Empty: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      http.get("/v1/devices", () =>
        HttpResponse.json({ items: [], next_cursor: null })
      )
    );
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("No devices.")).toBeVisible();
  },
};
export const ReadFailed: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      http.get("/v1/devices", () =>
        HttpResponse.json(
          { detail: "Device list unavailable" },
          { status: 503 }
        )
      )
    );
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("alert")).toHaveTextContent(
      "Device list unavailable"
    );
  },
};
