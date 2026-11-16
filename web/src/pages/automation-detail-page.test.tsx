import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { StrictMode } from "react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { parseAutomationResponse } from "../api/automation-json";
import type {
  AutomationConditionDecision,
  AutomationDefinition,
  AutomationStepExecution,
  AutomationFunctionStep,
  AutomationHistorySummary,
} from "../api/types.ts";
import { readAutomationDocument } from "../features/automations/document";
import AutomationDetailPage from "./automation-detail-page.tsx";
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
import NotificationsPage from "./notifications-page.tsx";

const FUNCTION_FINGERPRINT = `sha256-v1:${"a".repeat(64)}`;

/** Loads the interrupted branch Run and returns its decision timeline. */
const openRetainedBranchRun = async () => {
  installAutomationFetch(
    detailRoutes([
      {
        method: "GET",
        path: HISTORY_PATH,
        respond: () => ({
          body: { items: [{ ...runSummary(), status: "interrupted" }] },
        }),
      },
      {
        method: "GET",
        path: HISTORY_ENTRY_PATTERN,
        respond: () => ({
          body: { kind: "run", run: interruptedBranchRunFixture() },
        }),
      },
      {
        method: "GET",
        path: "/v1/entities",
        respond: () => ({
          body: {
            items: [
              entityFixture(),
              entityFixture({
                id: BRANCH_STATE_ENTITY_ID,
                name: "Office brightness",
              }),
              entityFixture({
                id: UNSELECTED_ENTITY_ID,
                name: "Unselected sensor",
              }),
              entityFixture({
                id: ADMISSION_ENTITY_ID,
                name: "Admission readiness",
              }),
            ],
          },
        }),
      },
    ])
  );
  renderDetailPage();
  fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
  return screen.findByRole("table", { name: "Step execution timeline" });
};

/** Page route, distinct from the API paths the page reads. */
const DETAIL_ROUTE = `/automations/${AUTOMATION_ID}`;
const SECOND_AUTOMATION_ID = "aut_01920000-0000-7000-8000-00000000000c";
const SECOND_AUTOMATION_NAME = "Hallway light at dusk";
const DEFINITION_PATH = `/v1/automations/${AUTOMATION_ID}`;
const RUNS_PATH = `${DEFINITION_PATH}/runs`;
const HISTORY_PATH = `${DEFINITION_PATH}/history`;
/** One history entry read: the Run and Skip identities are the entry ids. */
const HISTORY_ENTRY_PATTERN = /\/history\/(?<entry>arn_|ask_)[^/]+$/u;
/** Opaque cursor holding `/`, `+` and `=`, like hearthd's own cursors. */
const NEXT_CURSOR = "page/1+=";

const runSummary = (): AutomationHistorySummary => ({
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
});

const skipSummary = (): AutomationHistorySummary => ({
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
});

/** Routes for a detail page: the definition read, the newest history page, and
    the Entities read the page uses to label Entity references. A test's
    overrides come first, so repeating a method and path replaces the default. */
const detailRoutes = (overrides: AutomationRoute[] = []): AutomationRoute[] => [
  ...overrides,
  {
    method: "GET",
    path: DEFINITION_PATH,
    respond: () => ({ body: automationFixture() }),
  },
  {
    method: "PUT",
    path: DEFINITION_PATH,
    respond: () => ({
      body: automationFixture({ revision: AUTOMATION_REVISION + 1 }),
    }),
  },
  {
    method: "GET",
    path: HISTORY_PATH,
    respond: () => ({ body: { items: [] } }),
  },
  {
    method: "GET",
    path: "/v1/entities",
    respond: () => ({ body: { items: [entityFixture()] } }),
  },
];

const renderDetailPage = (route = DETAIL_ROUTE, strict = false) => {
  const page = (
    <MemoryRouter initialEntries={[route]}>
      <Routes>
        <Route
          path="/automations/:automationId"
          element={<AutomationDetailPage />}
        />
      </Routes>
    </MemoryRouter>
  );
  return render(strict ? <StrictMode>{page}</StrictMode> : page);
};

/** The detail route plus a navigation button, so a test can change the route
    parameter while the page stays mounted. */
const RouteChangeHarness = () => {
  const navigate = useNavigate();
  return (
    <>
      <button
        type="button"
        onClick={() => navigate(`/automations/${SECOND_AUTOMATION_ID}`)}
      >
        Open other automation
      </button>
      <Routes>
        <Route
          path="/automations/:automationId"
          element={<AutomationDetailPage />}
        />
      </Routes>
    </>
  );
};
const subAutomationDefinition = (): AutomationDefinition => ({
  name: "Movie night",
  enabled: true,
  triggers: [],
  steps: [
    {
      id: "room",
      kind: "sub_automation",
      automation_id: SECOND_AUTOMATION_ID,
    },
  ],
});

const functionStep = (id: string): AutomationFunctionStep => ({
  id,
  kind: "function",
  adapter_id: "zigbee2mqtt",
  function_key: "refresh_entity",
  contract_fingerprint: FUNCTION_FINGERPRINT,
  arguments: {
    entity_id: "opaque-vendor-id",
    options: { force: true },
    values: [null, 42],
  },
});

const functionDefinition = (): AutomationDefinition =>
  structuredClone(
    readAutomationDocument(`{
    "name": "Refresh weather",
    "enabled": true,
    "triggers": [
      { "id": "button", "kind": "cron", "expression": "0 7 * * *" },
      { "id": "other", "kind": "cron", "expression": "0 8 * * *" }
    ],
    "steps": [
      ${JSON.stringify(functionStep("refresh"))},
      {
        "id": "route",
        "kind": "if",
        "conditions": { "id": "matched", "kind": "trigger", "trigger_ids": ["button"] },
        "then": [${JSON.stringify(functionStep("selected"))}],
        "else": [{
          "id": "alternative",
          "kind": "choose",
          "branches": [{
            "id": "other",
            "conditions": { "id": "other-match", "kind": "trigger", "trigger_ids": ["other"] },
            "steps": [${JSON.stringify(functionStep("unselected"))}]
          }],
          "default": [${JSON.stringify(functionStep("fallback"))}]
        }]
      }
    ]
  }`).definition
  );

describe("Automation detail page", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  describe("Automation Function Steps", () => {
    it("preserves literal Function argument numbers in display and the enablement PUT", async () => {
      const rawArguments = String.raw`{"large":9007199254740993,"decimal":0.123456789012345678901,"expr":"vendor data","nested":[1E+003,{"tiny":1e-999,"negative":-0,"text":"9007199254740993 \" [1e3] \u0031"}]}`;
      const def = `{"name":"Exact arguments","enabled":true,"triggers":[],"variables":{"n":-9223372036854775808},"variables_expr":{"origin":"trigger.kind"},"steps":[{"id":"exact","kind":"function","adapter_id":"example","function_key":"refresh","contract_fingerprint":"${FUNCTION_FINGERPRINT}","arguments":${rawArguments}},{"id":"set","kind":"set_variables","assignments":{"max":9223372036854775807},"assignments_expr":{"next":"vars.n + 1"}},{"id":"command","kind":"command","entity_id":"${ENTITY_ID}","operation":"set","parameters":{"n":9007199254740993}},{"id":"notify","kind":"notification","destination_id":"ndst_static","title":"hello","message_expr":"string(vars.n)"},{"id":"delay","kind":"delay","duration_ms":1001}]}`;
      const wire = `{"id":"${AUTOMATION_ID}","revision":3,"created_at":"2026-10-23T10:00:00Z","updated_at":"2026-10-23T10:00:00Z","definition":${def}}`;
      const puts: string[] = [];
      const requests = installAutomationFetch(detailRoutes());
      const serve = globalThis.fetch;
      vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input) === DEFINITION_PATH) {
          if (init?.method === "PUT") {
            puts.push(String(init.body));
          }
          return new Response(wire, {
            headers: { "content-type": "application/json" },
          });
        }
        return serve(input, init);
      });
      renderDetailPage();
      const args = await screen.findByLabelText("Arguments for exact");
      // Check the outgoing wire before the display assertion so the original
      // data-corrupting PUT (not only its rounded display) is a regression failure.
      fireEvent.click(screen.getByRole("switch", { name: "Enabled" }));
      await waitFor(() => expect(puts).toHaveLength(1));
      expect(puts[0]).toBe(
        `{"expected_revision":3,"definition":${def.replace('"enabled":true', '"enabled":false')}}`
      );
      for (const literal of [
        '"large": 9007199254740993',
        '"decimal": 0.123456789012345678901',
        "1E+003",
        '"tiny": 1e-999',
        '"negative": -0',
      ]) {
        expect(args.textContent).toContain(literal);
      }
      expect(requests.map((request) => request.path)).toContain(HISTORY_PATH);
    });

    it("shows every defined arm, fingerprint and opaque arguments without discovery, and preserves them on enablement changes", async () => {
      const definition = functionDefinition();
      const requests = installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: DEFINITION_PATH,
            respond: () => ({ body: automationFixture({ definition }) }),
          },
        ])
      );
      renderDetailPage();
      await screen.findByText("6 defined Steps · 0 commands · 4 functions");
      const outline = screen.getByRole("list", {
        name: "Step definition outline",
      });
      for (const id of ["refresh", "selected", "unselected", "fallback"]) {
        expect(
          within(outline).getByText(id).closest("li")?.textContent
        ).toContain("zigbee2mqtt / refresh_entity");
        expect(
          within(outline).getByLabelText(`Arguments for ${id}`).textContent
        ).toBe(JSON.stringify(functionStep(id).arguments, null, 2));
      }
      expect(
        within(outline).getAllByText(
          `Contract fingerprint: ${FUNCTION_FINGERPRINT}`
        )
      ).toHaveLength(4);
      expect(
        screen.getByText(/Function Steps use the exact-JSON inspector/u)
          .textContent
      ).toContain("stack builder");
      expect({
        functions:
          screen.getByText("Functions").nextElementSibling?.textContent,
        hasTextbox: screen.queryByRole("textbox") !== null,
      }).toStrictEqual({ functions: "4", hasTextbox: false });
      expect(
        requests.map((request) => [request.method, request.path])
      ).toStrictEqual([
        ["GET", DEFINITION_PATH],
        ["GET", HISTORY_PATH],
      ]);
      fireEvent.click(screen.getByRole("switch", { name: "Enabled" }));
      await waitFor(() =>
        expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1)
      );
      expect(
        requestsFor(requests, "PUT", DEFINITION_PATH)[0].body
      ).toStrictEqual({
        expected_revision: AUTOMATION_REVISION,
        definition: { ...definition, enabled: false },
      });
    });

    const started_at = "2026-10-23T10:00:00Z";
    const completed_at = "2026-10-23T10:00:01Z";
    const identity = {
      sequence: 0,
      step_id: "refresh",
      kind: "function" as const,
      started_at,
    };
    const evidence: Extract<AutomationStepExecution, { kind: "function" }>[] = [
      {
        ...identity,
        phase: "running",
        evidence: { invocation_id: "afi_reserved" },
      },
      {
        ...identity,
        phase: "succeeded",
        completed_at,
        evidence: {
          invocation_id: "afi_reserved",
          catalog_revision: "cat_retained",
          invocation_outcome: "succeeded",
        },
      },
      {
        ...identity,
        phase: "failed",
        completed_at,
        evidence: {
          invocation_id: "afi_reserved",
          catalog_revision: "cat_retained",
          invocation_outcome: "failed",
          reason_code: "adapter_error",
        },
      },
      {
        ...identity,
        phase: "failed",
        completed_at,
        evidence: {
          invocation_id: "afi_reserved",
          catalog_revision: "cat_retained",
          invocation_outcome: "not_executed",
          reason_code: "invalid_arguments",
        },
      },
      {
        ...identity,
        phase: "failed",
        completed_at,
        evidence: {
          invocation_id: "afi_reserved",
          catalog_revision: "cat_retained",
          invocation_outcome: "unknown",
          reason_code: "outcome_timeout",
        },
      },
      {
        ...identity,
        phase: "failed",
        completed_at,
        evidence: {
          invocation_id: "afi_reserved",
          reason_code: "function_contract_changed",
        },
      },
      {
        ...identity,
        phase: "interrupted",
        completed_at,
        evidence: {
          invocation_id: "afi_reserved",
          reason_code: "core_restarted",
        },
      },
      {
        ...identity,
        phase: "interrupted",
        completed_at,
        evidence: {
          invocation_id: "afi_reserved",
          reason_code: "executor_fault",
        },
      },
      {
        ...identity,
        phase: "interrupted",
        completed_at,
        evidence: {
          invocation_id: "afi_reserved",
          reason_code: "core_stopping",
        },
      },
    ];

    it.each(evidence)(
      "shows retained $phase Function evidence without treating a reservation as dispatch",
      async (execution) => {
        const snapshot = functionDefinition();
        installAutomationFetch(
          detailRoutes([
            {
              method: "GET",
              path: HISTORY_PATH,
              respond: () => ({ body: { items: [runSummary()] } }),
            },
            {
              method: "GET",
              path: HISTORY_ENTRY_PATTERN,
              respond: () => ({
                body: {
                  kind: "run",
                  run: runFixture({
                    status: execution.phase,
                    snapshot,
                    executions: [execution],
                  }),
                },
              }),
            },
          ])
        );
        renderDetailPage();
        fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
        const table = await screen.findByRole("table", {
          name: "Step execution timeline",
        });
        const [, row] = within(table).getAllByRole("row");
        const cells = within(row)
          .getAllByRole("cell")
          .map((cell) => cell.textContent);
        expect(cells.slice(0, 4)).toStrictEqual([
          "0",
          "refresh",
          "function",
          execution.phase,
        ]);
        const evidenceParts = [
          "afi_reserved",
          execution.evidence.invocation_outcome ??
            "— (no terminal invocation evidence)",
          execution.evidence.catalog_revision,
          execution.evidence.reason_code,
          execution.evidence.invocation_outcome === "unknown"
            ? "Effects may have occurred; success is unproven"
            : undefined,
        ].filter((part): part is string => part !== undefined);
        for (const part of evidenceParts) {
          expect(cells[4]).toContain(part);
        }
        expect(cells.slice(5)).toStrictEqual([
          started_at,
          execution.completed_at ?? "—",
        ]);
        expect(
          screen.getByText(/Reserved Command, attempt and invocation IDs/u)
            .textContent
        ).toContain("do not prove admission or dispatch");
      }
    );

    it.each(evidence)(
      "keeps unselected Steps and unrelated links out of the $phase Function timeline",
      async (execution) => {
        const snapshot = functionDefinition();
        const requests = installAutomationFetch(
          detailRoutes([
            {
              method: "GET",
              path: HISTORY_PATH,
              respond: () => ({ body: { items: [runSummary()] } }),
            },
            {
              method: "GET",
              path: HISTORY_ENTRY_PATTERN,
              respond: () => ({
                body: {
                  kind: "run",
                  run: runFixture({
                    status: execution.phase,
                    snapshot,
                    executions: [execution],
                  }),
                },
              }),
            },
          ])
        );
        renderDetailPage();
        fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
        const table = await screen.findByRole("table", {
          name: "Step execution timeline",
        });
        expect(within(table).queryByText("selected")).toBeNull();
        const [, retained] = screen.getAllByRole("list", {
          name: "Step definition outline",
        });
        expect(
          within(retained).getByLabelText("Arguments for unselected")
            .textContent
        ).toContain('"entity_id": "opaque-vendor-id"');
        expect(within(table).queryByRole("link")).toBeNull();
        expect(
          requests.map((request) => [request.method, request.path])
        ).toStrictEqual([
          ["GET", DEFINITION_PATH],
          ["GET", HISTORY_PATH],
          ["GET", "/v1/devices"],
          ["GET", "/v1/entities"],
          ["GET", `${HISTORY_PATH}/${RUN_ID}`],
        ]);
      }
    );

    it("orders mixed executions by one sequence rather than definition position or timestamps", async () => {
      const snapshot = functionDefinition();
      snapshot.steps.unshift(automationFixture().definition.steps[0]);
      const succeeded = {
        phase: "succeeded" as const,
        completed_at,
        evidence: {
          invocation_id: "afi_reserved",
          catalog_revision: "cat_retained",
          invocation_outcome: "succeeded" as const,
        },
      };
      installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: DEFINITION_PATH,
            respond: () => ({
              body: automationFixture({ definition: snapshot }),
            }),
          },
          {
            method: "GET",
            path: HISTORY_PATH,
            respond: () => ({ body: { items: [runSummary()] } }),
          },
          {
            method: "GET",
            path: HISTORY_ENTRY_PATTERN,
            respond: () => ({
              body: {
                kind: "run",
                run: runFixture({
                  snapshot,
                  executions: [
                    {
                      ...identity,
                      ...succeeded,
                      sequence: 2,
                      step_id: "selected",
                      evidence: {
                        ...succeeded.evidence,
                        invocation_id: "afi_second",
                      },
                    },
                    {
                      ...identity,
                      ...succeeded,
                      sequence: 1,
                      started_at: "2026-10-23T09:59:00Z",
                    },
                    ...runFixture().executions,
                  ],
                }),
              },
            }),
          },
        ])
      );
      renderDetailPage();
      fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
      const table = await screen.findByRole("table", {
        name: "Step execution timeline",
      });
      expect(
        within(table)
          .getAllByRole("row")
          .slice(1)
          .map((row) =>
            within(row)
              .getAllByRole("cell")
              .slice(0, 4)
              .map((cell) => cell.textContent)
          )
      ).toStrictEqual([
        ["0", "turn_on", "command", "succeeded"],
        ["1", "refresh", "function", "succeeded"],
        ["2", "selected", "function", "succeeded"],
      ]);
      expect(
        screen.getAllByText("7 defined Steps · 1 commands · 4 functions")
      ).toHaveLength(2);
      expect(within(table).queryByText("unselected")).toBeNull();
      expect(
        within(table)
          .getByRole("link", { name: VERIFIED_COMMAND_ID })
          .getAttribute("href")
      ).toBe(`/commands?command_id=${VERIFIED_COMMAND_ID}`);
    });

    it("renders an empty unified history without inventing reservations or unselected outcomes", async () => {
      const run = runFixture({
        status: "interrupted",
        snapshot: functionDefinition(),
        executions: [],
      });
      installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: HISTORY_PATH,
            respond: () => ({ body: { items: [runSummary()] } }),
          },
          {
            method: "GET",
            path: HISTORY_ENTRY_PATTERN,
            respond: () => ({ body: { kind: "run", run } }),
          },
        ])
      );
      renderDetailPage();
      fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
      const table = await screen.findByRole("table", {
        name: "Step execution timeline",
      });
      expect(
        within(table).getByText("This Run has no durably reached Steps.")
      ).not.toBeNull();
      expect(within(table).queryByRole("link")).toBeNull();
      expect(within(table).queryByText("refresh")).toBeNull();
      expect(
        screen.getByText(
          /absence does not establish why a Step was not reached/u
        )
      ).not.toBeNull();
    });
  });

  it("renders nested notification definitions read-only and preserves them in enablement replacements", async () => {
    const definition = branchingDefinitionFixture();
    definition.steps.push({
      id: "notify-route",
      kind: "choose",
      branches: [
        {
          id: "alert",
          conditions: {
            id: "matched",
            kind: "trigger",
            trigger_ids: ["button_press"],
          },
          steps: [
            {
              id: "notify",
              kind: "notification",
              destination_id: "ndst_household",
              title: "Freezer warning",
              message: "Too warm\nCheck freezer",
            },
          ],
        },
      ],
    });
    const requests = installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: DEFINITION_PATH,
          respond: () => ({ body: automationFixture({ definition }) }),
        },
      ])
    );
    renderDetailPage();
    const title = await screen.findByLabelText("Title for notify");
    expect(title.textContent).toBe('"Freezer warning"');
    expect(
      screen.getByRole("link", { name: "ndst_household" }).getAttribute("href")
    ).toBe("/notification-destinations/ndst_household");
    expect(
      screen.getByText(/Notification Steps are editable in the stack builder/u)
    ).not.toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
    fireEvent.click(screen.getByRole("switch", { name: "Enabled" }));
    await waitFor(() =>
      expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1)
    );
    expect(requestsFor(requests, "PUT", DEFINITION_PATH)[0].body).toStrictEqual(
      {
        expected_revision: AUTOMATION_REVISION,
        definition: { ...definition, enabled: !definition.enabled },
      }
    );
  });

  it("shows initialization and data Steps in the unified timeline without calling provider success a failure", async () => {
    const time = "2026-10-11T10:00:00Z";
    const snapshot: AutomationDefinition = {
      name: "Values",
      enabled: true,
      triggers: [],
      variables: { ready: true },
      steps: [
        {
          kind: "read_state",
          id: "read",
          entity_id: ENTITY_ID,
          value_pointer: "/value",
          on_unusable: "null",
        },
        { kind: "set_variables", id: "assign", assignments: { x: 5 } },
        functionStep("call"),
      ],
    };
    const run = runFixture({
      status: "failed",
      failure_code: "expression_limit",
      snapshot,
      data_evidence: "recorded",
      value_initialization: {
        kind: "initialized",
        recorded_at: time,
        values: { ready: true },
      },
      executions: [
        {
          kind: "read_state",
          step_id: "read",
          sequence: 0,
          phase: "succeeded",
          started_at: time,
          completed_at: time,
          evidence: {
            output: null,
            read_at: time,
            unusable_reason: "state_missing",
          },
        },
        {
          kind: "set_variables",
          step_id: "assign",
          sequence: 1,
          phase: "succeeded",
          started_at: time,
          completed_at: time,
          evidence: { values: { x: 5 } },
        },
        {
          kind: "function",
          step_id: "call",
          sequence: 2,
          phase: "failed",
          started_at: time,
          completed_at: time,
          evidence: {
            invocation_id: "inv_1",
            catalog_revision: "rev_1",
            inputs: {
              arguments: {
                entity_id: "opaque-vendor-id",
                options: { force: true },
                values: [null, 42],
              },
            },
            invocation_outcome: "succeeded",
            value_failure: { failure_code: "expression_limit" },
          },
        },
      ],
    });
    installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: HISTORY_PATH,
          respond: () => ({ body: { items: [runSummary()] } }),
        },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: () => ({ body: { kind: "run", run } }),
        },
      ])
    );
    renderDetailPage();
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    await expect(
      screen.findByText("Exact initialization JSON")
    ).resolves.not.toBeNull();
    const table = await screen.findByRole("table", {
      name: "Step execution timeline",
    });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(
      rows.map((row) => within(row).getAllByRole("cell")[1].textContent)
    ).toStrictEqual(["read", "assign", "call"]);
    expect([rows[0].textContent, rows[1].textContent]).toStrictEqual([
      expect.stringContaining('"unusable_reason": "state_missing"'),
      expect.stringContaining('"values"'),
    ]);
    for (const text of [
      "Invocation outcome: succeeded",
      "Step value failure: expression_limit",
    ]) {
      expect(rows[2].textContent).toContain(text);
    }
  });

  it.each([
    {
      step: {
        id: "unreserved",
        kind: "function" as const,
        adapter_id: "example",
        function_key: "run",
        contract_fingerprint: FUNCTION_FINGERPRINT,
        arguments_expr: "vars.missing.nested",
      },
      field: "arguments",
      code: "expression_evaluation" as const,
      kind: "function" as const,
    },
    {
      step: {
        id: "notify",
        kind: "notification" as const,
        destination_id: "ndst_household",
        title_expr: "vars.missing.nested",
        message: "hello",
      },
      field: "title",
      code: "expression_evaluation" as const,
      kind: "notification" as const,
    },
    {
      step: {
        id: "unresolved",
        kind: "command" as const,
        entity_id: ENTITY_ID,
        operation: "set",
        parameters_expr: "123",
      },
      field: "parameters",
      code: "resolved_input_invalid" as const,
      kind: "command" as const,
    },
  ])(
    "shows $kind failure before reservation without fabricating external IDs",
    async ({ step, field, code, kind }) => {
      const time = "2026-10-11T10:00:00Z";
      const failed = {
        step_id: step.id,
        sequence: 0,
        phase: "failed" as const,
        started_at: time,
        completed_at: time,
        evidence: { value_failure: { field, failure_code: code } },
      };
      let execution: AutomationStepExecution;
      if (kind === "function") {
        execution = { ...failed, kind };
      } else if (kind === "notification") {
        execution = { ...failed, kind };
      } else {
        execution = { ...failed, kind };
      }
      const snapshot: AutomationDefinition = {
        name: "Failure",
        enabled: true,
        triggers: [],
        steps: [step],
      };
      const run = runFixture({
        status: "failed",
        failure_code: code,
        snapshot,
        data_evidence: "recorded",
        value_initialization: {
          kind: "initialized",
          recorded_at: time,
          values: {},
        },
        executions: [execution],
      });
      installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: HISTORY_PATH,
            respond: () => ({ body: { items: [runSummary()] } }),
          },
          {
            method: "GET",
            path: HISTORY_ENTRY_PATTERN,
            respond: () => ({ body: { kind: "run", run } }),
          },
        ])
      );
      renderDetailPage();
      fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
      const table = await screen.findByRole("table", {
        name: "Step execution timeline",
      });
      const rows = within(table).getAllByRole("row").slice(1);
      expect(rows).toHaveLength(1);
      expect(rows[0].textContent).toContain(
        `Step value failure: ${code} · ${field}`
      );
      expect(within(rows[0]).queryByRole("link")).toBeNull();
      expect(rows[0].textContent).not.toContain("Reserved invocation:");
      expect(rows[0].textContent).not.toContain("Reserved Command:");
    }
  );

  it("links static read_state Entities and labels legacy value evidence", async () => {
    const definition = {
      ...automationFixture().definition,
      steps: [
        {
          id: "capture",
          kind: "read_state" as const,
          entity_id: ENTITY_ID,
          value_pointer: "/value",
          on_unusable: "null" as const,
        },
      ],
    };
    installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: DEFINITION_PATH,
          respond: () => ({ body: automationFixture({ definition }) }),
        },
        {
          method: "GET",
          path: HISTORY_PATH,
          respond: () => ({ body: { items: [runSummary()] } }),
        },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: () => ({
            body: {
              kind: "run",
              run: runFixture({
                snapshot: definition,
                executions: [],
                data_evidence: "legacy_unavailable",
                value_initialization: null,
              }),
            },
          }),
        },
      ])
    );
    const entitiesGate = Promise.withResolvers<undefined>();
    const serve = globalThis.fetch;
    vi.stubGlobal(
      "fetch",
      async (input: RequestInfo | URL, init?: RequestInit) => {
        if (
          new URL(String(input), "http://hearthd.test").pathname ===
          "/v1/entities"
        ) {
          await entitiesGate.promise;
        }
        return serve(input, init);
      }
    );
    renderDetailPage();
    const outline = await screen.findByRole("list", {
      name: "Step definition outline",
    });
    expect(
      within(outline)
        .getByRole("link", { name: ENTITY_ID })
        .getAttribute("href")
    ).toBe(`/entities/${ENTITY_ID}`);
    entitiesGate.resolve(undefined);
    const entityLink = await within(outline).findByRole("link", {
      name: ENTITY_NAME,
    });
    expect(entityLink.getAttribute("href")).toBe(`/entities/${ENTITY_ID}`);
    expect(outline.textContent).toContain("/value");
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    await expect(
      screen.findByText(
        "Value evidence unavailable (created before expression support)."
      )
    ).resolves.not.toBeNull();
  });

  it("shows initialization failure field and exact failure JSON", async () => {
    const snapshot: AutomationDefinition = {
      name: "Initialization",
      enabled: true,
      triggers: [],
      variables: { x: { expr: "trigger.missing" } },
      steps: [],
    };
    const run = runFixture({
      status: "failed",
      failure_code: "expression_missing",
      snapshot,
      executions: [],
      data_evidence: "recorded",
      value_initialization: {
        kind: "failed",
        recorded_at: "2026-10-11T10:00:00Z",
        failure: { field: "variables.x", failure_code: "expression_missing" },
      },
    });
    installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: HISTORY_PATH,
          respond: () => ({ body: { items: [runSummary()] } }),
        },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: () => ({ body: { kind: "run", run } }),
        },
      ])
    );
    renderDetailPage();
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    await expect(
      screen.findByText(
        "Initialization failure: expression_missing · variables.x"
      )
    ).resolves.not.toBeNull();
    const summary = screen.getByText("Exact initialization JSON");
    fireEvent.click(summary);
    expect(summary.closest("details")?.open).toBeTruthy();
    expect(summary.parentElement?.textContent).toContain(
      '"failure_code": "expression_missing"'
    );
  });

  it("shows an unreserved Command interruption without inventing a reservation", async () => {
    const at = "2026-10-11T10:00:00Z";
    const run = runFixture({
      status: "interrupted",
      failure_code: "core_stopping",
      data_evidence: "recorded",
      value_initialization: {
        kind: "initialized",
        recorded_at: at,
        values: {},
      },
      executions: [
        {
          kind: "command",
          step_id: "turn_on",
          sequence: 0,
          phase: "interrupted",
          started_at: at,
          completed_at: at,
          evidence: { reason_code: "core_stopping" },
        },
      ],
    });
    installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: HISTORY_PATH,
          respond: () => ({ body: { items: [runSummary()] } }),
        },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: () => ({ body: { kind: "run", run } }),
        },
      ])
    );
    renderDetailPage();
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    const table = await screen.findByRole("table", {
      name: "Step execution timeline",
    });
    const [, row] = within(table).getAllByRole("row");
    expect(row.textContent).toContain("core_stopping");
    expect(row.textContent).not.toContain("Reserved Command:");
    expect(within(row).queryByRole("link")).toBeNull();
  });

  it("shows matching branch expression failure and its exact execution evidence", async () => {
    const at = "2026-10-11T10:00:00Z";
    // This retained snapshot is intentionally not a valid new write definition.
    const snapshotSource = `{
        "name": "Branch",
        "enabled": true,
        "triggers": [],
        "steps": [{
          "id": "branch",
          "kind": "if",
          "conditions": { "id": "check", "kind": "expression", "expr": "vars.missing" },
          "then": []
        }]
      }`;
    expect(() => readAutomationDocument(snapshotSource)).toThrow(
      /must NOT have fewer than 1 items/u
    );
    const snapshot =
      parseAutomationResponse<AutomationDefinition>(snapshotSource);
    const run = runFixture({
      status: "failed",
      failure_code: "expression_missing",
      snapshot,
      data_evidence: "recorded",
      value_initialization: {
        kind: "initialized",
        recorded_at: at,
        values: {},
      },
      executions: [
        {
          kind: "if",
          step_id: "branch",
          sequence: 0,
          phase: "failed",
          started_at: at,
          completed_at: at,
          evidence: {
            outcome: "error",
            evaluations: [],
            failure_code: "expression_missing",
            value_failure: {
              condition_id: "check",
              failure_code: "expression_missing",
            },
          },
        },
      ],
    });
    installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: HISTORY_PATH,
          respond: () => ({ body: { items: [runSummary()] } }),
        },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: () => ({ body: { kind: "run", run } }),
        },
      ])
    );
    renderDetailPage();
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    const table = await screen.findByRole("table", {
      name: "Step execution timeline",
    });
    expect(table.textContent).toContain(
      "expression_missing · condition ID check"
    );
    fireEvent.click(within(table).getByText("Evidence for branch"));
    expect(table.textContent).toContain('"condition_id": "check"');
  });

  it("separates notification Step outcomes from Commands and preserves accepted evidence when attempt detail expires", async () => {
    const identity = {
      started_at: "2026-10-11T10:00:00Z",
      completed_at: "2026-10-11T10:00:01Z",
    };
    const notifications: AutomationStepExecution[] = [
      {
        ...identity,
        kind: "notification",
        step_id: "accepted-alert",
        sequence: 1,
        phase: "succeeded",
        evidence: { attempt_id: "ntf_expired", outcome: "accepted" },
      },
      {
        ...identity,
        kind: "notification",
        step_id: "uncertain-alert",
        sequence: 2,
        phase: "failed",
        evidence: {
          attempt_id: "ntf_unknown",
          outcome: "unknown",
          reason_code: "outcome_timeout",
        },
      },
      {
        ...identity,
        kind: "notification",
        step_id: "not-admitted",
        sequence: 3,
        phase: "failed",
        evidence: {
          attempt_id: "ntf_reserved",
          reason_code: "notification_admission_closed",
        },
      },
      {
        ...identity,
        kind: "notification",
        step_id: "interrupted-alert",
        sequence: 4,
        phase: "interrupted",
        evidence: {
          attempt_id: "ntf_interrupted",
          reason_code: "core_restarted",
        },
      },
      {
        started_at: identity.started_at,
        kind: "notification",
        step_id: "pending-alert",
        sequence: 5,
        phase: "running",
        evidence: { attempt_id: "ntf_pending" },
      },
    ];
    const requests = installAutomationFetch(
      detailRoutes([
        {
          method: "GET",
          path: HISTORY_PATH,
          respond: () => ({ body: { items: [runSummary()] } }),
        },
        {
          method: "GET",
          path: HISTORY_ENTRY_PATTERN,
          respond: () => ({
            body: {
              kind: "run",
              run: runFixture({
                executions: [...runFixture().executions, ...notifications],
              }),
            },
          }),
        },
        {
          method: "GET",
          path: "/v1/notifications/ntf_expired",
          respond: () => problemResponse(404, "not_found", "Expired"),
        },
        {
          method: "GET",
          path: "/v1/notifications",
          respond: () => ({ body: { items: [] } }),
        },
      ])
    );
    render(
      <MemoryRouter initialEntries={[DETAIL_ROUTE]}>
        <Routes>
          <Route
            path="/automations/:automationId"
            element={<AutomationDetailPage />}
          />
          <Route
            path="/notifications/:attemptId"
            element={<NotificationsPage />}
          />
        </Routes>
      </MemoryRouter>
    );
    fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
    const table = await screen.findByRole("table", {
      name: "Step execution timeline",
    });
    for (const [stepId, expected] of [
      ["accepted-alert", "accepted"],
      [
        "uncertain-alert",
        "Submission may have occurred; acceptance is unproven",
      ],
      ["not-admitted", "no confirmed attempt outcome"],
      ["interrupted-alert", "core_restarted"],
      ["pending-alert", "running"],
    ] as const) {
      expect(
        within(table).getByText(stepId).closest("tr")?.textContent
      ).toContain(expected);
    }
    expect(
      within(table).getByText("turn_on").closest("tr")?.textContent
    ).not.toContain("ntf_expired");
    fireEvent.click(within(table).getByRole("link", { name: "ntf_expired" }));
    const status = await screen.findByRole("status");
    expect(status.textContent).toContain(
      "Any retained Automation outcome is unchanged"
    );
    expect(screen.queryByRole("alert")).toBeNull();
    expect(requests.filter((request) => request.method !== "GET")).toHaveLength(
      0
    );
  });

  describe("reusable Automation history", () => {
    it("shows triggerless targets and preserves invocation when toggling enablement", async () => {
      const definition = subAutomationDefinition();
      const requests = installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: DEFINITION_PATH,
            respond: () => ({ body: automationFixture({ definition }) }),
          },
        ])
      );
      renderDetailPage();
      await screen.findByText("No automatic triggers");
      expect(
        screen.getByRole("list", { name: "Step definition outline" })
          .textContent
      ).toContain("Sub-automation");
      expect(
        screen
          .getByRole("link", { name: SECOND_AUTOMATION_ID })
          .getAttribute("href")
      ).toBe(`/automations/${SECOND_AUTOMATION_ID}`);
      expect(requests.map(({ path }) => path)).toStrictEqual([
        DEFINITION_PATH,
        HISTORY_PATH,
        `/v1/automations/${SECOND_AUTOMATION_ID}`,
      ]);
      fireEvent.click(screen.getByRole("switch", { name: "Enabled" }));
      await waitFor(() =>
        expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1)
      );
      expect(
        requestsFor(requests, "PUT", DEFINITION_PATH)[0].body
      ).toStrictEqual({
        expected_revision: AUTOMATION_REVISION,
        definition: { ...definition, enabled: false },
      });
    });

    it("opens a linked child Run outside the current history page and links back to its parent", async () => {
      const childRunId = "arn_01920000-0000-7000-8000-00000000000d";
      const childPath = `/v1/automations/${SECOND_AUTOMATION_ID}`;
      const snapshot = subAutomationDefinition();
      const child = runFixture({
        id: childRunId,
        automation_id: SECOND_AUTOMATION_ID,
        cause: {
          kind: "invocation",
          parent_automation_id: AUTOMATION_ID,
          parent_run_id: RUN_ID,
          parent_step_id: "room",
        },
      });
      const requests = installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: HISTORY_ENTRY_PATTERN,
            respond: ({ path }) => ({
              body: {
                kind: "run",
                run: path.startsWith(childPath)
                  ? child
                  : runFixture({
                      snapshot,
                      executions: [
                        {
                          kind: "sub_automation",
                          step_id: "room",
                          sequence: 0,
                          phase: "succeeded",
                          started_at: "2026-10-31T12:00:00Z",
                          completed_at: "2026-10-31T12:00:01Z",
                          evidence: { child_run_id: childRunId },
                        },
                      ],
                    }),
              },
            }),
          },
          {
            method: "GET",
            path: childPath,
            respond: () => ({
              body: automationFixture({ id: SECOND_AUTOMATION_ID }),
            }),
          },
          {
            method: "GET",
            path: `${childPath}/history`,
            respond: () => ({ body: { items: [] } }),
          },
        ])
      );
      renderDetailPage(`${DETAIL_ROUTE}?history_id=${RUN_ID}`);
      const timeline = await screen.findByRole("table", {
        name: "Step execution timeline",
      });
      expect(within(timeline).getByText("Sub-automation")).toBeTruthy();
      fireEvent.click(within(timeline).getByRole("link", { name: childRunId }));
      await screen.findByText("Parent Step");
      expect(
        screen.getByText("Parent Step").nextElementSibling?.textContent
      ).toBe("room");
      expect(
        screen.getByRole("link", { name: RUN_ID }).getAttribute("href")
      ).toBe(`${DETAIL_ROUTE}?history_id=${RUN_ID}`);
      expect(
        requestsFor(requests, "GET", `${childPath}/history/${childRunId}`)
      ).toHaveLength(1);
      fireEvent.click(screen.getByRole("link", { name: RUN_ID }));
      const parentTimeline = await screen.findByRole("table", {
        name: "Step execution timeline",
      });
      await waitFor(() =>
        expect(
          within(parentTimeline)
            .getByRole("link", { name: childRunId })
            .getAttribute("href")
        ).toBe(`/automations/${SECOND_AUTOMATION_ID}?history_id=${childRunId}`)
      );
    });

    it("links a Condition Skip and displays an expired history response", async () => {
      const snapshot = subAutomationDefinition();
      const requests = installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: `${HISTORY_PATH}/${RUN_ID}`,
            respond: () => ({
              body: {
                kind: "run",
                run: runFixture({
                  status: "failed",
                  failure_code: "conditions_false",
                  snapshot,
                  executions: [
                    {
                      kind: "sub_automation",
                      step_id: "room",
                      sequence: 0,
                      phase: "failed",
                      started_at: "2026-10-31T12:00:00Z",
                      completed_at: "2026-10-31T12:00:01Z",
                      evidence: {
                        reason_code: "conditions_false",
                        skip_id: SKIP_ID,
                      },
                    },
                  ],
                }),
              },
            }),
          },
          {
            method: "GET",
            path: `/v1/automations/${SECOND_AUTOMATION_ID}`,
            respond: () => ({
              body: automationFixture({ id: SECOND_AUTOMATION_ID }),
            }),
          },
          {
            method: "GET",
            path: `/v1/automations/${SECOND_AUTOMATION_ID}/history`,
            respond: () => ({ body: { items: [] } }),
          },
          {
            method: "GET",
            path: `/v1/automations/${SECOND_AUTOMATION_ID}/history/${SKIP_ID}`,
            respond: () =>
              problemResponse(404, "not_found", "History entry expired"),
          },
        ])
      );
      renderDetailPage(`${DETAIL_ROUTE}?history_id=${RUN_ID}`);
      const timeline = await screen.findByRole("table", {
        name: "Step execution timeline",
      });
      expect(within(timeline).getByText("conditions_false").textContent).toBe(
        "conditions_false"
      );
      fireEvent.click(within(timeline).getByRole("link", { name: SKIP_ID }));
      await screen.findByText(/History entry expired/u);
      expect(
        requestsFor(
          requests,
          "GET",
          `/v1/automations/${SECOND_AUTOMATION_ID}/history/${SKIP_ID}`
        )
      ).toHaveLength(1);
    });

    it("retains the child's failure separately from the parent failure", async () => {
      installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: HISTORY_ENTRY_PATTERN,
            respond: () => ({
              body: {
                kind: "run",
                run: runFixture({
                  status: "failed",
                  failure_code: "automation_run_failed",
                  snapshot: subAutomationDefinition(),
                  executions: [
                    {
                      kind: "sub_automation",
                      step_id: "room",
                      sequence: 0,
                      phase: "failed",
                      started_at: "2026-10-31T12:00:00Z",
                      completed_at: "2026-10-31T12:00:01Z",
                      evidence: {
                        child_run_id:
                          "arn_01920000-0000-7000-8000-00000000000d",
                        reason_code: "automation_run_failed",
                        child_status: "failed",
                        child_failure_code: "command_rejected",
                      },
                    },
                  ],
                }),
              },
            }),
          },
        ])
      );
      renderDetailPage(`${DETAIL_ROUTE}?history_id=${RUN_ID}`);
      const timeline = await screen.findByRole("table", {
        name: "Step execution timeline",
      });
      expect(
        within(timeline).getByText("Child outcome: failed · command_rejected")
          .textContent
      ).toBe("Child outcome: failed · command_rejected");
      expect(
        within(timeline).getByText("automation_run_failed").textContent
      ).toBe("automation_run_failed");
    });
  });

  describe("AutomationDetailPage manual Run", () => {
    it("completes manual Run and enablement actions after StrictMode effect replay", async () => {
      const requests = installAutomationFetch(
        detailRoutes([
          {
            method: "POST",
            path: RUNS_PATH,
            respond: () => ({ status: 202, body: runFixture() }),
          },
        ])
      );
      renderDetailPage(DETAIL_ROUTE, true);
      fireEvent.click(await screen.findByRole("button", { name: "Run now" }));
      await expect(screen.findByText("Step executions")).resolves.toBeTruthy();
      expect(requestsFor(requests, "POST", RUNS_PATH)).toHaveLength(1);
      const toggle = screen.getByRole("switch");
      // The Run renders before its definition refresh releases the switch.
      await waitFor(() => expect(toggle).not.toHaveProperty("disabled", true));
      fireEvent.click(toggle);
      await waitFor(() =>
        expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1)
      );
      await waitFor(() => expect(toggle).not.toHaveProperty("disabled", true));
    });

    it("starts exactly one Run, shows its outcome, and restarts history", async () => {
      const requests = installAutomationFetch(
        detailRoutes([
          {
            method: "POST",
            path: RUNS_PATH,
            respond: () => ({ status: 202, body: runFixture() }),
          },
        ])
      );
      renderDetailPage();

      fireEvent.click(
        await screen.findByRole(
          "button",
          { name: "Run now" },
          { timeout: 5000 }
        )
      );

      // The outcome is the admitted Run itself, including its verified Command.
      await expect(
        screen.findByText("Step executions")
      ).resolves.not.toBeNull();
      expect({
        commandHref: screen
          .getByRole("link", { name: VERIFIED_COMMAND_ID })
          .getAttribute("href"),
        hasTimeline:
          screen.queryByRole("table", {
            name: "Step execution timeline",
          }) !== null,
        succeeded: screen.getAllByText(/succeeded/u).length > 0,
      }).toStrictEqual({
        commandHref: `/commands?command_id=${VERIFIED_COMMAND_ID}`,
        hasTimeline: true,
        succeeded: true,
      });

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
              problemResponse(
                409,
                "automation_busy",
                "automation already has a running run"
              ),
          },
        ])
      );
      renderDetailPage();

      fireEvent.click(await screen.findByRole("button", { name: "Run now" }));

      const alert = await screen.findByRole("alert");
      expect(alert.textContent).toContain(
        "automation already has a running run"
      );
      expect(screen.getByText(/409 automation_busy/u)).not.toBeNull();
      expect(screen.getByText(/not retried/u)).not.toBeNull();
      // Non-idempotent: an ambiguous or rejected attempt is never repeated.
      expect(requestsFor(requests, "POST", RUNS_PATH)).toHaveLength(1);
      expect(screen.queryByText("Step executions")).toBeNull();
    });
  });

  describe("AutomationDetailPage enablement", () => {
    it("preserves nested delay nodes exactly in enablement replacement", async () => {
      const definition = delayDefinitionFixture();
      const requests = installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: DEFINITION_PATH,
            respond: () => ({ body: automationFixture({ definition }) }),
          },
        ])
      );
      renderDetailPage();
      fireEvent.click(await screen.findByRole("switch"));
      await waitFor(() =>
        expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1)
      );
      expect(
        requestsFor(requests, "PUT", DEFINITION_PATH)[0].body
      ).toStrictEqual({
        expected_revision: AUTOMATION_REVISION,
        definition: { ...definition, enabled: false },
      });
    });

    it("replaces the definition with its expected revision when toggled", async () => {
      const requests = installAutomationFetch(detailRoutes());
      renderDetailPage();

      fireEvent.click(await screen.findByRole("switch"));

      await waitFor(() =>
        expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1)
      );
      const [put] = requestsFor(requests, "PUT", DEFINITION_PATH);
      expect(put.body).toStrictEqual({
        expected_revision: AUTOMATION_REVISION,
        definition: { ...automationFixture().definition, enabled: false },
      });
      // The reloaded definition, not the optimistic click, is the truth shown.
      await waitFor(() =>
        expect(requestsFor(requests, "GET", DEFINITION_PATH)).toHaveLength(2)
      );
    });

    it("reports a revision conflict and reloads instead of overwriting", async () => {
      let definitionReads = 0;
      const requests = installAutomationFetch(
        detailRoutes([
          {
            method: "PUT",
            path: DEFINITION_PATH,
            respond: () =>
              problemResponse(
                409,
                "revision_conflict",
                "automation revision conflict"
              ),
          },
          {
            method: "GET",
            path: DEFINITION_PATH,
            respond: () => {
              definitionReads += 1;
              // Another writer bumped the revision before this click landed.
              return {
                body: automationFixture({
                  revision: definitionReads === 1 ? 3 : 4,
                }),
              };
            },
          },
        ])
      );
      renderDetailPage();

      fireEvent.click(await screen.findByRole("switch"));

      const notice = await screen.findByRole("alert");
      expect(notice.textContent).toContain("Revision conflict");
      expect(notice.textContent).toContain("not changed");
      expect(
        requestsFor(requests, "PUT", DEFINITION_PATH)[0].body
      ).toStrictEqual({
        expected_revision: 3,
        definition: { ...automationFixture().definition, enabled: false },
      });
      // Reloaded, not overwritten: the current revision and enablement are shown.
      await waitFor(() => expect(definitionReads).toBe(2));
      expect(screen.getByRole("switch").getAttribute("aria-checked")).toBe(
        "true"
      );
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
                  revision:
                    definitionReads === 1
                      ? AUTOMATION_REVISION
                      : AUTOMATION_REVISION + 1,
                  definition: {
                    ...automationFixture().definition,
                    enabled: definitionReads === 1,
                  },
                }),
              };
            },
          },
        ])
      );
      // Gate the reload (the definition's second read) behind a promise the test
      // releases, so the in-flight window is deterministic instead of timing-based.
      const servedFetch = globalThis.fetch;
      const reloadGate = Promise.withResolvers<undefined>();
      let gatedReads = 0;
      vi.stubGlobal(
        "fetch",
        async (input: RequestInfo | URL, init?: RequestInit) => {
          const method = (init?.method ?? "GET").toUpperCase();
          const path = new URL(String(input), "http://hearthd.test").pathname;
          if (method === "GET" && path === DEFINITION_PATH) {
            gatedReads += 1;
            if (gatedReads === 2) {
              await reloadGate.promise;
            }
          }
          return servedFetch(input, init);
        }
      );

      renderDetailPage();

      const toggle = await screen.findByRole("switch");
      expect(toggle.getAttribute("aria-checked")).toBe("true");

      fireEvent.click(toggle);
      await waitFor(() =>
        expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1)
      );
      // The reload started but has not answered: the switch must stay disabled.
      await waitFor(() => expect(gatedReads).toBe(2));
      expect(toggle.hasAttribute("disabled")).toBeTruthy();

      // A second click while the reload is pending must not resubmit.
      fireEvent.click(toggle);
      expect(requestsFor(requests, "PUT", DEFINITION_PATH)).toHaveLength(1);

      reloadGate.resolve(undefined);
      // The reloaded definition is the truth shown, and the switch works again.
      await waitFor(() =>
        expect(toggle.getAttribute("aria-checked")).toBe("false")
      );
      expect(toggle.hasAttribute("disabled")).toBeFalsy();
    });
  });

  describe("AutomationDetailPage scope changes", () => {
    it("drops the previous Automation's manual-Run outcome when the route changes", async () => {
      installAutomationFetch([
        {
          method: "POST",
          path: RUNS_PATH,
          respond: () => ({ status: 202, body: runFixture() }),
        },
        {
          method: "GET",
          path: /\/history$/u,
          respond: () => ({ body: { items: [] } }),
        },
        {
          method: "GET",
          path: `/v1/automations/${SECOND_AUTOMATION_ID}`,
          respond: () => ({
            body: automationFixture({
              id: SECOND_AUTOMATION_ID,
              revision: 1,
              definition: {
                ...automationFixture().definition,
                name: SECOND_AUTOMATION_NAME,
              },
            }),
          }),
        },
        ...detailRoutes(),
      ]);

      render(
        <MemoryRouter initialEntries={[DETAIL_ROUTE]}>
          <RouteChangeHarness />
        </MemoryRouter>
      );

      fireEvent.click(await screen.findByRole("button", { name: "Run now" }));
      await screen.findByText("Step executions");

      fireEvent.click(
        screen.getByRole("button", { name: "Open other automation" })
      );

      const names = await screen.findAllByText(SECOND_AUTOMATION_NAME);
      expect(names.length).toBeGreaterThan(0);
      // The admitted Run belonged to the previous Automation, so it is not shown here.
      expect(screen.queryByText("Step executions")).toBeNull();
    });
  });

  describe("AutomationDetailPage history", () => {
    it.each([
      { previous: {}, expected: "— (not reported)" },
      { previous: { previous_value: null }, expected: "null" },
    ])(
      "distinguishes retained previous value $expected from absence and renders previous comparisons",
      async ({ previous, expected }) => {
        const trigger = {
          id: "became-null",
          kind: "observation" as const,
          entity_id: ENTITY_ID,
          dispositions: ["applied" as const],
          comparisons: [
            { value_pointer: "", operator: "eq" as const, operand: null },
          ],
          ...("previous_value" in previous
            ? {
                previous_comparisons: [
                  { value_pointer: "", operator: "eq" as const, operand: null },
                ],
              }
            : {}),
        };
        installAutomationFetch(
          detailRoutes([
            {
              method: "GET",
              path: HISTORY_PATH,
              respond: () => ({ body: { items: [skipSummary()] } }),
            },
            {
              method: "GET",
              path: HISTORY_ENTRY_PATTERN,
              respond: () => ({
                body: {
                  kind: "skip",
                  skip: skipFixture({
                    matched_triggers: [trigger],
                    cause: {
                      kind: "device_fact",
                      fact: {
                        fact_id: "fct_01920000-0000-7000-8000-000000000006",
                        family: "observation",
                        entity_id: ENTITY_ID,
                        observation_id:
                          "obs_01920000-0000-7000-8000-000000000007",
                        disposition: "applied",
                        value: null,
                        emitted_at: "2026-02-01T10:05:00Z",
                        ...previous,
                      },
                    },
                  }),
                },
              }),
            },
          ])
        );
        renderDetailPage();
        fireEvent.click(await screen.findByRole("button", { name: SKIP_ID }));
        await screen.findByText("Previous value");
        expect(
          screen.getByText("Previous value").nextElementSibling?.textContent
        ).toBe(expected);
        expect(
          screen.getByText("Reported value").nextElementSibling?.textContent
        ).toBe("null");
        expect(
          screen.getByText("Observation id").nextElementSibling?.textContent
        ).toBe("obs_01920000-0000-7000-8000-000000000007");
        expect(
          screen.queryByText(
            "previous_value" in previous
              ? /previous comparisons eq null/u
              : /previous comparisons/u
          ) !== null
        ).toBe("previous_value" in previous);
        expect(screen.queryByText("Event name")).toBeNull();
      }
    );

    it("shows selected null separately from missing State selection and unknown reasons", async () => {
      const snapshot = {
        id: "state-check",
        kind: "all" as const,
        children: [
          {
            id: "known-null",
            kind: "entity_state" as const,
            entity_id: ENTITY_ID,
            value_pointer: "",
            operator: "eq" as const,
            operand: null,
          },
          {
            id: "missing-state",
            kind: "entity_state" as const,
            entity_id: UNSELECTED_ENTITY_ID,
            value_pointer: "",
            operator: "eq" as const,
            operand: true,
          },
        ],
      };
      const decision: AutomationConditionDecision = {
        mode: "evaluated",
        bypass_requested: false,
        snapshot,
        evaluation: {
          evaluated_at: "2026-02-01T10:05:00Z",
          result: "unknown",
          nodes: [
            {
              id: "known-null",
              kind: "entity_state",
              result: "true",
              selected_value: null,
              observation_id: "obs_01920000-0000-7000-8000-000000000007",
              observed_at: "2026-02-01T10:04:59Z",
            },
            {
              id: "missing-state",
              kind: "entity_state",
              result: "unknown",
              unknown_reason: "state_missing",
            },
          ],
        },
      };
      installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: HISTORY_PATH,
            respond: () => ({
              body: {
                items: [
                  {
                    ...skipSummary(),
                    reason: "conditions_unknown",
                    cause: { kind: "manual" },
                    condition_mode: "evaluated",
                    condition_result: "unknown",
                  },
                ],
              },
            }),
          },
          {
            method: "GET",
            path: HISTORY_ENTRY_PATTERN,
            respond: () => ({
              body: {
                kind: "skip",
                skip: skipFixture({
                  reason: "conditions_unknown",
                  condition_decision: decision,
                  cause: { kind: "manual" },
                  matched_triggers: [],
                }),
              },
            }),
          },
        ])
      );
      renderDetailPage();
      fireEvent.click(await screen.findByRole("button", { name: SKIP_ID }));
      await expect(
        screen.findByText("Admission Conditions: unknown")
      ).resolves.not.toBeNull();
      expect(
        screen.getByText(
          /known-null: true · selected value null · observation obs_/u
        )
      ).not.toBeNull();
      expect(
        screen.getByText("missing-state: unknown · state_missing").textContent
      ).not.toContain("selected value");
    });

    it("renders delay-only definitions with exact durations and no command positions or Entity references", async () => {
      const definition = delayDefinitionFixture();
      definition.steps.push(
        { id: "second-wait", kind: "delay", duration_ms: 1000 },
        { id: "hour-wait", kind: "delay", duration_ms: 7_200_000 }
      );
      const requests = installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: DEFINITION_PATH,
            respond: () => ({ body: automationFixture({ definition }) }),
          },
        ])
      );
      renderDetailPage();
      await expect(
        screen.findByText("8 defined Steps · 0 commands")
      ).resolves.not.toBeNull();
      expect(screen.getByText("Commands").nextElementSibling?.textContent).toBe(
        "0"
      );
      const outline = screen.getByRole("list", {
        name: "Step definition outline",
      });
      for (const text of [
        "wait · delay · 5 minutes",
        "precise-wait · delay · 1001 milliseconds",
        "short-wait · delay · 1 millisecond",
        "long-wait · delay · 1 day",
        "second-wait · delay · 1 second",
        "hour-wait · delay · 2 hours",
      ]) {
        expect(
          within(outline)
            .getAllByRole("listitem")
            .map((row) => row.textContent)
        ).toContain(text);
      }
      expect(within(outline).queryByText(/position/u)).toBeNull();
      expect(screen.queryByRole("link")).toBeNull();
      expect(requestsFor(requests, "GET", "/v1/entities")).toHaveLength(0);
    });

    it.each([
      ["running", undefined, "No wait resumes"],
      ["succeeded", undefined, "No wait resumes"],
      ["interrupted", "core_stopping", "core_stopping"],
      ["interrupted", "core_restarted", "core_restarted"],
      ["interrupted", "executor_fault", "executor_fault"],
    ] as const)(
      "renders %s delay evidence with reason %s in reached order",
      async (status, failure_code, reason) => {
        const started_at = "2026-10-04T12:00:00Z";
        const due_at = "2026-10-04T12:05:00Z";
        const completed_at = "2026-10-04T12:04:59Z";
        const run = runFixture({
          status,
          completed_at: status === "running" ? undefined : completed_at,
          failure_code,
          snapshot: delayDefinitionFixture(),
          executions: [
            {
              sequence: 1,
              kind: "delay",
              step_id: "precise-wait",
              started_at,
              ...(status === "running"
                ? { phase: "running" }
                : { phase: status, completed_at }),
              evidence: { reason_code: failure_code },
            },
            {
              sequence: 0,
              kind: "delay",
              step_id: "wait",
              phase: "succeeded",
              started_at,
              completed_at: "2026-10-04T12:05:01Z",
              evidence: {},
            },
          ],
        });
        installAutomationFetch(
          detailRoutes([
            {
              method: "GET",
              path: HISTORY_PATH,
              respond: () => ({ body: { items: [runSummary()] } }),
            },
            {
              method: "GET",
              path: HISTORY_ENTRY_PATTERN,
              respond: () => ({ body: { kind: "run", run } }),
            },
          ])
        );
        renderDetailPage();
        fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
        const table = await screen.findByRole("table", {
          name: "Step execution timeline",
        });
        const rows = within(table).getAllByRole("row").slice(1);
        expect({
          headers: within(table)
            .getAllByRole("columnheader")
            .map((cell) => cell.textContent),
          waitCells: within(rows[0])
            .getAllByRole("cell")
            .slice(0, 4)
            .map((cell) => cell.textContent),
        }).toStrictEqual({
          headers: [
            "Sequence",
            "Step id",
            "Kind",
            "Phase",
            "Evidence",
            "Started",
            "Completed",
          ],
          waitCells: ["0", "wait", "delay", "succeeded"],
        });
        for (const text of ["5 minutes", new Date(due_at).toISOString()]) {
          expect(rows[0].textContent).toContain(text);
        }
        expect(
          within(rows[1])
            .getAllByRole("cell")
            .slice(0, 4)
            .map((cell) => cell.textContent)
        ).toStrictEqual(["1", "precise-wait", "delay", status]);
        for (const text of [
          "1001 milliseconds",
          "2026-10-04T12:00:01.001Z",
          reason,
        ]) {
          expect(rows[1].textContent).toContain(text);
        }
        expect(
          within(rows[1])
            .getAllByRole("cell")
            .slice(5)
            .map((cell) => cell.textContent)
        ).toStrictEqual([
          started_at,
          status === "running" ? "—" : completed_at,
        ]);
      }
    );

    it.each([
      ["running", undefined, "No wait resumes"],
      ["succeeded", undefined, "No wait resumes"],
      ["interrupted", "core_stopping", "core_stopping"],
      ["interrupted", "core_restarted", "core_restarted"],
      ["interrupted", "executor_fault", "executor_fault"],
    ] as const)(
      "annotates %s delay evidence with reason %s without links or positions",
      async (status, failure_code, reason) => {
        const started_at = "2026-10-04T12:00:00Z";
        const completed_at = "2026-10-04T12:04:59Z";
        const run = runFixture({
          status,
          completed_at: status === "running" ? undefined : completed_at,
          failure_code,
          snapshot: delayDefinitionFixture(),
          executions: [
            {
              sequence: 1,
              kind: "delay",
              step_id: "precise-wait",
              started_at,
              ...(status === "running"
                ? { phase: "running" }
                : { phase: status, completed_at }),
              evidence: { reason_code: failure_code },
            },
            {
              sequence: 0,
              kind: "delay",
              step_id: "wait",
              phase: "succeeded",
              started_at,
              completed_at: "2026-10-04T12:05:01Z",
              evidence: {},
            },
          ],
        });
        installAutomationFetch(
          detailRoutes([
            {
              method: "GET",
              path: HISTORY_PATH,
              respond: () => ({ body: { items: [runSummary()] } }),
            },
            {
              method: "GET",
              path: HISTORY_ENTRY_PATTERN,
              respond: () => ({ body: { kind: "run", run } }),
            },
          ])
        );
        renderDetailPage();
        fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
        const table = await screen.findByRole("table", {
          name: "Step execution timeline",
        });
        const rows = within(table).getAllByRole("row").slice(1);
        expect(rows[1].textContent).toContain(reason);
        expect(
          screen.getByText(/Expected due times are diagnostic UTC timestamps/u)
        ).not.toBeNull();
        expect(within(table).getAllByText(/No wait resumes/u)).toHaveLength(2);
        expect(within(table).queryByRole("link")).toBeNull();
        const [, retained] = screen.getAllByRole("list", {
          name: "Step definition outline",
        });
        expect(
          within(retained)
            .getAllByRole("listitem")
            .map((row) => row.textContent)
        ).toContain("precise-wait · delay · 1001 milliseconds");
      }
    );

    it("says no delay was reached even when the snapshot defines delays", async () => {
      installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: HISTORY_PATH,
            respond: () => ({ body: { items: [runSummary()] } }),
          },
          {
            method: "GET",
            path: HISTORY_ENTRY_PATTERN,
            respond: () => ({
              body: {
                kind: "run",
                run: runFixture({
                  snapshot: delayDefinitionFixture(),
                  executions: [],
                }),
              },
            }),
          },
        ])
      );
      renderDetailPage();
      fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
      const table = await screen.findByRole("table", {
        name: "Step execution timeline",
      });
      expect(
        within(table).getByText("This Run has no durably reached Steps.")
      ).not.toBeNull();
      expect(screen.getByText("6 defined Steps · 0 commands")).not.toBeNull();
    });

    it("keeps nested Command definitions and links without execution positions", async () => {
      const definition = delayDefinitionFixture();
      const [, choose] = definition.steps;
      if (choose.kind !== "choose" || !choose.default) {
        throw new Error("missing default arm in fixture");
      }
      choose.default.push({
        id: "finish",
        kind: "command",
        entity_id: ENTITY_ID,
        operation: "set",
        parameters: { value: false },
      });
      definition.steps.unshift(automationFixture().definition.steps[0]);
      installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: DEFINITION_PATH,
            respond: () => ({ body: automationFixture({ definition }) }),
          },
        ])
      );
      renderDetailPage();
      await expect(
        screen.findByText("8 defined Steps · 2 commands")
      ).resolves.not.toBeNull();
      const outline = screen.getByRole("list", {
        name: "Step definition outline",
      });
      const commands = within(outline)
        .getAllByRole("listitem")
        .filter((row) => row.textContent?.includes(" · command"));
      expect(
        commands.map((row) => row.textContent?.split(" · command")[0])
      ).toStrictEqual(["turn_on", "finish"]);
      expect(within(outline).queryByText(/position/u)).toBeNull();
      const links = await within(outline).findAllByRole("link", {
        name: ENTITY_NAME,
      });
      expect(links).toHaveLength(2);
      expect(links.map((link) => link.getAttribute("href"))).toStrictEqual([
        `/entities/${ENTITY_ID}`,
        `/entities/${ENTITY_ID}`,
      ]);
    });

    it("shows committed branch selections without claiming child completion", async () => {
      const decisions = await openRetainedBranchRun();
      const rows = within(decisions).getAllByRole("row").slice(1);
      expect(rows.map((row) => row.textContent)).toStrictEqual([
        expect.stringContaining(
          "0route-buttonchoosesucceededSelection: Alternative press"
        ),
        expect.stringContaining("1set-levelifsucceededSelection: then"),
      ]);
      expect(
        within(decisions).getAllByText(
          /Selection only; does not establish child completion/u
        )
      ).toHaveLength(2);
      for (const text of ["core_stopping", "run: interrupted"]) {
        expect(screen.getByText(text)).not.toBeNull();
      }
      expect({
        rows: rows.length,
        links: within(decisions).queryAllByRole("link").length,
        dim: within(decisions).queryByText("dim"),
        succeeded: within(decisions).getAllByText("succeeded").length,
      }).toStrictEqual({ rows: 2, links: 0, dim: null, succeeded: 2 });
    });

    it("renders retained nested definitions and their Entity links", async () => {
      await openRetainedBranchRun();
      const outlines = screen.getAllByRole("list", {
        name: "Step definition outline",
      });
      const [, retained] = outlines;
      await within(retained).findByRole("link", { name: "Office brightness" });
      expect(
        within(retained)
          .getByRole("link", { name: "Unselected sensor" })
          .getAttribute("href")
      ).toBe(`/entities/${UNSELECTED_ENTITY_ID}`);
      await expect(
        screen.findByRole("link", { name: "Admission readiness" })
      ).resolves.not.toBeNull();
      for (const arm of [
        "Then",
        "Else",
        "Alternative press",
        "Alternative other",
        "Default",
      ]) {
        expect(within(retained).getByText(arm)).not.toBeNull();
      }
      const commandRows = within(retained)
        .getAllByRole("listitem")
        .filter((row) => row.textContent?.includes(" · command"));
      expect(
        commandRows.map((row) => row.textContent?.split(" · command")[0])
      ).toStrictEqual(["dim", "brighten", "off", "default-level", "finish"]);
      expect(screen.getByText("7 defined Steps · 5 commands")).not.toBeNull();
    });

    it("renders retained branch evidence details", async () => {
      const decisions = await openRetainedBranchRun();
      fireEvent.click(within(decisions).getByText("Evidence for set-level"));
      const evidence = within(decisions).getByText(/"selected_value": 120/u);
      expect(evidence.closest("details")?.open).toBeTruthy();
      for (const text of [
        '"observed_at": "2026-10-03T11:59:58Z"',
        '"matched_trigger_ids"',
      ]) {
        expect(evidence.textContent).toContain(text);
      }
      expect(
        within(decisions).getByText(
          "matched: true · matched triggers button_press"
        )
      ).not.toBeNull();
      expect(
        within(decisions).getByText(
          /low: false · selected value 120 · observation obs_/u
        )
      ).not.toBeNull();
    });

    it("renders all nested current-definition references and defined counts", async () => {
      installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: DEFINITION_PATH,
            respond: () => ({
              body: automationFixture({
                definition: branchingDefinitionFixture(),
              }),
            }),
          },
        ])
      );
      renderDetailPage();
      await expect(
        screen.findByText("7 defined Steps · 5 commands")
      ).resolves.not.toBeNull();
      expect(
        screen.getByText("Defined Steps").nextElementSibling?.textContent
      ).toBe("7");
      expect(screen.getByText("Commands").nextElementSibling?.textContent).toBe(
        "5"
      );
      const outline = screen.getByRole("list", {
        name: "Step definition outline",
      });
      expect(
        within(outline).getByRole("link", { name: UNSELECTED_ENTITY_ID })
      ).not.toBeNull();
    });

    it.each(["run", "skip"] as const)(
      "renders a cron expression and schedule %s without an empty Entity link",
      async (kind) => {
        const trigger = {
          id: "morning",
          kind: "cron" as const,
          expression: "0 7 * * MON-FRI",
        };
        const definition = {
          ...automationFixture().definition,
          triggers: [trigger],
        };
        const entryId = kind === "run" ? RUN_ID : SKIP_ID;
        installAutomationFetch(
          detailRoutes([
            {
              method: "GET",
              path: DEFINITION_PATH,
              respond: () => ({ body: automationFixture({ definition }) }),
            },
            {
              method: "GET",
              path: HISTORY_PATH,
              respond: () => ({
                body: {
                  items: [
                    {
                      ...(kind === "run" ? runSummary() : skipSummary()),
                      cause: { kind: "schedule" },
                    },
                  ],
                },
              }),
            },
            {
              method: "GET",
              path: HISTORY_ENTRY_PATTERN,
              respond: () => ({
                body:
                  kind === "run"
                    ? {
                        kind,
                        run: runFixture({
                          cause: { kind: "schedule" },
                          matched_trigger_ids: ["morning"],
                          snapshot: definition,
                        }),
                      }
                    : {
                        kind,
                        skip: skipFixture({
                          cause: { kind: "schedule" },
                          matched_triggers: [trigger],
                        }),
                      },
              }),
            },
          ])
        );
        renderDetailPage();

        await expect(
          screen.findByText(trigger.expression)
        ).resolves.not.toBeNull();
        fireEvent.click(await screen.findByRole("button", { name: entryId }));
        await expect(
          screen.findByText("cause: schedule")
        ).resolves.not.toBeNull();
        expect(screen.getAllByText(trigger.expression)).toHaveLength(2);
        expect(screen.queryByText("Fact id")).toBeNull();
        // Steps still have their real Entity link. Cron must not add an empty one.
        for (const link of screen.getAllByRole("link")) {
          expect(link.getAttribute("href")).not.toBe("/entities/undefined");
          expect(link.getAttribute("href")).not.toBe("/entities/");
        }
        expect(screen.queryByText(/dispositions/u)).toBeNull();
      }
    );

    it("shows held-State duration and comparisons in the definition and matched Trigger", async () => {
      const heldTrigger = {
        id: "light_on",
        kind: "held_state" as const,
        entity_id: ENTITY_ID,
        comparisons: [
          { value_pointer: "", operator: "eq" as const, operand: true },
        ],
        for_seconds: 60,
      };
      installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: DEFINITION_PATH,
            respond: () => ({
              body: automationFixture({
                definition: {
                  ...automationFixture().definition,
                  triggers: [heldTrigger],
                },
              }),
            }),
          },
          {
            method: "GET",
            path: HISTORY_PATH,
            respond: () => ({ body: { items: [skipSummary()] } }),
          },
          {
            method: "GET",
            path: HISTORY_ENTRY_PATTERN,
            respond: () => ({
              body: {
                kind: "skip",
                skip: skipFixture({
                  matched_triggers: [heldTrigger],
                  cause: {
                    kind: "held_state",
                    evidence: {
                      trigger_id: "light_on",
                      started_at: "2026-02-01T10:04:00Z",
                      due_at: "2026-02-01T10:05:00Z",
                    },
                  },
                }),
              },
            }),
          },
        ])
      );
      renderDetailPage();

      await expect(screen.findByText("60 seconds")).resolves.not.toBeNull();
      fireEvent.click(await screen.findByRole("button", { name: SKIP_ID }));
      await screen.findByText("Matched Triggers");
      expect(screen.getAllByText("60 seconds")).toHaveLength(2);
      expect(screen.getAllByText("eq true")).toHaveLength(2);
      expect({
        heldTrigger:
          screen.getByText("Held trigger").nextElementSibling?.textContent,
        holdStarted:
          screen.getByText("Hold started").nextElementSibling?.textContent,
        holdDue: screen.getByText("Hold due").nextElementSibling?.textContent,
      }).toStrictEqual({
        heldTrigger: "light_on",
        holdStarted: "2026-02-01T10:04:00Z",
        holdDue: "2026-02-01T10:05:00Z",
      });
      expect({
        factId: screen.queryByText("Fact id"),
        dispositions: screen.queryByText(/dispositions/u),
      }).toStrictEqual({ factId: null, dispositions: null });
    });

    it("pages with an encoded cursor and loads a selected Run's executions", async () => {
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
        ])
      );
      renderDetailPage();

      // Selecting an entry loads its recorded detail in place, with no new page route.
      fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
      await screen.findByText("Step executions");
      expect(
        requestsFor(requests, "GET", `${HISTORY_PATH}/${RUN_ID}`)
      ).toHaveLength(1);
      // A Trigger reference is labeled with the resolved Entity name.
      expect({
        command: screen.queryByText(VERIFIED_COMMAND_ID) !== null,
        satisfied: screen.getAllByText(/satisfied/u).length > 0,
        entityName: screen.getAllByText(ENTITY_NAME).length > 0,
      }).toStrictEqual({ command: true, satisfied: true, entityName: true });

      fireEvent.click(screen.getByRole("button", { name: "Next page" }));

      await screen.findByRole("button", { name: SKIP_ID });
      expect(
        requestsFor(requests, "GET", HISTORY_PATH).map((request) => request.url)
      ).toStrictEqual([
        `${HISTORY_PATH}?limit=50`,
        // The cursor round-trips percent-encoded: `+` unencoded would arrive as a space.
        `${HISTORY_PATH}?limit=50&cursor=page%2F1%2B%3D`,
      ]);
      expect(screen.queryByRole("button", { name: RUN_ID })).toBeNull();
    });

    it("renders a selected Skip's reason, Fact, and matched Trigger snapshots", async () => {
      const requests = installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: HISTORY_PATH,
            respond: () => ({ body: { items: [skipSummary()] } }),
          },
          {
            method: "GET",
            path: HISTORY_ENTRY_PATTERN,
            respond: () => ({ body: { kind: "skip", skip: skipFixture() } }),
          },
        ])
      );
      renderDetailPage();

      // Nothing is fetched for an entry until one is selected.
      expect(
        requestsFor(requests, "GET", `${HISTORY_PATH}/${SKIP_ID}`)
      ).toHaveLength(0);

      fireEvent.click(await screen.findByRole("button", { name: SKIP_ID }));

      await screen.findByText("Matched Triggers");
      expect(
        requestsFor(requests, "GET", `${HISTORY_PATH}/${SKIP_ID}`)
      ).toHaveLength(1);
      expect({
        running: screen.queryByText(/already had a running Run/u) !== null,
        busy: screen.getAllByText(/automation_busy/u).length > 0,
        trigger: screen.getAllByText("single_press").length > 0,
        factNote:
          screen.queryByText(/an Entity Event Fact carries no value/u) !== null,
      }).toStrictEqual({
        running: true,
        busy: true,
        trigger: true,
        factNote: true,
      });
    });

    it.each([
      ["conditions_false", "Conditions were false, so no Run started."],
      [
        "conditions_unknown",
        "Conditions could not be confirmed, so no Run started.",
      ],
    ] as const)(
      "explains a held-State %s Skip without Fact evidence",
      async (reason, explanation) => {
        installAutomationFetch(
          detailRoutes([
            {
              method: "GET",
              path: HISTORY_PATH,
              respond: () => ({ body: { items: [skipSummary()] } }),
            },
            {
              method: "GET",
              path: HISTORY_ENTRY_PATTERN,
              respond: () => ({
                body: {
                  kind: "skip",
                  skip: skipFixture({ reason, cause: { kind: "manual" } }),
                },
              }),
            },
          ])
        );
        renderDetailPage();

        fireEvent.click(await screen.findByRole("button", { name: SKIP_ID }));
        await expect(screen.findByText(explanation)).resolves.not.toBeNull();
        expect(screen.queryByText(/Fact that was already too old/u)).toBeNull();
        expect(screen.queryByText("Fact id")).toBeNull();
      }
    );

    it("refetches the selected entry's detail when history is refreshed", async () => {
      let entryReads = 0;
      const requests = installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: HISTORY_PATH,
            respond: () => ({ body: { items: [runSummary()] } }),
          },
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
                        executions: [
                          {
                            sequence: 0,
                            step_id: "turn_on",
                            kind: "command",
                            phase: "running",
                            started_at: "2026-02-01T10:00:00.100Z",
                            evidence: {
                              command_id: VERIFIED_COMMAND_ID,
                              correlation_id: "cor_reserved",
                            },
                          },
                        ],
                      }),
                    },
                  }
                : { body: { kind: "run", run: runFixture() } };
            },
          },
        ])
      );
      renderDetailPage();

      fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
      await screen.findByText("Step executions");
      // First read: the Step is still running and has no verified Command yet.
      expect({
        verified: screen.queryByText(VERIFIED_COMMAND_ID),
        link: screen.queryByRole("link", { name: VERIFIED_COMMAND_ID }),
        reserved: screen.getByText(`Reserved Command: ${VERIFIED_COMMAND_ID}`)
          .textContent,
        running: screen.getAllByText("running").length > 0,
      }).toStrictEqual({
        verified: null,
        link: null,
        reserved: `Reserved Command: ${VERIFIED_COMMAND_ID}`,
        running: true,
      });

      fireEvent.click(screen.getByRole("button", { name: "Refresh history" }));

      // The summaries and the selected entry's detail are both refetched, so the
      // visible Step status is the latest one instead of the stale first read.
      await screen.findByText(VERIFIED_COMMAND_ID);
      expect({
        history: requestsFor(requests, "GET", HISTORY_PATH).length,
        entry: requestsFor(requests, "GET", `${HISTORY_PATH}/${RUN_ID}`).length,
        entryReads,
      }).toStrictEqual({ history: 2, entry: 2, entryReads: 2 });
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
        ])
      );
      renderDetailPage();

      fireEvent.click(await screen.findByRole("button", { name: RUN_ID }));
      await screen.findByText("Step executions");

      fireEvent.click(screen.getByRole("button", { name: "Next page" }));

      await screen.findByRole("button", { name: SKIP_ID });
      // The selection belonged to the page being left, so its detail is dropped
      // instead of staying visible under the new page's rows.
      expect(screen.queryByText("Step executions")).toBeNull();
      expect(screen.queryByText(VERIFIED_COMMAND_ID)).toBeNull();
      expect(
        requestsFor(requests, "GET", `${HISTORY_PATH}/${RUN_ID}`)
      ).toHaveLength(1);
    });

    it("shows retained history when the current definition is gone", async () => {
      const requests = installAutomationFetch(
        detailRoutes([
          {
            method: "GET",
            path: DEFINITION_PATH,
            respond: () =>
              problemResponse(404, "not_found", "automation not found"),
          },
          {
            method: "GET",
            path: HISTORY_PATH,
            respond: () => ({ body: { items: [skipSummary()] } }),
          },
          {
            method: "GET",
            path: HISTORY_ENTRY_PATTERN,
            respond: () => ({ body: { kind: "skip", skip: skipFixture() } }),
          },
        ])
      );
      renderDetailPage();

      // The definition read failed and is reported, but history is read from its
      // own endpoint, which the backend keeps serving for a deleted Automation.
      const alert = await screen.findByRole("alert");
      expect(alert.textContent).toContain("automation not found");

      fireEvent.click(await screen.findByRole("button", { name: SKIP_ID }));
      await screen.findByText("Matched Triggers");
      expect(screen.getByText(/already had a running Run/u)).not.toBeNull();

      // Views driven only by the current definition stay gated on it.
      expect({
        triggers: screen.queryByText("Triggers"),
        steps: screen.queryByText("Steps"),
        switch: screen.queryByRole("switch"),
        runNow: screen.queryByRole("button", { name: "Run now" }),
        rawJson: screen.queryByText("Raw automation JSON"),
        definitionReads: requestsFor(requests, "GET", DEFINITION_PATH).length,
      }).toStrictEqual({
        triggers: null,
        steps: null,
        switch: null,
        runNow: null,
        rawJson: null,
        definitionReads: 1,
      });
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
          {
            method: "GET",
            path: HISTORY_PATH,
            respond: () => ({ body: { items: [runSummary()] } }),
          },
        ])
      );
      renderDetailPage();

      // The definition loaded, so its controls are actionable.
      expect({
        hasSwitch: (await screen.findByRole("switch")) !== null,
        runNow: screen.queryByRole("button", { name: "Run now" }) !== null,
        rawJson: screen.queryByText("Raw automation JSON") !== null,
      }).toStrictEqual({ hasSwitch: true, runNow: true, rawJson: true });

      fireEvent.click(screen.getByRole("button", { name: "Refresh" }));

      // The refresh reports the Automation gone: the retained definition must not
      // keep rendering its header, definition, actions, or raw JSON.
      await waitFor(() => expect(definitionReads).toBe(2));
      await waitFor(() => expect(screen.queryByRole("switch")).toBeNull());
      // History is read from its own endpoint and stays independently available.
      const historyButton = await screen.findByRole("button", { name: RUN_ID });
      expect({
        runNow: screen.queryByRole("button", { name: "Run now" }),
        triggers: screen.queryByText("Triggers"),
        steps: screen.queryByText("Steps"),
        rawJson: screen.queryByText("Raw automation JSON"),
        history: historyButton !== null,
        definitionReads: requestsFor(requests, "GET", DEFINITION_PATH).length,
      }).toStrictEqual({
        runNow: null,
        triggers: null,
        steps: null,
        rawJson: null,
        history: true,
        definitionReads: 2,
      });
    });
  });
});
