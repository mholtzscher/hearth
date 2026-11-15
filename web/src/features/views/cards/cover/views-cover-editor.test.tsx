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
import { CoverEditor } from "./editor";
import { coverSchema } from "./schema";
import type { CoverProps } from "./schema";

const id = (n: number) =>
  `ent_019a3e6a-4111-7000-8000-${String(n).padStart(12, "0")}`;
const choice = (
  n: number,
  name: string,
  type: string,
  device: string
): SensorChoice => ({ id: id(n), name, type, device_name: device });
const choices = [
  choice(1, "Door", "hearth.cover/v1", "Garage"),
  choice(2, "Shade", "hearth.cover/v1", "Office"),
  choice(3, "Light", "hearth.power/v1", "Garage"),
];
const initial: CoverProps = {
  layout: "card",
  source: {
    kind: "entity-state",
    entity_id: id(1),
    entity_type: "hearth.cover/v1",
  },
};

const Harness = ({ onChange }: { onChange: (value: CoverProps) => void }) => {
  const [value, setValue] = useState(initial);
  return (
    <CoverEditor
      value={value}
      choices={choices}
      onChange={(next) => {
        onChange(next);
        setValue(next);
      }}
    />
  );
};

describe("Cover editor", () => {
  afterEach(cleanup);

  it("offers only covers and keeps the opening confirmation on", () => {
    render(<Harness onChange={() => {}} />);
    const select = within(screen.getByLabelText("Cover"));
    expect(select.getByText("Garage · Door")).toBeTruthy();
    expect(select.queryByText(/Light/u)).toBeNull();
    const confirm = screen.getByLabelText(
      "Ask before opening"
    ) as HTMLInputElement;
    expect(confirm.checked).toBeTruthy();
  });

  it("changes the cover and turns off the opening confirmation", () => {
    let latest: CoverProps = initial;
    render(
      <Harness
        onChange={(next) => {
          latest = next;
        }}
      />
    );
    const confirm = screen.getByLabelText(
      "Ask before opening"
    ) as HTMLInputElement;
    fireEvent.change(screen.getByLabelText("Cover"), {
      target: { value: id(2) },
    });
    fireEvent.click(confirm);
    expect(latest).toMatchObject({
      source: { entity_id: id(2) },
      confirm_open: false,
    });
    expect(coverSchema.safeParse(latest).success).toBeTruthy();
    fireEvent.click(confirm);
    expect(latest.confirm_open).toBeUndefined();
  });
});
