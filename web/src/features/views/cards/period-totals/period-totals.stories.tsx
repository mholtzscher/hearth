import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect } from "storybook/test";

import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import { BoundPeriodTotals } from "./period-totals";

const meta = {
  title: "Components/Cards/PeriodTotals",
  component: BoundPeriodTotals,
  tags: ["ai-generated"],
  args: {
    label: "Front door",
    source: {
      kind: "entity-state",
      entity_id: entityId(1),
      entity_type: "hearth.binarysensor/v1",
    },
    words: { true: "Open", false: "Closed" },
    period: "day",
    style: "columns",
  },
  parameters: {
    docs: {
      description: {
        component:
          "How long a yes/no field was true in each period: the last 7 days or the last 24 hours, as columns or as a ledger. The current period is tinted and outlined; a period without history shows no total.",
      },
    },
  },
} satisfies Meta<typeof BoundPeriodTotals>;
export default meta;
type Story = StoryObj<typeof meta>;

const days = { name: "Front door, last 7 days" };
const hours = { name: "Front door, last 24 hours" };

export const DaysAsColumns: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("region", days)).toHaveTextContent(
      "Daily average"
    );
  },
};
export const DaysAsLedger: Story = {
  args: { style: "ledger" },
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("region", days)).toHaveTextContent(
      "so far"
    );
  },
};
export const HoursAsColumns: Story = {
  args: { period: "hour" },
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("region", hours)).toHaveTextContent(
      "open so far"
    );
  },
};
export const HoursAsLedger: Story = {
  args: { period: "hour", style: "ledger" },
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("region", hours)).toHaveTextContent(
      "open so far"
    );
  },
};

/** One whole day and the earliest hours report no known seconds: neither reads as zero. */
const withGaps = http.post(
  "/v1/entities/:id/state/changes",
  async ({ request }) => {
    const query = (await request.json()) as {
      range: { from: string; to: string };
    };
    const from = Date.parse(query.range.from);
    const to = Date.parse(query.range.to);
    const emptyDate = new Date(from).getDate() + 2;
    return HttpResponse.json({
      kind: "summary",
      entity_id: entityId(1),
      select_field: "",
      range: query.range,
      time_axis: "observed_at",
      coverage: { basis: "retained_changes", seeded: true },
      bucket: { duration: "1h0m0s", alignment: "range_start" },
      buckets: Array.from(
        { length: Math.ceil((to - from) / 3_600_000) },
        (_, index) => {
          const start = from + index * 3_600_000;
          const end = Math.min(start + 3_600_000, to);
          const unknown = index < 3 || new Date(start).getDate() === emptyDate;
          const seconds = (end - start) / 1000;
          const on = Math.min(index % 4 === 0 ? 1200 : 300, seconds);
          return {
            range: {
              from: new Date(start).toISOString(),
              to: new Date(end).toISOString(),
            },
            seconds_true: unknown ? 0 : on,
            known_seconds: unknown ? 0 : seconds,
            transitions_into_true: unknown ? 0 : 1,
          };
        }
      ),
    });
  }
);
export const DayWithoutHistory: Story = {
  beforeEach: ({ msw }) => {
    msw.use(withGaps);
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("region", days)).toHaveTextContent(
      "no history."
    );
  },
};
export const HourWithoutHistory: Story = {
  args: { period: "hour" },
  beforeEach: ({ msw }) => {
    msw.use(withGaps);
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("region", hours)).toHaveTextContent(
      "no history."
    );
  },
};
export const EntityUnavailable: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(1, "unavailable"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Unavailable")).toBeVisible();
  },
};
export const HistoryError: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      http.post("/v1/entities/:id/state/changes", () =>
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
  args: { period: "hour" },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByRole("region", hours);
    await expectNoHorizontalOverflow(canvasElement);
  },
};
export const PhoneLedger: Story = {
  args: { period: "hour", style: "ledger" },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByRole("region", hours);
    await expectNoHorizontalOverflow(canvasElement);
  },
};
