import { fireEvent, render, screen, cleanup, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ViewEditor } from "./ViewEditor";
import type { ViewDefinition } from "../catalog";
import { TimeSeriesEditor } from "../cards/time-series/Editor";
import { ToggleListEditor } from "../cards/toggle-list/Editor";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it("distinguishes power endpoints on the same Device and falls back to identity for duplicate names", () => {
  const first = "ent_019a3e6a-4111-7000-8000-000000000001", second = "ent_019a3e6a-4111-7000-8000-000000000002", third = "ent_019a3e6a-4111-7000-8000-000000000003";
  const choices = [
    { id: first, name: "Left Power", type: "hearth.power/v1", device_id: "device", device_name: "Desk outlet" },
    { id: second, name: "Right Power", type: "hearth.power/v1", device_id: "device", device_name: "Desk outlet" },
    { id: third, name: "Right Power", type: "hearth.power/v1", device_id: "device", device_name: "Desk outlet" },
  ];
  const onChange = vi.fn();
  render(<ToggleListEditor value={{ title: "Room", rows: [{ source: { kind: "entity-state", entity_id: first, entity_type: "hearth.power/v1" } }] }} choices={choices} onChange={onChange} />);
  const select = screen.getByLabelText("Power source");
  expect(within(select).getByRole("option", { name: "Desk outlet · Left Power" }).getAttribute("value")).toBe(first);
  expect(within(select).getByRole("option", { name: `Desk outlet · Right Power · ${second}` }).getAttribute("value")).toBe(second);
  fireEvent.change(select, { target: { value: second } });
  expect(onChange.mock.calls[0][0].rows[0]).toEqual({ label: `Desk outlet · Right Power · ${second}`, source: { kind: "entity-state", entity_id: second, entity_type: "hearth.power/v1" } });
});
it("relabels a switch row when replacing its controlled Device", () => {
  const first = "ent_019a3e6a-4111-7000-8000-000000000001", second = "ent_019a3e6a-4111-7000-8000-000000000002";
  const onChange = vi.fn();
  render(<ToggleListEditor value={{ title: "Room", rows: [{ label: "Office Fan", source: { kind: "entity-state", entity_id: first, entity_type: "hearth.power/v1" } }] }} choices={[
    { id: first, name: "Power", type: "hearth.power/v1", device_name: "Office Fan" },
    { id: second, name: "Power", type: "hearth.power/v1", device_name: "Heater" },
  ]} onChange={onChange} />);
  fireEvent.change(screen.getByLabelText("Power source"), { target: { value: second } });
  expect(onChange).toHaveBeenLastCalledWith({ title: "Room", rows: [{ label: "Heater", source: { kind: "entity-state", entity_id: second, entity_type: "hearth.power/v1" } }] });
});
it("keeps long Device-plus-endpoint labels within the portable limit without dropping identity", () => {
  const id = "ent_019a3e6a-4111-7000-8000-000000000001";
  const onChange = vi.fn();
  render(<ToggleListEditor value={{ title: "Room", rows: [{ source: { kind: "entity-state", entity_id: id, entity_type: "hearth.power/v1" } }] }} choices={[
    { id, name: "Left Power", type: "hearth.power/v1", device_id: "device", device_name: "A".repeat(120) },
    { id: "ent_019a3e6a-4111-7000-8000-000000000002", name: "Right Power", type: "hearth.power/v1", device_id: "device", device_name: "A".repeat(120) },
  ]} onChange={onChange} />);
  fireEvent.change(screen.getByLabelText("Power source"), { target: { value: id } });
  const label = onChange.mock.calls[0][0].rows[0].label;
  expect(label.length).toBe(120);
  expect(label.endsWith(id)).toBe(true);
});
it("loads same-type sensor choices when the final API page has a null cursor", async () => {
  const id = "ent_019a3e6a-4111-7000-8000-000000000001";
  const selected = "ent_019a3e6a-4111-7000-8000-000000000002";
  const temperature = { "": { unit: "Cel", label: "°C", fraction_digits: 1 } };
  vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ items: [
    { id, name: "Kitchen", type: "hearth.temperature/v1", quantities: temperature },
    { id: selected, name: "Original", type: "hearth.temperature/v1", quantities: temperature },
    { id: "ent_019a3e6a-4111-7000-8000-000000000003", name: "Bathroom humidity", type: "hearth.relativehumidity/v1" },
  ], next_cursor: null })));
  const definition: ViewDefinition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Temperatures", spec: { root: "chart", elements: { chart: { type: "TimeSeries", props: { title: "History", series: [{ source: { kind: "entity-series", entity_id: "ent_019a3e6a-4111-7000-8000-000000000002", entity_type: "hearth.temperature/v1", window: "24h" } }] } } } } };
  render(<ViewEditor definition={definition} onChange={vi.fn()} />);
  fireEvent.click(screen.getByText("TimeSeries · chart"));
  expect(await screen.findByRole("option", { name: "Kitchen" })).toBeTruthy();
  expect(screen.queryByRole("option", { name: "Bathroom humidity" })).toBeNull();
  expect(screen.queryByText(/Sensor list unavailable/)).toBeNull();
});
it("allows empty intermediate text, reports invalidity, then applies the complete replacement", () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response('{"items":[],"next_cursor":null}'));
  const original: ViewDefinition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Original", spec: { root: "note", elements: { note: { type: "Note", props: { text: "Original note" } } } } };
  const onChange = vi.fn();
  const valid = vi.fn();
  render(<ViewEditor definition={original} onChange={onChange} onValidityChange={valid} />);
  const title = screen.getByLabelText("Dashboard title");
  fireEvent.change(title, { target: { value: "" } });
  expect((title as HTMLInputElement).value).toBe("");
  expect(onChange).not.toHaveBeenCalled();
  expect(valid).toHaveBeenLastCalledWith(false);
  expect(title.getAttribute("aria-invalid")).toBe("true");
  expect(document.getElementById(title.getAttribute("aria-describedby")!)?.textContent).toBe("Use 1–80 characters for the dashboard title.");
  fireEvent.change(title, { target: { value: "Replacement" } });
  expect(onChange).toHaveBeenLastCalledWith({ ...original, title: "Replacement" });
  expect(valid).toHaveBeenLastCalledWith(true);
  expect(title.hasAttribute("aria-invalid")).toBe(false);
  expect(original.title).toBe("Original");
  fireEvent.click(screen.getByText("Note · note"));
  const note = screen.getByLabelText("Text");
  fireEvent.change(note, { target: { value: "" } });
  expect(note.getAttribute("aria-invalid")).toBe("true");
  expect(document.getElementById(note.getAttribute("aria-describedby")!)?.textContent).toBe("Use 1–500 characters for this note.");
  expect(screen.getByText(/Fix invalid fields/).closest("summary")).not.toBeNull();
});

it("offers only compatible sensors using the selected field's returned units", () => {
  const type = "hearth.numericsetting/v1";
  const source = { kind: "entity-series" as const, entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: type, field: "/value", window: "6h" as const };
  const choices = [
    { id: source.entity_id, name: "Original", type, support: { state: { unit: "%" } }, quantities: { "/value": { unit: "%", label: "%", fraction_digits: 1 } } },
    { id: "ent_019a3e6a-4111-7000-8000-000000000002", name: "Compatible", type, support: { state: { unit: "%" } }, quantities: { "/value": { unit: "%", label: "%", fraction_digits: 1 } } },
    { id: "ent_019a3e6a-4111-7000-8000-000000000003", name: "Other unit", type, support: { state: { unit: "Cel" } }, quantities: { "/value": { unit: "Cel", label: "°C", fraction_digits: 1 } } },
  ];
  const onChange = vi.fn();
  render(<TimeSeriesEditor value={{ title: "Settings", series: [{ source }] }} choices={choices} onChange={onChange} />);
  expect(screen.getByRole("option", { name: "Compatible" })).toBeTruthy();
  expect(screen.queryByRole("option", { name: "Other unit" })).toBeNull();
  fireEvent.change(screen.getByLabelText("Add sensor"), { target: { value: choices[1].id } });
  expect(onChange).toHaveBeenLastCalledWith({ title: "Settings", series: [{ source }, { source: { ...source, entity_id: choices[1].id } }] });
});

it("discovers only rain sensors with matching delivered and canonical units", async () => {
  const type = "hearth.numericsensor/v1", original = "ent_019a3e6a-4111-7000-8000-000000000001";
  const support = { state: { unit: "mm/h", minimum: 0, maximum: 100 } };
  const quantity = (unit: string) => ({ "": { unit, label: unit, fraction_digits: 2 } });
  const entities = [
    { id: original, name: "Original", type, support, quantities: quantity("[in_i]/h"), state: null },
    { id: "ent_019a3e6a-4111-7000-8000-000000000002", name: "Classified rain", type, support, quantities: quantity("[in_i]/h") },
    { id: "ent_019a3e6a-4111-7000-8000-000000000003", name: "Unclassified rain", type, support, quantities: quantity("mm/h") },
    { id: "ent_019a3e6a-4111-7000-8000-000000000004", name: "Missing metadata", type, support },
    { id: "ent_019a3e6a-4111-7000-8000-000000000005", name: "Different canonical unit", type, support: { state: { unit: "[in_i]/h", minimum: 0, maximum: 100 } }, quantities: quantity("[in_i]/h") },
  ];
  vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ items: entities, next_cursor: null })));
  const definition: ViewDefinition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Rain", spec: { root: "chart", elements: {
    chart: { type: "TimeSeries", props: { title: "Rain", series: [{ source: { kind: "entity-series", entity_id: original, entity_type: type, window: "6h" } }] } },
  } } };
  render(<ViewEditor definition={definition} onChange={vi.fn()} />);
  fireEvent.click(screen.getByText("TimeSeries · chart"));
  expect(await screen.findByRole("option", { name: "Classified rain" })).toBeTruthy();
  expect(screen.queryByRole("option", { name: "Unclassified rain" })).toBeNull();
  expect(screen.queryByRole("option", { name: "Missing metadata" })).toBeNull();
  expect(screen.queryByRole("option", { name: "Different canonical unit" })).toBeNull();
});

it("changes every chart series field together", () => {
  const source = { kind: "entity-series" as const, entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: "hearth.colorhs/v1", field: "/hue", window: "6h" as const };
  const second = { ...source, entity_id: "ent_019a3e6a-4111-7000-8000-000000000002" };
  const onChange = vi.fn();
  render(<TimeSeriesEditor value={{ title: "Color", series: [{ source }, { source: second }] }} choices={[]} onChange={onChange} />);
  fireEvent.change(screen.getByLabelText("State field"), { target: { value: "/saturation" } });
  expect(onChange).toHaveBeenLastCalledWith({ title: "Color", series: [
    { source: { ...source, field: "/saturation" } }, { source: { ...second, field: "/saturation" } },
  ] });
});

it("discovers same-Device sparkline choices without offering another plug's metric", async () => {
  const power = "ent_019a3e6a-4111-7000-8000-000000000001", metric = "ent_019a3e6a-4111-7000-8000-000000000002";
  const device = "dev_019a3e6a-4111-7000-8000-000000000001";
  const entities = [
    { id: power, name: "Power", type: "hearth.power/v1", device_id: device },
    { id: metric, name: "Electrical Power", type: "hearth.numericsensor/v1", device_id: device, support: { state: { unit: "W" } } },
    { id: "ent_019a3e6a-4111-7000-8000-000000000003", name: "Other plug draw", type: "hearth.numericsensor/v1", device_id: "other", support: { state: { unit: "W" } } },
  ];
  const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async input => new Response(JSON.stringify({ items: String(input).startsWith("/v1/devices") ? [{ id: device, name: "Office Plug Fan" }] : entities, next_cursor: null })));
  const definition: ViewDefinition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Plug", spec: { root: "plug", elements: {
    plug: { type: "ToggleTile", props: { source: { kind: "entity-state", entity_id: power, entity_type: "hearth.power/v1" }, metric: { source: { kind: "entity-state", entity_id: metric, entity_type: "hearth.numericsensor/v1" }, window: "24h" } } },
  } } };
  const onChange = vi.fn();
  render(<ViewEditor definition={definition} onChange={onChange} />);
  fireEvent.click(screen.getByText("ToggleTile · plug"));
  expect(await within(screen.getByLabelText("Sparkline source")).findByRole("option", { name: "Electrical Power (W)" })).toBeTruthy();
  expect(screen.queryByRole("option", { name: /Other plug draw/ })).toBeNull();
  expect(onChange).not.toHaveBeenCalled();
  expect(fetch.mock.calls.every(([, init]) => !init?.method || init.method === "GET")).toBe(true);
  fireEvent.change(screen.getByLabelText("Sparkline source"), { target: { value: "" } });
  expect(onChange.mock.calls[0][0].spec.elements.plug.props.metric).toBeUndefined();
});

it("names new switch rows by Device and prevents duplicate source choices", async () => {
  const first = "ent_019a3e6a-4111-7000-8000-000000000001", second = "ent_019a3e6a-4111-7000-8000-000000000002";
  const a = "dev_019a3e6a-4111-7000-8000-000000000001", b = "dev_019a3e6a-4111-7000-8000-000000000002";
  vi.spyOn(globalThis, "fetch").mockImplementation(async input => new Response(JSON.stringify({ items: String(input).startsWith("/v1/devices") ? [{ id: a, name: "Office Fan" }, { id: b, name: "Living Room Fan" }] : [
    { id: first, name: "Power", type: "hearth.power/v1", device_id: a }, { id: second, name: "Power", type: "hearth.power/v1", device_id: b },
  ], next_cursor: null })));
  const definition: ViewDefinition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Room", spec: { root: "room", elements: {
    room: { type: "ToggleList", props: { title: "Room", rows: [{ source: { kind: "entity-state", entity_id: first, entity_type: "hearth.power/v1" }, label: "Office Fan" }] } },
  } } };
  const onChange = vi.fn();
  render(<ViewEditor definition={definition} onChange={onChange} />);
  fireEvent.click(screen.getByText("ToggleList · room"));
  const add = screen.getByLabelText("Add switch");
  expect(await within(add).findByRole("option", { name: "Living Room Fan" })).toBeTruthy();
  expect(within(add).queryByRole("option", { name: "Office Fan" })).toBeNull();
  fireEvent.change(add, { target: { value: second } });
  expect(onChange.mock.calls[0][0].spec.elements.room.props.rows[1]).toEqual({ label: "Living Room Fan", source: { kind: "entity-state", entity_id: second, entity_type: "hearth.power/v1" } });
  expect(within(screen.getByLabelText("Add switch")).queryByRole("option", { name: "Living Room Fan" })).toBeNull();
});
