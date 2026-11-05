import { z } from "zod";
import { ApiError, apiFetch, getBaseUrl } from "./client";
import type { ViewDefinition } from "../features/views/catalog";

export const artifactSchema = z.object({ kind: z.literal("hearth.view"), artifact_id: z.string().min(1), definition: z.json() });
export const viewContextSchema = z.object({ definition: z.json(), source_dashboard_id: z.string().optional(), source_revision: z.number().int().positive().optional() }).refine(value => (value.source_dashboard_id === undefined) === (value.source_revision === undefined));
const callSchema = z.object({ id: z.string(), name: z.string(), arguments: z.string() });
const messageSchema = z.object({ message_id: z.number().int(), role: z.string(), content: z.string(), tool_calls: z.array(z.string()).nullish(), calls: z.array(callSchema).nullish(), tool_call_id: z.string().nullish(), tool_name: z.string().nullish(), artifacts: z.array(artifactSchema).nullish(), view_context: viewContextSchema.nullish(), created_at: z.string() });
const summarySchema = z.object({ id: z.string(), created_at: z.string(), message_count: z.number(), preview: z.string(), last_message_at: z.string().nullish() });
const eventSchema = z.object({ type: z.string(), call_id: z.string().optional(), name: z.string().optional(), arguments: z.string().optional(), result: z.string().optional(), reply: z.string().optional(), error: z.string().optional(), artifact: artifactSchema.optional() }).refine(event => {
  if (event.type === "tool.preparing" || event.type === "tool.started") return !!event.call_id && !!event.name;
  if (event.type === "tool.finished") return !!event.call_id;
  if (event.type === "view.presented") return !!event.call_id && !!event.artifact;
  return true;
}, "Malformed agent event");
const turnSchema = z.object({ reply: z.string(), tool_calls: z.array(z.string()).nullish(), artifacts: z.array(artifactSchema).nullish() });
export type Artifact = z.infer<typeof artifactSchema>;
export type AgentMessage = z.infer<typeof messageSchema>;
export type TurnEvent = z.infer<typeof eventSchema>;
export type ViewContext = { definition: ViewDefinition; source_dashboard_id?: string; source_revision?: number };

const parse = <T>(schema: z.ZodType<T>) => (text: string): T => schema.parse(JSON.parse(text));
export async function listConversations(signal?: AbortSignal) {
  return (await apiFetch("/v1/agent/conversations", { signal }, parse(z.object({ conversations: z.array(summarySchema) })))).conversations;
}
export async function createConversation(signal?: AbortSignal) {
  return (await apiFetch("/v1/agent/conversations", { method: "POST", signal }, parse(z.object({ id: z.string() })))).id;
}
export async function loadHistory(id: string, signal?: AbortSignal) {
  return (await apiFetch(`/v1/agent/conversations/${encodeURIComponent(id)}/messages`, { signal }, parse(z.object({ messages: z.array(messageSchema) })))).messages;
}
export async function sendMessage(id: string, text: string, context?: ViewContext, signal?: AbortSignal) {
  return apiFetch(`/v1/agent/conversations/${encodeURIComponent(id)}/messages`, { method: "POST", body: JSON.stringify({ text, ...(context ? { view_context: context } : {}) }), signal }, parse(turnSchema));
}
export async function streamTurn(id: string, text: string, context: ViewContext | undefined, onEvent: (event: TurnEvent) => void, signal: AbortSignal) {
  const response = await fetch(`${getBaseUrl()}/v1/agent/conversations/${encodeURIComponent(id)}/messages/stream`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ text, ...(context ? { view_context: context } : {}) }), signal });
  if (!response.ok || !response.body) throw new ApiError(response.status, `${response.status} ${response.statusText}`);
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value, { stream: !done });
    const frames = buffer.split(/\r?\n\r?\n/);
    buffer = frames.pop() ?? "";
    for (const frame of frames) {
      const data = frame.split(/\r?\n/).filter(line => line.startsWith("data:")).map(line => line.slice(5).trimStart()).join("\n");
      if (data) onEvent(eventSchema.parse(JSON.parse(data)));
    }
    if (done) break;
  }
}
