import { fireEvent, render, screen, cleanup } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ViewEditor } from "./ViewEditor";
import type { ViewDefinition } from "../catalog";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it("loads temperature choices when the final API page has a null cursor", async () => {
  const id = "ent_019a3e6a-4111-7000-8000-000000000001";
  vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ items: [{ id, name: "Kitchen", type: "hearth.temperature/v1" }], next_cursor: null })));
  const definition: ViewDefinition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Temperatures", spec: { root: "chart", elements: { chart: { type: "TimeSeries", props: { title: "History", display: { unit: "celsius" }, series: [{ source: { kind: "entity-series", entity_id: "ent_019a3e6a-4111-7000-8000-000000000002", entity_type: "hearth.temperature/v1", window: "24h" } }] } } } } };
  render(<ViewEditor definition={definition} onChange={vi.fn()} />);
  fireEvent.click(screen.getByText("TimeSeries · chart"));
  expect(await screen.findByRole("option", { name: "Kitchen" })).toBeTruthy();
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
  fireEvent.change(title, { target: { value: "Replacement" } });
  expect(onChange).toHaveBeenLastCalledWith({ ...original, title: "Replacement" });
  expect(valid).toHaveBeenLastCalledWith(true);
  expect(original.title).toBe("Original");
});
