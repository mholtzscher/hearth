import { afterEach, describe, expect, it, vi } from "vitest";
import { apiFetch, ApiError } from "./client.ts";
import { automationFetch, automationJsonText, parseAutomationResponse, serializeAutomationEnablement } from "./automation-json.ts";
import type { Automation, AutomationChooseStep, AutomationFunctionStep, AutomationHistoryEntry, AutomationIfStep, AutomationRun, Collection } from "./types.ts";

// Synthetic HTTP JSON, deliberately never constructed from JavaScript numbers.
const argumentsSource = String.raw`{"large":9007199254740993,"fraction":123.456789012345678901234,"exponents":[1E+003,1e-999,-2.500e+40,-0],"escaped":"\"9007199254740993\" \\ \u0031 [1e3],{}:","empty":[{},[]],"kind":"function","arguments":{"vendor":9007199254740995},"steps":[{"kind":"function"}]}`;
const step = (id: string) => `{"id":"${id}","kind":"function","adapter_id":"example","function_key":"refresh","contract_fingerprint":"sha256-v1:${"a".repeat(64)}","arguments":${argumentsSource}}`;
const condition = '{"id":"matched","kind":"trigger","trigger_ids":["morning"]}';
const steps = `[${step("root")},{"id":"if","kind":"if","conditions":${condition},"then":[${step("then")}],"else":[${step("else")}]},{"id":"choose","kind":"choose","branches":[{"id":"branch","conditions":${condition},"steps":[${step("branch")}]}],"default":[${step("default")}]}]`;
const definition = `{"name":"Exact source","enabled" : true,"triggers":[{"id":"morning","kind":"cron","expression":"0 7 * * *"}],"steps":${steps}}`;
const wire = `{"id":"aut_example","revision":3,"definition":${definition}}`;

afterEach(() => vi.unstubAllGlobals());

describe("Automation argument source preservation", () => {
  it("preserves v3 bindings, initialization and complete unified executions without rounding", () => {
    const values = '{"large":9007199254740993,"nested":[-9223372036854775808,9223372036854775807]}';
    const snapshot = `{"name":"Exact","enabled":true,"triggers":[],"variables":{"initial":{"literal":${values}}},"steps":[{"id":"assign","kind":"set_variables","assignments":{"n":{"literal":${values}}}},{"id":"f","kind":"function","arguments":{"literal":${values}}}]}`;
    const source = `{"snapshot":${snapshot},"data_evidence":"recorded","value_initialization":{"kind":"initialized","recorded_at":"now","values":${values}},"executions":[{"sequence":0,"step_id":"f","kind":"function","phase":"succeeded","started_at":"now","completed_at":"now","evidence":{"invocation_id":"inv","invocation_outcome":"succeeded","output":${values}}}]}`;
    const run = parseAutomationResponse<AutomationRun>(source);
    const assignment = run.snapshot.steps[0];
    if (assignment.kind !== "set_variables" || !run.value_initialization) throw new Error("Bad fixture");
    for (const value of [run.snapshot.variables!, run.snapshot.variables!.initial, assignment.assignments, assignment.assignments.n, run.value_initialization, run.executions[0]]) {
      const exact = automationJsonText(value);
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
      expect(automationJsonText(leaf.arguments)).toContain('"large": 9007199254740993');
      expect(automationJsonText(leaf.arguments)).toContain('"fraction": 123.456789012345678901234');
      expect(automationJsonText(leaf.arguments)).toContain("-2.500e+40");
      expect(automationJsonText(leaf.arguments)).toContain(String.raw`"escaped": "\"9007199254740993\" \\ \u0031 [1e3],{}:"`);
      expect(automationJsonText(leaf.arguments)).toContain('"vendor": 9007199254740995');
    }
    // Parsed values stay ordinary JS values; they are not the write/display source.
    expect((root.arguments as unknown as { large: number }).large).toBe(9007199254740992);
    expect(JSON.parse(replacement).definition.enabled).toBe(false);
  });

  it("pretty-prints structural whitespace without changing string or number tokens", () => {
    const response = parseAutomationResponse<Automation>(`{"definition":{"enabled":true,"steps":[{"kind":"function","arguments": { "a" : [1E+003, {"b":-0}], "empty":{}, "array":[], "s":"[\\\"1e3\\\"]" }}]}}`);
    const leaf = response.definition.steps[0] as AutomationFunctionStep;
    expect(automationJsonText(leaf.arguments)).toBe(String.raw`{
  "a": [
    1E+003,
    {
      "b": -0
    }
  ],
  "empty": {},
  "array": [],
  "s": "[\"1e3\"]"
}`);
  });

  it("matches escaped JSON member names and native last-member semantics", () => {
    const source = String.raw`{"definition":{"enabled":true,"st\u0065ps":[{"kind":"function","arguments":{"wrong":1},"arg\u0075ments":{"right":9007199254740993}}]}}`;
    const response = parseAutomationResponse<Automation>(source);
    const leaf = response.definition.steps[0] as AutomationFunctionStep;
    expect(automationJsonText(leaf.arguments)).toBe('{\n  "right": 9007199254740993\n}');
    expect(serializeAutomationEnablement(response, false)).toContain('"arg\\u0075ments":{"right":9007199254740993}');
  });

  it("retains list items, history snapshots and direct manual Run snapshots", () => {
    const list = parseAutomationResponse<Collection<Automation>>(`{"items":[${wire}]}`);
    expect(serializeAutomationEnablement(list.items[0], false)).toContain(`"arguments":${argumentsSource}`);
    expect(automationJsonText(list)).toContain('"large": 9007199254740993');
    const runSource = `{"snapshot":${definition},"steps":[]}`;
    const history = parseAutomationResponse<AutomationHistoryEntry>(`{"kind":"run","run":${runSource}}`);
    if (history.kind !== "run") throw new Error("Expected Run fixture");
    const manualRun = parseAutomationResponse<AutomationRun>(runSource);
    for (const run of [history.run, manualRun]) {
      const leaf = run.snapshot.steps[0] as AutomationFunctionStep;
      expect(automationJsonText(leaf.arguments)).toContain('"large": 9007199254740993');
      expect(automationJsonText(run)).toContain('"fraction": 123.456789012345678901234');
    }
  });

  it("fails closed when an enablement baseline has lost its source", () => {
    const untracked: Automation = JSON.parse(wire);
    expect(() => serializeAutomationEnablement(untracked, false)).toThrow("Exact Automation source unavailable");
    const leaf = untracked.definition.steps[0] as AutomationFunctionStep;
    expect(automationJsonText(leaf.arguments)).toBe("Exact Automation JSON unavailable. Refresh to reload it.");
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
