import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, waitFor } from "storybook/test";

import { createEntities, entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import { stateFields } from "../../bindings/state-fields";
import { BoundWeekGrid } from "./week-grid";

const source = {
  kind: "entity-state",
  entity_id: entityId(15),
  entity_type: "hearth.temperature/v1",
} as const;

const iso = (milliseconds: number) => new Date(milliseconds).toISOString();
const unit = stateFields["hearth.temperature/v1"][""];
const canonicalUnit =
  "quantity" in unit && typeof unit.quantity.unit === "string"
    ? unit.quantity.unit
    : "Cel";

/**
 * A week of three-hour history that warms toward the afternoon, with one
 * evening block and the whole of one day missing. The shared story handler
 * serves only 1, 5 and 30 minute buckets.
 */
const threeHourWeek = (gaps = true) =>
  http.post("/v1/entities/:id/state/history", async ({ request }) => {
    const query = (await request.json()) as {
      range: { from: string; to: string };
    };
    const from = Date.parse(query.range.from);
    const to = Date.parse(query.range.to);
    const step = 3 * 3_600_000;
    const display = createEntities().find(
      (entity) => entity.id === entityId(15)
    )?.quantities?.[""];
    const buckets = Array.from(
      { length: Math.ceil((to - from) / step) },
      (_, index) => {
        const start = from + index * step;
        const date = new Date(start);
        const hour = date.getHours() + 1.5;
        const day = Math.floor(index / 8);
        const gap = gaps && (day === 2 || (day === 4 && hour > 18));
        const mean =
          19 + 3 * Math.sin(((hour - 9) / 24) * 2 * Math.PI) + day / 5;
        const metric = gap ? null : mean;
        return {
          range: { from: iso(start), to: iso(Math.min(start + step, to)) },
          canonical: {
            unit: canonicalUnit,
            metrics: { min: metric, max: metric, mean: metric },
          },
          display: {
            ...display,
            metrics: { min: metric, max: metric, mean: metric },
          },
        };
      }
    );
    return HttpResponse.json({
      kind: "summary",
      entity_id: entityId(15),
      range: query.range,
      time_axis: "observed_at",
      coverage: { basis: "retained_observations", completeness: "unverified" },
      bucket: { duration: "3h0m0s", alignment: "range_start" },
      buckets,
    });
  });

const meta = {
  title: "Components/Cards/WeekGrid",
  component: BoundWeekGrid,
  tags: ["ai-generated"],
  args: { label: "Living room temperature", source },
  beforeEach: ({ msw }) => {
    msw.use(threeHourWeek());
  },
  parameters: {
    docs: {
      description: {
        component:
          "One numeric field as seven days by eight three-hour blocks. Each block prints its average; a dashed block has no readings and an outlined block is still to come.",
      },
    },
  },
} satisfies Meta<typeof BoundWeekGrid>;
export default meta;
type Story = StoryObj<typeof meta>;

const regionName = "Living room temperature, last 7 days";

export const RecordedWeek: Story = {
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("region", { name: regionName })
    ).toHaveTextContent("Warmest around");
  },
};
export const GapsAndFutureCells: Story = {
  play: async ({ canvasElement, canvas }) => {
    await canvas.findByRole("region", { name: regionName });
    await waitFor(() => {
      expect(
        canvasElement.querySelectorAll("[data-unknown]").length
      ).toBeGreaterThan(0);
      expect(
        canvasElement.querySelectorAll("[data-future]").length
      ).toBeGreaterThan(0);
    });
  },
};
export const NoHistory: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      http.post("/v1/entities/:id/state/history", async ({ request }) => {
        const query = (await request.json()) as {
          range: { from: string; to: string };
        };
        return HttpResponse.json({
          kind: "summary",
          entity_id: entityId(15),
          range: query.range,
          time_axis: "observed_at",
          coverage: {
            basis: "retained_observations",
            completeness: "unverified",
          },
          bucket: { duration: "3h0m0s", alignment: "range_start" },
          buckets: [],
        });
      })
    );
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Historical readings will appear here.")
    ).toBeVisible();
  },
};
export const EntityUnavailable: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(15, "unavailable"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Unavailable")).toBeVisible();
  },
};
export const HistoryError: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      http.post("/v1/entities/:id/state/history", () =>
        HttpResponse.json({ detail: "History unavailable" }, { status: 503 })
      )
    );
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Couldn't load this reading")
    ).toBeVisible();
  },
};
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByRole("region", { name: regionName });
    await expectNoHorizontalOverflow(canvasElement);
  },
};
