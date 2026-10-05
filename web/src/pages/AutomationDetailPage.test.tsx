import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AutomationHistorySummary } from "../api/types.ts";
import {
  AUTOMATION_ID,
  AUTOMATION_NAME,
  AUTOMATION_REVISION,
  ENTITY_ID,
  ENTITY_NAME,
  RUN_ID,
  SKIP_ID,
  VERIFIED_COMMAND_ID,
  automationFixture,
  branchingDefinitionFixture,
  delayDefinitionFixture,
  interruptedBranchRunFixture,
  BRANCH_STATE_ENTITY_ID,
  UNSELECTED_ENTITY_ID,
  ADMISSION_ENTITY_ID,
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

    fireEvent.click(await screen.findByRole("button", { name: "Run now" }, { timeout: 5_000 }));

    // The outcome is the admitted Run itself, including its verified Command.
    expect(await screen.findByText("Step attempts")).not.toBeNull();
    expect(screen.getByText(VERIFIED_COMMAND_ID)).not.toBeNull();
    expect(screen.getByRole("link", { name: VERIFIED_COMMAND_ID }).getAttribute("href"))
      .toBe(`/commands?command_id=${VERIFIED_COMMAND_ID}`);
    expect(screen.getByText("No branch decisions recorded.")).not.toBeNull();
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
  it("preserves nested delay nodes exactly in enablement replacement", async () => {
    const definition = delayDefinitionFixture();
    const requests = installAutomationFetch(detailRoutes([
      { method: "GET", path: DEFINITION_PATH, respond: () => ({ body: automationFixture({ definition }) }) },
    ]));
    renderDetailPage();
    fireEvent.click(await screen.findByRole("switch"));
    await waitFor(() => expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1));
    expect(requestsFor(requests, "PUT", DEFINITION_PATH)[0].body).toEqual({
      expected_revision: AUTOMATION_REVISION,
      definition: { ...definition, enabled: false },
    });
  });

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
  it("renders delay-only definitions with exact durations and no command positions or Entity references", async () => {
    const definition = delayDefinitionFixture();
    definition.steps.push(
      { id: "second-wait", kind: "delay", duration_ms: 1000 },
      { id: "hour-wait", kind: "delay", duration_ms: 7200000 },
    );
    const requests = installAutomationFetch(detailRoutes([
      { method: "GET", path: DEFINITION_PATH, respond: () => ({ body: automationFixture({ definition }) }) },
    ]));
    renderDetailPage();
    expect(await screen.findByText("8 defined Steps · 0 commands")).not.toBeNull();
    expect(screen.getByText("Commands").nextElementSibling?.textContent).toBe("0");
    const outline = screen.getByRole("list", { name: "Step definition outline" });
    for (const text of ["wait · delay · 5 minutes", "precise-wait · delay · 1001 milliseconds", "short-wait · delay · 1 millisecond", "long-wait · delay · 1 day", "second-wait · delay · 1 second", "hour-wait · delay · 2 hours"]) {
      expect(within(outline).getAllByRole("listitem").map((row) => row.textContent)).toContain(text);
    }
    expect(within(outline).queryByText(/position/)).toBeNull();
    expect(screen.queryByRole("link")).toBeNull();
    expect(requestsFor(requests, "GET", "/v1/entities")).toHaveLength(0);
  });

  it.each([
    ["running", undefined],
    ["completed", undefined],
    ["interrupted", "core_stopping"],
    ["interrupted", "core_restarted"],
    ["interrupted", "executor_fault"],
  ] as const)("renders %s delay evidence with reason %s in reached order", async (status, failure_code) => {
    const started_at = "2026-10-04T12:00:00Z";
    const due_at = "2026-10-04T12:05:00Z";
    const completed_at = status === "running" ? undefined : "2026-10-04T12:04:59Z";
    const run = runFixture({
      status: status === "completed" ? "succeeded" : status,
      completed_at,
      failure_code,
      snapshot: delayDefinitionFixture(),
      steps: [],
      delays: [
        { position: 1, step_id: "precise-wait", duration_ms: 1001, status, started_at, due_at: "2026-10-04T12:00:01.001Z", completed_at, failure_code },
        { position: 0, step_id: "wait", duration_ms: 300000, status: "completed", started_at, due_at, completed_at: "2026-10-04T12:05:01Z" },
      ],
    });
    installAutomationFetch(detailRoutes([
      { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [runSummary()] } }) },
      { method: "GET", path: HISTORY_ENTRY_PATTERN, respond: () => ({ body: { kind: "run", run } }) },
    ]));
    renderDetailPage();
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    const table = await screen.findByRole("table", { name: "Delay executions" });
    expect(within(table).getAllByRole("columnheader").map((cell) => cell.textContent)).toEqual([
      "Reached position", "Step id", "Duration", "Status", "Started", "Expected due (diagnostic)", "Completed", "Interruption reason",
    ]);
    const rows = within(table).getAllByRole("row").slice(1);
    expect(within(rows[0]).getAllByRole("cell").map((cell) => cell.textContent)).toEqual([
      "0", "wait", "5 minutes", "completed", started_at, due_at, "2026-10-04T12:05:01Z", "—",
    ]);
    expect(within(rows[1]).getAllByRole("cell").map((cell) => cell.textContent)).toEqual([
      "1", "precise-wait", "1001 milliseconds", status, started_at, "2026-10-04T12:00:01.001Z", completed_at ?? "—", failure_code ?? "—",
    ]);
    expect(screen.getByText(/Expected due times are diagnostic UTC timestamps/)).not.toBeNull();
    expect(screen.getByText(/No wait resumes/)).not.toBeNull();
    expect(within(screen.getByRole("table", { name: "Command Step attempts" })).queryByRole("link")).toBeNull();
    const retained = screen.getAllByRole("list", { name: "Step definition outline" })[1];
    expect(within(retained).getAllByRole("listitem").map((row) => row.textContent)).toContain("precise-wait · delay · 1001 milliseconds");
  });

  it("says no delay was reached even when the snapshot defines delays", async () => {
    installAutomationFetch(detailRoutes([
      { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [runSummary()] } }) },
      { method: "GET", path: HISTORY_ENTRY_PATTERN, respond: () => ({ body: { kind: "run", run: runFixture({ snapshot: delayDefinitionFixture(), steps: [], delays: [] }) } }) },
    ]));
    renderDetailPage();
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    const table = await screen.findByRole("table", { name: "Delay executions" });
    expect(within(table).getByText("This Run reached no delay.")).not.toBeNull();
    expect(screen.getByText("6 defined Steps · 0 commands")).not.toBeNull();
  });

  it("keeps mixed nested command positions and links independent of delays", async () => {
    const definition = delayDefinitionFixture();
    const choose = definition.steps[1];
    if (choose.kind !== "choose" || !choose.default) throw new Error("missing default arm in fixture");
    choose.default.push({ id: "finish", entity_id: ENTITY_ID, operation: "set", parameters: { value: false } });
    definition.steps.unshift(automationFixture().definition.steps[0]);
    installAutomationFetch(detailRoutes([
      { method: "GET", path: DEFINITION_PATH, respond: () => ({ body: automationFixture({ definition }) }) },
    ]));
    renderDetailPage();
    expect(await screen.findByText("8 defined Steps · 2 commands")).not.toBeNull();
    const outline = screen.getByRole("list", { name: "Step definition outline" });
    const commands = within(outline).getAllByRole("listitem").filter((row) => row.textContent?.includes(" · command"));
    expect(commands.map((row) => row.textContent?.match(/^(.*?) · command · position (\d+)/)?.slice(1))).toEqual([["turn_on", "0"], ["finish", "1"]]);
    const links = await within(outline).findAllByRole("link", { name: ENTITY_NAME });
    expect(links).toHaveLength(2);
    expect(links.map((link) => link.getAttribute("href"))).toEqual([`/entities/${ENTITY_ID}`, `/entities/${ENTITY_ID}`]);
  });

  it("shows retained nested definitions and committed selections without claiming command completion", async () => {
    installAutomationFetch(detailRoutes([
      { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [{ ...runSummary(), status: "interrupted" }] } }) },
      { method: "GET", path: HISTORY_ENTRY_PATTERN, respond: () => ({ body: { kind: "run", run: interruptedBranchRunFixture() } }) },
      { method: "GET", path: "/v1/entities", respond: () => ({ body: { items: [
        entityFixture(),
        entityFixture({ id: BRANCH_STATE_ENTITY_ID, name: "Office brightness" }),
        entityFixture({ id: UNSELECTED_ENTITY_ID, name: "Unselected sensor" }),
        entityFixture({ id: ADMISSION_ENTITY_ID, name: "Admission readiness" }),
      ] } }) },
    ]));
    renderDetailPage();
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    const decisions = await screen.findByRole("table", { name: "Branch selection decisions" });
    const rows = within(decisions).getAllByRole("row").slice(1);
    expect(rows[0].textContent).toContain("0route-buttonAlternative press2026-10-03T12:00:00Z");
    expect(rows[1].textContent).toContain("1set-levelthen2026-10-03T12:00:00Z");
    expect(screen.getByText(/arm selection, not command completion/)).not.toBeNull();
    expect(screen.getByText("core_stopping")).not.toBeNull();

    const attempts = screen.getByRole("table", { name: "Command Step attempts" });
    expect(within(attempts).getAllByText("not_attempted")).toHaveLength(5);
    expect(within(attempts).queryByRole("link")).toBeNull();
    expect(within(attempts).queryByText("satisfied")).toBeNull();
    expect(screen.queryByText("succeeded")).toBeNull();

    const outlines = screen.getAllByRole("list", { name: "Step definition outline" });
    const retained = outlines[1];
    await within(retained).findByRole("link", { name: "Office brightness" });
    expect(within(retained).getByRole("link", { name: "Unselected sensor" }).getAttribute("href")).toBe(`/entities/${UNSELECTED_ENTITY_ID}`);
    expect(await screen.findByRole("link", { name: "Admission readiness" })).not.toBeNull();
    for (const arm of ["Then", "Else", "Alternative press", "Alternative other", "Default"]) {
      expect(within(retained).getByText(arm)).not.toBeNull();
    }
    const commandRows = within(retained).getAllByRole("listitem").filter((row) => row.textContent?.includes(" · command"));
    expect(commandRows.map((row) => row.textContent?.match(/^(.*?) · command · position (\d+)/)?.slice(1)))
      .toEqual([["dim", "0"], ["brighten", "1"], ["off", "2"], ["default-level", "3"], ["finish", "4"]]);
    expect(screen.getByText("7 defined Steps · 5 commands")).not.toBeNull();

    fireEvent.click(within(decisions).getByText("Evidence for set-level"));
    const evidence = within(decisions).getByText(/"selected_value": 120/);
    expect(evidence.closest("details")?.open).toBe(true);
    expect(evidence.textContent).toContain('"observed_at": "2026-10-03T11:59:58Z"');
    expect(evidence.textContent).toContain('"matched_trigger_ids"');
  });

  it("renders all nested current-definition references and defined counts", async () => {
    installAutomationFetch(detailRoutes([
      { method: "GET", path: DEFINITION_PATH, respond: () => ({ body: automationFixture({ definition: branchingDefinitionFixture() }) }) },
    ]));
    renderDetailPage();
    expect(await screen.findByText("7 defined Steps · 5 commands")).not.toBeNull();
    expect(screen.getByText("Defined Steps").nextElementSibling?.textContent).toBe("7");
    expect(screen.getByText("Commands").nextElementSibling?.textContent).toBe("5");
    const outline = screen.getByRole("list", { name: "Step definition outline" });
    expect(within(outline).getByRole("link", { name: UNSELECTED_ENTITY_ID })).not.toBeNull();
  });

  it.each(["run", "skip"] as const)("renders a cron expression and schedule %s without an empty Entity link", async (kind) => {
    const trigger = { id: "morning", kind: "cron" as const, expression: "0 7 * * MON-FRI" };
    const definition = { ...automationFixture().definition, triggers: [trigger] };
    const entryId = kind === "run" ? RUN_ID : SKIP_ID;
    installAutomationFetch(detailRoutes([
      { method: "GET", path: DEFINITION_PATH, respond: () => ({ body: automationFixture({ definition }) }) },
      { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [
        { ...(kind === "run" ? runSummary() : skipSummary()), source: "schedule" },
      ] } }) },
      { method: "GET", path: HISTORY_ENTRY_PATTERN, respond: () => ({ body: kind === "run"
        ? { kind, run: runFixture({ source: "schedule", matched_trigger_ids: ["morning"], snapshot: definition }) }
        : { kind, skip: skipFixture({ source: "schedule", fact: undefined, matched_triggers: [trigger] }) },
      }) },
    ]));
    renderDetailPage();

    expect(await screen.findByText(trigger.expression)).not.toBeNull();
    fireEvent.click(await screen.findByRole("button", { name: entryId }));
    expect(await screen.findByText("source: schedule")).not.toBeNull();
    expect(screen.getAllByText(trigger.expression)).toHaveLength(2);
    expect(screen.queryByText("Fact id")).toBeNull();
    // Steps still have their real Entity link. Cron must not add an empty one.
    for (const link of screen.getAllByRole("link")) {
      expect(link.getAttribute("href")).not.toBe("/entities/undefined");
      expect(link.getAttribute("href")).not.toBe("/entities/");
    }
    expect(screen.queryByText(/dispositions/)).toBeNull();
  });

  it("shows held-State duration and comparisons in the definition and matched Trigger", async () => {
    const heldTrigger = {
      id: "light_on",
      kind: "held_state" as const,
      entity_id: ENTITY_ID,
      comparisons: [{ value_pointer: "", operator: "eq" as const, operand: true }],
      for_seconds: 60,
    };
    installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: DEFINITION_PATH,
          respond: () => ({
            body: automationFixture({
              definition: { ...automationFixture().definition, triggers: [heldTrigger] },
            }),
          }),
        },
        { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [skipSummary()] } }) },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: () => ({
            body: { kind: "skip", skip: skipFixture({ matched_triggers: [heldTrigger] }) },
          }),
        },
      ]),
    );
    renderDetailPage();

    expect(await screen.findByText("60 seconds")).not.toBeNull();
    fireEvent.click(await screen.findByRole("button", { name: SKIP_ID }));
    await screen.findByText("Matched Triggers");
    expect(screen.getAllByText("60 seconds")).toHaveLength(2);
    expect(screen.getAllByText("eq true")).toHaveLength(2);
    expect(screen.queryByText(/dispositions/)).toBeNull();
  });

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

  it.each([
    ["conditions_false", "Conditions were false, so no Run started."],
    ["conditions_unknown", "Conditions could not be confirmed, so no Run started."],
  ] as const)("explains a held-State %s Skip without Fact evidence", async (reason, explanation) => {
    installAutomationFetch(
      detailRoutes([
        { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [skipSummary()] } }) },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: () => ({
            body: { kind: "skip", skip: skipFixture({ reason, fact: undefined }) },
          }),
        },
      ]),
    );
    renderDetailPage();

    fireEvent.click(await screen.findByRole("button", { name: SKIP_ID }));
    expect(await screen.findByText(explanation)).not.toBeNull();
    expect(screen.queryByText(/Fact that was already too old/)).toBeNull();
    expect(screen.queryByText("Fact id")).toBeNull();
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
