import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor } from "storybook/test";
import { delay, http, HttpResponse } from "msw";
import { fixtureEntity } from "../../../../.storybook/fixtures";
import { useObservedEntity } from "../../../../.storybook/use-observed-entity";
import { PowerSwitch } from "./PowerSwitch";

const meta = {
  component: PowerSwitch,
  tags: ["ai-generated"],
  args: { entity: fixtureEntity(4), label: "Office plug power" },
  render: function Observed(args) {
    const entity = useObservedEntity(args.entity)!;
    return <PowerSwitch {...args} entity={entity} />;
  },
  parameters: {
    layout: "centered",
    docs: { description: { component: "A power Entity's switch. It shows the requested position while a Command is pending and observed State otherwise. The owning card supplies the refreshed Entity." } },
  },
} satisfies Meta<typeof PowerSwitch>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Off: Story = {
  play: async ({ canvas }) => { await expect(canvas.getByRole("switch", { name: "Office plug power" })).toHaveAttribute("aria-checked", "false"); },
};
export const On: Story = {
  args: { entity: fixtureEntity(3), label: "Office fan power" },
  play: async ({ canvas }) => { await expect(canvas.getByRole("switch", { name: "Office fan power" })).toHaveAttribute("aria-checked", "true"); },
};
export const TurnOn: Story = {
  play: async ({ canvas, userEvent }) => {
    const control = canvas.getByRole("switch", { name: "Office plug power" });
    await userEvent.click(control);
    await waitFor(() => expect(control).not.toHaveAttribute("aria-busy", "true"));
    await expect(control).toHaveAttribute("aria-checked", "true");
  },
};
/** The switch shows the requested position, marked busy, until the Command settles. */
export const Pending: Story = {
  beforeEach: ({ msw }) => { msw.use(http.post("/v1/entities/:id/commands", async () => { await delay("infinite"); return new HttpResponse(null); })); },
  play: async ({ canvas, userEvent }) => {
    const control = canvas.getByRole("switch");
    await userEvent.click(control);
    await waitFor(() => expect(control).toHaveAttribute("aria-checked", "true"));
    await expect(control).toHaveAttribute("aria-busy", "true");
  },
};
export const Disabled: Story = {
  args: { entity: fixtureEntity(4, { enabled: false }) },
  play: async ({ canvas }) => { await expect(canvas.getByRole("switch")).toBeDisabled(); },
};
