import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fireEvent, waitFor } from "storybook/test";
import { delay, http, HttpResponse } from "msw";
import { fixtureEntity } from "../../../../.storybook/fixtures";
import { createCoreHandlers } from "../../../../.storybook/msw-handlers";
import { useObservedEntity } from "../../../../.storybook/use-observed-entity";
import { BrightnessSlider } from "./BrightnessSlider";

const meta = {
  component: BrightnessSlider,
  tags: ["ai-generated"],
  args: { entity: fixtureEntity(13), label: "Hallway lamp brightness" },
  render: function Observed(args) {
    const entity = useObservedEntity(args.entity)!;
    return <BrightnessSlider {...args} entity={entity} />;
  },
  parameters: { docs: { description: { component: "A light's brightness from 1 to 100 %. A drag sends once on release and keyboard steps send after they settle. Only the power switch turns a light off." } } },
} satisfies Meta<typeof BrightnessSlider>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Observed: Story = {
  play: async ({ canvas }) => { await expect(canvas.getByRole("slider", { name: "Hallway lamp brightness" })).toHaveAttribute("aria-valuetext", "64 %"); },
};
export const SetBrightness: Story = {
  play: async ({ canvas }) => {
    const slider = canvas.getByRole("slider");
    await fireEvent.change(slider, { target: { value: "65" } });
    await fireEvent.blur(slider);
    await waitFor(() => expect(slider).not.toHaveAttribute("aria-busy", "true"));
    await expect(slider).toHaveAttribute("aria-valuetext", "65 %");
  },
};
/** An input change shows at once and sends one Command after it settles. */
export const SettledChange: Story = {
  beforeEach: ({ msw }) => { msw.use(http.post("/v1/entities/:id/commands", async () => { await delay("infinite"); return new HttpResponse(null); })); },
  play: async ({ canvas }) => {
    const slider = canvas.getByRole("slider");
    await fireEvent.change(slider, { target: { value: "65" } });
    await expect(slider).toHaveAttribute("aria-valuetext", "65 %");
    await expect(await canvas.findByText("Setting brightness to 65 %…")).toBeVisible();
  },
};
export const NeverObserved: Story = {
  args: { entity: fixtureEntity(13, { state: null }) },
  beforeEach: ({ msw }) => { msw.use(...createCoreHandlers([fixtureEntity(13, { state: null })])); },
  play: async ({ canvas }) => { await expect(canvas.getByRole("slider")).toHaveAttribute("aria-valuetext", "No value"); },
};
export const FirstObservation: Story = {
  args: { entity: fixtureEntity(13, { state: null }) },
  beforeEach: ({ msw }) => { msw.use(...createCoreHandlers([fixtureEntity(13, { state: null })])); },
  play: async ({ canvas }) => {
    const slider = canvas.getByRole("slider");
    await expect(slider).toHaveAttribute("aria-valuetext", "No value");
    await fireEvent.change(slider, { target: { value: "40" } });
    await fireEvent.blur(slider);
    await waitFor(() => {
      expect(slider).not.toHaveAttribute("aria-busy", "true");
      expect(slider).toHaveAttribute("aria-valuetext", "40 %");
    });
  },
};
export const Disabled: Story = {
  args: { entity: fixtureEntity(13, { enabled: false }) },
  play: async ({ canvas }) => { await expect(canvas.getByRole("slider")).toBeDisabled(); },
};
