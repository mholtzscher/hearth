import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, waitFor } from "storybook/test";

import { fixtureEntity, entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import {
  pairedEntityResponse,
  pairedHistoryResponse,
} from "../../../../../.storybook/paired-fixtures";
import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import { BoundTimeInRange } from "./time-in-range";

const livingRoom = {
  kind: "entity-state" as const,
  entity_id: entityId(15),
  entity_type: "hearth.temperature/v1" as const,
};

const meta = {
  title: "Components/Cards/TimeInRange",
  component: BoundTimeInRange,
  tags: ["ai-generated"],
  args: {
    label: "Living room temperature",
    source: livingRoom,
    window: "7d",
    // Milli-Celsius, the field's canonical unit: 21 and 22 °C.
    thresholds: [21_000, 22_000],
  },
  parameters: {
    docs: {
      description: {
        component:
          "The share of a window a numeric field spent below, between, and above its thresholds. Thresholds are saved in the canonical unit and named in the household's unit.",
      },
    },
  },
} satisfies Meta<typeof BoundTimeInRange>;
export default meta;
type Story = StoryObj<typeof meta>;

const regionName = "Living room temperature, share of time in each range";
// The fixture history's counts of 30-minute intervals below, between, and above 21 and 22 °C.
const fixtureShares = ["45 h 30 min, 28%", "62 h 30 min, 38%", "57 h, 35%"];
const expectFixtureShares = async (plot: HTMLElement) => {
  await Promise.all(
    fixtureShares.map((text) => expect(plot).toHaveTextContent(text))
  );
};

const celsius = (index: number) => 21.5 + Math.sin(index / 12) * 0.86;
const at = (value: number | null, scale: number, plus: number) =>
  value === null ? null : value * scale + plus;

/** The same living room read in °F: display values are paired with the canonical ones. */
const fahrenheit = () => {
  const display = fixtureEntity(15, {
    state: { ...fixtureEntity(15).state, value: 70.7 } as never,
    quantities: {
      "": { unit: "[degF]", label: "°F", fraction_digits: 1 },
    },
  });
  const canonical = fixtureEntity(15, {
    state: { ...fixtureEntity(15).state, value: 21_500 } as never,
    quantities: { "": { unit: "mCel", label: "mCel", fraction_digits: 0 } },
  });
  return [
    http.get(`/v1/entities/${entityId(15)}`, () =>
      HttpResponse.json(pairedEntityResponse(display, canonical))
    ),
    http.post(
      `/v1/entities/${entityId(15)}/state/history`,
      async ({ request }) => {
        const query = (await request.json()) as {
          range: { from: string; to: string };
        };
        const from = Date.parse(query.range.from);
        const to = Date.parse(query.range.to);
        const step = 1_800_000;
        const buckets = Array.from(
          { length: Math.ceil((to - from) / step) },
          (_, index) => {
            const gap = index >= 70 && index < 76;
            const mean = gap ? null : celsius(index);
            return {
              range: {
                from: new Date(from + index * step).toISOString(),
                to: new Date(
                  Math.min(from + (index + 1) * step, to)
                ).toISOString(),
              },
              fahrenheit: {
                min: at(mean, 1.8, 32 - 0.18),
                max: at(mean, 1.8, 32 + 0.18),
                mean: at(mean, 1.8, 32),
              },
              milli: {
                min: at(mean, 1000, -100),
                max: at(mean, 1000, 100),
                mean: at(mean, 1000, 0),
              },
            };
          }
        );
        return HttpResponse.json(
          pairedHistoryResponse(
            {
              kind: "summary",
              entity_id: entityId(15),
              range: query.range,
              time_axis: "observed_at",
              quantity: { unit: "[degF]", label: "°F", fraction_digits: 1 },
              buckets: buckets.map((bucket) => ({
                range: bucket.range,
                metrics: bucket.fahrenheit,
              })),
            },
            {
              quantity: { unit: "mCel", label: "mCel", fraction_digits: 0 },
              buckets: buckets.map((bucket) => ({
                range: bucket.range,
                metrics: bucket.milli,
              })),
            }
          )
        );
      }
    ),
  ];
};

export const ThreeRanges: Story = {
  play: async ({ canvas }) => {
    const plot = await canvas.findByRole("region", { name: regionName });
    await waitFor(() => expect(plot).toHaveTextContent("21 to 22 °C"));
    await expect(plot).toHaveTextContent("Below 21 °C");
    await expect(plot).toHaveTextContent("Above 22 °C");
    await expectFixtureShares(plot);
    await expect(
      await canvas.findByText(/of the last 7 days between 21 and 22 °C/u)
    ).toBeVisible();
  },
};
export const TwoRanges: Story = {
  args: { thresholds: [21_500] },
  play: async ({ canvas }) => {
    const plot = await canvas.findByRole("region", { name: regionName });
    await waitFor(() => expect(plot).toHaveTextContent("21.5 °C and above"));
    await expect(plot).toHaveTextContent("Below 21.5 °C");
    await expect(
      await canvas.findByText(/of the last 7 days below 21.5 °C/u)
    ).toBeVisible();
  },
};
/** Saved in milli-Celsius, shown in °F: the labels change, the shares do not. */
export const SavedInCelsiusShownInFahrenheit: Story = {
  beforeEach: ({ msw }) => {
    msw.use(...fahrenheit());
  },
  play: async ({ canvas }) => {
    const plot = await canvas.findByRole("region", { name: regionName });
    await waitFor(() => expect(plot).toHaveTextContent("69.8 to 71.6 °F"));
    await expect(plot).toHaveTextContent("Below 69.8 °F");
    await expect(plot).toHaveTextContent("Above 71.6 °F");
    await expect(plot).not.toHaveTextContent("°C");
    await expectFixtureShares(plot);
  },
};
export const NoHistory: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      http.post("/v1/entities/:id/state/history", async ({ request }) => {
        const query = (await request.json()) as {
          range: { from: string; to: string };
        };
        const from = Date.parse(query.range.from);
        const to = Date.parse(query.range.to);
        const step = 1_800_000;
        const empty = { min: null, max: null, mean: null };
        const buckets = Array.from(
          { length: Math.ceil((to - from) / step) },
          (_, index) => ({
            range: {
              from: new Date(from + index * step).toISOString(),
              to: new Date(
                Math.min(from + (index + 1) * step, to)
              ).toISOString(),
            },
            metrics: empty,
          })
        );
        const quantity = { unit: "Cel", label: "°C", fraction_digits: 1 };
        return HttpResponse.json(
          pairedHistoryResponse(
            {
              kind: "summary",
              entity_id: entityId(15),
              range: query.range,
              time_axis: "observed_at",
              quantity,
              buckets,
            },
            {
              quantity: { unit: "mCel", label: "mCel", fraction_digits: 0 },
              buckets,
            }
          )
        );
      })
    );
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("No history in this window.")
    ).toBeVisible();
  },
};
export const EntityUnavailable: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(15, "unavailable"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Unavailable")).toBeVisible();
    await expect(
      await canvas.findByRole("region", { name: regionName })
    ).toHaveTextContent("Below 21 °C");
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
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Couldn't load this reading")
    ).toBeVisible();
  },
};
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    const plot = await canvas.findByRole("region", { name: regionName });
    await waitFor(() => expect(plot).toHaveTextContent("21 to 22 °C"));
    await expectNoHorizontalOverflow(canvasElement);
  },
};
