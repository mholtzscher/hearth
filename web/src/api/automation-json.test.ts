import { afterEach, describe, expect, it, vi } from "vitest";
import { apiFetch, ApiError } from "./client.ts";
import { automationFetch, automationJsonFieldText, automationJsonText, parseAutomationResponse, serializeAutomationEnablement } from "./automation-json.ts";
import type { Automation, AutomationChooseStep, AutomationFunctionStep, AutomationHistoryEntry, AutomationIfStep, AutomationRun, Collection } from "./types.ts";

it("accepts logical failure targets and rejects obsolete or conflicting history targets", () => {
  const step = (kind: string, failure: object, extra = {}) => JSON.stringify({ executions: [{ kind, evidence: { value_failure: failure, ...extra } }] });
  for (const source of [
    step("command", { field: "parameters", failure_code: "expression_missing" }),
    step("notification", { failure_code: "resolved_input_invalid" }),
    step("set_variables", { field: "assignments.Room_1", failure_code: "expression_limit" }),
    step("if", { condition_id: "check", failure_code: "expression_missing" }, { failure_code: "expression_missing" }),
    step("choose", { failure_code: "run_data_limit" }, { failure_code: "run_data_limit" }),
    step("read_state", { failure_code: "read_state_read_failed" }),
    step("function", { failure_code: "expression_limit" }, { invocation_outcome: "succeeded" }),
    JSON.stringify({ value_initialization: { kind: "failed", failure: { field: "variables.room", failure_code: "expression_missing" } } }),
  ]) expect(() => parseAutomationResponse(source)).not.toThrow();
  for (const source of [
    step("command", { path: "/steps/0/parameters_expr", failure_code: "expression_missing" }),
    step("command", { field: "parameters", source_start: 0, source_end: 1, failure_code: "expression_missing" }),
    step("command", { field: "parameters_expr", failure_code: "expression_missing" }),
    step("command", { field: "arguments", failure_code: "expression_missing" }),
    step("set_variables", { field: "variables.x", failure_code: "expression_limit" }),
    JSON.stringify({ value_initialization: { kind: "failed", failure: { field: "parameters", failure_code: "expression_limit" } } }),
    step("set_variables", { field: "assignments.__hearth_hidden", failure_code: "expression_missing" }),
    step("if", { field: "title", condition_id: "check", failure_code: "expression_missing" }, { failure_code: "expression_missing" }),
    step("if", { failure_code: "expression_missing" }, { failure_code: "expression_missing" }),
    step("if", { condition_id: "check", failure_code: "expression_missing" }, { failure_code: "expression_limit" }),
    step("read_state", { field: "parameters", failure_code: "read_state_read_failed" }),
    step("function", { field: "arguments", failure_code: "expression_limit" }, { invocation_outcome: "succeeded" }),
    JSON.stringify({ value_initialization: { kind: "failed", failure: { condition_id: "check", failure_code: "expression_missing" } } }),
  ]) expect(() => parseAutomationResponse(source)).toThrow("Invalid Automation value failure");
});

// Synthetic HTTP JSON, deliberately never constructed from JavaScript numbers.
const argumentsSource = String.raw`{"large":9007199254740993,"fraction":123.456789012345678901234,"exponents":[1E+003,1e-999,-2.500e+40,-0],"escaped":"\"9007199254740993\" \\ \u0031 [1e3],{}:","empty":[{},[]],"kind":"function","expr":"vendor data","arguments":{"vendor":9007199254740995},"steps":[{"kind":"function"}]}`;
const step = (id: string) => `{"id":"${id}","kind":"function","adapter_id":"example","function_key":"refresh","contract_fingerprint":"sha256-v1:${"a".repeat(64)}","arguments":${argumentsSource}}`;
const condition = '{"id":"matched","kind":"trigger","trigger_ids":["morning"]}';
const steps = `[${step("root")},{"id":"if","kind":"if","conditions":${condition},"then":[${step("then")}],"else":[${step("else")}]},{"id":"choose","kind":"choose","branches":[{"id":"branch","conditions":${condition},"steps":[${step("branch")}]}],"default":[${step("default")}]}]`;
const definition = `{"name":"Exact source","enabled" : true,"triggers":[{"id":"morning","kind":"cron","expression":"0 7 * * *"}],"steps":${steps}}`;
const wire = `{"id":"aut_example","revision":3,"definition":${definition}}`;

afterEach(() => vi.unstubAllGlobals());

describe("Automation argument source preservation", () => {
  it("retains exact selected State and Fact values in both admission and branch evidence", () => {
    const node = '{"id":"c","kind":"entity_state","result":"true","selected_value":{"n":9007199254740993},"observation_id":"obs","observed_at":"now"}';
    const evaluation = `{"evaluated_at":"now","result":"true","nodes":[${node}]}`;
    const source = `{"kind":"run","run":{"cause":{"kind":"device_fact","fact":{"family":"observation","value":9223372036854775807,"previous_value":-9223372036854775808}},"condition_decision":{"mode":"evaluated","evaluation":${evaluation}},"executions":[{"kind":"if","evidence":{"outcome":"then","evaluations":[{"evaluation":${evaluation}}]}}]}}`;
    const history = parseAutomationResponse<AutomationHistoryEntry>(source);
    if (history.kind !== "run" || history.run.cause.kind !== "device_fact" || history.run.condition_decision.mode !== "evaluated" || history.run.executions[0].kind !== "if") throw new Error("Bad fixture");
    expect(automationJsonFieldText(history.run.cause.fact, "value")).toBe("9223372036854775807");
    expect(automationJsonFieldText(history.run.cause.fact, "previous_value")).toBe("-9223372036854775808");
    expect(automationJsonFieldText(history.run.condition_decision.evaluation.nodes[0], "selected_value")).toContain("9007199254740993");
    const evidence = history.run.executions[0].evidence;
    if (evidence.outcome !== "then") throw new Error("Bad branch");
    expect(automationJsonFieldText(evidence.evaluations[0].evaluation.nodes[0], "selected_value")).toContain("9007199254740993");
  });
  it.each(["9007199254740993", "-9223372036854775808", "9223372036854775807"])("preserves scalar and nested execution output %s", (token) => {
    const source = `{"snapshot":${definition},"data_evidence":"recorded","value_initialization":{"kind":"initialized","recorded_at":"now","values":{"n":${token}}},"executions":[{"sequence":0,"step_id":"scalar","kind":"function","phase":"succeeded","started_at":"now","completed_at":"now","evidence":{"invocation_id":"inv","catalog_revision":"rev","invocation_outcome":"succeeded","output":${token}}},{"sequence":1,"step_id":"assign","kind":"set_variables","phase":"succeeded","started_at":"now","completed_at":"now","evidence":{"values":{"nested":[${token}]}}},{"sequence":2,"step_id":"nested","kind":"function","phase":"succeeded","started_at":"now","completed_at":"now","evidence":{"invocation_id":"inv2","catalog_revision":"rev","invocation_outcome":"succeeded","output":{"nested":[${token}]}}}]}`;
    const history = parseAutomationResponse<AutomationHistoryEntry>(`{"kind":"run","run":${source}}`);
    if (history.kind !== "run" || !history.run.value_initialization) throw new Error("Bad fixture");
    expect(automationJsonText(history.run.value_initialization)).toContain(`"n": ${token}`);
    expect(automationJsonText(history.run.executions[0])).toContain(`"output": ${token}`);
    for (const execution of history.run.executions.slice(1)) expect(automationJsonText(execution)).toContain(token);
  });
  it("retains each binding kind and nested expression Condition source", () => {
    const source = `{"definition":{"name":"Values","enabled":true,"triggers":[],"variables":{"x":9007199254740993},"variables_expr":{"origin":"trigger.kind"},"steps":[{"id":"assign","kind":"set_variables","assignments":{"x":-9223372036854775808},"assignments_expr":{"next":"vars.x + 1"}},{"id":"c","kind":"command","parameters":{"nested":[9223372036854775807]}},{"id":"n","kind":"notification","title":"Hi","message_expr":"string(vars.x)"},{"id":"d","kind":"delay","duration_ms":1001},{"id":"if","kind":"if","conditions":{"id":"not","kind":"not","child":{"id":"expr","kind":"expression","expr":"vars.x > 0"}},"then":[]}]}}`;
    const automation = parseAutomationResponse<Automation>(source);
    const [assign, command, notification, delay, branch] = automation.definition.steps;
    if (assign.kind !== "set_variables" || command.kind !== "command" || notification.kind !== "notification" || delay.kind !== "delay" || branch.kind !== "if" || branch.conditions.kind !== "not" || branch.conditions.child.kind !== "expression") throw new Error("Bad fixture");
    expect(automationJsonText(automation.definition.variables!)).toContain("9007199254740993");
    expect(automationJsonFieldText(automation.definition.variables!, "x")).toBe("9007199254740993");
    expect(automationJsonFieldText(assign.assignments!, "x")).toBe("-9223372036854775808");
    expect(automationJsonFieldText(command, "parameters")).toContain("9223372036854775807");
    expect(automationJsonFieldText(notification, "title")).toBe('"Hi"');
    expect(notification.message_expr).toBe("string(vars.x)");
    expect(automationJsonFieldText(delay, "duration_ms")).toBe("1001");
    expect(branch.conditions.child.expr).toBe("vars.x > 0");
    expect(automation.definition.variables_expr?.origin).toBe("trigger.kind");
    expect(assign.assignments_expr?.next).toBe("vars.x + 1");
  });
  it("preserves v3 bindings, initialization and complete unified executions without rounding", () => {
    const values = '{"large":9007199254740993,"nested":[-9223372036854775808,9223372036854775807]}';
    const snapshot = `{"name":"Exact","enabled":true,"triggers":[],"variables":{"initial":${values}},"steps":[{"id":"assign","kind":"set_variables","assignments":{"n":${values}}},{"id":"f","kind":"function","arguments":${values}}]}`;
    const source = `{"snapshot":${snapshot},"data_evidence":"recorded","value_initialization":{"kind":"initialized","recorded_at":"now","values":${values}},"executions":[{"sequence":0,"step_id":"f","kind":"function","phase":"succeeded","started_at":"now","completed_at":"now","evidence":{"invocation_id":"inv","invocation_outcome":"succeeded","output":${values}}}]}`;
    const run = parseAutomationResponse<AutomationRun>(source);
    const assignment = run.snapshot.steps[0];
    if (assignment.kind !== "set_variables" || !run.value_initialization) throw new Error("Bad fixture");
    for (const exact of [automationJsonText(run.snapshot.variables!), automationJsonFieldText(run.snapshot.variables!, "initial"), automationJsonText(assignment.assignments!), automationJsonFieldText(assignment.assignments!, "n"), automationJsonText(run.value_initialization), automationJsonText(run.executions[0])]) {
      expect(exact).toContain("9007199254740993");
      expect(exact).toContain("-9223372036854775808");
      expect(exact).toContain("9223372036854775807");
    }
    expect(automationJsonText(run.executions[0])).toContain('"sequence": 0');
    const automation = parseAutomationResponse<Automation>(`{"revision":3,"definition":${snapshot}}`);
    expect(serializeAutomationEnablement(automation, false)).toContain(values);
  });
  it("copies every nested arm's original arguments and changes only enablement", () => {
    const automation = parseAutomationResponse<Automation>(wire);
    const replacement = serializeAutomationEnablement(automation, false);
    expect(replacement).toBe(`{"expected_revision":3,"definition":${definition.replace('"enabled" : true', '"enabled" : false')}}`);
    expect(replacement.match(/9007199254740993/g)).toHaveLength(10); // Number plus numeric-looking string, in all five leaves.
    const root = automation.definition.steps[0] as AutomationFunctionStep;
    const ifStep = automation.definition.steps[1] as AutomationIfStep;
    const chooseStep = automation.definition.steps[2] as AutomationChooseStep;
    for (const leaf of [root, ifStep.then[0], ifStep.else![0], chooseStep.branches[0].steps[0], chooseStep.default![0]] as AutomationFunctionStep[]) {
      expect(automationJsonFieldText(leaf, "arguments")).toContain('"large": 9007199254740993');
      expect(automationJsonFieldText(leaf, "arguments")).toContain('"fraction": 123.456789012345678901234');
      expect(automationJsonFieldText(leaf, "arguments")).toContain("-2.500e+40");
      expect(automationJsonFieldText(leaf, "arguments")).toContain(String.raw`"escaped": "\"9007199254740993\" \\ \u0031 [1e3],{}:"`);
      expect(automationJsonFieldText(leaf, "arguments")).toContain('"vendor": 9007199254740995');
    }
    // Parsed values stay ordinary JS values; they are not the write/display source.
    expect(root.arguments?.large).toBe(9007199254740992);
    expect(JSON.parse(replacement).definition.enabled).toBe(false);
  });

  it("pretty-prints structural whitespace without changing string or number tokens", () => {
    const response = parseAutomationResponse<Automation>(`{"definition":{"enabled":true,"steps":[{"kind":"function","arguments": { "literal": { "a" : [1E+003, {"b":-0}], "empty":{}, "array":[], "s":"[\\\"1e3\\\"]" } }}]}}`);
    const leaf = response.definition.steps[0] as AutomationFunctionStep;
    expect(automationJsonFieldText(leaf, "arguments")).toBe(String.raw`{
  "literal": {
    "a": [
      1E+003,
      {
        "b": -0
      }
    ],
    "empty": {},
    "array": [],
    "s": "[\"1e3\"]"
  }
}`);
  });

  it("matches escaped JSON member names and native last-member semantics", () => {
    const source = String.raw`{"definition":{"enabled":true,"st\u0065ps":[{"kind":"function","arguments":{"literal":{"wrong":1}},"arg\u0075ments":{"literal":{"right":9007199254740993}}}]}}`;
    const response = parseAutomationResponse<Automation>(source);
    const leaf = response.definition.steps[0] as AutomationFunctionStep;
    expect(automationJsonFieldText(leaf, "arguments")).toBe('{\n  "literal": {\n    "right": 9007199254740993\n  }\n}');
    expect(serializeAutomationEnablement(response, false)).toContain('"arg\\u0075ments":{"literal":{"right":9007199254740993}}');
  });

  it("retains list items, history snapshots and direct manual Run snapshots", () => {
    const list = parseAutomationResponse<Collection<Automation>>(`{"items":[${wire}]}`);
    expect(serializeAutomationEnablement(list.items[0], false)).toContain(`"arguments":${argumentsSource}`);
    expect(automationJsonText(list)).toContain('"large": 9007199254740993');
    const runSource = `{"snapshot":${definition},"executions":[],"data_evidence":"legacy_unavailable","value_initialization":null}`;
    const history = parseAutomationResponse<AutomationHistoryEntry>(`{"kind":"run","run":${runSource}}`);
    if (history.kind !== "run") throw new Error("Expected Run fixture");
    const manualRun = parseAutomationResponse<AutomationRun>(runSource);
    for (const run of [history.run, manualRun]) {
      const leaf = run.snapshot.steps[0] as AutomationFunctionStep;
      expect(automationJsonFieldText(leaf, "arguments")).toContain('"large": 9007199254740993');
      expect(automationJsonText(run)).toContain('"fraction": 123.456789012345678901234');
    }
  });

  it("fails closed when an enablement baseline has lost its source", () => {
    const untracked: Automation = JSON.parse(wire);
    expect(() => serializeAutomationEnablement(untracked, false)).toThrow("Exact Automation source unavailable");
    const leaf = untracked.definition.steps[0] as AutomationFunctionStep;
    expect(automationJsonFieldText(leaf, "arguments")).toBe("Exact value unavailable");
  });

  it("rejects invalid JSON before scanning and bounds recursive Step traversal", () => {
    expect(() => parseAutomationResponse('{"definition":')).toThrow(SyntaxError);
    const nested = (children: string) => `[{"kind":"if","then":${children}}]`;
    let deep = `[${step("deep")}]`;
    for (let i = 0; i < 8; i++) deep = nested(deep);
    expect(() => parseAutomationResponse(`{"definition":{"steps":${deep}}}`)).toThrow("source preservation bounds");
    expect(() => parseAutomationResponse(`{"definition":{"steps":[${Array.from({ length: 33 }, () => step("leaf")).join(",")}]}}`)).toThrow("source preservation bounds");
  });

  it("works without modern JSON extensions and leaves other API numeric semantics and errors unchanged", async () => {
    // This parser uses neither rawJSON nor the optional reviver context.
    const nativeParse = JSON.parse;
    vi.stubGlobal("JSON", { ...JSON, rawJSON: undefined, parse: (text: string) => nativeParse(text), stringify: JSON.stringify });
    vi.stubGlobal("fetch", async () => new Response(wire));
    const automation = await automationFetch<Automation>("/v1/automations/aut_example");
    expect(serializeAutomationEnablement(automation, false)).toContain('"large":9007199254740993');
    vi.stubGlobal("fetch", async () => new Response('{"value":9007199254740993}'));
    expect(await apiFetch("/v1/entities/example")).toEqual({ value: 9007199254740992 });
    vi.stubGlobal("fetch", async () => new Response('{"title":"Conflict","code":"revision_conflict"}', { status: 409 }));
    await expect(automationFetch("/v1/automations/aut_example")).rejects.toMatchObject({ status: 409, detail: { code: "revision_conflict" } });
    await expect(automationFetch("/v1/automations/aut_example")).rejects.toBeInstanceOf(ApiError);
  });
});
