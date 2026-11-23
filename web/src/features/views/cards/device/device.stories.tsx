import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, waitFor, within } from "storybook/test";

import {
  deviceId,
  entityId,
  fixtureEntity,
} from "../../../../../.storybook/fixtures";
import { createCoreHandlers } from "../../../../../.storybook/msw-handlers";
import {
  expectNoHorizontalOverflow,
  watchHeight,
} from "../../../../../.storybook/play";
import { BoundDevice } from "./device";

const meta = {
  title: "Components/Cards/Device",
  component: BoundDevice,
  tags: ["ai-generated"],
  args: {
    label: "Hallway lamp",
    device: { kind: "device", device_id: deviceId(2) },
  },
  parameters: {
    docs: {
      description: {
        component:
          "A capability-derived device card. A lamp combines one power Entity, one brightness Entity, and supported color controls.",
      },
    },
  },
} satisfies Meta<typeof BoundDevice>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Lamp: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("switch")).toHaveAttribute(
      "aria-checked",
      "true"
    );
  },
};
export const NarrowLampFailure: Story = {
  name: "Motion · Light failure status",
  tags: ["motion"],
  decorators: [
    (Story) => (
      <div className="w-[248px]">
        <Story />
      </div>
    ),
  ],
  beforeEach: ({ msw }) => {
    const color = fixtureEntity(12);
    color.support.operations = {};
    msw.use(
      http.post(`/v1/entities/${entityId(14)}/commands`, () =>
        HttpResponse.json({ detail: "Unreachable" }, { status: 503 })
      ),
      ...createCoreHandlers([color])
    );
  },
  play: async ({ canvas, userEvent }) => {
    const power = await canvas.findByRole("switch");
    await canvas.findByRole("slider");
    await canvas.findByText(/Hue 210.*Saturation 64/u);
    await watchHeight(canvas.getByRole("article"), async () => {
      await userEvent.click(power);
      await expect(await canvas.findByRole("alert")).toHaveTextContent(
        "Couldn't reach the light. Its adapter or the light is offline."
      );
    });
  },
};
/** The real color picker portal is exercised, rather than a story-only replacement. */
export const AdjustColor: Story = {
  play: async ({ canvas, canvasElement, userEvent }) => {
    const adjust = await canvas.findByRole("button", { name: /Adjust color/u });
    const body = within(canvasElement.ownerDocument.body);
    await watchHeight(canvas.getByRole("article"), async () => {
      await userEvent.click(adjust);
      await expect(
        await body.findByRole("heading", { name: "Light color" })
      ).toBeVisible();
      await userEvent.clear(body.getByRole("textbox", { name: "Hex" }));
      await userEvent.type(
        body.getByRole("textbox", { name: "Hex" }),
        "#ff0000"
      );
      await userEvent.click(body.getByRole("button", { name: "Set hex" }));
      await waitFor(() =>
        expect(body.getByRole("textbox", { name: "Hex" })).toHaveValue(
          "#FF0000"
        )
      );
      await expect(
        await canvas.findByText(/Hue 0.*Saturation 100/u)
      ).toBeVisible();
      await expect(body.queryByRole("alert")).not.toBeInTheDocument();
    });
  },
};
export const PhoneColorSheet: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    const adjust = await canvas.findByRole("button", { name: /Adjust color/u });
    const body = within(canvasElement.ownerDocument.body);
    await watchHeight(canvas.getByRole("article"), async () => {
      await userEvent.click(adjust);
      await expect(
        await body.findByRole("dialog", { name: "Light color" })
      ).toBeVisible();
      await userEvent.click(
        body.getByRole("button", { name: "Close color picker" })
      );
      await expect(adjust).toHaveFocus();
    });
    await expectNoHorizontalOverflow(canvasElement);
  },
};
export const ColorModeUnavailable: Story = {
  beforeEach: ({ msw }) => {
    const mode = fixtureEntity(11);
    mode.availability.status = "unavailable";
    msw.use(...createCoreHandlers([mode]));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Unavailable")).toBeVisible();
    await expect(canvas.getByText("Lamp color mode")).toBeVisible();
  },
};
export const AdditionalReadingsTruncated: Story = {
  beforeEach: ({ msw }) => {
    const readings = Array.from({ length: 13 }, (_, index) =>
      fixtureEntity(6, {
        id: entityId(500 + index),
        device_id: deviceId(2),
        name: `Circuit ${index + 1}`,
      })
    );
    msw.use(...createCoreHandlers(readings));
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText(
        "Showing 12 of 13 readings. Open Source for every Entity."
      )
    ).toBeVisible();
  },
};
