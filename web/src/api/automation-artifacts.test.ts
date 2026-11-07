import { afterEach, expect, it, vi } from "vitest";
import { automationArtifactSchema, automationContextSchema, loadHistory, streamTurn, type TurnEvent } from "./agent";
import { emptyTurn, pendingArtifactCalls, projectHistory, reduceTurn } from "../features/conversation/runtime";

const id = "aut_019a5b00-0000-7000-8000-000000000001";
const artifactID = "aart_019a5b00-0000-7000-8000-000000000002";
const definition = '{"name":"Exact proposal","enabled":false,"triggers":[],"variables":{"n":9007199254740993},"steps":[{"id":"wait","kind":"delay","duration_ms":1000}]}';
const artifact = { kind: "hearth.automation", artifact_id: artifactID, schema_version: 1, mode: "replace", automation_id: id, expected_revision: 3, definition_json: definition };
afterEach(() => vi.unstubAllGlobals());

it("identifies Automation proposals while their tool call is still being prepared", () => {
  const turn = reduceTurn(emptyTurn(), { type: "tool.preparing", call_id: "pending", name: "present_automation" }, "scope", null);
  expect(pendingArtifactCalls(turn)).toEqual([{ id: "pending", kind: "automation" }]);
});

it("rejects mixed variants, invalid revisions/IDs and unknown fields without losing exact definition strings", () => {
  const parsed = automationArtifactSchema.parse(artifact);
  expect(parsed.mode !== "reference" && parsed.definition_json).toBe(definition);
  for (const invalid of [{ ...artifact, expected_revision: 0 }, { ...artifact, automation_id: "name" }, { ...artifact, mode: "create" }, { ...artifact, extra: true }, { ...artifact, definition_json: definition.replace('"duration_ms":1000', '"duration_ms":0') }]) expect(automationArtifactSchema.safeParse(invalid).success).toBe(false);
  expect(automationContextSchema.safeParse({ mode: "create", definition_json: definition }).success).toBe(true);
  expect(automationContextSchema.safeParse({ mode: "create", definition_json: definition, expected_revision: 1 }).success).toBe(false);
});

it("rejects cross-node and overlapping value bindings at artifact and selected-context admission", () => {
  const duplicate = definition.replace('"steps":[', '"steps":[{"id":"wait","kind":"delay","duration_ms":1},');
  const overlapping = definition.replace('"variables":{"n":9007199254740993}', '"variables":{"n":9007199254740993},"variables_expr":{"n":"1"}');
  const assignments = definition.replace('"kind":"delay","duration_ms":1000', '"kind":"set_variables","assignments":{"n":1},"assignments_expr":{"n":"2"}');
  for (const source of [duplicate, overlapping, assignments]) {
    expect(automationArtifactSchema.safeParse({ ...artifact, definition_json: source }).success).toBe(false);
    expect(automationContextSchema.safeParse({ mode: "create", definition_json: source }).success).toBe(false);
  }
});

it("sends exact selected Automation context and projects one artifact across repeated live events", async () => {
  let sent = "";
  const event = { type: "automation.presented", call_id: "call", artifact };
  vi.stubGlobal("fetch", async (_url: unknown, init: RequestInit) => { sent = String(init.body); return new Response(`data: ${JSON.stringify(event)}\n\ndata: ${JSON.stringify(event)}\n\ndata: {"type":"turn.finished"}\n\n`); });
  const events: TurnEvent[] = [];
  await streamTurn("conversation", "edit it", { mode: "replace", automation_id: id, expected_revision: 3, definition_json: definition }, event => events.push(event), new AbortController().signal);
  expect(JSON.parse(sent)).toEqual({ text: "edit it", automation_context: { mode: "replace", automation_id: id, expected_revision: 3, definition_json: definition } });
  expect(JSON.parse(sent).automation_context.definition_json).toContain("9007199254740993");
  const turn = events.reduce((turn, event) => reduceTurn(turn, event, "scope", null), emptyTurn());
  expect(turn.artifacts).toHaveLength(1);
  expect(turn.artifacts[0].artifact).toEqual(artifact);
});

it("retains unsupported artifacts without rejecting surrounding conversation rows", async () => {
  const rows = [{ message_id: 1, role: "user", content: "draft", created_at: "", automation_context: { mode: "create", definition_json: definition } }, { message_id: 2, role: "assistant", content: "", created_at: "", calls: [{ id: "call", name: "present_automation", arguments: "{}" }] }, { message_id: 3, role: "tool", content: "exact original", created_at: "", tool_call_id: "call", artifacts: [artifact, { ...artifact, artifact_id: "future", schema_version: 99 }] }];
  vi.stubGlobal("fetch", async () => new Response(JSON.stringify({ messages: rows })));
  const history = projectHistory(await loadHistory("conversation"));
  expect(history[0].context).toEqual(rows[0].automation_context);
  expect(history[1].activities[0].result).toBe("exact original");
  expect(history[1].artifacts.map(item => item.artifact.kind)).toEqual(["hearth.automation", "unsupported"]);
});
