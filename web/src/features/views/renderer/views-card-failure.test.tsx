import { act, cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ViewDataProvider } from "../data/provider";
import { ViewRenderer } from "./ViewRenderer";

vi.mock("../cards/metric/Metric", () => ({
  BoundMetric: () => { throw new Error("card failed"); },
}));

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("reports a failing card in place and keeps the rest of the view", async () => {
  // React reports the caught render error; the assertion is on what the household sees.
  vi.spyOn(console, "error").mockImplementation(() => {});
  const source = { kind: "entity-state", entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: "hearth.temperature/v1" };
  const raw = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Readings", spec: {
    root: "root", elements: {
      root: { type: "Stack", props: { gap: "medium" }, children: ["note", "metric"] },
      note: { type: "Note", props: { text: "Still readable" } },
      metric: { type: "Metric", props: { source } },
    },
  } };
  await act(async () => render(<ViewDataProvider><ViewRenderer headingLevel={3} definition={raw} /></ViewDataProvider>));
  expect(screen.getByText("Still readable")).toBeTruthy();
  // The failure keeps the card's frame and title, says so in the status, and names the recovery.
  const card = screen.getByRole("article", { name: "Reading" });
  expect(within(card).getByText("This card couldn't be shown")).toBeTruthy();
  expect(within(card).getByText("Edit the view or ask Hearth to change it.")).toBeTruthy();
});
