import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import type { ComponentProps } from "react";
import { expect, waitFor } from "storybook/test";

import { fixtureEntity } from "../../../../.storybook/fixtures";
import { useObservedEntity } from "../../../../.storybook/use-observed-entity";
import { PowerSwitch } from "./power-switch";

const ObservedPowerSwitch = (args: ComponentProps<typeof PowerSwitch>) => {
  const entity = useObservedEntity(args.entity);
  if (!entity) {
    return null;
  }
  return <PowerSwitch {...args} entity={entity} />;
};

const meta = {
  title: "Components/Controls/PowerSwitch",
  component: PowerSwitch,
  tags: ["ai-generated"],
  args: { entity: fixtureEntity(4), label: "Office plug power" },
  render: (args) => <ObservedPowerSwitch {...args} />,
  parameters: {
    docs: {
      description: {
        component:
          "A power Entity's switch. It shows the requested position while a Command is pending and observed State otherwise. The owning card supplies the refreshed Entity.",
      },
    },
  },
} satisfies Meta<typeof PowerSwitch>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Off: Story = {
  play: async ({ canvas }) => {
    await expect(
      canvas.getByRole("switch", { name: "Office plug power" })
    ).toHaveAttribute("aria-checked", "false");
  },
};
export const On: Story = {
  args: { entity: fixtureEntity(3), label: "Office fan power" },
  play: async ({ canvas }) => {
    await expect(
      canvas.getByRole("switch", { name: "Office fan power" })
    ).toHaveAttribute("aria-checked", "true");
  },
};
export const TurnOn: Story = {
  play: async ({ canvas, userEvent }) => {
    const control = canvas.getByRole("switch", { name: "Office plug power" });
    await userEvent.click(control);
    await waitFor(() =>
      expect(control).not.toHaveAttribute("aria-busy", "true")
    );
    await expect(control).toHaveAttribute("aria-checked", "true");
  },
};
/** The switch shows the requested position, marked busy, until the Command settles. */
export const Pending: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      http.post("/v1/entities/:id/commands", async () => {
        await delay("infinite");
        return new HttpResponse(null);
      })
    );
  },
  play: async ({ canvas, userEvent }) => {
    const control = canvas.getByRole("switch");
    await userEvent.click(control);
    await waitFor(() =>
      expect(control).toHaveAttribute("aria-checked", "true")
    );
    await expect(control).toHaveAttribute("aria-busy", "true");
  },
};
export const Disabled: Story = {
  args: { entity: fixtureEntity(4, { enabled: false }) },
  play: async ({ canvas }) => {
    await expect(canvas.getByRole("switch")).toBeDisabled();
  },
};

export const MotionRefuse: Story = {
  name: "Motion · Rejected switch",
  tags: ["motion"],
  beforeEach: ({ msw }) => {
    msw.use(
      http.post("/v1/entities/:id/commands", async () => {
        await delay(450);
        return HttpResponse.json({ detail: "Rejected" }, { status: 502 });
      })
    );
  },
  play: async ({ canvas, userEvent }) => {
    const control = canvas.getByRole("switch");
    await userEvent.click(control);
    await expect(control).toHaveAttribute("aria-checked", "true");
    await waitFor(() =>
      expect(control).toHaveAttribute("aria-checked", "false")
    );
    await expect(getComputedStyle(control).animationName).toBe(
      matchMedia("(prefers-reduced-motion: reduce)").matches
        ? "none"
        : "motion-refuse"
    );
    await expect(control).toHaveFocus();
  },
};
