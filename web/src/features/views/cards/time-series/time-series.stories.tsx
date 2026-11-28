import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, waitFor } from "storybook/test";

import { entityId } from "../../../../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import { BoundTimeSeries } from "./time-series";

const meta = {
  title: "Components/Cards/TimeSeries",
  component: BoundTimeSeries,
  tags: ["ai-generated"],
  args: {
    title: "Temperatures",
    series: [
      {
        label: "Outdoor",
        source: {
          entity_id: entityId(101),
          entity_type: "hearth.temperature/v1",
          window: "24h",
        },
      },
      {
        label: "Bedroom",
        source: {
          entity_id: entityId(114),
          entity_type: "hearth.temperature/v1",
          window: "24h",
        },
      },
    ],
  },
  parameters: {
    docs: {
      description: {
        component:
          "Compare compatible quantities over the same window. Missing history stays a gap rather than becoming zero.",
      },
    },
  },
} satisfies Meta<typeof BoundTimeSeries>;
export default meta;
type Story = StoryObj<typeof meta>;

export const WithGaps: Story = {
  play: async ({ canvas }) => {
    const chart = await canvas.findByRole("region", {
      name: "Temperatures, average line with minimum to maximum band",
    });
    await expect(chart).toHaveTextContent("Outdoor: latest");
  },
};
export const OneSeries: Story = {
  args: {
    series: [
      {
        label: "Outdoor",
        source: {
          entity_id: entityId(101),
          entity_type: "hearth.temperature/v1",
          window: "24h",
        },
      },
    ],
  },
  play: async ({ canvas }) => {
    const chart = await canvas.findByRole("region", { name: /^Temperatures/u });
    await expect(chart).toHaveTextContent("Outdoor: latest");
    await expect(chart).not.toHaveTextContent("Bedroom");
  },
};
export const HistoryUnavailable: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      http.post("/v1/entities/:id/state/history", () =>
        HttpResponse.json({ detail: "History unavailable" }, { status: 503 })
      )
    );
  },
  // Each series reports its own failure, and the two requests settle separately.
  play: async ({ canvas }) => {
    await waitFor(() =>
      expect(canvas.getAllByText("Couldn't load this reading")).toHaveLength(2)
    );
  },
};
export const InspectReading: Story = {
  args: OneSeries.args,
  play: async ({ canvas, userEvent }) => {
    const chart = await canvas.findByRole("region", { name: /^Temperatures/u });
    await expect(chart).toHaveTextContent("Outdoor: latest");
    const plot = chart.querySelector("svg");
    if (!plot) {
      throw new Error("The history plot did not render");
    }
    const rect = plot.getBoundingClientRect();
    await userEvent.pointer({
      target: plot,
      coords: {
        clientX: rect.left + rect.width / 2,
        clientY: rect.top + rect.height / 2,
      },
    });
    await expect(await canvas.findByText(/Outdoor avg/u)).toBeInTheDocument();
  },
};
/** A set value is a step line with a dot at each change; the measured room keeps its band. */
export const SteppedTarget: Story = {
  args: {
    title: "Hallway thermostat",
    series: [
      {
        label: "Heat to",
        source: {
          entity_id: entityId(501),
          entity_type: "hearth.thermostat/v1",
          field: "/target_temperature_low",
          window: "24h",
        },
      },
      {
        label: "Room",
        source: {
          entity_id: entityId(501),
          entity_type: "hearth.thermostat/v1",
          field: "/current_temperature",
          window: "24h",
        },
      },
    ],
  },
  play: async ({ canvas }) => {
    const chart = await canvas.findByRole("region", {
      name: "Hallway thermostat, average lines with minimum to maximum bands, and step lines of set values",
    });
    await expect(chart).toHaveTextContent(/Heat to: latest .* changes over/u);
    await expect(chart).toHaveTextContent(/Room: latest .* range/u);
  },
};
/** Today from local midnight over the last 7 days' range for each time of day. */
export const AgainstTypical: Story = {
  args: { ...OneSeries.args, title: "Outdoor temperature", compare: "typical" },
  play: async ({ canvas }) => {
    const chart = await canvas.findByRole("region", {
      name: /^Outdoor temperature, today against the last 7 days/u,
    });
    await expect(chart).toHaveTextContent("Today: latest");
    // The 7-day read settles after today's; the band and its key arrive with it.
    await expect(
      await canvas.findByText("Usual range, last 7 days")
    ).toBeInTheDocument();
    await expect(chart).toHaveTextContent("Usual range at this time");
  },
};
export const AgainstTypicalPhone: Story = {
  args: AgainstTypical.args,
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByRole("region", { name: /today against/u });
    await expectNoHorizontalOverflow(canvasElement);
  },
};
