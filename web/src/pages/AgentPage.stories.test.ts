// @vitest-environment node
import { getResponse } from "msw";
import { expect, it } from "vitest";
import { automationId, definition } from "../../.storybook/automation-fixtures";
import { conversationId, createApplicationHandlers, queuedConversationId } from "../../.storybook/application-fixtures";

const base = "http://localhost";
async function request(handlers: ReturnType<typeof createApplicationHandlers>, path: string, init?: RequestInit) {
  const response = await getResponse(handlers, new Request(new URL(path, base), init), { baseUrl: base });
  if (!response) throw new Error(`No fixture response for ${init?.method ?? "GET"} ${path}`);
  return response;
}

it("lists a newly committed conversation while its create response waits for the canonical turn", async () => {
  const handlers = createApplicationHandlers({ turn: "queue-collision" });
  const path = `/v1/agent/conversations/${queuedConversationId}`;
  expect((await request(handlers, `${path}/messages`)).status).toBe(404);
  const list = request(handlers, "/v1/agent/conversations");
  const create = request(handlers, "/v1/agent/conversations", { method: "POST" });
  let createDelivered = false;
  void create.then(() => { createDelivered = true; });
  const summaries = await (await list).json();
  expect(summaries.conversations).toContainEqual({
    id: queuedConversationId, created_at: "2026-11-09T10:00:30Z", message_count: 0, preview: "",
  });
  expect(queuedConversationId).not.toBe(conversationId);
  expect(queuedConversationId).toMatch(/^aconv_[0-9a-f]{32}$/);
  expect(await (await request(handlers, `${path}/messages`)).json()).toEqual({ messages: [] });
  expect(createDelivered).toBe(false);

  const stream = request(handlers, `${path}/messages/stream`, {
    method: "POST", headers: { "content-type": "application/json" },
    body: JSON.stringify({ text: "Check the hallway." }),
  });
  expect(await (await create).json()).toEqual({ id: queuedConversationId, created_at: "2026-11-09T10:00:30Z" });
  expect(await (await request(handlers, `${path}/messages`)).json()).toEqual({
    messages: [{ message_id: 1, role: "user", content: "Check the hallway.", created_at: "2026-11-09T10:00:30Z" }],
  });
  const updated = await (await request(handlers, "/v1/agent/conversations")).json();
  expect(updated.conversations).toContainEqual({
    id: queuedConversationId, created_at: "2026-11-09T10:00:30Z", last_message_at: "2026-11-09T10:00:30Z",
    message_count: 1, preview: "Check the hallway.",
  });
  await (await stream).body?.cancel();
});

it.each([
  ["automation-create", { mode: "create", definition }],
  ["automation-replace", { mode: "replace", automation_id: automationId, expected_revision: 3, definition }],
  ["automation-reference", { mode: "reference", automation_id: automationId }],
] as const)("returns a matching present_automation call and tool result for %s", async (messages, argumentsValue) => {
  const handlers = createApplicationHandlers({ messages });
  const response = await request(handlers, `/v1/agent/conversations/${conversationId}/messages`);
  const history = await response.json();
  const call = history.messages[1].calls[0];
  const result = history.messages[2];
  expect(call.name).toBe("present_automation");
  expect(JSON.parse(call.arguments)).toEqual(argumentsValue);
  expect(result.tool_call_id).toBe(call.id);
  expect(result.tool_name).toBe(call.name);
  expect(JSON.parse(result.content)).toEqual(result.artifacts[0]);
  expect(result.artifacts[0]).toMatchObject({ kind: "hearth.automation", schema_version: 1, mode: argumentsValue.mode });
});
