import { fireEvent, render, screen, cleanup } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ViewEditor } from "./ViewEditor";
import type { ViewDefinition } from "../catalog";
import { TimeSeriesEditor } from "../cards/time-series/Editor";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it("loads same-type sensor choices when the final API page has a null cursor", async () => {
  const id = "ent_019a3e6a-4111-7000-8000-000000000001";
  vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ items: [
    { id, name: "Kitchen", type: "hearth.temperature/v1" },
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

it("offers only canonical-unit-compatible sensors and preserves the selected field", () => {
  const type = "hearth.numericsetting/v1";
  const source = { kind: "entity-series" as const, entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: type, field: "/value", window: "6h" as const };
  const choices = [
    { id: source.entity_id, name: "Original", type, support: { state: { unit: "%" } } },
    { id: "ent_019a3e6a-4111-7000-8000-000000000002", name: "Compatible", type, support: { state: { unit: "%" } } },
    { id: "ent_019a3e6a-4111-7000-8000-000000000003", name: "Other unit", type, support: { state: { unit: "Cel" } } },
  ];
  const onChange = vi.fn();
  render(<TimeSeriesEditor value={{ title: "Settings", series: [{ source }] }} choices={choices} onChange={onChange} />);
  expect(screen.getByRole("option", { name: "Compatible" })).toBeTruthy();
  expect(screen.queryByRole("option", { name: "Other unit" })).toBeNull();
  fireEvent.change(screen.getByLabelText("Add sensor"), { target: { value: choices[1].id } });
  expect(onChange).toHaveBeenLastCalledWith({ title: "Settings", series: [{ source }, { source: { ...source, entity_id: choices[1].id } }] });
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
