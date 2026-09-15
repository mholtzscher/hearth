import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Automation } from "../api/types.ts";
import {
  AUTOMATION_ID,
  AUTOMATION_NAME,
  ENTITY_ID,
  automationFixture,
  installAutomationFetch,
} from "./automation-fetch-fake.ts";
import AutomationsPage from "./AutomationsPage.tsx";

/** A second page of definitions, so keyset paging has somewhere to go. */
const SECOND_AUTOMATION_ID = "aut_01920000-0000-7000-8000-00000000000b";
const SECOND_AUTOMATION_NAME = "Hallway light at dusk";
/** Opaque cursor holding `/`, `+` and `=`, like hearthd's own cursors. */
const NEXT_CURSOR = "page/1+=";

function secondAutomation(): Automation {
  return automationFixture({
    id: SECOND_AUTOMATION_ID,
    revision: 7,
    updated_at: "2026-02-02T08:00:00.000Z",
    definition: {
      name: SECOND_AUTOMATION_NAME,
      enabled: false,
      triggers: [
        {
          id: "hallway_motion",
          kind: "observation",
          entity_id: ENTITY_ID,
          dispositions: ["applied", "unchanged"],
          comparisons: [{ pointer: "value", operator: "eq", operand: true }],
        },
      ],
      steps: [
        { id: "dim", entity_id: ENTITY_ID, operation: "set", parameters: { value: 20 } },
        { id: "settle", entity_id: ENTITY_ID, operation: "set", parameters: { value: 40 } },
      ],
    },
  });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("AutomationsPage", () => {
  it("lists definitions with enablement, revision, counts, update time, and id", async () => {
    installAutomationFetch([
      {
        method: "GET",
        path: "/v1/automations",
        respond: () => ({ body: { items: [automationFixture(), secondAutomation()] } }),
      },
    ]);

    render(
      <MemoryRouter>
        <AutomationsPage />
      </MemoryRouter>,
    );

    const link = await screen.findByRole("link", { name: AUTOMATION_NAME });
    const row = link.closest("tr");
    if (!row) throw new Error(`the row for ${AUTOMATION_ID} did not render`);

    // Columns: name, enabled, revision, triggers, steps, updated, id.
    const cells = within(row).getAllByRole("cell");
    expect(cells[0].textContent).toBe(AUTOMATION_NAME);
    expect(cells[1].textContent).toContain("enabled");
    expect(cells[2].textContent).toBe("3");
    expect(cells[3].textContent).toBe("1");
    expect(cells[4].textContent).toBe("1");
    expect(cells[5].textContent).toBe("2026-02-01T09:30:00.000Z");
    expect(cells[6].textContent).toContain(AUTOMATION_ID);

    const secondRow = screen
      .getByRole("link", { name: SECOND_AUTOMATION_NAME })
      .closest("tr");
    if (!secondRow) throw new Error(`the row for ${SECOND_AUTOMATION_ID} did not render`);
    const secondCells = within(secondRow).getAllByRole("cell");
    expect(secondCells[1].textContent).toContain("disabled");
    expect(secondCells[2].textContent).toBe("7");
    // The second Automation has one Trigger and two Steps.
    expect(secondCells[3].textContent).toBe("1");
    expect(secondCells[4].textContent).toBe("2");

    expect(link.getAttribute("href")).toBe(`/automations/${AUTOMATION_ID}`);
    // The raw page stays available for debugging.
    expect(screen.getByRole("button", { name: "Raw list JSON" })).not.toBeNull();
  });

  it("pages the ID-ascending keyset with an encoded cursor", async () => {
    const requests = installAutomationFetch([
      {
        method: "GET",
        path: "/v1/automations",
        respond: (request) =>
          new URL(request.url, "http://hearthd.test").searchParams.has("cursor")
            ? { body: { items: [secondAutomation()] } }
            : { body: { items: [automationFixture()], next_cursor: NEXT_CURSOR } },
      },
    ]);

    render(
      <MemoryRouter>
        <AutomationsPage />
      </MemoryRouter>,
    );

    await screen.findByRole("link", { name: AUTOMATION_NAME });
    expect(requests.map((request) => request.url)).toEqual(["/v1/automations?limit=50"]);

    fireEvent.click(screen.getByRole("button", { name: "Next page" }));

    await screen.findByRole("link", { name: SECOND_AUTOMATION_NAME });
    expect(requests.map((request) => request.url)).toEqual([
      "/v1/automations?limit=50",
      // The cursor round-trips percent-encoded: `+` unencoded would arrive as a space.
      "/v1/automations?limit=50&cursor=page%2F1%2B%3D",
    ]);
    expect(screen.queryByRole("button", { name: "Next page" })).toBeNull();
  });

  it("reports a failed read instead of an empty list", async () => {
    installAutomationFetch([
      {
        method: "GET",
        path: "/v1/automations",
        respond: () => ({ status: 503, body: { title: "Service Unavailable", detail: "closed" } }),
      },
    ]);

    render(
      <MemoryRouter>
        <AutomationsPage />
      </MemoryRouter>,
    );

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("closed");
    expect(screen.queryByText("No automations.")).toBeNull();
  });
});
