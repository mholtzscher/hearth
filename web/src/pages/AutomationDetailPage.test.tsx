import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AutomationHistorySummary } from "../api/types.ts";
import {
  AUTOMATION_ID,
  AUTOMATION_NAME,
  AUTOMATION_REVISION,
  ENTITY_NAME,
  RUN_ID,
  SKIP_ID,
  VERIFIED_COMMAND_ID,
  automationFixture,
  entityFixture,
  installAutomationFetch,
  problemResponse,
  requestsFor,
  runFixture,
  skipFixture,
} from "./automation-fetch-fake.ts";
import type { AutomationRoute } from "./automation-fetch-fake.ts";
import AutomationDetailPage from "./AutomationDetailPage.tsx";

/** Page route, distinct from the API paths the page reads. */
const DETAIL_ROUTE = `/automations/${AUTOMATION_ID}`;
const SECOND_AUTOMATION_ID = "aut_01920000-0000-7000-8000-00000000000c";
const SECOND_AUTOMATION_NAME = "Hallway light at dusk";
const DEFINITION_PATH = `/v1/automations/${AUTOMATION_ID}`;
const RUNS_PATH = `${DEFINITION_PATH}/runs`;
const HISTORY_PATH = `${DEFINITION_PATH}/history`;
/** One history entry read: the Run and Skip identities are the entry ids. */
const HISTORY_ENTRY_PATTERN = /\/history\/(arn_|ask_)[^/]+$/;
/** Opaque cursor holding `/`, `+` and `=`, like hearthd's own cursors. */
const NEXT_CURSOR = "page/1+=";

function runSummary(): AutomationHistorySummary {
  return {
    id: RUN_ID,
    kind: "run",
    automation_id: AUTOMATION_ID,
    automation_name: AUTOMATION_NAME,
    revision: AUTOMATION_REVISION,
    recorded_at: "2026-02-01T10:00:00.000Z",
    status: "succeeded",
  };
}

function skipSummary(): AutomationHistorySummary {
  return {
    id: SKIP_ID,
    kind: "skip",
    automation_id: AUTOMATION_ID,
    automation_name: AUTOMATION_NAME,
    revision: AUTOMATION_REVISION,
    recorded_at: "2026-02-01T10:05:00.050Z",
    reason: "automation_busy",
  };
}

/** Routes for a detail page: the definition read, the newest history page, and
    the Entities read the page uses to label Entity references. A test's
    overrides come first, so repeating a method and path replaces the default. */
function detailRoutes(overrides: AutomationRoute[] = []): AutomationRoute[] {
  return [
    ...overrides,
    { method: "GET", path: DEFINITION_PATH, respond: () => ({ body: automationFixture() }) },
    {
      method: "PUT",
      path: DEFINITION_PATH,
      respond: () => ({ body: automationFixture({ revision: AUTOMATION_REVISION + 1 }) }),
    },
    { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [] } }) },
    { method: "GET", path: "/v1/entities", respond: () => ({ body: { items: [entityFixture()] } }) },
  ];
}

function renderDetailPage() {
  return render(
    <MemoryRouter initialEntries={[DETAIL_ROUTE]}>
      <Routes>
        <Route path="/automations/:automationId" element={<AutomationDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

/** The detail route plus a navigation button, so a test can change the route
    parameter while the page stays mounted. */
function RouteChangeHarness() {
  const navigate = useNavigate();
  return (
    <>
      <button type="button" onClick={() => navigate(`/automations/${SECOND_AUTOMATION_ID}`)}>
        Open other automation
      </button>
      <Routes>
        <Route path="/automations/:automationId" element={<AutomationDetailPage />} />
      </Routes>
    </>
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("AutomationDetailPage manual Run", () => {
  it("starts exactly one Run, shows its outcome, and restarts history", async () => {
    const requests = installAutomationFetch(
      detailRoutes([
        { method: "POST", path: RUNS_PATH, respond: () => ({ status: 202, body: runFixture() }) },
      ]),
    );
    renderDetailPage();

    fireEvent.click(await screen.findByRole("button", { name: "Run now" }));

    // The outcome is the admitted Run itself, including its verified Command.
    expect(await screen.findByText("Step attempts")).not.toBeNull();
    expect(screen.getByText(VERIFIED_COMMAND_ID)).not.toBeNull();
    expect(screen.getAllByText(/succeeded/).length).toBeGreaterThan(0);

    const posts = requestsFor(requests, "POST", RUNS_PATH);
    expect(posts).toHaveLength(1);
    expect(posts[0].body).toBeNull();
    // The new Run is the newest history row, so history restarts at page one.
    expect(requestsFor(requests, "GET", HISTORY_PATH)).toHaveLength(2);
  });

  it("reports a busy conflict instead of retrying the POST", async () => {
    const requests = installAutomationFetch(
      detailRoutes([
        {
          method: "POST",
          path: RUNS_PATH,
          respond: () =>
            problemResponse(409, "automation_busy", "automation already has a running run"),
        },
      ]),
    );
    renderDetailPage();

    fireEvent.click(await screen.findByRole("button", { name: "Run now" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("automation already has a running run");
    expect(screen.getByText(/409 automation_busy/)).not.toBeNull();
    expect(screen.getByText(/not retried/)).not.toBeNull();
    // Non-idempotent: an ambiguous or rejected attempt is never repeated.
    expect(requestsFor(requests, "POST", RUNS_PATH)).toHaveLength(1);
    expect(screen.queryByText("Step attempts")).toBeNull();
  });
});

describe("AutomationDetailPage enablement", () => {
  it("replaces the definition with its expected revision when toggled", async () => {
    const requests = installAutomationFetch(detailRoutes());
    renderDetailPage();

    fireEvent.click(await screen.findByRole("switch"));

    await waitFor(() =>
      expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1),
    );
    const put = requestsFor(requests, "PUT", DEFINITION_PATH)[0];
    expect(put.body).toEqual({
      expected_revision: AUTOMATION_REVISION,
      definition: { ...automationFixture().definition, enabled: false },
    });
    // The reloaded definition, not the optimistic click, is the truth shown.
    await waitFor(() => expect(requestsFor(requests, "GET", DEFINITION_PATH)).toHaveLength(2));
  });

  it("reports a revision conflict and reloads instead of overwriting", async () => {
    let definitionReads = 0;
    const requests = installAutomationFetch(
      detailRoutes([
        {
          method: "PUT",
          path: DEFINITION_PATH,
          respond: () => problemResponse(409, "revision_conflict", "automation revision conflict"),
        },
        {
          method: "GET",
          path: DEFINITION_PATH,
          respond: () => {
            definitionReads += 1;
            // Another writer bumped the revision before this click landed.
            return { body: automationFixture({ revision: definitionReads === 1 ? 3 : 4 }) };
          },
        },
      ]),
    );
    renderDetailPage();

    fireEvent.click(await screen.findByRole("switch"));

    const notice = await screen.findByRole("alert");
    expect(notice.textContent).toContain("Revision conflict");
    expect(notice.textContent).toContain("not changed");
    expect(requestsFor(requests, "PUT", DEFINITION_PATH)[0].body).toEqual({
      expected_revision: 3,
      definition: { ...automationFixture().definition, enabled: false },
    });
    // Reloaded, not overwritten: the current revision and enablement are shown.
    await waitFor(() => expect(definitionReads).toBe(2));
    expect(screen.getByRole("switch").getAttribute("aria-checked")).toBe("true");
  });

  it("keeps the switch disabled through a pending authoritative reload so no stale revision is resubmitted", async () => {
    // The page reads the definition once on mount and again after the PUT. The
    // second read is the authoritative reload: hold it open to show the switch
    // stays disabled and a second click cannot resubmit the stale revision.
    let definitionReads = 0;
    const requests = installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: DEFINITION_PATH,
          respond: () => {
            definitionReads += 1;
            return {
              body: automationFixture({
                revision: definitionReads === 1 ? AUTOMATION_REVISION : AUTOMATION_REVISION + 1,
                definition: {
                  ...automationFixture().definition,
                  enabled: definitionReads === 1,
                },
              }),
            };
          },
        },
      ]),
    );
    // Gate the reload (the definition's second read) behind a promise the test
    // releases, so the in-flight window is deterministic instead of timing-based.
    const servedFetch = globalThis.fetch;
    let releaseReload: () => void = () => {};
    const reloadGate = new Promise<void>((resolve) => {
      releaseReload = resolve;
    });
    let gatedReads = 0;
    vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
      const method = (init?.method ?? "GET").toUpperCase();
      const path = new URL(String(input), "http://hearthd.test").pathname;
      if (method === "GET" && path === DEFINITION_PATH) {
        gatedReads += 1;
        if (gatedReads === 2) await reloadGate;
      }
      return servedFetch(input, init);
    });

    renderDetailPage();

    const toggle = await screen.findByRole("switch");
    expect(toggle.getAttribute("aria-checked")).toBe("true");

    fireEvent.click(toggle);
    await waitFor(() => expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1));
    // The reload started but has not answered: the switch must stay disabled.
    await waitFor(() => expect(gatedReads).toBe(2));
    expect(toggle.hasAttribute("disabled")).toBe(true);

    // A second click while the reload is pending must not resubmit.
    fireEvent.click(toggle);
    expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1);

    releaseReload();
    // The reloaded definition is the truth shown, and the switch works again.
    await waitFor(() => expect(toggle.getAttribute("aria-checked")).toBe("false"));
    expect(toggle.hasAttribute("disabled")).toBe(false);
  });
});

describe("AutomationDetailPage scope changes", () => {
  it("drops the previous Automation's manual-Run outcome when the route changes", async () => {
    installAutomationFetch([
      { method: "POST", path: RUNS_PATH, respond: () => ({ status: 202, body: runFixture() }) },
      { method: "GET", path: /\/history$/, respond: () => ({ body: { items: [] } }) },
      {
        method: "GET",
        path: `/v1/automations/${SECOND_AUTOMATION_ID}`,
        respond: () => ({
          body: automationFixture({
            id: SECOND_AUTOMATION_ID,
            revision: 1,
            definition: { ...automationFixture().definition, name: SECOND_AUTOMATION_NAME },
          }),
        }),
      },
      ...detailRoutes(),
    ]);

    render(
      <MemoryRouter initialEntries={[DETAIL_ROUTE]}>
        <RouteChangeHarness />
      </MemoryRouter>,
    );

    fireEvent.click(await screen.findByRole("button", { name: "Run now" }));
    await screen.findByText("Step attempts");

    fireEvent.click(screen.getByRole("button", { name: "Open other automation" }));

    expect((await screen.findAllByText(SECOND_AUTOMATION_NAME)).length).toBeGreaterThan(0);
    // The admitted Run belonged to the previous Automation, so it is not shown here.
    expect(screen.queryByText("Step attempts")).toBeNull();
  });
});

describe("AutomationDetailPage history", () => {
  it("pages with an encoded cursor and loads a selected Run's step attempts", async () => {
    const requests = installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: HISTORY_PATH,
          respond: (request) =>
            request.url.includes("cursor=")
              ? { body: { items: [skipSummary()] } }
              : { body: { items: [runSummary()], next_cursor: NEXT_CURSOR } },
        },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: (request) =>
            request.path.endsWith(RUN_ID)
              ? { body: { kind: "run", run: runFixture() } }
              : { body: { kind: "skip", skip: skipFixture() } },
        },
      ]),
    );
    renderDetailPage();

    // Selecting an entry loads its recorded detail in place, with no new page route.
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    await screen.findByText("Step attempts");
    expect(requestsFor(requests, "GET", `${HISTORY_PATH}/${RUN_ID}`)).toHaveLength(1);
    expect(screen.getByText(VERIFIED_COMMAND_ID)).not.toBeNull();
    expect(screen.getAllByText(/satisfied/).length).toBeGreaterThan(0);
    // A Trigger reference is labeled with the resolved Entity name.
    expect(screen.getAllByText(ENTITY_NAME).length).toBeGreaterThan(0);

    fireEvent.click(screen.getByRole("button", { name: "Next page" }));

    await screen.findByRole("button", { name: SKIP_ID });
    expect(requestsFor(requests, "GET", HISTORY_PATH).map((request) => request.url)).toEqual([
      `${HISTORY_PATH}?limit=50`,
      // The cursor round-trips percent-encoded: `+` unencoded would arrive as a space.
      `${HISTORY_PATH}?limit=50&cursor=page%2F1%2B%3D`,
    ]);
    expect(screen.queryByRole("button", { name: RUN_ID })).toBeNull();
  });

  it("renders a selected Skip's reason, Fact, and matched Trigger snapshots", async () => {
    const requests = installAutomationFetch(
      detailRoutes([
        { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [skipSummary()] } }) },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: () => ({ body: { kind: "skip", skip: skipFixture() } }),
        },
      ]),
    );
    renderDetailPage();

    // Nothing is fetched for an entry until one is selected.
    expect(requestsFor(requests, "GET", `${HISTORY_PATH}/${SKIP_ID}`)).toHaveLength(0);

    fireEvent.click(await screen.findByRole("button", { name: SKIP_ID }));

    await screen.findByText("Matched Triggers");
    expect(requestsFor(requests, "GET", `${HISTORY_PATH}/${SKIP_ID}`)).toHaveLength(1);
    expect(screen.getByText(/already had a running Run/)).not.toBeNull();
    expect(screen.getAllByText(/automation_busy/).length).toBeGreaterThan(0);
    expect(screen.getAllByText("single_press").length).toBeGreaterThan(0);
    expect(screen.getByText(/an Entity Event Fact carries no value/)).not.toBeNull();
  });

  it("refetches the selected entry's detail when history is refreshed", async () => {
    let entryReads = 0;
    const requests = installAutomationFetch(
      detailRoutes([
        { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [runSummary()] } }) },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: () => {
            entryReads += 1;
            // The Run is still executing on the first read and has finished by
            // the second, like a real Run interleaved with a page refresh.
            return entryReads === 1
              ? {
                  body: {
                    kind: "run",
                    run: runFixture({
                      status: "running",
                      steps: [{ position: 0, step_id: "turn_on", status: "running" }],
                    }),
                  },
                }
              : { body: { kind: "run", run: runFixture() } };
          },
        },
      ]),
    );
    renderDetailPage();

    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    await screen.findByText("Step attempts");
    // First read: the Step is still running and has no verified Command yet.
    expect(screen.queryByText(VERIFIED_COMMAND_ID)).toBeNull();
    expect(screen.getAllByText("running").length).toBeGreaterThan(0);

    fireEvent.click(screen.getByRole("button", { name: "Refresh history" }));

    // The summaries and the selected entry's detail are both refetched, so the
    // visible Step status is the latest one instead of the stale first read.
    await screen.findByText(VERIFIED_COMMAND_ID);
    expect(requestsFor(requests, "GET", HISTORY_PATH)).toHaveLength(2);
    expect(requestsFor(requests, "GET", `${HISTORY_PATH}/${RUN_ID}`)).toHaveLength(2);
    expect(entryReads).toBe(2);
    expect(screen.queryByText("running")).toBeNull();
  });

  it("clears the selected entry when moving to another history page", async () => {
    const requests = installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: HISTORY_PATH,
          respond: (request) =>
            request.url.includes("cursor=")
              ? { body: { items: [skipSummary()] } }
              : { body: { items: [runSummary()], next_cursor: NEXT_CURSOR } },
        },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: () => ({ body: { kind: "run", run: runFixture() } }),
        },
      ]),
    );
    renderDetailPage();

    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    await screen.findByText("Step attempts");

    fireEvent.click(screen.getByRole("button", { name: "Next page" }));

    await screen.findByRole("button", { name: SKIP_ID });
    // The selection belonged to the page being left, so its detail is dropped
    // instead of staying visible under the new page's rows.
    expect(screen.queryByText("Step attempts")).toBeNull();
    expect(screen.queryByText(VERIFIED_COMMAND_ID)).toBeNull();
    expect(requestsFor(requests, "GET", `${HISTORY_PATH}/${RUN_ID}`)).toHaveLength(1);
  });

  it("shows retained history when the current definition is gone", async () => {
    const requests = installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: DEFINITION_PATH,
          respond: () => problemResponse(404, "not_found", "automation not found"),
        },
        { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [skipSummary()] } }) },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: () => ({ body: { kind: "skip", skip: skipFixture() } }),
        },
      ]),
    );
    renderDetailPage();

    // The definition read failed and is reported, but history is read from its
    // own endpoint, which the backend keeps serving for a deleted Automation.
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("automation not found");

    fireEvent.click(await screen.findByRole("button", { name: SKIP_ID }));
    await screen.findByText("Matched Triggers");
    expect(screen.getByText(/already had a running Run/)).not.toBeNull();

    // Views driven only by the current definition stay gated on it.
    expect(screen.queryByText("Triggers")).toBeNull();
    expect(screen.queryByText("Steps")).toBeNull();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByRole("button", { name: "Run now" })).toBeNull();
    expect(screen.queryByText("Raw automation JSON")).toBeNull();
    expect(requestsFor(requests, "GET", DEFINITION_PATH)).toHaveLength(1);
  });

  it("hides the current definition's controls after a refresh reports it gone, keeping history", async () => {
    let definitionReads = 0;
    const requests = installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: DEFINITION_PATH,
          respond: () => {
            definitionReads += 1;
            // The first read succeeds; a manual refresh later finds it deleted.
            // `useApi` keeps the previous data on error, so the page must treat
            // the retained definition as absent instead of rendering it.
            return definitionReads === 1
              ? { body: automationFixture() }
              : problemResponse(404, "not_found", "automation not found");
          },
        },
        { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [runSummary()] } }) },
      ]),
    );
    renderDetailPage();

    // The definition loaded, so its controls are actionable.
    expect(await screen.findByRole("switch")).not.toBeNull();
    expect(screen.getByRole("button", { name: "Run now" })).not.toBeNull();
    expect(screen.getByText("Raw automation JSON")).not.toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));

    // The refresh reports the Automation gone: the retained definition must not
    // keep rendering its header, definition, actions, or raw JSON.
    await waitFor(() => expect(definitionReads).toBe(2));
    await waitFor(() => expect(screen.queryByRole("switch")).toBeNull());
    expect(screen.queryByRole("button", { name: "Run now" })).toBeNull();
    expect(screen.queryByText("Triggers")).toBeNull();
    expect(screen.queryByText("Steps")).toBeNull();
    expect(screen.queryByText("Raw automation JSON")).toBeNull();
    // History is read from its own endpoint and stays independently available.
    expect(await screen.findByRole("button", { name: RUN_ID })).not.toBeNull();
    expect(requestsFor(requests, "GET", DEFINITION_PATH)).toHaveLength(2);
  });
});
