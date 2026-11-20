import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it } from "vitest";

import type { SensorChoice } from "../time-series/editor";
import { ThermostatEditor } from "./editor";
import { thermostatSchema } from "./schema";
import type { ThermostatProps } from "./schema";

const id = (n: number) =>
  `ent_019a3e6a-4111-7000-8000-${String(n).padStart(12, "0")}`;
const choice = (
  n: number,
  name: string,
  type: string,
  device: string
): SensorChoice => ({ id: id(n), name, type, device_name: device });
const choices = [
  choice(1, "HVAC", "hearth.thermostat/v1", "Hallway"),
  choice(2, "Radiator", "hearth.thermostat/v1", "Office"),
  choice(3, "Temperature", "hearth.temperature/v1", "Hallway"),
];
const source = (n: number) => ({
  kind: "entity-state" as const,
  entity_id: id(n),
  entity_type: "hearth.thermostat/v1" as const,
});

const Harness = ({
  initial,
  onChange,
}: {
  initial: ThermostatProps;
  onChange: (value: ThermostatProps) => void;
}) => {
  const [value, setValue] = useState(initial);
  return (
    <ThermostatEditor
      value={value}
      choices={choices}
      onChange={(next) => {
        onChange(next);
        setValue(next);
      }}
    />
  );
};

describe("Thermostat editor", () => {
  afterEach(cleanup);

  it("offers only thermostats and changes the source", () => {
    let latest: ThermostatProps = { source: source(1) };
    render(
      <Harness
        initial={latest}
        onChange={(next) => {
          latest = next;
        }}
      />
    );
    const select = within(screen.getByLabelText("Thermostat"));
    expect(select.getByText("Hallway · HVAC")).toBeTruthy();
    expect(select.queryByText(/Temperature/u)).toBeNull();
    fireEvent.change(screen.getByLabelText("Thermostat"), {
      target: { value: id(2) },
    });
    expect(latest.source).toStrictEqual(source(2));
    expect(thermostatSchema.safeParse(latest).success).toBeTruthy();
  });

  it("keeps a saved source that is no longer offered", () => {
    render(<Harness initial={{ source: source(9) }} onChange={() => {}} />);
    const select = screen.getByLabelText<HTMLSelectElement>("Thermostat");
    expect(select.value).toBe(id(9));
    expect(within(select).getByText(`Existing source · ${id(9)}`)).toBeTruthy();
  });
});
