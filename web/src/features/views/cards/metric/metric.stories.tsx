import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, waitFor } from "storybook/test";

import { entityId, observedAt } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { watchHeight } from "../../../../../.storybook/play";
import { Button } from "../../../../components/button";
import { BoundMetric, Metric } from "./metric";

const meta = {
  title: "Components/Cards/Metric",
  component: BoundMetric,
  tags: ["ai-generated"],
  args: {
    label: "Outdoor humidity",
    source: {
      entity_id: entityId(102),
      entity_type: "hearth.relativehumidity/v1",
    },
    trend: "24h",
    range: true,
  },
  parameters: {
    docs: {
      description: {
        component:
          "One quantity reading with optional trend and catalog-supported range. Never invent a reading for missing State.",
      },
    },
  },
} satisfies Meta<typeof BoundMetric>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Observed: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("94.0")).toBeVisible();
  },
};
export const Loading: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(102, "loading"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Loading…")).toBeVisible();
  },
};
export const NeverObserved: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(102, "unobserved"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Never observed")).toBeVisible();
  },
};
/** The last reading stays visible beside the availability status. */
export const Unavailable: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(102, "unavailable"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Unavailable")).toBeVisible();
    await expect(canvas.getByText("94.0")).toBeVisible();
  },
};
export const ReadFailed: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(102, "error"));
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText(/Couldn't load this reading/u)
    ).toBeVisible();
  },
};
export const RangeNotDeclared: Story = {
  args: {
    label: "Outdoor temperature",
    source: { entity_id: entityId(101), entity_type: "hearth.temperature/v1" },
    trend: undefined,
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("9.4")).toBeVisible();
    await expect(canvas.getByText("Range unavailable.")).toBeVisible();
  },
};

/** The card's content width, rather than the viewport, controls the compact reading. */
export const NarrowCard: Story = {
  args: { trend: undefined, range: undefined },
  decorators: [
    (Story) => (
      <div style={{ width: 236 }}>
        <Story />
      </div>
    ),
  ],
  play: async ({ canvas }) => {
    const reading = await canvas.findByText("94.0");
    const card = canvas.getByRole("article", { name: "Outdoor humidity" });
    const time = card.querySelector("time");
    if (!(time instanceof HTMLTimeElement)) {
      throw new TypeError("Expected the card observation time");
    }
    const cardStyle = getComputedStyle(card);
    await expect(
      card.clientWidth -
        Number(cardStyle.paddingLeft.replace(/px$/u, "")) -
        Number(cardStyle.paddingRight.replace(/px$/u, ""))
    ).toBeLessThanOrEqual(200);
    await expect(getComputedStyle(reading).fontSize).toBe("32px");
    await expect(time).toHaveAttribute("aria-label", "Observed just now");
    await expect(time.firstElementChild).not.toBeVisible();
    await expect(canvas.getByRole("button", { name: "Source" })).toBeVisible();
  },
};

const MetricMotionSample = ({ step }: { step: number }) => {
  const [sample, setSample] = useState(0);
  return (
    <div className="grid gap-3">
      <Metric
        label="Living room"
        resource={{
          status: "ready",
          metadata: {
            id: entityId(101),
            label: "Living room",
            availability: "available",
            enabled: true,
          },
          data: {
            value: 21.5 + sample * step,
            observed_at: new Date(
              Date.parse(observedAt) + sample * 1000
            ).toISOString(),
            format: { unit_label: "°C", fraction_digits: 1 },
          },
        }}
      />
      <Button
        variant="outline"
        onClick={() => setSample((current) => current + 1)}
      >
        New reading
      </Button>
    </div>
  );
};

export const MotionRoll: Story = {
  name: "Motion · Single-digit roll",
  tags: ["motion"],
  render: () => <MetricMotionSample step={0.1} />,
  play: async ({ canvas, userEvent }) => {
    const card = canvas.getByRole("article", { name: "Living room" });
    await watchHeight(card, async () => {
      await userEvent.click(
        canvas.getByRole("button", { name: "New reading" })
      );
      await expect(
        card.querySelector('[data-slot="rolling-digit"]')
      ).toHaveTextContent("6");
      await waitFor(() =>
        expect(card.querySelector('[data-slot="rolling-digit"]')).toBeNull()
      );
      await expect(canvas.getByText("21.6")).toBeVisible();
    });
  },
};

export const MotionAtomicValue: Story = {
  name: "Motion · Multi-digit change",
  tags: ["motion"],
  render: () => <MetricMotionSample step={2.1} />,
  play: async ({ canvas, userEvent }) => {
    const card = canvas.getByRole("article", { name: "Living room" });
    await watchHeight(card, async () => {
      await userEvent.click(
        canvas.getByRole("button", { name: "New reading" })
      );
      await expect(canvas.getByText("23.6")).toBeVisible();
      await expect(
        card.querySelector('[data-slot="rolling-digit"]')
      ).toBeNull();
    });
  },
};
