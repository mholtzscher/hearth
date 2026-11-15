import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { ViewDefinition } from "../../catalog";
import { ViewEditor } from "../../editor/view-editor";
import type { SensorChoice } from "../time-series/editor";
import { AirPurifierEditor } from "./editor";
import { airPurifierSchema } from "./schema";
import type { AirPurifierProps } from "./schema";

const id = (n: number) =>
  `ent_019a3e6a-4111-7000-8000-${String(n).padStart(12, "0")}`;
const dev = (n: number) =>
  `dev_019a3e6a-4111-7000-8000-${String(n).padStart(12, "0")}`;
const choice = (
  n: number,
  name: string,
  type: string,
  state: object,
  device = 1
): SensorChoice => ({
  id: id(n),
  name,
  type,
  device_id: dev(device),
  device_name: device === 1 ? "Living room" : "Bedroom",
  support: { state },
});
const choices: SensorChoice[] = [
  choice(1, "Power", "hearth.power/v1", {}),
  choice(2, "Particles", "hearth.numericsensor/v1", { unit: "µg/m³" }),
  choice(3, "Filter", "hearth.numericsensor/v1", { unit: "%" }),
  choice(4, "Modes", "hearth.enumsetting/v1", {
    choices: ["auto", "sleep", "manual"],
  }),
  choice(5, "Levels", "hearth.enumsetting/v1", { choices: ["1", "2", "3"] }),
  choice(6, "Display", "hearth.power/v1", {}),
  choice(7, "Lock", "hearth.power/v1", {}),
  choice(8, "Other particles", "hearth.numericsensor/v1", { unit: "µg/m³" }, 2),
  choice(9, "Other power", "hearth.power/v1", {}, 2),
];
const initial: AirPurifierProps = {
  layout: "card",
  power: {
    kind: "entity-state",
    entity_id: id(1),
    entity_type: "hearth.power/v1",
  },
  pm25: {
    kind: "entity-state",
    entity_id: id(2),
    entity_type: "hearth.numericsensor/v1",
  },
};
const harness = () => {
  let latest = initial;
  const Harness = () => {
    const [value, setValue] = useState(initial);
    return (
      <AirPurifierEditor
        value={value}
        choices={choices}
        onChange={(next) => {
          latest = next;
          setValue(next);
        }}
      />
    );
  };
  render(<Harness />);
  return () => latest;
};

describe(AirPurifierEditor, () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("filters by role, unit and Device", () => {
    harness();
    expect(
      within(screen.getByLabelText("Power")).getByText("Living room · Power")
    ).toBeTruthy();
    const pm = within(screen.getByLabelText("PM2.5"));
    expect(pm.getByText("Living room · Particles")).toBeTruthy();
    expect(pm.queryByText(/Other particles/u)).toBeNull();
    expect(pm.queryByText(/Filter/u)).toBeNull();
    expect(
      within(screen.getByLabelText("Mode")).getByText("None")
    ).toBeTruthy();
  });

  it("excludes roles that do not match each selection", () => {
    harness();
    expect(
      within(screen.getByLabelText("Mode")).queryByText(/Levels/u)
    ).toBeNull();
    expect(
      within(screen.getByLabelText("Fan level")).queryByText(/Modes/u)
    ).toBeNull();
    fireEvent.change(screen.getByLabelText("Display"), {
      target: { value: id(6) },
    });
    expect(
      within(screen.getByLabelText("Child lock")).queryByText(/Display/u)
    ).toBeNull();
  });

  it("produces schema-valid selections and clears incompatible roles", () => {
    const latest = harness();
    fireEvent.change(screen.getByLabelText("Mode"), {
      target: { value: id(4) },
    });
    fireEvent.change(screen.getByLabelText("Fan level"), {
      target: { value: id(5) },
    });
    fireEvent.change(screen.getByLabelText("Filter life"), {
      target: { value: id(3) },
    });
    expect(airPurifierSchema.safeParse(latest()).success).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Power"), {
      target: { value: id(9) },
    });
    expect(latest().mode).toBeUndefined();
    expect(latest().display).toBeUndefined();
    expect(latest().pm25.entity_id).toBe("");
    fireEvent.change(screen.getByLabelText("PM2.5"), {
      target: { value: id(8) },
    });
    expect(airPurifierSchema.safeParse(latest()).success).toBeTruthy();
  });

  it("preserves existing sources when discovery is unavailable", () => {
    render(
      <AirPurifierEditor value={initial} choices={[]} onChange={() => {}} />
    );
    expect(screen.getAllByText(/Existing source ·/u)).toHaveLength(2);
    expect(
      screen.getByText(/Existing source selections are preserved/u)
    ).toBeTruthy();
  });

  it("discovers and edits purifier roles through ViewEditor", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation((input) =>
      Promise.resolve(
        Response.json(
          String(input).includes("/v1/devices?")
            ? {
                items: [
                  { id: dev(1), name: "Living room" },
                  { id: dev(2), name: "Bedroom" },
                ],
                next_cursor: null,
              }
            : { items: choices, next_cursor: null }
        )
      )
    );
    const definition: ViewDefinition = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Air",
      spec: {
        root: "air",
        elements: { air: { type: "AirPurifier", props: initial } },
      },
    };
    const onChange = vi.fn<Parameters<typeof ViewEditor>[0]["onChange"]>();
    render(<ViewEditor definition={definition} onChange={onChange} />);
    fireEvent.click(screen.getByText("AirPurifier · air"));
    await expect(
      screen.findByRole("option", { name: "Living room · Modes" })
    ).resolves.toBeTruthy();
    fireEvent.change(screen.getByLabelText("Mode"), {
      target: { value: id(4) },
    });
    const { air } = onChange.mock.calls[0][0].spec.elements;
    if (air?.type !== "AirPurifier") {
      throw new Error("Expected an AirPurifier element");
    }
    expect(air.props.mode?.entity_id).toBe(id(4));
    expect(airPurifierSchema.safeParse(air.props).success).toBeTruthy();
  });
});
