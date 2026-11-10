import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { ViewDataProvider } from "../data/provider";
import { ViewRenderer } from "./ViewRenderer";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const id = "ent_019a3e6a-4111-7000-8000-000000000001";
const source = { kind: "entity-state", entity_id: id, entity_type: "hearth.temperature/v1" };
const view = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Garage", spec: {
  root: "root", elements: {
    root: { type: "Stack", props: { gap: "large" }, children: ["heading", "metric"] },
    heading: { type: "Heading", props: { text: "Climate" } },
    metric: { type: "Metric", props: { label: "Garage temperature", source } },
  },
} };

function respond(availability: string) {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({
    id, type: "hearth.temperature/v1", name: "Temperature", enabled: true, availability: { status: availability }, support: {},
    quantities: { "": { unit: "Cel", label: "°C", fraction_digits: 1 } },
    state: { value: 20, observed_at: new Date().toISOString() },
  })));
}

it("takes heading levels from the host: view headings, then card titles one level below", async () => {
  respond("available");
  await act(async () => render(<ViewDataProvider><ViewRenderer headingLevel={3} definition={view} /></ViewDataProvider>, { wrapper: MemoryRouter }));
  expect(screen.getByRole("heading", { level: 3, name: "Climate" })).toBeTruthy();
  expect(screen.getByRole("heading", { level: 4, name: "Garage temperature" })).toBeTruthy();
  cleanup();
  await act(async () => render(<ViewDataProvider><ViewRenderer headingLevel={4} definition={view} /></ViewDataProvider>, { wrapper: MemoryRouter }));
  expect(screen.getByRole("heading", { level: 4, name: "Climate" })).toBeTruthy();
  expect(screen.getByRole("heading", { level: 5, name: "Garage temperature" })).toBeTruthy();
  // Cards are named articles, not landmarks.
  expect(screen.queryAllByRole("region")).toHaveLength(0);
});

it("dims an unavailable reading, leads with its status, and links Source to the Entity", async () => {
  respond("unavailable");
  await act(async () => render(<ViewDataProvider><ViewRenderer headingLevel={3} definition={view} /></ViewDataProvider>, { wrapper: MemoryRouter }));
  const card = await screen.findByRole("article", { name: "Garage temperature" });
  const status = await within(card).findByText("Unavailable");
  expect(status.closest("li")?.getAttribute("data-tone")).toBe("warn");
  expect(card.querySelector(".view-metric-value")?.getAttribute("data-value")).toBe("dimmed");
  expect(card.querySelector(".view-card-arrival")).toBeNull();
  const toggle = within(card).getByRole("button", { name: "Source" });
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(toggle);
  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  const details = within(screen.getByRole("dialog", { name: "Reading details" }));
  expect(details.getByRole("link", { name: "Temperature" }).getAttribute("href")).toBe(`/entities/${id}`);
  expect(details.getByText("Unavailable")).toBeTruthy();
  expect(details.getByText("Enabled")).toBeTruthy();
});

it("separates combined statuses into distinct list items", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({
    id, type: "hearth.temperature/v1", name: "Temperature", enabled: false, availability: { status: "unavailable" }, support: {},
    quantities: { "": { unit: "Cel", label: "°C", fraction_digits: 1 } },
    state: { value: 20, observed_at: new Date().toISOString() },
  })));
  await act(async () => render(<ViewDataProvider><ViewRenderer headingLevel={3} definition={view} /></ViewDataProvider>, { wrapper: MemoryRouter }));
  const card = await screen.findByRole("article", { name: "Garage temperature" });
  await within(card).findByText("Disabled");
  expect([...card.querySelectorAll(".view-status li")].map(li => li.textContent)).toEqual(["Unavailable", "Disabled"]);
});
