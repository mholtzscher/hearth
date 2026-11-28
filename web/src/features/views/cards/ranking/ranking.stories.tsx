import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, waitFor } from "storybook/test";

import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import { BoundRanking } from "./ranking";

const rain = (number: number, label: string) => ({
  label,
  source: {
    kind: "entity-state" as const,
    entity_id: entityId(number),
    entity_type: "hearth.numericsensor/v1" as const,
  },
});
const temperature = (number: number, label: string) => ({
  label,
  source: {
    kind: "entity-state" as const,
    entity_id: entityId(number),
    entity_type: "hearth.temperature/v1" as const,
  },
});

const meta = {
  title: "Components/Cards/Ranking",
  component: BoundRanking,
  tags: ["ai-generated"],
  args: {
    title: "Rain totals",
    rows: [rain(106, "Today"), rain(107, "Storm"), rain(108, "This week")],
  },
  parameters: {
    docs: {
      description: {
        component:
          "The current reading of several Entities, highest first. Bars grow from zero where zero means none; temperatures, a set range window, or a negative reading draw a dot on a shared scale instead.",
      },
    },
  },
} satisfies Meta<typeof BoundRanking>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Bars: Story = {
  play: async ({ canvas }) => {
    const plot = await canvas.findByRole("region", {
      name: "Rain totals, bars from zero, highest first",
    });
    await waitFor(() => expect(plot).toHaveTextContent("This week: 22.9 mm."));
    await expect(plot).toHaveTextContent(
      /Storm: 18\.6 mm\..*Today: 16\.2 mm\./u
    );
    await expect(
      await canvas.findByText(
        "This week is highest · Today is lowest at 16.2 mm"
      )
    ).toBeVisible();
  },
};
/** Temperatures have no meaningful zero, so Hearth draws the range plot. */
export const RangePlot: Story = {
  args: {
    title: "Room temperatures",
    rows: [
      temperature(15, "Living room"),
      temperature(114, "Bedroom"),
      temperature(101, "Outdoor"),
    ],
  },
  play: async ({ canvas }) => {
    const plot = await canvas.findByRole("region", {
      name: "Room temperatures, dot on a shared scale, highest first",
    });
    await waitFor(() =>
      expect(plot).toHaveTextContent("Living room: 21.5 °C.")
    );
    await expect(canvas.queryByText("Last 24 hours")).toBeNull();
  },
};
export const RangeOverWindow: Story = {
  args: { ...RangePlot.args, range: "24h" },
  play: async ({ canvas }) => {
    const plot = await canvas.findByRole("region", {
      name: "Room temperatures, dot on a shared scale, highest first",
    });
    await waitFor(() =>
      expect(plot).toHaveTextContent("over the last 24 hours")
    );
    await expect(await canvas.findByText("Last 24 hours")).toBeVisible();
  },
};
export const UnavailableRow: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(107, "unavailable"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Unavailable")).toBeVisible();
    const plot = await canvas.findByRole("region", {
      name: "Rain totals, bars from zero, highest first",
    });
    await expect(plot).toHaveTextContent(
      "Storm: 18.6 mm, unavailable, last reading."
    );
  },
};
export const IncompatibleUnits: Story = {
  args: {
    title: "Mixed readings",
    rows: [temperature(101, "Outdoor"), rain(106, "Rain today")],
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText(/incompatible units/u)).toBeVisible();
    await expect(canvas.queryByRole("region")).toBeNull();
  },
};
export const HistoryUnavailable: Story = {
  args: { ...RangePlot.args, range: "24h" },
  beforeEach: ({ msw }) => {
    msw.use(
      http.post("/v1/entities/:id/state/history", () =>
        HttpResponse.json({ detail: "History unavailable" }, { status: 503 })
      )
    );
  },
  // The current readings still draw; each row says its history failed.
  play: async ({ canvas }) => {
    await waitFor(() =>
      expect(canvas.getAllByText("Couldn't load this reading")).toHaveLength(3)
    );
    const plot = await canvas.findByRole("region", {
      name: "Room temperatures, dot on a shared scale, highest first",
    });
    await waitFor(() =>
      expect(plot).toHaveTextContent("Living room: 21.5 °C.")
    );
  },
};
export const ReadingUnavailable: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(106, "error"));
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Couldn't load this reading")
    ).toBeVisible();
    await expect(await canvas.findByText("—")).toBeVisible();
  },
};
export const Phone: Story = {
  args: { ...RangeOverWindow.args },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await waitFor(async () =>
      expect(
        await canvas.findByRole("region", {
          name: "Room temperatures, dot on a shared scale, highest first",
        })
      ).toHaveTextContent("over the last 24 hours")
    );
    await expectNoHorizontalOverflow(canvasElement);
  },
};
