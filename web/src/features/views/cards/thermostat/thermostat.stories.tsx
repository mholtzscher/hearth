import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import { expect, userEvent, waitFor } from "storybook/test";

import { entityId } from "../../../../../.storybook/fixtures";
import { pairedEntityResponse } from "../../../../../.storybook/paired-fixtures";
import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import {
  applyThermostatCommand,
  thermostatEntities,
} from "../../../../../.storybook/thermostat-fixtures";
import type {
  ThermostatFixture,
  ThermostatState,
} from "../../../../../.storybook/thermostat-fixtures";
import type { ThermostatMode } from "./state";
import { BoundThermostat } from "./thermostat";

const id = entityId(501);
const heatCool: ThermostatState = {
  mode: "heat_cool",
  activity: "heating",
  current_temperature: 20_000,
  target_temperature_low: 21_000,
  target_temperature_high: 24_444,
  fan_mode: "Automatic",
  current_humidity: 54,
};

/**
 * Story-local Core for one thermostat. Commands are satisfied, rejected, or
 * held open; synthetic State changes only in this story.
 */
const thermostatCore = (
  fixture: Omit<ThermostatFixture, "id">,
  outcome: "satisfy" | "reject" | "hold" = "satisfy"
) => {
  let current: ThermostatFixture = { id, ...fixture };
  return [
    http.get(`/v1/entities/${id}`, () => {
      const { display, canonical } = thermostatEntities(current);
      return HttpResponse.json(pairedEntityResponse(display, canonical));
    }),
    http.post(`/v1/entities/${id}/commands`, async ({ request }) => {
      const body = (await request.json()) as {
        operation: string;
        parameters: Record<string, unknown>;
      };
      if (outcome === "hold") {
        await delay("infinite");
      }
      if (outcome === "reject") {
        return HttpResponse.json({ detail: "Rejected" }, { status: 502 });
      }
      const next =
        current.state &&
        applyThermostatCommand(current.state, body.operation, body.parameters);
      if (!next) {
        return HttpResponse.json({ detail: "Unsupported" }, { status: 400 });
      }
      current = {
        ...current,
        state: next,
        observedAt: new Date().toISOString(),
      };
      return HttpResponse.json({
        command_id: "cmd_story_thermostat",
        status: "satisfied",
        observation_id: "obs_story_thermostat",
        value: body.parameters,
      });
    }),
  ];
};

const meta = {
  title: "Components/Cards/Thermostat",
  component: BoundThermostat,
  tags: ["ai-generated"],
  args: {
    label: "Hallway thermostat",
    source: {
      kind: "entity-state",
      entity_id: entityId(501),
      entity_type: "hearth.thermostat/v1",
    },
  },
  decorators: [
    (Story) => (
      <div className="w-80">
        <Story />
      </div>
    ),
  ],
  parameters: {
    docs: {
      description: {
        component:
          "A thermostat led by its targets, with −/+ controls for the targets the current mode uses. Fixtures are synthetic; Commands are simulated and prove no equipment effect.",
      },
    },
  },
} satisfies Meta<typeof BoundThermostat>;
export default meta;
type Story = StoryObj<typeof meta>;

/** A paired-target thermostat in Heat & cool: both targets apply. */
export const HeatAndCool: Story = {
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Heat & cool · Heating")
    ).toBeVisible();
    await expect(canvas.getByText("Heat to")).toBeVisible();
    await expect(canvas.getByText("Cool to")).toBeVisible();
  },
};

/** A single-target radiator in a Celsius household steps in half degrees. */
export const SingleTargetCelsius: Story = {
  args: { label: "Office radiator" },
  beforeEach: ({ msw }) => {
    msw.use(
      ...thermostatCore({
        name: "Office radiator",
        units: "metric",
        operations: ["set_mode", "set_target"],
        modes: ["off", "heat", "cool"],
        fanModes: [],
        state: {
          mode: "heat",
          activity: "idle",
          current_temperature: 21_000,
          target_temperature: 20_000,
        },
      })
    );
  },
  play: async ({ canvas }) => {
    await userEvent.click(
      await canvas.findByRole("button", { name: /^Raise heat target/u })
    );
    await expect(await canvas.findByText("20.5")).toBeVisible();
  },
};

/** Off uses no targets: the mode stands where the targets go. */
export const Off: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      ...thermostatCore({
        state: { ...heatCool, mode: "off", activity: "off" },
      })
    );
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("No target in this mode.")
    ).toBeVisible();
  },
};

/** While a target Command is pending, the requested value shows in violet. */
export const Pending: Story = {
  beforeEach: ({ msw }) => {
    msw.use(...thermostatCore({ state: heatCool }, "hold"));
  },
  play: async ({ canvas }) => {
    await userEvent.click(
      await canvas.findByRole("button", { name: /^Raise heat target/u })
    );
    await expect(
      await canvas.findByText("Setting heat to 70 °F…")
    ).toBeVisible();
  },
};

/** A rejected Command returns the target to observed State and says why. */
export const Rejected: Story = {
  beforeEach: ({ msw }) => {
    msw.use(...thermostatCore({ state: heatCool }, "reject"));
  },
  play: async ({ canvas }) => {
    await userEvent.click(
      await canvas.findByRole("button", { name: /^Lower cool target/u })
    );
    await expect(await canvas.findByRole("alert")).toHaveTextContent(
      "Targets: Change rejected. Try again."
    );
  },
};

/** An unavailable thermostat dims its last values; Commands may not reach it. */
export const Unavailable: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      ...thermostatCore({
        state: heatCool,
        availability: "unavailable",
        observedAt: "2026-11-09T07:12:00Z",
      })
    );
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Unavailable")).toBeVisible();
  },
};

const allModes: ThermostatMode[] = [
  "off",
  "heat",
  "cool",
  "heat_cool",
  "auto",
  "dry",
  "fan_only",
];

/**
 * Two widths of one thermostat stay one height each through every mode,
 * pending and settled, because the target block never changes height.
 */
export const StableHeight: Story = {
  decorators: [
    (Story) => (
      <div className="flex items-start gap-6">
        <div className="w-[248px] shrink-0">
          <Story />
        </div>
        <div className="w-80 shrink-0">
          <Story />
        </div>
      </div>
    ),
  ],
  beforeEach: ({ msw }) => {
    msw.use(...thermostatCore({ state: heatCool, modes: allModes }));
  },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findAllByText("Heat & cool · Heating");
    const heights = () =>
      [...canvasElement.querySelectorAll("article")].map(
        (article) => article.getBoundingClientRect().height
      );
    const [narrow, wide] = heights();
    const widths = [...canvasElement.querySelectorAll("article")].map(
      (article) => article.getBoundingClientRect().width
    );
    await expect(widths).toStrictEqual([248, 320]);
    const [select] = canvas.getAllByRole("combobox", { name: "Mode" });
    // Modes change one after another, each settling before the next.
    const visit = async (index: number): Promise<void> => {
      const mode = allModes[index];
      if (!mode) {
        return;
      }
      await userEvent.selectOptions(select, mode);
      await expect(heights()).toStrictEqual([narrow, wide]);
      await waitFor(async () => {
        await expect(canvas.queryByText(/^Switching to/u)).toBeNull();
      });
      await expect(heights()).toStrictEqual([narrow, wide]);
      await visit(index + 1);
    };
    await visit(0);
  },
};

/** One card at phone width, with 44px targets. */
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByText("Heat & cool · Heating");
    await expectNoHorizontalOverflow(canvasElement);
  },
};
