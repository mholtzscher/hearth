import {
  fireEvent,
  render,
  screen,
  cleanup,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { TimeSeriesEditor } from "../cards/time-series/editor";
import { ToggleListEditor } from "../cards/toggle-list/editor";
import { SlotSelect } from "../cards/weather/slot-select";
import { WindEditor } from "../cards/wind/editor";
import type { ViewDefinition } from "../catalog";
import { ViewEditor } from "./view-editor";

const quantity = (unit: string) => ({
  "": { unit, label: unit, fraction_digits: 2 },
});

describe("views editor", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("discards invalid local edits when the same definition returns after another definition", () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response('{"items":[],"next_cursor":null}')
    );
    const first: ViewDefinition = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "First",
      spec: {
        root: "note",
        elements: { note: { type: "Note", props: { text: "First note" } } },
      },
    };
    const second = { ...first, title: "Second" };
    const onChange = vi.fn<Parameters<typeof ViewEditor>[0]["onChange"]>();
    const { rerender } = render(
      <ViewEditor definition={first} onChange={onChange} />
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Dashboard title" }), {
      target: { value: "" },
    });
    expect(
      screen.getByRole("textbox", { name: "Dashboard title" })
    ).toHaveProperty("value", "");
    rerender(<ViewEditor definition={second} onChange={onChange} />);
    expect(
      screen.getByRole("textbox", { name: "Dashboard title" })
    ).toHaveProperty("value", "Second");
    rerender(<ViewEditor definition={first} onChange={onChange} />);
    expect(
      screen.getByRole("textbox", { name: "Dashboard title" })
    ).toHaveProperty("value", "First");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("reorders view children without changing their definitions", () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response('{"items":[],"next_cursor":null}')
    );
    const definition: ViewDefinition = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Room",
      spec: {
        root: "root",
        elements: {
          root: {
            type: "Stack",
            props: { gap: "medium" },
            children: ["one", "two"],
          },
          one: { type: "Heading", props: { text: "First" } },
          two: { type: "Note", props: { text: "Second" } },
        },
      },
    };
    const onChange = vi.fn<Parameters<typeof ViewEditor>[0]["onChange"]>();
    render(<ViewEditor definition={definition} onChange={onChange} />);
    fireEvent.click(screen.getByText("Stack · root"));
    expect(
      (screen.getByRole("button", { name: "Move one up" }) as HTMLButtonElement)
        .disabled
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Move two up" }));
    expect(onChange).toHaveBeenLastCalledWith({
      ...definition,
      spec: {
        ...definition.spec,
        elements: {
          ...definition.spec.elements,
          root: {
            type: "Stack",
            props: { gap: "medium" },
            children: ["two", "one"],
          },
        },
      },
    });
  });

  it("retains a missing weather selection and allows choosing or clearing a compatible Entity", () => {
    const existing = {
      kind: "entity-state" as const,
      entity_id: "ent_existing",
      entity_type: "hearth.speed/v1",
    };
    const onChange = vi.fn<(source: typeof existing | undefined) => void>();
    render(
      <SlotSelect
        label="Wind speed"
        value={existing}
        optional
        type="hearth.speed/v1"
        choices={[
          {
            id: "ent_new",
            name: "Wind",
            type: "hearth.speed/v1",
            device_name: "Station",
          },
          {
            id: "ent_other",
            name: "Temperature",
            type: "hearth.temperature/v1",
          },
        ]}
        onChange={onChange}
      />
    );
    const select = screen.getByRole("combobox", { name: "Wind speed" });
    expect((select as HTMLSelectElement).value).toBe("ent_existing");
    expect(
      within(select).getByRole("option", {
        name: "Existing sensor · ent_existing",
      })
    ).toBeTruthy();
    expect(
      within(select).queryByRole("option", { name: "Temperature" })
    ).toBeNull();
    fireEvent.change(select, { target: { value: "ent_new" } });
    expect(onChange).toHaveBeenLastCalledWith({
      kind: "entity-state",
      entity_id: "ent_new",
      entity_type: "hearth.speed/v1",
    });
    fireEvent.change(select, { target: { value: "" } });
    expect(onChange).toHaveBeenLastCalledWith(undefined);
  });

  it("adds, changes, and removes Wind speed history without changing its slots", () => {
    const speed = {
      kind: "entity-state" as const,
      entity_id: "ent_019a3e6a-4111-7000-8000-000000000001",
      entity_type: "hearth.speed/v1" as const,
    };
    const onChange = vi.fn<Parameters<typeof WindEditor>[0]["onChange"]>();
    const { rerender } = render(
      <WindEditor value={{ speed }} choices={[]} onChange={onChange} />
    );
    expect((screen.getByLabelText("Trend") as HTMLSelectElement).value).toBe(
      ""
    );
    for (const trend of ["1h", "6h", "24h", "7d"] as const) {
      fireEvent.change(screen.getByLabelText("Trend"), {
        target: { value: trend },
      });
      expect(onChange).toHaveBeenLastCalledWith({ speed, trend });
      rerender(
        <WindEditor value={{ speed, trend }} choices={[]} onChange={onChange} />
      );
      expect((screen.getByLabelText("Trend") as HTMLSelectElement).value).toBe(
        trend
      );
    }
    fireEvent.change(screen.getByLabelText("Trend"), { target: { value: "" } });
    expect(onChange).toHaveBeenLastCalledWith({ speed, trend: undefined });
  });

  it("distinguishes power endpoints on the same Device and falls back to identity for duplicate names", () => {
    const first = "ent_019a3e6a-4111-7000-8000-000000000001";
    const second = "ent_019a3e6a-4111-7000-8000-000000000002";
    const third = "ent_019a3e6a-4111-7000-8000-000000000003";
    const choices = [
      {
        id: first,
        name: "Left Power",
        type: "hearth.power/v1",
        device_id: "device",
        device_name: "Desk outlet",
      },
      {
        id: second,
        name: "Right Power",
        type: "hearth.power/v1",
        device_id: "device",
        device_name: "Desk outlet",
      },
      {
        id: third,
        name: "Right Power",
        type: "hearth.power/v1",
        device_id: "device",
        device_name: "Desk outlet",
      },
    ];
    const onChange =
      vi.fn<Parameters<typeof ToggleListEditor>[0]["onChange"]>();
    render(
      <ToggleListEditor
        value={{
          title: "Room",
          rows: [
            {
              source: {
                kind: "entity-state",
                entity_id: first,
                entity_type: "hearth.power/v1",
              },
            },
          ],
        }}
        choices={choices}
        onChange={onChange}
      />
    );
    const select = screen.getByLabelText("Power source");
    expect(
      within(select)
        .getByRole("option", { name: "Desk outlet · Left Power" })
        .getAttribute("value")
    ).toBe(first);
    expect(
      within(select)
        .getByRole("option", { name: `Desk outlet · Right Power · ${second}` })
        .getAttribute("value")
    ).toBe(second);
    fireEvent.change(select, { target: { value: second } });
    expect(onChange.mock.calls[0][0].rows[0]).toStrictEqual({
      label: `Desk outlet · Right Power · ${second}`,
      source: {
        kind: "entity-state",
        entity_id: second,
        entity_type: "hearth.power/v1",
      },
    });
  });

  it("relabels a switch row when replacing its controlled Device", () => {
    const first = "ent_019a3e6a-4111-7000-8000-000000000001";
    const second = "ent_019a3e6a-4111-7000-8000-000000000002";
    const onChange =
      vi.fn<Parameters<typeof ToggleListEditor>[0]["onChange"]>();
    render(
      <ToggleListEditor
        value={{
          title: "Room",
          rows: [
            {
              label: "Office Fan",
              source: {
                kind: "entity-state",
                entity_id: first,
                entity_type: "hearth.power/v1",
              },
            },
          ],
        }}
        choices={[
          {
            id: first,
            name: "Power",
            type: "hearth.power/v1",
            device_name: "Office Fan",
          },
          {
            id: second,
            name: "Power",
            type: "hearth.power/v1",
            device_name: "Heater",
          },
        ]}
        onChange={onChange}
      />
    );
    fireEvent.change(screen.getByLabelText("Power source"), {
      target: { value: second },
    });
    expect(onChange).toHaveBeenLastCalledWith({
      title: "Room",
      rows: [
        {
          label: "Heater",
          source: {
            kind: "entity-state",
            entity_id: second,
            entity_type: "hearth.power/v1",
          },
        },
      ],
    });
  });

  it("keeps long Device-plus-endpoint labels within the portable limit without dropping identity", () => {
    const id = "ent_019a3e6a-4111-7000-8000-000000000001";
    const onChange =
      vi.fn<Parameters<typeof ToggleListEditor>[0]["onChange"]>();
    render(
      <ToggleListEditor
        value={{
          title: "Room",
          rows: [
            {
              source: {
                kind: "entity-state",
                entity_id: id,
                entity_type: "hearth.power/v1",
              },
            },
          ],
        }}
        choices={[
          {
            id,
            name: "Left Power",
            type: "hearth.power/v1",
            device_id: "device",
            device_name: "A".repeat(120),
          },
          {
            id: "ent_019a3e6a-4111-7000-8000-000000000002",
            name: "Right Power",
            type: "hearth.power/v1",
            device_id: "device",
            device_name: "A".repeat(120),
          },
        ]}
        onChange={onChange}
      />
    );
    fireEvent.change(screen.getByLabelText("Power source"), {
      target: { value: id },
    });
    const [[value]] = onChange.mock.calls;
    const [{ label }] = value.rows;
    if (typeof label !== "string") {
      throw new TypeError("Expected the switch row to retain its label");
    }
    expect(label).toHaveLength(120);
    expect(label.endsWith(id)).toBeTruthy();
  });

  it("loads same-type sensor choices when the final API page has a null cursor", async () => {
    const id = "ent_019a3e6a-4111-7000-8000-000000000001";
    const selected = "ent_019a3e6a-4111-7000-8000-000000000002";
    const temperature = {
      "": { unit: "Cel", label: "°C", fraction_digits: 1 },
    };
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      Response.json({
        items: [
          {
            id,
            name: "Kitchen",
            type: "hearth.temperature/v1",
            quantities: temperature,
          },
          {
            id: selected,
            name: "Original",
            type: "hearth.temperature/v1",
            quantities: temperature,
          },
          {
            id: "ent_019a3e6a-4111-7000-8000-000000000003",
            name: "Bathroom humidity",
            type: "hearth.relativehumidity/v1",
          },
        ],
        next_cursor: null,
      })
    );
    const definition: ViewDefinition = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Temperatures",
      spec: {
        root: "chart",
        elements: {
          chart: {
            type: "TimeSeries",
            props: {
              title: "History",
              series: [
                {
                  source: {
                    kind: "entity-series",
                    entity_id: "ent_019a3e6a-4111-7000-8000-000000000002",
                    entity_type: "hearth.temperature/v1",
                    window: "24h",
                  },
                },
              ],
            },
          },
        },
      },
    };
    render(
      <ViewEditor
        definition={definition}
        onChange={vi.fn<Parameters<typeof ViewEditor>[0]["onChange"]>()}
      />
    );
    fireEvent.click(screen.getByText("TimeSeries · chart"));
    await expect(
      screen.findByRole("option", { name: "Kitchen" })
    ).resolves.toBeTruthy();
    expect(
      screen.queryByRole("option", { name: "Bathroom humidity" })
    ).toBeNull();
    expect(screen.queryByText(/Sensor list unavailable/u)).toBeNull();
  });

  it("keeps the previous definition while the dashboard title is temporarily empty", () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response('{"items":[],"next_cursor":null}')
    );
    const original: ViewDefinition = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Original",
      spec: {
        root: "note",
        elements: { note: { type: "Note", props: { text: "Original note" } } },
      },
    };
    const onChange = vi.fn<Parameters<typeof ViewEditor>[0]["onChange"]>();
    const valid = vi.fn<(valid: boolean) => void>();
    render(
      <ViewEditor
        definition={original}
        onChange={onChange}
        onValidityChange={valid}
      />
    );
    const title = screen.getByLabelText("Dashboard title");
    fireEvent.change(title, { target: { value: "" } });
    expect((title as HTMLInputElement).value).toBe("");
    expect(onChange).not.toHaveBeenCalled();
    expect(valid).toHaveBeenLastCalledWith(false);
    expect(title.getAttribute("aria-invalid")).toBe("true");
    expect(
      document.querySelector(`[id="${title.getAttribute("aria-describedby")}"]`)
        ?.textContent
    ).toBe("Use 1–80 characters for the dashboard title.");
  });

  it("applies a complete dashboard title replacement after an empty intermediate value", () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response('{"items":[],"next_cursor":null}')
    );
    const original: ViewDefinition = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Original",
      spec: {
        root: "note",
        elements: { note: { type: "Note", props: { text: "Original note" } } },
      },
    };
    const onChange = vi.fn<Parameters<typeof ViewEditor>[0]["onChange"]>();
    const valid = vi.fn<(valid: boolean) => void>();
    render(
      <ViewEditor
        definition={original}
        onChange={onChange}
        onValidityChange={valid}
      />
    );
    const title = screen.getByLabelText("Dashboard title");
    fireEvent.change(title, { target: { value: "" } });
    fireEvent.change(title, { target: { value: "Replacement" } });
    expect(onChange).toHaveBeenLastCalledWith({
      ...original,
      title: "Replacement",
    });
    expect(valid).toHaveBeenLastCalledWith(true);
    expect(title.hasAttribute("aria-invalid")).toBeFalsy();
    expect(original.title).toBe("Original");
  });

  it("reports an empty Note and links it to the invalid-fields summary", () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response('{"items":[],"next_cursor":null}')
    );
    const original: ViewDefinition = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Original",
      spec: {
        root: "note",
        elements: { note: { type: "Note", props: { text: "Original note" } } },
      },
    };
    const onChange = vi.fn<Parameters<typeof ViewEditor>[0]["onChange"]>();
    render(<ViewEditor definition={original} onChange={onChange} />);
    fireEvent.click(screen.getByText("Note · note"));
    const note = screen.getByLabelText("Text");
    fireEvent.change(note, { target: { value: "" } });
    expect(note.getAttribute("aria-invalid")).toBe("true");
    expect(
      document.querySelector(`[id="${note.getAttribute("aria-describedby")}"]`)
        ?.textContent
    ).toBe("Use 1–500 characters for this note.");
    expect(
      screen.getByText(/Fix invalid fields/u).closest("summary")
    ).not.toBeNull();
  });

  it("offers only compatible sensors using the selected field's returned units", () => {
    const type = "hearth.numericsetting/v1";
    const source = {
      kind: "entity-series" as const,
      entity_id: "ent_019a3e6a-4111-7000-8000-000000000001",
      entity_type: type,
      field: "/value",
      window: "6h" as const,
    };
    const choices = [
      {
        id: source.entity_id,
        name: "Original",
        type,
        support: { state: { unit: "%" } },
        quantities: { "/value": { unit: "%", label: "%", fraction_digits: 1 } },
      },
      {
        id: "ent_019a3e6a-4111-7000-8000-000000000002",
        name: "Compatible",
        type,
        support: { state: { unit: "%" } },
        quantities: { "/value": { unit: "%", label: "%", fraction_digits: 1 } },
      },
      {
        id: "ent_019a3e6a-4111-7000-8000-000000000003",
        name: "Other unit",
        type,
        support: { state: { unit: "Cel" } },
        quantities: {
          "/value": { unit: "Cel", label: "°C", fraction_digits: 1 },
        },
      },
    ];
    const onChange =
      vi.fn<Parameters<typeof TimeSeriesEditor>[0]["onChange"]>();
    render(
      <TimeSeriesEditor
        value={{ title: "Settings", series: [{ source }] }}
        choices={choices}
        onChange={onChange}
      />
    );
    expect(screen.getByRole("option", { name: "Compatible" })).toBeTruthy();
    expect(screen.queryByRole("option", { name: "Other unit" })).toBeNull();
    fireEvent.change(screen.getByLabelText("Add sensor"), {
      target: { value: choices[1].id },
    });
    expect(onChange).toHaveBeenLastCalledWith({
      title: "Settings",
      series: [{ source }, { source: { ...source, entity_id: choices[1].id } }],
    });
  });

  it("discovers only rain sensors with matching delivered and canonical units", async () => {
    const original = "ent_019a3e6a-4111-7000-8000-000000000001";
    const type = "hearth.numericsensor/v1";
    const support = { state: { unit: "mm/h", minimum: 0, maximum: 100 } };
    const entities = [
      {
        id: original,
        name: "Original",
        type,
        support,
        quantities: quantity("[in_i]/h"),
        state: null,
      },
      {
        id: "ent_019a3e6a-4111-7000-8000-000000000002",
        name: "Classified rain",
        type,
        support,
        quantities: quantity("[in_i]/h"),
      },
      {
        id: "ent_019a3e6a-4111-7000-8000-000000000003",
        name: "Unclassified rain",
        type,
        support,
        quantities: quantity("mm/h"),
      },
      {
        id: "ent_019a3e6a-4111-7000-8000-000000000004",
        name: "Missing metadata",
        type,
        support,
      },
      {
        id: "ent_019a3e6a-4111-7000-8000-000000000005",
        name: "Different canonical unit",
        type,
        support: { state: { unit: "[in_i]/h", minimum: 0, maximum: 100 } },
        quantities: quantity("[in_i]/h"),
      },
    ];
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      Response.json({ items: entities, next_cursor: null })
    );
    const definition: ViewDefinition = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Rain",
      spec: {
        root: "chart",
        elements: {
          chart: {
            type: "TimeSeries",
            props: {
              title: "Rain",
              series: [
                {
                  source: {
                    kind: "entity-series",
                    entity_id: original,
                    entity_type: type,
                    window: "6h",
                  },
                },
              ],
            },
          },
        },
      },
    };
    render(
      <ViewEditor
        definition={definition}
        onChange={vi.fn<Parameters<typeof ViewEditor>[0]["onChange"]>()}
      />
    );
    fireEvent.click(screen.getByText("TimeSeries · chart"));
    await expect(
      screen.findByRole("option", { name: "Classified rain" })
    ).resolves.toBeTruthy();
    expect(
      screen.queryByRole("option", { name: "Unclassified rain" })
    ).toBeNull();
    expect(
      screen.queryByRole("option", { name: "Missing metadata" })
    ).toBeNull();
    expect(
      screen.queryByRole("option", { name: "Different canonical unit" })
    ).toBeNull();
  });

  it("changes every chart series field together", () => {
    const source = {
      kind: "entity-series" as const,
      entity_id: "ent_019a3e6a-4111-7000-8000-000000000001",
      entity_type: "hearth.colorhs/v1",
      field: "/hue",
      window: "6h" as const,
    };
    const second = {
      ...source,
      entity_id: "ent_019a3e6a-4111-7000-8000-000000000002",
    };
    const onChange =
      vi.fn<Parameters<typeof TimeSeriesEditor>[0]["onChange"]>();
    render(
      <TimeSeriesEditor
        value={{ title: "Color", series: [{ source }, { source: second }] }}
        choices={[]}
        onChange={onChange}
      />
    );
    fireEvent.change(screen.getByLabelText("State field"), {
      target: { value: "/saturation" },
    });
    expect(onChange).toHaveBeenLastCalledWith({
      title: "Color",
      series: [
        { source: { ...source, field: "/saturation" } },
        { source: { ...second, field: "/saturation" } },
      ],
    });
  });

  it("discovers same-Device sparkline choices without offering another plug's metric", async () => {
    const metric = "ent_019a3e6a-4111-7000-8000-000000000002";
    const power = "ent_019a3e6a-4111-7000-8000-000000000001";
    const device = "dev_019a3e6a-4111-7000-8000-000000000001";
    const entities = [
      { id: power, name: "Power", type: "hearth.power/v1", device_id: device },
      {
        id: metric,
        name: "Electrical Power",
        type: "hearth.numericsensor/v1",
        device_id: device,
        support: { state: { unit: "W" } },
      },
      {
        id: "ent_019a3e6a-4111-7000-8000-000000000003",
        name: "Other plug draw",
        type: "hearth.numericsensor/v1",
        device_id: "other",
        support: { state: { unit: "W" } },
      },
    ];
    const fetch = vi.spyOn(globalThis, "fetch").mockImplementation((input) =>
      Promise.resolve(
        Response.json({
          items: String(input).startsWith("/v1/devices")
            ? [{ id: device, name: "Office Plug Fan" }]
            : entities,
          next_cursor: null,
        })
      )
    );
    const definition: ViewDefinition = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Plug",
      spec: {
        root: "plug",
        elements: {
          plug: {
            type: "ToggleTile",
            props: {
              source: {
                kind: "entity-state",
                entity_id: power,
                entity_type: "hearth.power/v1",
              },
              metric: {
                source: {
                  kind: "entity-state",
                  entity_id: metric,
                  entity_type: "hearth.numericsensor/v1",
                },
                window: "24h",
              },
            },
          },
        },
      },
    };
    const onChange = vi.fn<Parameters<typeof ViewEditor>[0]["onChange"]>();
    render(<ViewEditor definition={definition} onChange={onChange} />);
    fireEvent.click(screen.getByText("ToggleTile · plug"));
    await expect(
      within(screen.getByLabelText("Sparkline source")).findByRole("option", {
        name: "Electrical Power (W)",
      })
    ).resolves.toBeTruthy();
    expect(
      screen.queryByRole("option", { name: /Other plug draw/u })
    ).toBeNull();
    expect(onChange).not.toHaveBeenCalled();
    expect(
      fetch.mock.calls.every(
        ([, init]) => !init?.method || init.method === "GET"
      )
    ).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Sparkline source"), {
      target: { value: "" },
    });
    const { plug } = onChange.mock.calls[0][0].spec.elements;
    if (plug.type !== "ToggleTile") {
      throw new TypeError("Expected the edited element to remain a ToggleTile");
    }
    expect(plug.props.metric).toBeUndefined();
  });

  it("names new switch rows by Device and prevents duplicate source choices", async () => {
    const first = "ent_019a3e6a-4111-7000-8000-000000000001";
    const second = "ent_019a3e6a-4111-7000-8000-000000000002";
    const a = "dev_019a3e6a-4111-7000-8000-000000000001";
    const b = "dev_019a3e6a-4111-7000-8000-000000000002";
    vi.spyOn(globalThis, "fetch").mockImplementation((input) =>
      Promise.resolve(
        Response.json({
          items: String(input).startsWith("/v1/devices")
            ? [
                { id: a, name: "Office Fan" },
                { id: b, name: "Living Room Fan" },
              ]
            : [
                {
                  id: first,
                  name: "Power",
                  type: "hearth.power/v1",
                  device_id: a,
                },
                {
                  id: second,
                  name: "Power",
                  type: "hearth.power/v1",
                  device_id: b,
                },
              ],
          next_cursor: null,
        })
      )
    );
    const definition: ViewDefinition = {
      schema_version: 1,
      catalog_version: "hearth.dashboard/v1",
      title: "Room",
      spec: {
        root: "room",
        elements: {
          room: {
            type: "ToggleList",
            props: {
              title: "Room",
              rows: [
                {
                  source: {
                    kind: "entity-state",
                    entity_id: first,
                    entity_type: "hearth.power/v1",
                  },
                  label: "Office Fan",
                },
              ],
            },
          },
        },
      },
    };
    const onChange = vi.fn<Parameters<typeof ViewEditor>[0]["onChange"]>();
    render(<ViewEditor definition={definition} onChange={onChange} />);
    fireEvent.click(screen.getByText("ToggleList · room"));
    const add = screen.getByLabelText("Add switch");
    await expect(
      within(add).findByRole("option", { name: "Living Room Fan" })
    ).resolves.toBeTruthy();
    expect(
      within(add).queryByRole("option", { name: "Office Fan" })
    ).toBeNull();
    fireEvent.change(add, { target: { value: second } });
    const { room } = onChange.mock.calls[0][0].spec.elements;
    if (room.type !== "ToggleList") {
      throw new TypeError("Expected the edited element to remain a ToggleList");
    }
    expect(room.props.rows[1]).toStrictEqual({
      label: "Living Room Fan",
      source: {
        kind: "entity-state",
        entity_id: second,
        entity_type: "hearth.power/v1",
      },
    });
    expect(
      within(screen.getByLabelText("Add switch")).queryByRole("option", {
        name: "Living Room Fan",
      })
    ).toBeNull();
  });
});
