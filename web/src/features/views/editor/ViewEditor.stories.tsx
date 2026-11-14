import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn } from "storybook/test";
import { http, HttpResponse } from "msw";
import { dashboardDefinition } from "../../../../.storybook/fixtures";
import { ViewEditor } from "./ViewEditor";

const meta = {
  component: ViewEditor,
  tags: ["ai-generated"],
  args: { definition: dashboardDefinition, onChange: fn(), onValidityChange: fn() },
  parameters: { docs: { description: { component: "Edits a local validated definition. Draft changes are not persisted until the owning workspace saves." } } },
} satisfies Meta<typeof ViewEditor>;
export default meta;
type Story = StoryObj<typeof meta>;

export const RenameDraft: Story = {
  play: async ({ canvas, userEvent, args }) => {
    const title = canvas.getByRole("textbox", { name: "Dashboard title" });
    await userEvent.clear(title);
    await userEvent.type(title, "Evening overview");
    await expect(args.onChange).toHaveBeenLastCalledWith(expect.objectContaining({ title: "Evening overview" }));
    await expect(args.onValidityChange).toHaveBeenLastCalledWith(true);
  },
};
export const InvalidTitle: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.clear(canvas.getByRole("textbox", { name: "Dashboard title" }));
    await expect(canvas.getByRole("textbox", { name: "Dashboard title" })).toHaveAttribute("aria-invalid", "true");
    await expect(args.onValidityChange).toHaveBeenLastCalledWith(false);
    await expect(canvas.getByText("Use 1–80 characters for the dashboard title.")).toBeVisible();
  },
};
export const ChoicesUnavailable: Story = {
  beforeEach: ({ msw }) => { msw.use(http.get("/v1/entities", () => HttpResponse.json({ detail: "Unavailable" }, { status: 503 }))); },
  play: async ({ canvas }) => { await expect(await canvas.findByRole("status")).toHaveTextContent("Sensor list unavailable; existing selections are preserved."); },
};
