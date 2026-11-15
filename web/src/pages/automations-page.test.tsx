import {
  cleanup,
  fireEvent,
  render as baseRender,
  screen,
  within,
} from "@testing-library/react";
import type { ReactElement } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Automation } from "../api/types.ts";
import {
  AUTOMATION_ID,
  AUTOMATION_NAME,
  ENTITY_ID,
  automationFixture,
  branchingDefinitionFixture,
  delayDefinitionFixture,
  installAutomationFetch,
} from "./automation-fetch-fake.ts";
import AutomationsPage from "./automations-page.tsx";

/** A second page of definitions, so keyset paging has somewhere to go. */
const SECOND_AUTOMATION_ID = "aut_01920000-0000-7000-8000-00000000000b";
const SECOND_AUTOMATION_NAME = "Hallway light at dusk";
/** Opaque cursor holding `/`, `+` and `=`, like hearthd's own cursors. */
const NEXT_CURSOR = "page/1+=";

// Existing debug-column contracts remain available in the explicit Table layout.
const render = (ui: ReactElement) => {
  const result = baseRender(ui);
  fireEvent.click(screen.getByRole("button", { name: "Table" }));
  return result;
};

const secondAutomation = (): Automation =>
  automationFixture({
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
          comparisons: [
            { value_pointer: "value", operator: "eq", operand: true },
          ],
        },
      ],
      steps: [
        {
          id: "dim",
          kind: "command",
          entity_id: ENTITY_ID,
          operation: "set",
          parameters: { value: 20 },
        },
        {
          id: "settle",
          kind: "command",
          entity_id: ENTITY_ID,
          operation: "set",
          parameters: { value: 40 },
        },
      ],
    },
  });

const renderAutomationsPage = () =>
  render(
    <MemoryRouter>
      <AutomationsPage />
    </MemoryRouter>
  );

/** Install the two-definition list every column assertion reads. */
const installTwoDefinitionFetch = () =>
  installAutomationFetch([
    {
      method: "GET",
      path: "/v1/automations",
      respond: () => ({
        body: { items: [automationFixture(), secondAutomation()] },
      }),
    },
  ]);

/** Install the delay-only and mixed definitions the Command-count assertions read. */
const installDelayCountFetch = () => {
  const delayOnly = delayDefinitionFixture();
  const mixed = { ...delayDefinitionFixture(), name: SECOND_AUTOMATION_NAME };
  mixed.steps.push(...automationFixture().definition.steps);
  return installAutomationFetch([
    {
      method: "GET",
      path: "/v1/automations",
      respond: () => ({
        body: {
          items: [
            automationFixture({ definition: delayOnly }),
            automationFixture({
              id: SECOND_AUTOMATION_ID,
              definition: mixed,
            }),
          ],
        },
      }),
    },
  ]);
};

/** The table row for a definition link, failing loudly when the row is absent. */
const rowForName = async (name: string) => {
  const link = await screen.findByRole("link", { name });
  const row = link.closest("tr");
  if (!row) {
    throw new Error(`the row for ${name} did not render`);
  }
  return { link, row };
};

describe(AutomationsPage, () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("counts delay-only definitions without counting waits as Commands", async () => {
    installDelayCountFetch();
    renderAutomationsPage();
    const row = await screen.findByRole("link", { name: AUTOMATION_NAME });
    const tableRow = row.closest("tr");
    if (!tableRow) {
      throw new Error("missing Automation row");
    }
    const cells = within(tableRow).getAllByRole("cell");
    expect(cells[4].textContent).toBe("6");
    expect(cells[5].textContent).toBe("0");
  });

  it("counts mixed definitions with waits as a single Command", async () => {
    installDelayCountFetch();
    renderAutomationsPage();
    const row = await screen.findByRole("link", {
      name: SECOND_AUTOMATION_NAME,
    });
    const tableRow = row.closest("tr");
    if (!tableRow) {
      throw new Error("missing Automation row");
    }
    const cells = within(tableRow).getAllByRole("cell");
    expect(cells[4].textContent).toBe("7");
    expect(cells[5].textContent).toBe("1");
  });

  it("lists a definition's name, enablement, and revision", async () => {
    installTwoDefinitionFetch();
    renderAutomationsPage();
    const { row } = await rowForName(AUTOMATION_NAME);
    const cells = within(row).getAllByRole("cell");
    expect(cells[0].textContent).toBe(AUTOMATION_NAME);
    expect(cells[1].textContent).toContain("enabled");
    expect(cells[2].textContent).toBe("3");
  });

  it("counts a definition's Triggers, defined Steps, and Commands", async () => {
    installTwoDefinitionFetch();
    renderAutomationsPage();
    const { row } = await rowForName(AUTOMATION_NAME);
    const cells = within(row).getAllByRole("cell");
    expect(cells[3].textContent).toBe("1");
    expect(cells[4].textContent).toBe("1");
    expect(cells[5].textContent).toBe("1");
  });

  it("shows a definition's update time and id", async () => {
    installTwoDefinitionFetch();
    renderAutomationsPage();
    const { row } = await rowForName(AUTOMATION_NAME);
    const cells = within(row).getAllByRole("cell");
    expect(cells[6].textContent).toBe("2026-02-01T09:30:00.000Z");
    expect(cells[7].textContent).toContain(AUTOMATION_ID);
  });

  it("shows a disabled definition's revision, Trigger, and Step counts", async () => {
    installTwoDefinitionFetch();
    renderAutomationsPage();
    const { row } = await rowForName(SECOND_AUTOMATION_NAME);
    const cells = within(row).getAllByRole("cell");
    expect(cells[1].textContent).toContain("disabled");
    expect(cells[2].textContent).toBe("7");
    // The second Automation has one Trigger and two Steps.
    expect(cells[3].textContent).toBe("1");
    expect(cells[4].textContent).toBe("2");
  });

  it("links each definition and keeps the raw list JSON available", async () => {
    installTwoDefinitionFetch();
    renderAutomationsPage();
    const { link } = await rowForName(AUTOMATION_NAME);
    expect(link.getAttribute("href")).toBe(`/automations/${AUTOMATION_ID}`);
    // The raw page stays available for debugging.
    expect(
      screen.getByRole("button", { name: "Raw list JSON" })
    ).not.toBeNull();
  });

  it("pages the ID-ascending keyset with an encoded cursor", async () => {
    const requests = installAutomationFetch([
      {
        method: "GET",
        path: "/v1/automations",
        respond: (request) =>
          new URL(request.url, "http://hearthd.test").searchParams.has("cursor")
            ? { body: { items: [secondAutomation()] } }
            : {
                body: {
                  items: [automationFixture()],
                  next_cursor: NEXT_CURSOR,
                },
              },
      },
    ]);

    renderAutomationsPage();

    await screen.findByRole("link", { name: AUTOMATION_NAME });
    expect(requests.map((request) => request.url)).toStrictEqual([
      "/v1/automations?limit=50",
    ]);

    fireEvent.click(screen.getByRole("button", { name: "Next page" }));

    await screen.findByRole("link", { name: SECOND_AUTOMATION_NAME });
    expect(requests.map((request) => request.url)).toStrictEqual([
      "/v1/automations?limit=50",
      // The cursor round-trips percent-encoded: `+` unencoded would arrive as a space.
      "/v1/automations?limit=50&cursor=page%2F1%2B%3D",
    ]);
    expect(screen.queryByRole("button", { name: "Next page" })).toBeNull();
  });

  it("counts branch nodes and every command arm rather than top-level Steps", async () => {
    installAutomationFetch([
      {
        method: "GET",
        path: "/v1/automations",
        respond: () => ({
          body: {
            items: [
              automationFixture({ definition: branchingDefinitionFixture() }),
            ],
          },
        }),
      },
    ]);
    renderAutomationsPage();
    const { row } = await rowForName(AUTOMATION_NAME);
    const cells = within(row).getAllByRole("cell");
    expect(cells[4].textContent).toBe("7");
    expect(cells[5].textContent).toBe("5");
  });

  it("reports a failed read instead of an empty list", async () => {
    installAutomationFetch([
      {
        method: "GET",
        path: "/v1/automations",
        respond: () => ({
          status: 503,
          body: { title: "Service Unavailable", detail: "closed" },
        }),
      },
    ]);

    renderAutomationsPage();

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("closed");
    expect(screen.queryByText("No automations.")).toBeNull();
  });
});
