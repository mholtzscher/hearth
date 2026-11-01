import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ViewDataProvider } from "../data/provider";
import { ViewRenderer } from "./ViewRenderer";
import { readDefinition } from "../catalog/compatibility";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
const source = { kind: "entity-state", entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: "hearth.temperature/v1" };
const metric = { type: "Metric", props: { source, display: { unit: "celsius" } } };

it("preserves an unknown container and suppresses descendant reads while showing supported siblings", async () => {
  const fetch = vi.spyOn(globalThis, "fetch");
  const raw = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Future", spec: {
    root: "root", elements: {
      root: { type: "Stack", props: { gap: "medium" }, children: ["note", "future"] },
      note: { type: "Note", props: { text: "Still readable" } },
      future: { type: "FutureLayout", props: { opaque: "preserve me" }, children: ["metric"] },
      metric,
    },
  } };
  const before = JSON.stringify(raw);
  await act(async () => render(<ViewDataProvider><ViewRenderer definition={raw} /></ViewDataProvider>));
  expect(screen.getByText("Still readable")).toBeTruthy();
  expect(screen.getByText(/Unsupported component/)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Download original definition" })).toBeTruthy();
  expect(fetch).not.toHaveBeenCalled();
  expect(JSON.stringify(raw)).toBe(before);
  expect(readDefinition(raw).status).toBe("partial");
});

it("does not pass unknown properties or bindings to resource adapters", async () => {
  const fetch = vi.spyOn(globalThis, "fetch");
  for (const props of [
    { ...metric.props, futureOption: true },
    { ...metric.props, source: { ...source, kind: "future-binding" } },
  ]) {
    const raw = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Future", spec: {
      root: "metric", elements: { metric: { ...metric, props } },
    } };
    await act(async () => render(<ViewDataProvider><ViewRenderer definition={raw} /></ViewDataProvider>));
    expect(screen.getByText(/Unsupported component/)).toBeTruthy();
    expect(fetch).not.toHaveBeenCalled();
    cleanup();
  }
});

it("renders whole-view recovery for invalid graphs and unsupported majors without fetching", async () => {
  const fetch = vi.spyOn(globalThis, "fetch");
  for (const raw of [
    { schema_version: 2, catalog_version: "hearth.dashboard/v2", title: "Future", spec: { root: "metric", elements: { metric } } },
    { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Invalid", spec: { root: "missing", elements: { metric } } },
  ]) {
    await act(async () => render(<ViewDataProvider><ViewRenderer definition={raw} /></ViewDataProvider>));
    expect(screen.getByRole("button", { name: "Download original definition" })).toBeTruthy();
    expect(readDefinition(raw).status).toBe("unavailable");
    expect(fetch).not.toHaveBeenCalled();
    cleanup();
  }
});

it("keeps older supported cards as summaries without live subscriptions", async () => {
  const fetch = vi.spyOn(globalThis, "fetch");
  const raw = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Earlier view", spec: { root: "metric", elements: { metric } } };
  await act(async () => render(<ViewDataProvider><ViewRenderer definition={raw} active={false} /></ViewDataProvider>));
  expect(screen.getByText("Earlier view")).toBeTruthy();
  expect(fetch).not.toHaveBeenCalled();
});
