import { describe, expect, it } from "vitest";
import type { AgentMessage, Artifact } from "../../api/agent";
import { emptyTurn, groupReplies, projectHistory, reduceTurn } from "./runtime";

const row = (message_id: number, role: string, values: Partial<AgentMessage> = {}): AgentMessage => ({ message_id, role, content: "", created_at: "", ...values });
const artifact: Artifact = { kind: "hearth.view", artifact_id: "view-1", definition: { future: true } };

describe("conversation projections", () => {
  it("groups consecutive assistant messages into one reply per user turn", () => {
    const activity = (id: string) => ({ key: id, id, name: "get_device", arguments: "{}" });
    const replies = groupReplies([
      { id: "u1", role: "user", text: "temps", activities: [], artifacts: [] },
      { id: "a1", role: "assistant", text: "", activities: [activity("c1")], artifacts: [] },
      { id: "a2", role: "assistant", text: "Checking.", activities: [activity("c2")], artifacts: [{ artifact, base: null }] },
      { id: "a3", role: "assistant", text: "Done.", activities: [], artifacts: [] },
      { id: "u2", role: "user", text: "again", activities: [], artifacts: [] },
      { id: "a4", role: "assistant", text: "Sure.", activities: [], artifacts: [] },
    ]);
    expect(replies.map(reply => reply.id)).toEqual(["u1", "a1", "u2", "a4"]);
    expect(replies[1]).toMatchObject({ text: "Done.", activities: [activity("c1"), activity("c2")], artifacts: [{ artifact, base: null }] });
  });
  it("renders a visual-only terminal reply without resurrecting tool-call preambles after reload", () => {
    const replies = groupReplies(projectHistory([
      row(1, "user", { content: "Show temperatures" }),
      row(2, "assistant", { content: "I'll look up the sensors.", calls: [{ id: "read", name: "list_entities", arguments: "{}" }] }),
      row(3, "tool", { tool_call_id: "read", content: "sensors" }),
      row(4, "assistant", { content: "Here are the temperatures.", tool_calls: ["present_view"] }),
      row(5, "tool", { tool_name: "present_view", content: "view", artifacts: [artifact] }),
      row(6, "assistant"),
    ]));
    expect(replies).toHaveLength(2);
    expect(replies[1].text).toBe("");
    expect(replies[1].artifacts).toEqual([{ artifact, base: null }]);
    expect(replies[1].activities.map(call => call.result)).toEqual(["sensors", "view"]);
  });
  it("matches parallel results by native ID in their assistant batch and retains the user's base", () => {
    const items = projectHistory([
      row(1, "user", { content: "change this", view_context: { definition: {}, source_dashboard_id: "dash-1", source_revision: 2 } }),
      row(2, "assistant", { calls: [{ id: "same", name: "present_view", arguments: "{}" }, { id: "other", name: "present_view", arguments: "{}" }] }),
      row(3, "tool", { tool_call_id: "other", content: "second", artifacts: [artifact] }),
      row(4, "tool", { tool_call_id: "same", content: "first", artifacts: [artifact] }),
      row(5, "user", { content: "again" }),
      row(6, "assistant", { calls: [{ id: "same", name: "inspect", arguments: "{}" }] }),
      row(7, "tool", { tool_call_id: "same", content: "new turn" }),
    ]);
    expect(items[1].activities.map(call => call.result)).toEqual(["first", "second"]);
    expect(items[1].artifacts).toEqual([{ artifact, base: { dashboard_id: "dash-1", revision: 2 } }]);
    expect(items[3].activities[0].result).toBe("new turn");
  });
  it("leaves missing tool results incomplete and deduplicates streamed artifacts", () => {
    const history = projectHistory([row(1, "assistant", { calls: [{ id: "x", name: "read", arguments: "{}" }] })]);
    expect(history[0].activities[0].result).toBeUndefined();
    let live = reduceTurn(emptyTurn(), { type: "tool.started", call_id: "x", name: "read" }, "request-1", null);
    live = reduceTurn(live, { type: "view.presented", call_id: "x", artifact }, "request-1", null);
    live = reduceTurn(live, { type: "view.presented", call_id: "x", artifact }, "request-1", null);
    live = reduceTurn(live, { type: "turn.failed", error: "failed" }, "request-1", null);
    expect(live.artifacts).toHaveLength(1);
    expect(live.activities[0].key).toBe("request-1:x:0");
    expect(live.failed).toBe("failed");
  });
  it("does not update an earlier call when a later batch reuses its native ID", () => {
    let live = reduceTurn(emptyTurn(), { type: "tool.started", call_id: "same", name: "read" }, "request", null);
    live = reduceTurn(live, { type: "tool.finished", call_id: "same", result: "first" }, "request", null);
    live = reduceTurn(live, { type: "tool.started", call_id: "same", name: "read" }, "request", null);
    live = reduceTurn(live, { type: "tool.finished", call_id: "same", result: "second" }, "request", null);
    expect(live.activities.map(item => item.result)).toEqual(["first", "second"]);
    expect(live.activities.map(item => item.key)).toEqual(["request:same:0", "request:same:1"]);
  });
  it("pairs legacy name-only batches locally without treating synthetic IDs as global", () => {
    const items = projectHistory([
      row(1, "user"), row(2, "assistant", { tool_calls: ["read", "read"] }),
      row(3, "tool", { tool_call_id: "call-1", tool_name: "read", content: "first" }),
      row(4, "tool", { tool_call_id: "call-2", tool_name: "read", content: "second" }),
      row(5, "user"), row(6, "assistant", { tool_calls: ["read"] }),
      row(7, "tool", { tool_call_id: "call-1", tool_name: "read", content: "another turn" }),
    ]);
    expect(items[1].activities.map(call => call.result)).toEqual(["first", "second"]);
    expect(items[3].activities[0].result).toBe("another turn");
  });
});
