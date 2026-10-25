import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AutomationConditionDecision, AutomationDefinition, AutomationFunctionExecution, AutomationFunctionStep, AutomationHistorySummary, AutomationNotificationExecution, AutomationRunDataRecord } from "../api/types.ts";
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
import NotificationsPage from "./NotificationsPage.tsx";

const FUNCTION_FINGERPRINT = `sha256-v1:${"a".repeat(64)}`;

it("labels dynamic bindings while keeping targets static and links every State capture", async () => {
  const definition: AutomationDefinition = { name: "Value flow", enabled: true, triggers: [], variables: { target: { expr: "run.revision" } }, steps: [
    { id: "capture", kind: "read_state", entity_id: ENTITY_ID, value_pointer: "/temperature", max_age_seconds: 300, on_unusable: "null" },
    { id: "assign", kind: "set_variables", assignments: { target: { expr: "steps.capture.output ?? 20" } } },
    { id: "branch", kind: "if", conditions: { id: "expression-check", kind: "expression", value: { expr: "vars.target > 25" } }, then: [
      { id: "dynamic-command", kind: "command", entity_id: UNSELECTED_ENTITY_ID, operation: "set", parameters: { expr: "{value: vars.target}" } },
    ], else: [{ id: "fallback-capture", kind: "read_state", entity_id: BRANCH_STATE_ENTITY_ID, value_pointer: "" }] },
    { ...functionStep("dynamic-function"), arguments: { expr: "{target: vars.target}" } },
    { id: "dynamic-notification", kind: "notification", destination_id: "ndst_static", title: { literal: "Target" }, message: { expr: "string(vars.target)" } },
  ] };
  const requests = installAutomationFetch(detailRoutes([{ method: "GET", path: DEFINITION_PATH, respond: () => ({ body: automationFixture({ definition }) }) }]));
  renderDetailPage();
  expect((await screen.findByLabelText("Parameters for dynamic-command")).textContent).toContain('"expr": "{value: vars.target}"');
  expect(screen.getAllByText(/Expression \(resolved when reached\)/)).toHaveLength(6);
  expect(screen.getByText("Title for dynamic-notification: Literal")).not.toBeNull();
  expect(screen.getByText("Initial variables")).not.toBeNull();
  expect(screen.getByText("Assignments for assign")).not.toBeNull();
  const outline = screen.getByRole("list", { name: "Step definition outline" });
  expect(within(outline).getByText("capture").closest("li")?.textContent).toContain("/temperature · max age 300s · on unusable: null");
  expect(within(outline).getByText("fallback-capture").closest("li")?.textContent).toContain("(root) · on unusable: fail");
  for (const id of [ENTITY_ID, BRANCH_STATE_ENTITY_ID, UNSELECTED_ENTITY_ID]) expect(within(outline).getByTitle(id).getAttribute("href")).toBe(`/entities/${id}`);
  expect(screen.queryByRole("textbox")).toBeNull();
  expect(requests.map((request) => request.path)).toEqual([DEFINITION_PATH, HISTORY_PATH, "/v1/entities"]);
});

it("loads only detail evidence and shows all six complete records in journal order with exact scalar and nested values", async () => {
  const recorded_at = "2026-10-25T12:00:00Z";
  const snapshot: AutomationDefinition = { name: "Retained value flow", enabled: true, triggers: [], variables: { target: { literal: 20 } }, steps: [
    { id: "assign", kind: "set_variables", assignments: { target: { literal: 30 } } },
    { id: "capture", kind: "read_state", entity_id: ENTITY_ID, value_pointer: "" },
    { ...functionStep("function"), arguments: { literal: { target: 30 } } },
    { id: "branch", kind: "if", conditions: { id: "expression-check", kind: "expression", value: { expr: "vars.target == 30" } }, then: [
      { id: "failure", kind: "command", entity_id: ENTITY_ID, operation: "set", parameters: { expr: "vars.missing" } },
    ] },
  ] };
  const records: AutomationRunDataRecord[] = [
    { sequence: 0, recorded_at, kind: "variables_initialized", values: { target: 20 } },
    { sequence: 1, recorded_at, step_id: "assign", kind: "variables_assigned", values: { target: 30 } },
    { sequence: 2, recorded_at, step_id: "capture", kind: "state_captured", entity_id: ENTITY_ID, value_pointer: "", read_at: recorded_at, observation: { observation_id: "obs_exact", observed_at: recorded_at }, output: null },
    { sequence: 3, recorded_at, step_id: "function", kind: "inputs_resolved", values: { arguments: { target: 30 } } },
    { sequence: 4, recorded_at, step_id: "function", kind: "function_output", output: null },
    { sequence: 5, recorded_at, step_id: "failure", kind: "failed", failure_code: "expression_missing", path: "/steps/3/then/0/parameters", source_start: 0, source_end: 12 },
  ];
  const dataSource = JSON.stringify([...records].reverse()).replace('"kind":"function_output","output":null', '"kind":"function_output","output":{"nested":[9007199254740993,-9223372036854775808,9223372036854775807]}').replace('"output":null', '"output":9007199254740993');
  installAutomationFetch(detailRoutes([{ method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [runSummary()] } }) }]));
  const serve = globalThis.fetch;
  const detailWire = JSON.stringify({ kind: "run", run: runFixture({ status: "failed", failure_code: "expression_missing", started_at: recorded_at, completed_at: recorded_at, snapshot,
    steps: [{ position: 0, step_id: "failure", status: "not_attempted" }],
    functions: [{ position: 0, step_id: "function", invocation_id: "afi_retained", status: "succeeded", started_at: recorded_at, completed_at: recorded_at, catalog_revision: "cat_retained", invocation_outcome: "succeeded" }],
    branch_decisions: [{ position: 0, step_id: "branch", kind: "if", outcome: "then", evaluated_at: recorded_at, evaluations: [{ evaluation: { evaluated_at: recorded_at, result: "true", nodes: [{ id: "expression-check", kind: "expression", result: "true", value: true }] } }] }],
  }) }).replace('"data":[]', `"data":${dataSource}`);
  vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => String(input) === `${HISTORY_PATH}/${RUN_ID}` ? new Response(detailWire) : serve(input, init));
  renderDetailPage();
  const select = await screen.findByRole("button", { name: RUN_ID });
  expect(screen.queryByRole("table", { name: "Run value evidence" })).toBeNull();
  fireEvent.click(select);
  const table = await screen.findByRole("table", { name: "Run value evidence" });
  const rows = within(table).getAllByRole("row").slice(1);
  expect(rows.map((row) => within(row).getAllByRole("cell")[0].textContent)).toEqual(["0", "1", "2", "3", "4", "5"]);
  expect(rows.map((row) => within(row).getAllByRole("cell")[2].textContent)).toEqual(records.map((record) => record.kind));
  expect(within(rows[0]).getAllByRole("cell")[1].textContent).toBe("Initialization");
  expect(within(rows[5]).getAllByRole("cell")[4].textContent).toBe("expression_missing/steps/3/then/0/parameters");
  for (const [index, row] of rows.entries()) {
    const summary = within(row).getByText(`Exact JSON for record ${index}`);
    fireEvent.click(summary);
    const exact = summary.parentElement?.querySelector("pre")?.textContent;
    expect(exact).toContain(`"sequence": ${index}`);
    expect(exact).toContain(`"kind": "${records[index].kind}"`);
    expect(exact).toContain(`"recorded_at": "${recorded_at}"`);
  }
  expect(rows[2].querySelector("pre")?.textContent).toContain('"output": 9007199254740993');
  for (const token of ["9007199254740993", "-9223372036854775808", "9223372036854775807"]) expect(rows[4].querySelector("pre")?.textContent).toContain(token);
  expect(rows[5].querySelector("pre")?.textContent).toContain('"source_end": 12');
  expect(screen.getByText("expression-check: true · expression value true")).not.toBeNull();
});

it("labels legacy Runs without inventing value evidence", async () => {
  installAutomationFetch(detailRoutes([
    { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [runSummary()] } }) },
    { method: "GET", path: HISTORY_ENTRY_PATTERN, respond: () => ({ body: { kind: "run", run: runFixture({ data_evidence: "legacy_unavailable", data: [] }) } }) },
  ]));
  renderDetailPage();
  fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
  expect(await screen.findByText("Value evidence unavailable (created before expression support)")).not.toBeNull();
  expect(screen.queryByRole("table", { name: "Run value evidence" })).toBeNull();
});

function functionStep(id: string): AutomationFunctionStep {
  return { id, kind: "function", adapter_id: "zigbee2mqtt", function_key: "refresh_entity", contract_fingerprint: FUNCTION_FINGERPRINT, arguments: { literal: { entity_id: "opaque-vendor-id", options: { force: true }, values: [null, 42] } } };
}

function functionDefinition(): AutomationDefinition {
  return { name: "Refresh weather", enabled: true, triggers: [
    { id: "button", kind: "cron", expression: "0 7 * * *" },
    { id: "other", kind: "cron", expression: "0 8 * * *" },
  ], steps: [
    functionStep("refresh"),
    { id: "route", kind: "if", conditions: { id: "matched", kind: "trigger", trigger_ids: ["button"] }, then: [functionStep("selected")], else: [
      { id: "alternative", kind: "choose", branches: [{ id: "other", conditions: { id: "other-match", kind: "trigger", trigger_ids: ["other"] }, steps: [functionStep("unselected")] }], default: [functionStep("fallback")] },
    ] },
  ] };
}

describe("Automation Function Steps", () => {
  it("preserves literal Function argument numbers in display and the enablement PUT", async () => {
    const rawArguments = String.raw`{"literal":{"large":9007199254740993,"min":-9223372036854775808,"max":9223372036854775807,"decimal":0.123456789012345678901,"nested":[1E+003,{"tiny":1e-999,"negative":-0,"text":"9007199254740993 \" [1e3] \u0031"}]}}`;
    const wire = `{"id":"${AUTOMATION_ID}","revision":3,"created_at":"2026-10-23T10:00:00Z","updated_at":"2026-10-23T10:00:00Z","definition":{"name":"Exact arguments","enabled":true,"triggers":[],"steps":[{"id":"exact","kind":"function","adapter_id":"example","function_key":"refresh","contract_fingerprint":"${FUNCTION_FINGERPRINT}","arguments":${rawArguments}}]}}`;
    const puts: string[] = [];
    const requests = installAutomationFetch(detailRoutes());
    const serve = globalThis.fetch;
    vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === DEFINITION_PATH) {
        if (init?.method === "PUT") puts.push(String(init.body));
        return new Response(wire, { headers: { "content-type": "application/json" } });
      }
      return serve(input, init);
    });
    renderDetailPage();
    const args = await screen.findByLabelText("Arguments for exact");
    // Check the outgoing wire before the display assertion so the original
    // data-corrupting PUT (not only its rounded display) is a regression failure.
    fireEvent.click(screen.getByRole("switch", { name: "Enabled" }));
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0]).toContain(`"arguments":${rawArguments}`);
    expect(puts[0]).toContain('"enabled":false');
    expect(args.textContent).toContain('"large": 9007199254740993');
    expect(args.textContent).toContain('"min": -9223372036854775808');
    expect(args.textContent).toContain('"max": 9223372036854775807');
    expect(args.textContent).toContain('"decimal": 0.123456789012345678901');
    expect(args.textContent).toContain("1E+003");
    expect(args.textContent).toContain('"tiny": 1e-999');
    expect(args.textContent).toContain('"negative": -0');
    expect(requests.map((request) => request.path)).toEqual([HISTORY_PATH]);
  });

  it("shows every defined arm, fingerprint and opaque arguments without discovery, and preserves them on enablement changes", async () => {
    const definition = functionDefinition();
    const requests = installAutomationFetch(detailRoutes([
      { method: "GET", path: DEFINITION_PATH, respond: () => ({ body: automationFixture({ definition }) }) },
    ]));
    renderDetailPage();
    await screen.findByText("6 defined Steps · 0 commands · 4 functions");
    expect(screen.getByText("Functions").nextElementSibling?.textContent).toBe("4");
    const outline = screen.getByRole("list", { name: "Step definition outline" });
    for (const id of ["refresh", "selected", "unselected", "fallback"]) {
      expect(within(outline).getByText(id).closest("li")?.textContent).toContain("zigbee2mqtt / refresh_entity");
      expect(within(outline).getByLabelText(`Arguments for ${id}`).textContent).toBe(JSON.stringify(functionStep(id).arguments, null, 2));
    }
    expect(within(outline).getAllByText(`Contract fingerprint: ${FUNCTION_FINGERPRINT}`)).toHaveLength(4);
    expect(screen.getByText(/Function Steps are read-only here/).textContent).toContain("API/MCP");
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(requests.map((request) => [request.method, request.path])).toEqual([["GET", DEFINITION_PATH], ["GET", HISTORY_PATH]]);
    fireEvent.click(screen.getByRole("switch", { name: "Enabled" }));
    await waitFor(() => expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1));
    expect(requestsFor(requests, "PUT", DEFINITION_PATH)[0].body).toEqual({ expected_revision: AUTOMATION_REVISION, definition: { ...definition, enabled: false } });
  });

  const started_at = "2026-10-23T10:00:00Z";
  const completed_at = "2026-10-23T10:00:01Z";
  const identity = { position: 0, step_id: "refresh", invocation_id: "afi_reserved", started_at };
  const evidence: AutomationFunctionExecution[] = [
    { ...identity, status: "running" },
    { ...identity, status: "succeeded", completed_at, catalog_revision: "cat_retained", invocation_outcome: "succeeded" },
    { ...identity, status: "failed", completed_at, catalog_revision: "cat_retained", invocation_outcome: "failed", reason_code: "adapter_error" },
    { ...identity, status: "failed", completed_at, catalog_revision: "cat_retained", invocation_outcome: "not_executed", reason_code: "invalid_arguments" },
    { ...identity, status: "failed", completed_at, catalog_revision: "cat_retained", invocation_outcome: "unknown", reason_code: "outcome_timeout" },
    { ...identity, status: "failed", completed_at, reason_code: "function_contract_changed" },
    { ...identity, status: "interrupted", completed_at, reason_code: "core_restarted" },
    { ...identity, status: "interrupted", completed_at, reason_code: "executor_fault" },
    { ...identity, status: "interrupted", completed_at, reason_code: "core_stopping" },
  ];
  it.each(evidence)("shows retained $status / $invocation_outcome / $reason_code evidence without treating a reservation as dispatch", async (execution) => {
    const snapshot = functionDefinition();
    const requests = installAutomationFetch(detailRoutes([
      { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [runSummary()] } }) },
      { method: "GET", path: HISTORY_ENTRY_PATTERN, respond: () => ({ body: { kind: "run", run: runFixture({ status: execution.status, snapshot, steps: [], functions: [execution] }) } }) },
    ]));
    renderDetailPage();
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    const table = await screen.findByRole("table", { name: "Function Step executions" });
    const row = within(table).getAllByRole("row")[1];
    const cells = within(row).getAllByRole("cell").map((cell) => cell.textContent);
    expect(cells.slice(0, 4)).toEqual(["0", "refresh", execution.status, "afi_reserved"]);
    expect(cells[4]).toContain(execution.invocation_outcome ?? "— (no terminal invocation evidence)");
    expect(cells.slice(5)).toEqual([execution.catalog_revision ?? "—", execution.reason_code ?? "—", started_at, execution.completed_at ?? "—"]);
    if (execution.invocation_outcome === "unknown") expect(cells[4]).toContain("Effects may have occurred; success is unproven");
    expect(screen.getByText(/Invocation IDs are reserved before effects/).textContent).toContain("do not prove admission or dispatch");
    expect(screen.getByText("Not attempted (no reservation): selected, unselected, fallback").textContent).not.toContain("refresh");
    const retained = screen.getAllByRole("list", { name: "Step definition outline" })[1];
    expect(within(retained).getByLabelText("Arguments for unselected").textContent).toContain('"entity_id": "opaque-vendor-id"');
    expect(within(table).queryByRole("link")).toBeNull();
    expect(requests.map((request) => [request.method, request.path])).toEqual([
      ["GET", DEFINITION_PATH], ["GET", HISTORY_PATH], ["GET", "/v1/entities"], ["GET", `${HISTORY_PATH}/${RUN_ID}`],
    ]);
  });

  it("keeps reached Function order independent of definition and Command positions", async () => {
    const snapshot = functionDefinition();
    snapshot.steps.unshift(automationFixture().definition.steps[0]);
    const succeeded = { status: "succeeded" as const, completed_at, catalog_revision: "cat_retained", invocation_outcome: "succeeded" as const };
    installAutomationFetch(detailRoutes([
      { method: "GET", path: DEFINITION_PATH, respond: () => ({ body: automationFixture({ definition: snapshot }) }) },
      { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [runSummary()] } }) },
      { method: "GET", path: HISTORY_ENTRY_PATTERN, respond: () => ({ body: { kind: "run", run: runFixture({ snapshot, functions: [
        { ...identity, ...succeeded, position: 1, step_id: "selected", invocation_id: "afi_second" },
        { ...identity, ...succeeded },
      ] }) } }) },
    ]));
    renderDetailPage();
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    const table = await screen.findByRole("table", { name: "Function Step executions" });
    expect(within(table).getAllByRole("row").slice(1).map((row) => within(row).getAllByRole("cell").slice(0, 4).map((cell) => cell.textContent))).toEqual([
      ["0", "refresh", "succeeded", "afi_reserved"], ["1", "selected", "succeeded", "afi_second"],
    ]);
    expect(screen.getAllByText("7 defined Steps · 1 commands · 4 functions")).toHaveLength(2);
    expect(screen.getByText("Not attempted (no reservation): unselected, fallback").textContent).toContain("unselected");
    expect(within(screen.getByRole("table", { name: "Command Step attempts" })).getByRole("link", { name: VERIFIED_COMMAND_ID }).getAttribute("href")).toBe(`/commands?command_id=${VERIFIED_COMMAND_ID}`);
  });

  it.each(["empty", "omitted", "unreached"])("renders %s Function history without inventing reservations", async (mode) => {
    const run = runFixture(mode === "unreached" ? { snapshot: functionDefinition(), steps: [] } : {});
    const { functions, ...legacyRun } = run;
    expect(functions).toEqual([]);
    installAutomationFetch(detailRoutes([
      { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [runSummary()] } }) },
      { method: "GET", path: HISTORY_ENTRY_PATTERN, respond: () => ({ body: { kind: "run", run: mode === "omitted" ? legacyRun : run } }) },
    ]));
    renderDetailPage();
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    const table = await screen.findByRole("table", { name: "Function Step executions" });
    expect(within(table).getByText("This Run reached no Function.").textContent).toBe("This Run reached no Function.");
    if (mode === "unreached") {
      expect(screen.getByText("Not attempted (no reservation): refresh, selected, unselected, fallback").textContent).toContain("refresh");
    } else {
      expect(screen.getByRole("link", { name: VERIFIED_COMMAND_ID }).getAttribute("href")).toBe(`/commands?command_id=${VERIFIED_COMMAND_ID}`);
      expect(screen.queryByText(/Not attempted \(no reservation\)/)).toBeNull();
    }
  });
});

it("renders nested notification definitions read-only and preserves them in enablement replacements", async () => {
  const definition = branchingDefinitionFixture();
  definition.steps.push({ id: "notify-route", kind: "choose", branches: [{ id: "alert", conditions: { id: "matched", kind: "trigger", trigger_ids: ["button_press"] }, steps: [{ id: "notify", kind: "notification", destination_id: "ndst_household", title: { literal: "Freezer warning" }, message: { literal: "Too warm\nCheck freezer" } }] }] });
  const requests = installAutomationFetch(detailRoutes([{ method: "GET", path: DEFINITION_PATH, respond: () => ({ body: automationFixture({ definition }) }) }]));
  renderDetailPage();
  expect((await screen.findByLabelText("Title for notify")).textContent).toContain("Freezer warning");
  expect(screen.getByRole("link", { name: "ndst_household" }).getAttribute("href")).toBe("/notification-destinations/ndst_household");
  expect(screen.getByText(/Notification Steps are read-only here/)).not.toBeNull();
  expect(screen.queryByRole("textbox")).toBeNull();
  fireEvent.click(screen.getByRole("switch", { name: "Enabled" }));
  await waitFor(() => expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1));
  expect(requestsFor(requests, "PUT", DEFINITION_PATH)[0].body).toEqual({ expected_revision: AUTOMATION_REVISION, definition: { ...definition, enabled: !definition.enabled } });
});

it("separates notification Step outcomes from Commands and preserves accepted evidence when attempt detail expires", async () => {
  const identity = { started_at: "2026-10-11T10:00:00Z", completed_at: "2026-10-11T10:00:01Z" };
  const notifications: AutomationNotificationExecution[] = [
    { ...identity, step_id: "accepted-alert", position: 0, attempt_id: "ntf_expired", status: "accepted", outcome: "accepted" },
    { ...identity, step_id: "uncertain-alert", position: 1, attempt_id: "ntf_unknown", status: "failed", outcome: "unknown", reason_code: "outcome_timeout" },
    { ...identity, step_id: "not-admitted", position: 2, attempt_id: "ntf_reserved", status: "failed", reason_code: "notification_admission_closed" },
    { ...identity, step_id: "interrupted-alert", position: 3, attempt_id: "ntf_interrupted", status: "interrupted", reason_code: "core_restarted" },
    { ...identity, completed_at: null, step_id: "pending-alert", position: 4, attempt_id: "ntf_pending", status: "running" },
  ];
  const requests = installAutomationFetch(detailRoutes([
    { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [runSummary()] } }) },
    { method: "GET", path: HISTORY_ENTRY_PATTERN, respond: () => ({ body: { kind: "run", run: runFixture({ notifications }) } }) },
    { method: "GET", path: "/v1/notifications/ntf_expired", respond: () => problemResponse(404, "not_found", "Expired") },
    { method: "GET", path: "/v1/notifications", respond: () => ({ body: { items: [] } }) },
  ]));
  render(<MemoryRouter initialEntries={[DETAIL_ROUTE]}><Routes>
    <Route path="/automations/:automationId" element={<AutomationDetailPage />} />
    <Route path="/notifications/:attemptId" element={<NotificationsPage />} />
  </Routes></MemoryRouter>);
  fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
  const table = await screen.findByRole("table", { name: "Notification Step executions" });
  expect(within(table).getByText("accepted-alert").closest("tr")?.textContent).toContain("accepted");
  expect(within(table).getByText("uncertain-alert").closest("tr")?.textContent).toContain("Submission may have occurred; acceptance is unproven");
  expect(within(table).getByText("not-admitted").closest("tr")?.textContent).toContain("no confirmed attempt outcome");
  expect(within(table).getByText("interrupted-alert").closest("tr")?.textContent).toContain("core_restarted");
  expect(within(table).getByText("pending-alert").closest("tr")?.textContent).toContain("running");
  expect(within(screen.getByRole("table", { name: "Command Step attempts" })).queryByText("ntf_expired")).toBeNull();
  fireEvent.click(within(table).getByRole("link", { name: "ntf_expired" }));
  expect((await screen.findByRole("status")).textContent).toContain("Any retained Automation outcome is unchanged");
  expect(screen.queryByRole("alert")).toBeNull();
  expect(requests.filter((request) => request.method !== "GET")).toHaveLength(0);
});

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
    cause: { kind: "manual" },
    condition_mode: "not_configured",
    bypass_requested: false,
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
    cause: skipFixture().cause,
    condition_mode: "not_configured",
    bypass_requested: false,
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
  it.each([
    { previous: {}, expected: "— (not reported)" },
    { previous: { previous_value: null }, expected: "null" },
  ])("distinguishes retained previous value $expected from absence and renders previous comparisons", async ({ previous, expected }) => {
    const trigger = {
      id: "became-null", kind: "observation" as const, entity_id: ENTITY_ID,
      dispositions: ["applied" as const],
      comparisons: [{ value_pointer: "", operator: "eq" as const, operand: null }],
      ...("previous_value" in previous ? {
        previous_comparisons: [{ value_pointer: "", operator: "eq" as const, operand: null }],
      } : {}),
    };
    installAutomationFetch(detailRoutes([
      { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [skipSummary()] } }) },
      { method: "GET", path: HISTORY_ENTRY_PATTERN, respond: () => ({ body: { kind: "skip", skip: skipFixture({
        matched_triggers: [trigger], cause: { kind: "device_fact", fact: {
          fact_id: "fct_01920000-0000-7000-8000-000000000006", family: "observation", entity_id: ENTITY_ID,
          observation_id: "obs_01920000-0000-7000-8000-000000000007", disposition: "applied", value: null,
          emitted_at: "2026-02-01T10:05:00Z", ...previous,
        } },
      }) } }) },
    ]));
    renderDetailPage();
    fireEvent.click(await screen.findByRole("button", { name: SKIP_ID }));
    await screen.findByText("Previous value");
    expect(screen.getByText("Previous value").nextElementSibling?.textContent).toBe(expected);
    expect(screen.getByText("Reported value").nextElementSibling?.textContent).toBe("null");
    expect(screen.getByText("Observation id").nextElementSibling?.textContent).toBe("obs_01920000-0000-7000-8000-000000000007");
    if ("previous_value" in previous) {
      expect(screen.getByText(/previous comparisons eq null/)).not.toBeNull();
    } else {
      expect(screen.queryByText(/previous comparisons/)).toBeNull();
    }
    expect(screen.queryByText("Event name")).toBeNull();
  });

  it("shows selected null separately from missing State selection and unknown reasons", async () => {
    const snapshot = { id: "state-check", kind: "all" as const, children: [
      { id: "known-null", kind: "entity_state" as const, entity_id: ENTITY_ID, value_pointer: "", operator: "eq" as const, operand: null },
      { id: "missing-state", kind: "entity_state" as const, entity_id: UNSELECTED_ENTITY_ID, value_pointer: "", operator: "eq" as const, operand: true },
    ] };
    const decision: AutomationConditionDecision = {
      mode: "evaluated", bypass_requested: false, snapshot,
      evaluation: { evaluated_at: "2026-02-01T10:05:00Z", result: "unknown", nodes: [
        { id: "known-null", kind: "entity_state", result: "true", selected_value: null,
          observation_id: "obs_01920000-0000-7000-8000-000000000007", observed_at: "2026-02-01T10:04:59Z" },
        { id: "missing-state", kind: "entity_state", result: "unknown", unknown_reason: "state_missing" },
      ] },
    };
    installAutomationFetch(detailRoutes([
      { method: "GET", path: HISTORY_PATH, respond: () => ({ body: { items: [{ ...skipSummary(), reason: "conditions_unknown", cause: { kind: "manual" }, condition_mode: "evaluated", condition_result: "unknown" }] } }) },
      { method: "GET", path: HISTORY_ENTRY_PATTERN, respond: () => ({ body: { kind: "skip", skip: skipFixture({
        reason: "conditions_unknown", condition_decision: decision, cause: { kind: "manual" }, matched_triggers: [],
      }) } }) },
    ]));
    renderDetailPage();
    fireEvent.click(await screen.findByRole("button", { name: SKIP_ID }));
    expect(await screen.findByText("Admission Conditions: unknown")).not.toBeNull();
    expect(screen.getByText(/known-null: true · selected value null · observation obs_/)).not.toBeNull();
    expect(screen.getByText("missing-state: unknown · state_missing").textContent).not.toContain("selected value");
  });

  it("renders delay-only definitions with exact durations and no command positions or Entity references", async () => {
    const definition = delayDefinitionFixture();
    definition.steps.push(
      { id: "second-wait", kind: "delay", duration_ms: { literal: 1000 } },
      { id: "hour-wait", kind: "delay", duration_ms: { literal: 7200000 } },
    );
    const requests = installAutomationFetch(detailRoutes([
      { method: "GET", path: DEFINITION_PATH, respond: () => ({ body: automationFixture({ definition }) }) },
    ]));
    renderDetailPage();
    expect(await screen.findByText("8 defined Steps · 0 commands")).not.toBeNull();
    expect(screen.getByText("Commands").nextElementSibling?.textContent).toBe("0");
    const outline = screen.getByRole("list", { name: "Step definition outline" });
    for (const text of ["wait · delay · Literal · 5 minutes", "precise-wait · delay · Literal · 1001 milliseconds", "short-wait · delay · Literal · 1 millisecond", "long-wait · delay · Literal · 1 day", "second-wait · delay · Literal · 1 second", "hour-wait · delay · Literal · 2 hours"]) {
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
    expect(within(retained).getAllByRole("listitem").map((row) => row.textContent)).toContain("precise-wait · delay · Literal · 1001 milliseconds");
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
    choose.default.push({ id: "finish", kind: "command", entity_id: ENTITY_ID, operation: "set", parameters: { literal: { value: false } } });
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
    expect(within(decisions).getByText("matched: true · matched triggers button_press")).not.toBeNull();
    expect(within(decisions).getByText(/low: false · selected value 120 · observation obs_/)).not.toBeNull();
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
        { ...(kind === "run" ? runSummary() : skipSummary()), cause: { kind: "schedule" } },
      ] } }) },
      { method: "GET", path: HISTORY_ENTRY_PATTERN, respond: () => ({ body: kind === "run"
        ? { kind, run: runFixture({ cause: { kind: "schedule" }, matched_trigger_ids: ["morning"], snapshot: definition }) }
        : { kind, skip: skipFixture({ cause: { kind: "schedule" }, matched_triggers: [trigger] }) },
      }) },
    ]));
    renderDetailPage();

    expect(await screen.findByText(trigger.expression)).not.toBeNull();
    fireEvent.click(await screen.findByRole("button", { name: entryId }));
    expect(await screen.findByText("cause: schedule")).not.toBeNull();
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
            body: { kind: "skip", skip: skipFixture({ matched_triggers: [heldTrigger], cause: {
              kind: "held_state", evidence: { trigger_id: "light_on", started_at: "2026-02-01T10:04:00Z", due_at: "2026-02-01T10:05:00Z" },
            } }) },
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
    expect(screen.getByText("Held trigger").nextElementSibling?.textContent).toBe("light_on");
    expect(screen.getByText("Hold started").nextElementSibling?.textContent).toBe("2026-02-01T10:04:00Z");
    expect(screen.getByText("Hold due").nextElementSibling?.textContent).toBe("2026-02-01T10:05:00Z");
    expect(screen.queryByText("Fact id")).toBeNull();
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
            body: { kind: "skip", skip: skipFixture({ reason, cause: { kind: "manual" } }) },
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
                      steps: [{ position: 0, step_id: "turn_on", status: "running", started_at: "2026-02-01T10:00:00.100Z" }],
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
