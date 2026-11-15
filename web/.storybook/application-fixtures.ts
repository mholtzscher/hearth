import { http, HttpResponse } from "msw";
import { z } from "zod";
import type { AgentMessage, TurnEvent } from "../src/api/agent";
import { viewContextSchema } from "../src/api/agent";
import type { Dashboard } from "../src/api/dashboards";
import { dashboardDefinition, fixtureNow } from "./fixtures";

export const conversationId = "conversation_story_morning";
export const dashboardId = "dashboard_story_morning";
export const conversationPath = `/conversations/${conversationId}`;
export const dashboardPath = `/dashboards/${dashboardId}`;
export const conversationPrompt = "Show me a morning overview.";
export const conversationReply = "Here is your morning overview. The hallway lamp is on.";
export const sentReply = "The living room is 21.5 °C.";

const artifact = { kind: "hearth.view" as const, artifact_id: "view_story_morning", definition: dashboardDefinition };
const loadedMessages: AgentMessage[] = [
  { message_id: 1, role: "user", content: conversationPrompt, created_at: fixtureNow },
  { message_id: 2, role: "assistant", content: "", created_at: fixtureNow,
    calls: [{ id: "read-home", name: "present_view", arguments: JSON.stringify({ title: dashboardDefinition.title }) }] },
  { message_id: 3, role: "tool", content: "View prepared from synthetic household readings.",
    tool_call_id: "read-home", tool_name: "present_view", artifacts: [artifact], created_at: fixtureNow },
  { message_id: 4, role: "assistant", content: conversationReply, created_at: fixtureNow },
];

export type ApplicationScenario = {
  server?: "ready" | "not-ready" | "unavailable";
  turn?: "reply" | "pending" | "pending-view" | "fail-once";
  historyFailure?: boolean;
  saveConflict?: boolean;
  dashboards?: "empty";
  conversations?: "searchable";
  messages?: "long";
};

/** Each factory owns its history, stream attempts, and saved revision; stories never share writes. */
export function createApplicationHandlers(scenario: ApplicationScenario = {}) {
  const histories = new Map<string, AgentMessage[]>([[conversationId, structuredClone(loadedMessages)]]);
  if (scenario.conversations === "searchable") {
    for (let index = 1; index <= 12; index++) {
      histories.set(`conversation_story_${index}`, [
        { message_id: 1, role: "user", content: `Check room ${index}.`, created_at: fixtureNow },
      ]);
    }
  }
  if (scenario.messages === "long") {
    histories.set(conversationId, Array.from({ length: 30 }, (_, index) => ({
      message_id: index + 1, role: index % 2 ? "assistant" : "user",
      content: index % 2 ? `Room ${Math.ceil(index / 2)} is comfortable. The temperature is 21.5 °C and the lamp is on.` : `Check room ${index / 2 + 1}.`,
      created_at: fixtureNow,
    })));
  }
  const initial: Dashboard = {
    id: dashboardId, revision: 1, definition: structuredClone(dashboardDefinition),
    created_at: fixtureNow, updated_at: fixtureNow,
  };
  const dashboards = new Map([[dashboardId, initial]]);
  if (scenario.dashboards === "empty") dashboards.clear();
  let conflicted = false;
  let attempts = 0;
  let created = 0;
  return [
    http.get("/healthz", () => HttpResponse.json({ status: "ok" })),
    http.get("/readyz", () => {
      if (scenario.server === "unavailable") return HttpResponse.error();
      return HttpResponse.json({ status: scenario.server === "not-ready" ? "not_ready" : "ready" },
        { status: scenario.server === "not-ready" ? 503 : 200 });
    }),
    http.get("/v1/agent/conversations", () => HttpResponse.json({
      conversations: [...histories].map(([id, messages]) => ({
        id, created_at: fixtureNow, last_message_at: fixtureNow, message_count: messages.length,
        preview: messages.find(message => message.role === "user")?.content ?? "New conversation",
      })),
    })),
    http.post("/v1/agent/conversations", () => {
      const id = `conversation_story_new_${++created}`;
      histories.set(id, []);
      return HttpResponse.json({ id });
    }),
    http.get("/v1/agent/conversations/:id/messages", ({ params }) => {
      if (scenario.historyFailure) return HttpResponse.json({ detail: "Conversation history unavailable" }, { status: 503 });
      return HttpResponse.json({ messages: histories.get(String(params.id)) ?? [] });
    }),
    http.post("/v1/agent/conversations/:id/messages/stream", async ({ params, request }) => {
      const { text, view_context } = z.object({ text: z.string(), view_context: viewContextSchema.optional() }).parse(await request.json());
      if (scenario.turn === "fail-once" && ++attempts === 1) {
        return HttpResponse.json({ detail: "Synthetic provider unavailable" }, { status: 503 });
      }
      const pending = scenario.turn === "pending" || scenario.turn === "pending-view";
      const events: TurnEvent[] = [
        { type: "tool.started", call_id: "read-temperature", name: scenario.turn === "pending-view" ? "present_view" : "get_entity", arguments: "{}" },
      ];
      if (!pending) {
        const messages = histories.get(String(params.id)) ?? [];
        const next = messages.length + 1;
        histories.set(String(params.id), [...messages,
          { message_id: next, role: "user", content: text, view_context, created_at: fixtureNow },
          { message_id: next + 1, role: "assistant", content: "", created_at: fixtureNow,
            calls: [{ id: "read-temperature", name: "get_entity", arguments: "{}" }] },
          { message_id: next + 2, role: "tool", content: '{"value":21.5}', created_at: fixtureNow,
            tool_call_id: "read-temperature", tool_name: "get_entity" },
          { message_id: next + 3, role: "assistant", content: sentReply, created_at: fixtureNow },
        ]);
        events.push({ type: "tool.finished", call_id: "read-temperature", result: '{"value":21.5}' },
          { type: "turn.finished", reply: sentReply });
      }
      const encoder = new TextEncoder();
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          for (const event of events) controller.enqueue(encoder.encode(`data: ${JSON.stringify(event)}\n\n`));
          if (!pending) controller.close();
        },
      });
      return new HttpResponse(stream, { headers: { "Content-Type": "text/event-stream" } });
    }),
    http.get("/v1/dashboards", () => HttpResponse.json({
      items: [...dashboards.values()].map(saved => ({
        id: saved.id, revision: saved.revision, title: z.object({ title: z.string() }).parse(saved.definition).title,
        created_at: saved.created_at, updated_at: saved.updated_at,
      })),
    })),
    http.delete("/v1/dashboards/:id", ({ params, request }) => {
      const saved = dashboards.get(String(params.id));
      if (!saved) return HttpResponse.json({ detail: "Dashboard not found" }, { status: 404 });
      if (new URL(request.url).searchParams.get("expected_revision") !== String(saved.revision)) {
        return HttpResponse.json({ detail: "Revision conflict" }, { status: 409 });
      }
      dashboards.delete(saved.id);
      return new HttpResponse(null, { status: 204 });
    }),
    http.get("/v1/dashboards/:id", ({ params }) => {
      const saved = dashboards.get(String(params.id));
      return saved ? HttpResponse.json(saved) : HttpResponse.json({ detail: "Dashboard not found" }, { status: 404 });
    }),
    http.put("/v1/dashboards/:id", async ({ params, request }) => {
      const body = z.object({ expected_revision: z.number(), definition: z.json() }).parse(await request.json());
      const saved = dashboards.get(String(params.id));
      if (!saved) return HttpResponse.json({ detail: "Dashboard not found" }, { status: 404 });
      if (scenario.saveConflict && !conflicted) {
        conflicted = true;
        dashboards.set(saved.id, { ...saved, revision: saved.revision + 1,
          definition: { ...structuredClone(dashboardDefinition), title: "Updated elsewhere" } });
        return HttpResponse.json({ detail: "Revision conflict" }, { status: 409 });
      }
      if (body.expected_revision !== saved.revision) {
        return HttpResponse.json({ detail: "Revision conflict" }, { status: 409 });
      }
      const updated = { ...saved, revision: saved.revision + 1, definition: body.definition, updated_at: fixtureNow };
      dashboards.set(saved.id, updated);
      return HttpResponse.json(updated);
    }),
    http.post("/v1/dashboards", async ({ request }) => {
      const { definition } = z.object({ definition: z.json() }).parse(await request.json());
      const copy = { ...initial, id: `dashboard_story_copy_${++created}`, revision: 1, definition };
      dashboards.set(copy.id, copy);
      return HttpResponse.json(copy);
    }),
  ];
}
