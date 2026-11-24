import type { Meta } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";

import { applicationMeta } from "../../.storybook/application";
import type { ApplicationStory } from "../../.storybook/application";
import { entityId } from "../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../.storybook/play";
import App from "../app";

const meta = {
  title: "Application/Development",
  component: App,
  tags: ["ai-generated", "!autodocs"],
  decorators: applicationMeta.decorators,
  parameters: applicationMeta.parameters,
  argTypes: applicationMeta.argTypes,
  loaders: applicationMeta.loaders,
  beforeEach: applicationMeta.beforeEach,
  render: (_args, { loaded }) => <App router={loaded.router} />,
} satisfies Meta<typeof App>;
export default meta;

/** The phone toolbar keeps page switching, Entity jump, and diagnostics reachable. */
export const PhoneDevelopmentNavigation: ApplicationStory = {
  parameters: { appPath: "/devices" },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await expect(
      await canvas.findByRole("cell", { name: "Weather station" })
    ).toBeVisible();
    const pages = canvas.getByRole("combobox", { name: "Development page" });
    await expect(pages).toHaveValue("/devices");
    await userEvent.selectOptions(pages, "/entities");
    await expect(
      await canvas.findByRole("heading", { name: "Entities", level: 1 })
    ).toBeVisible();
    await expect(pages).toHaveValue("/entities");
    const jump = canvas.getByRole("textbox", { name: "Jump to entity id" });
    await userEvent.type(jump, `${entityId(4)}{Enter}`);
    await expect(
      await canvas.findByRole("heading", { name: "Office plug", level: 1 })
    ).toBeVisible();
    await expect(pages).toHaveValue(`/entities/${entityId(4)}`);
    const settings = canvas.getByRole("button", {
      name: "Connection settings",
    });
    await userEvent.click(settings);
    const body = within(canvasElement.ownerDocument.body);
    await expect(await body.findByText("healthz: ok")).toBeVisible();
    await expect(await body.findByText("readyz: ready")).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await expect(settings).toHaveFocus();
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const PhoneDevelopmentReturnToList: ApplicationStory = {
  parameters: { appPath: `/entities/${entityId(4)}` },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await expect(
      await canvas.findByRole("heading", { name: "Office plug", level: 1 })
    ).toBeVisible();
    const pages = canvas.getByRole("combobox", { name: "Development page" });
    await expect(pages).toHaveValue(`/entities/${entityId(4)}`);
    await userEvent.selectOptions(pages, "/entities");
    await expect(
      await canvas.findByRole("heading", { name: "Entities", level: 1 })
    ).toBeVisible();
    await expect(
      await canvas.findByRole("cell", { name: "Office plug" })
    ).toBeVisible();
    await expect(pages).toHaveValue("/entities");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const DevelopmentConnectionSettings: ApplicationStory = {
  parameters: { appPath: "/devices" },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await expect(
      await canvas.findByRole("heading", { name: "Devices" })
    ).toBeVisible();
    const trigger = canvas.getByRole("button", { name: "Connection settings" });
    await userEvent.click(trigger);
    const body = within(canvasElement.ownerDocument.body);
    await waitFor(() =>
      expect(
        body.getByRole("textbox", { name: "hearthd base URL" })
      ).toBeVisible()
    );
    await expect(
      body.getByRole("link", { name: "Open OpenAPI JSON" })
    ).toHaveAttribute("href", "/openapi.json");
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(trigger).toHaveFocus());
  },
};
