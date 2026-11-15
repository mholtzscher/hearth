import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState, type ComponentProps } from "react";
import { createMemoryRouter } from "react-router-dom";
import { expect } from "storybook/test";
import { createApplicationHandlers } from "../../.storybook/application-fixtures";
import { automationHandlers, automationId } from "../../.storybook/automation-fixtures";
import { expectNoHorizontalOverflow } from "../../.storybook/play";
import App, { appRoutes } from "../App";

const meta = {
  title: "application/Automations",
  component: App,
  tags: ["ai-generated", "!autodocs"],
  parameters: { application: true, layout: "fullscreen" },
  beforeEach: ({ msw }) => { msw.use(...createApplicationHandlers(), ...automationHandlers()); },
  render: function RoutedApplication() {
    const [router, setRouter] = useState<ReturnType<typeof createMemoryRouter>>();
    useEffect(() => {
      const instance = createMemoryRouter(appRoutes, { initialEntries: ["/automations"] });
      setRouter(instance);
      return () => instance.dispose();
    }, []);
    return router ? <App router={router}/> : <></>;
  },
} satisfies Meta<typeof App>;
export default meta;
type Story = StoryObj<Partial<ComponentProps<typeof App>>>;

export const Library: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("link", { name: "Open Hallway light at dusk" })).toHaveAttribute("href", `/automations/${automationId}`);
    await expect(await canvas.findByText("Household")).toBeVisible();
  },
};

export const NewDraft: Story = {
  play: async ({ canvas, userEvent }) => {
    await canvas.findByRole("link", { name: "Open Hallway light at dusk" });
    await userEvent.click(canvas.getByRole("button", { name: "New Automation" }));
    await expect(await canvas.findByRole("region", { name: "Automation editor" })).toBeVisible();
    await expect(canvas.getByText("Draft changes do not execute. Run saved Automations from their detail page.")).toBeVisible();
  },
};

export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await expect(await canvas.findByRole("link", { name: "Open Hallway light at dusk" })).toBeVisible();
    await canvas.findByText("Household");
    await expectNoHorizontalOverflow(canvasElement);
  },
};
