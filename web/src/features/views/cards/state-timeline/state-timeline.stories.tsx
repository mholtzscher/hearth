import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect } from "storybook/test";

import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { BoundStateTimeline } from "./state-timeline";

const meta = {
  title: "Components/Cards/StateTimeline",
  component: BoundStateTimeline,
  tags: ["ai-generated"],
  args: {
    title: "Around the house",
    style: "ledger",
    window: "24h",
    rows: [
      {
        label: "Front door",
        source: {
          kind: "entity-state",
          entity_id: entityId(1),
          entity_type: "hearth.binarysensor/v1",
        },
        words: { true: "Open", false: "Closed" },
      },
      {
        label: "Porch light",
        source: {
          kind: "entity-state",
          entity_id: entityId(2),
          entity_type: "hearth.power/v1",
        },
      },
      {
        label: "Thermostat",
        source: {
          kind: "entity-state",
          entity_id: entityId(10),
          entity_type: "hearth.enumsetting/v1",
        },
      },
    ],
  },
} satisfies Meta<typeof BoundStateTimeline>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Ledger: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Closed")).toBeVisible();
  },
};
export const Lanes: Story = {
  args: { style: "lanes" },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Open 23 h 20 min")).toBeVisible();
  },
};
export const UnavailableLanes: Story = {
  args: { style: "lanes" },
  beforeEach: ({ msw }) => {
    msw.use(
      entityScenario(1, "unavailable"),
      entityScenario(2, "disabled"),
      entityScenario(10, "unavailable")
    );
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("heat")).toBeVisible();
    await expect(canvas.getAllByText("Unavailable")[0]).toBeVisible();
  },
};
export const Feed: Story = {
  args: { style: "feed" },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("heat → auto")).toBeVisible();
  },
};
export const FrequentChanges: Story = {
  args: {
    style: "feed",
    window: "7d",
    rows: [
      {
        label: "Front door",
        source: {
          kind: "entity-state",
          entity_id: entityId(1),
          entity_type: "hearth.binarysensor/v1",
        },
        words: { true: "Open", false: "Closed" },
      },
    ],
  },
  beforeEach: ({ msw }) => {
    msw.use(
      http.post(
        "/v1/entities/:id/state/changes",
        async ({ params, request }) => {
          const query = (await request.json()) as {
            range: { from: string; to: string };
            select_field: string;
          };
          const from = Date.parse(query.range.from);
          const to = Date.parse(query.range.to);
          const step = 30 * 60_000;
          const intervals = Array.from(
            { length: Math.ceil((to - from) / step) },
            (_, index) => ({
              kind: "value",
              from: new Date(from + index * step).toISOString(),
              to: new Date(
                Math.min(from + (index + 1) * step, to)
              ).toISOString(),
              value: index % 2 === 0,
            })
          );
          return HttpResponse.json({
            kind: "timeline",
            entity_id: params.id,
            select_field: query.select_field,
            range: query.range,
            time_axis: "observed_at",
            coverage: {
              basis: "retained_changes",
              completeness: "unverified",
              seeded: true,
            },
            intervals,
          });
        }
      )
    );
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findAllByText(/times since/u)).toHaveLength(12);
    const more = canvas.getByRole("button", {
      name: /Show .* earlier changes/u,
    });
    more.scrollIntoView({ block: "end" });
    await expect(more).toBeVisible();
  },
};
