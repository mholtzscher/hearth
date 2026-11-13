import { useState } from "react";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ViewEditor } from "../../editor/ViewEditor";
import type { ViewDefinition } from "../../catalog";
import type { SensorChoice } from "../time-series/Editor";
import { AirPurifierEditor } from "./Editor";
import { airPurifierSchema, type AirPurifierProps } from "./schema";

const id = (n: number) => `ent_019a3e6a-4111-7000-8000-${String(n).padStart(12, "0")}`;
const dev = (n: number) => `dev_019a3e6a-4111-7000-8000-${String(n).padStart(12, "0")}`;
const choice = (n: number, name: string, type: string, state: object, device = 1): SensorChoice => ({ id: id(n), name, type, device_id: dev(device), device_name: device === 1 ? "Living room" : "Bedroom", support: { state } });
const choices: SensorChoice[] = [choice(1, "Power", "hearth.power/v1", {}), choice(2, "Particles", "hearth.numericsensor/v1", { unit: "µg/m³" }), choice(3, "Filter", "hearth.numericsensor/v1", { unit: "%" }), choice(4, "Modes", "hearth.enumsetting/v1", { choices: ["auto", "sleep", "manual"] }), choice(5, "Levels", "hearth.enumsetting/v1", { choices: ["1", "2", "3"] }), choice(6, "Display", "hearth.power/v1", {}), choice(7, "Lock", "hearth.power/v1", {}), choice(8, "Other particles", "hearth.numericsensor/v1", { unit: "µg/m³" }, 2), choice(9, "Other power", "hearth.power/v1", {}, 2)];
const initial: AirPurifierProps = { layout: "card", power: { kind: "entity-state", entity_id: id(1), entity_type: "hearth.power/v1" }, pm25: { kind: "entity-state", entity_id: id(2), entity_type: "hearth.numericsensor/v1" } };
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("filters by role, unit, Device and distinct Entity, and produces schema-valid selections", () => {
  let latest = initial;
  function Harness() { const [value, setValue] = useState(initial); return <AirPurifierEditor value={value} choices={choices} onChange={next => { latest = next; setValue(next); }} />; }
  render(<Harness />);
  expect(within(screen.getByLabelText("Power")).getByText("Living room · Power")).toBeTruthy();
  const pm = within(screen.getByLabelText("PM2.5"));
  expect(pm.getByText("Living room · Particles")).toBeTruthy();
  expect(pm.queryByText(/Other particles/)).toBeNull();
  expect(pm.queryByText(/Filter/)).toBeNull();
  expect(within(screen.getByLabelText("Mode")).getByText("None")).toBeTruthy();
  expect(within(screen.getByLabelText("Mode")).queryByText(/Levels/)).toBeNull();
  expect(within(screen.getByLabelText("Fan level")).queryByText(/Modes/)).toBeNull();
  fireEvent.change(screen.getByLabelText("Display"), { target: { value: id(6) } });
  expect(within(screen.getByLabelText("Child lock")).queryByText(/Display/)).toBeNull();
  fireEvent.change(screen.getByLabelText("Mode"), { target: { value: id(4) } });
  fireEvent.change(screen.getByLabelText("Fan level"), { target: { value: id(5) } });
  fireEvent.change(screen.getByLabelText("Filter life"), { target: { value: id(3) } });
  expect(airPurifierSchema.safeParse(latest).success).toBe(true);
  fireEvent.change(screen.getByLabelText("Power"), { target: { value: id(9) } });
  expect(latest.mode).toBeUndefined(); expect(latest.display).toBeUndefined();
  expect(latest.pm25.entity_id).toBe("");
  fireEvent.change(screen.getByLabelText("PM2.5"), { target: { value: id(8) } });
  expect(airPurifierSchema.safeParse(latest).success).toBe(true);
});

it("preserves existing sources when discovery is unavailable", () => {
  render(<AirPurifierEditor value={initial} choices={[]} onChange={() => {}} />);
  expect(screen.getAllByText(/Existing source ·/)).toHaveLength(2);
  expect(screen.getByText(/Existing source selections are preserved/)).toBeTruthy();
});

it("discovers and edits purifier roles through ViewEditor", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async input => new Response(JSON.stringify(String(input).includes("/v1/devices?") ? { items: [{ id: dev(1), name: "Living room" }, { id: dev(2), name: "Bedroom" }], next_cursor: null } : { items: choices, next_cursor: null })));
  const definition: ViewDefinition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Air", spec: { root: "air", elements: { air: { type: "AirPurifier", props: initial } } } };
  const onChange = vi.fn();
  render(<ViewEditor definition={definition} onChange={onChange} />);
  fireEvent.click(screen.getByText("AirPurifier · air"));
  expect(await screen.findByRole("option", { name: "Living room · Modes" })).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Mode"), { target: { value: id(4) } });
  expect(onChange.mock.calls[0][0].spec.elements.air.props.mode.entity_id).toBe(id(4));
  expect(airPurifierSchema.safeParse(onChange.mock.calls[0][0].spec.elements.air.props).success).toBe(true);
});
