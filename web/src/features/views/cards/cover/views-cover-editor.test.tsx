import { useState } from "react";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { SensorChoice } from "../time-series/Editor";
import { CoverEditor } from "./Editor";
import { coverSchema, type CoverProps } from "./schema";

const id = (n: number) => `ent_019a3e6a-4111-7000-8000-${String(n).padStart(12, "0")}`;
const choice = (n: number, name: string, type: string, device: string): SensorChoice => ({ id: id(n), name, type, device_name: device });
const choices = [choice(1, "Door", "hearth.cover/v1", "Garage"), choice(2, "Shade", "hearth.cover/v1", "Office"), choice(3, "Light", "hearth.power/v1", "Garage")];
const initial: CoverProps = { layout: "card", source: { kind: "entity-state", entity_id: id(1), entity_type: "hearth.cover/v1" } };
afterEach(cleanup);

it("offers only covers and keeps the opening confirmation on unless it is turned off", () => {
  let latest = initial;
  function Harness() { const [value, setValue] = useState(initial); return <CoverEditor value={value} choices={choices} onChange={next => { latest = next; setValue(next); }} />; }
  render(<Harness />);
  const select = within(screen.getByLabelText("Cover"));
  expect(select.getByText("Garage · Door")).toBeTruthy();
  expect(select.queryByText(/Light/)).toBeNull();
  const confirm = screen.getByLabelText("Ask before opening") as HTMLInputElement;
  expect(confirm.checked).toBe(true);
  fireEvent.change(screen.getByLabelText("Cover"), { target: { value: id(2) } });
  fireEvent.click(confirm);
  expect(latest).toMatchObject({ source: { entity_id: id(2) }, confirm_open: false });
  expect(coverSchema.safeParse(latest).success).toBe(true);
  fireEvent.click(confirm);
  expect(latest.confirm_open).toBeUndefined();
});
