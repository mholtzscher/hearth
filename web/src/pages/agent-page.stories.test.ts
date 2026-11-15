// @vitest-environment node
import { getResponse } from "msw";
import { describe, expect, it } from "vitest";

import {
  conversationId,
  createApplicationHandlers,
  queuedConversationId,
} from "../../.storybook/application-fixtures";
import type { ApplicationScenario } from "../../.storybook/application-fixtures";
import { automationId, definition } from "../../.storybook/automation-fixtures";
import { projectHistory } from "../features/conversation/runtime";

const base = "http://localhost";
const toolArguments =
  '{"entity_id":"ent_019a3e6a-4111-7000-8000-000000000004"}';
const queuedPath = `/v1/agent/conversations/${queuedConversationId}`;
const queuedHistory = {
  messages: [
    {
      message_id: 1,
      role: "user",
      content: "Check the hallway.",
      created_at: "2026-11-09T10:00:30Z",
    },
    {
      message_id: 2,
      role: "assistant",
      content: "",
      created_at: "2026-11-09T10:00:30Z",
      calls: [
        {
          id: "read-temperature",
          name: "get_entity",
          arguments: toolArguments,
        },
      ],
    },
  ],
};
const automationModes = [
  ["automation-create", { mode: "create", definition }],
  [
    "automation-replace",
    {
      mode: "replace",
      automation_id: automationId,
      expected_revision: 3,
      definition,
    },
  ],
  ["automation-reference", { mode: "reference", automation_id: automationId }],
] as const;

const request = async (
  handlers: ReturnType<typeof createApplicationHandlers>,
  path: string,
  init?: RequestInit
) => {
  const response = await getResponse(
    handlers,
    new Request(new URL(path, base), init),
    { baseUrl: base }
  );
  if (!response) {
    throw new Error(`No fixture response for ${init?.method ?? "GET"} ${path}`);
  }
  return response;
};
const queuedHandlers = () => {
  const { promise: queuedTurnFailure, resolve: failTurn } =
    Promise.withResolvers<undefined>();
  const handlers = createApplicationHandlers({
    turn: "queue-collision",
    queuedTurnFailure,
  });
  return { handlers, failTurn };
};
const startQueuedCreate = (
  handlers: ReturnType<typeof createApplicationHandlers>
) => request(handlers, "/v1/agent/conversations", { method: "POST" });
const streamQueuedTurn = (
  handlers: ReturnType<typeof createApplicationHandlers>
) =>
  request(handlers, `${queuedPath}/messages/stream`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ text: "Check the hallway." }),
  });
const queuedTurnReader = async (
  handlers: ReturnType<typeof createApplicationHandlers>
) => {
  const create = startQueuedCreate(handlers);
  const listed = await request(handlers, "/v1/agent/conversations");
  await listed.json();
  const stream = await streamQueuedTurn(handlers);
  const { body } = stream;
  if (!body) {
    throw new Error("Expected the queued turn stream to have a body");
  }
  const reader = body.getReader();
  await create;
  return reader;
};
const readEvent = async (reader: ReadableStreamDefaultReader<Uint8Array>) => {
  const chunk = await reader.read();
  expect(chunk.done).toBeFalsy();
  return JSON.parse(
    new TextDecoder()
      .decode(chunk.value)
      .replace(/^data: /u, "")
      .trim()
  );
};
const presentAutomationExchange = async (
  messages: ApplicationScenario["messages"]
) => {
  const handlers = createApplicationHandlers({ messages });
  const response = await request(
    handlers,
    `/v1/agent/conversations/${conversationId}/messages`
  );
  const history = await response.json();
  const [, assistant, toolResult] = history.messages;
  const [call] = assistant.calls;
  return { call, result: toolResult };
};

describe("agent page story API", () => {
  it("rejects messages for a conversation whose creation is still queued", async () => {
    const { handlers } = queuedHandlers();
    const response = await request(handlers, `${queuedPath}/messages`);
    expect(response.status).toBe(404);
  });

  it("lists the queued conversation while its create response is pending", async () => {
    const { handlers } = queuedHandlers();
    const list = request(handlers, "/v1/agent/conversations");
    const create = startQueuedCreate(handlers);
    let createDelivered = false;
    void create.finally(() => {
      createDelivered = true;
    });
    const listResponse = await list;
    const summaries = await listResponse.json();
    expect(summaries.conversations).toContainEqual({
      id: queuedConversationId,
      created_at: "2026-11-09T10:00:30Z",
      message_count: 0,
      preview: "",
    });
    expect(queuedConversationId).not.toBe(conversationId);
    expect(queuedConversationId).toMatch(/^aconv_[0-9a-f]{32}$/u);
    expect(createDelivered).toBeFalsy();
    await expect(
      request(handlers, `${queuedPath}/messages`).then((response) =>
        response.json()
      )
    ).resolves.toStrictEqual({ messages: [] });
  });

  it("returns the committed conversation once the canonical turn starts", async () => {
    const { handlers } = queuedHandlers();
    const create = startQueuedCreate(handlers);
    const listed = await request(handlers, "/v1/agent/conversations");
    await listed.json();
    const stream = await streamQueuedTurn(handlers);
    expect(stream.status).toBe(200);
    const createResponse = await create;
    const created = await createResponse.json();
    expect(created).toStrictEqual({
      id: queuedConversationId,
      created_at: "2026-11-09T10:00:30Z",
    });
  });

  it("streams the turn start and the tool call before the turn finishes", async () => {
    const { handlers } = queuedHandlers();
    const reader = await queuedTurnReader(handlers);
    await expect(readEvent(reader)).resolves.toStrictEqual({
      type: "turn.started",
    });
    await expect(readEvent(reader)).resolves.toStrictEqual({
      type: "tool.started",
      call_id: "read-temperature",
      name: "get_entity",
      arguments: toolArguments,
    });
  });

  it("records the user turn and its tool call in the conversation history", async () => {
    const { handlers } = queuedHandlers();
    const reader = await queuedTurnReader(handlers);
    await expect(readEvent(reader)).resolves.toStrictEqual({
      type: "turn.started",
    });
    const historyResponse = await request(handlers, `${queuedPath}/messages`);
    const history = await historyResponse.json();
    expect(history).toStrictEqual(queuedHistory);
  });

  it("fails the pending turn and closes the stream", async () => {
    const { handlers, failTurn } = queuedHandlers();
    const reader = await queuedTurnReader(handlers);
    await expect(readEvent(reader)).resolves.toStrictEqual({
      type: "turn.started",
    });
    await expect(readEvent(reader)).resolves.toStrictEqual({
      type: "tool.started",
      call_id: "read-temperature",
      name: "get_entity",
      arguments: toolArguments,
    });
    failTurn(undefined);
    await expect(readEvent(reader)).resolves.toStrictEqual({
      type: "turn.failed",
      error: "agent turn failed",
    });
    const final = await reader.read();
    expect(final.done).toBeTruthy();
  });

  it("keeps the stored history unchanged after the turn fails", async () => {
    const { handlers, failTurn } = queuedHandlers();
    const reader = await queuedTurnReader(handlers);
    failTurn(undefined);
    await reader.read();
    await reader.read();
    await readEvent(reader);
    const historyResponse = await request(handlers, `${queuedPath}/messages`);
    const reconciled = await historyResponse.json();
    expect(reconciled).toStrictEqual(queuedHistory);
  });

  it("projects the tool call without a result while the turn is unfinished", async () => {
    const { handlers } = queuedHandlers();
    await queuedTurnReader(handlers);
    const historyResponse = await request(handlers, `${queuedPath}/messages`);
    const reconciled = await historyResponse.json();
    const [, assistant] = projectHistory(reconciled.messages);
    const [activity] = assistant.activities;
    expect(activity).toMatchObject({
      id: "read-temperature",
      name: "get_entity",
      arguments: toolArguments,
    });
    expect(activity).not.toHaveProperty("result");
  });

  it("summarizes the queued conversation after its first turn is recorded", async () => {
    const { handlers } = queuedHandlers();
    await queuedTurnReader(handlers);
    const listResponse = await request(handlers, "/v1/agent/conversations");
    const updated = await listResponse.json();
    expect(updated.conversations).toContainEqual({
      id: queuedConversationId,
      created_at: "2026-11-09T10:00:30Z",
      last_message_at: "2026-11-09T10:00:30Z",
      message_count: 2,
      preview: "Check the hallway.",
    });
  });

  it.each(automationModes)(
    "returns a matching present_automation call for %s",
    async (messages, argumentsValue) => {
      const { call } = await presentAutomationExchange(messages);
      expect(call.name).toBe("present_automation");
      expect(JSON.parse(call.arguments)).toStrictEqual(argumentsValue);
    }
  );

  it.each(automationModes)(
    "returns a present_automation tool result for %s",
    async (messages, argumentsValue) => {
      const { call, result } = await presentAutomationExchange(messages);
      expect(result.tool_call_id).toBe(call.id);
      expect(result.tool_name).toBe(call.name);
      expect(JSON.parse(result.content)).toStrictEqual(result.artifacts[0]);
      expect(result.artifacts[0]).toMatchObject({
        kind: "hearth.automation",
        schema_version: 1,
        mode: argumentsValue.mode,
      });
    }
  );
});
