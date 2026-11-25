import { z } from "zod";

import { readAutomationDocument } from "../features/automations/document";
import type { ViewDefinition } from "../features/views/catalog";
import { ApiError, apiFetch, getBaseUrl } from "./client";

const canonicalID = z
  .string()
  .regex(
    /^aut_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u
  );
const revision = z.number().int().positive().max(Number.MAX_SAFE_INTEGER);
const definitionJSON = z.string().superRefine((source, context) => {
  try {
    readAutomationDocument(source);
  } catch (error) {
    context.addIssue({
      code: "custom",
      message:
        error instanceof Error
          ? error.message
          : "Invalid Automation definition",
    });
  }
});
const automationProposalVariants = [
  z.strictObject({
    mode: z.literal("create"),
    definition_json: definitionJSON,
  }),
  z.strictObject({
    mode: z.literal("replace"),
    automation_id: canonicalID,
    expected_revision: revision,
    definition_json: definitionJSON,
  }),
] as const;
export const automationContextSchema = z.discriminatedUnion(
  "mode",
  automationProposalVariants
);
const automationArtifactHeader = {
  kind: z.literal("hearth.automation"),
  artifact_id: z
    .string()
    .regex(
      /^aart_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u
    ),
  schema_version: z.literal(1),
};
export const automationArtifactSchema = z.discriminatedUnion("mode", [
  z.strictObject({
    ...automationArtifactHeader,
    mode: z.literal("reference"),
    automation_id: canonicalID,
  }),
  ...automationProposalVariants.map((variant) =>
    variant.extend(automationArtifactHeader)
  ),
]);
const viewArtifactSchema = z.object({
  kind: z.literal("hearth.view"),
  artifact_id: z.string().min(1),
  definition: z.json(),
});
export const artifactSchema = z.union([
  viewArtifactSchema,
  automationArtifactSchema,
]);
// Compatibility reads retain an unusable artifact without rejecting its whole conversation.
const retainedArtifactSchema = z.unknown().transform((value) => {
  const parsed = artifactSchema.safeParse(value);
  if (parsed.success) {
    return parsed.data;
  }
  const envelope = z
    .object({ artifact_id: z.string().min(1) })
    .safeParse(value);
  return {
    kind: "unsupported" as const,
    artifact_id: envelope.success
      ? envelope.data.artifact_id
      : "unavailable-artifact",
    original: value,
  };
});
export const viewContextSchema = z
  .object({
    definition: z.json(),
    source_dashboard_id: z.string().optional(),
    source_revision: z.number().int().positive().optional(),
  })
  .refine(
    (value) =>
      (value.source_dashboard_id === undefined) ===
      (value.source_revision === undefined)
  );
const callSchema = z.object({
  id: z.string(),
  name: z.string(),
  arguments: z.string(),
});
const messageSchema = z.object({
  message_id: z.number().int(),
  role: z.string(),
  content: z.string(),
  tool_calls: z.array(z.string()).nullish(),
  calls: z.array(callSchema).nullish(),
  tool_call_id: z.string().nullish(),
  tool_name: z.string().nullish(),
  artifacts: z.array(retainedArtifactSchema).nullish(),
  view_context: viewContextSchema.nullish(),
  automation_context: automationContextSchema.nullish(),
  created_at: z.string(),
});
const summarySchema = z.object({
  id: z.string(),
  created_at: z.string(),
  message_count: z.number(),
  preview: z.string(),
  last_message_at: z.string().nullish(),
});
const eventSchema = z
  .object({
    type: z.string(),
    call_id: z.string().optional(),
    name: z.string().optional(),
    arguments: z.string().optional(),
    result: z.string().optional(),
    reply: z.string().optional(),
    error: z.string().optional(),
    artifact: retainedArtifactSchema.optional(),
  })
  .refine((event) => {
    if (event.type === "tool.preparing" || event.type === "tool.started") {
      return !!event.call_id && !!event.name;
    }
    if (event.type === "tool.finished") {
      return !!event.call_id;
    }
    if (
      event.type === "view.presented" ||
      event.type === "automation.presented"
    ) {
      return !!event.call_id && !!event.artifact;
    }
    return true;
  }, "Malformed agent event");
const turnSchema = z.object({
  reply: z.string(),
  tool_calls: z.array(z.string()).nullish(),
  artifacts: z.array(artifactSchema).nullish(),
});
export type Artifact = z.infer<typeof retainedArtifactSchema>;
export type AutomationArtifact = z.infer<typeof automationArtifactSchema>;
export type AutomationContext = z.infer<typeof automationContextSchema>;
export type AgentMessage = z.infer<typeof messageSchema>;
export type TurnEvent = z.infer<typeof eventSchema>;
export interface ViewContext {
  definition: ViewDefinition;
  source_dashboard_id?: string;
  source_revision?: number;
}
export type SelectedContext = ViewContext | AutomationContext;
const contextBody = (context?: SelectedContext) => {
  if (!context) {
    return {};
  }
  if ("mode" in context) {
    return { automation_context: automationContextSchema.parse(context) };
  }
  return { view_context: context };
};

const parse =
  <T>(schema: z.ZodType<T>) =>
  (text: string): T =>
    schema.parse(JSON.parse(text));
export const listConversations = async (signal?: AbortSignal) => {
  const response = await apiFetch(
    "/v1/agent/conversations",
    { signal },
    parse(z.object({ conversations: z.array(summarySchema) }))
  );
  return response.conversations;
};
const starterQuestionsSchema = z.object({
  starter_questions: z.array(
    z.object({ title: z.string().min(1), prompt: z.string().min(1) })
  ),
  enabled: z.boolean(),
  generated_at: z.string().nullable(),
});
export type StarterQuestion = z.infer<
  typeof starterQuestionsSchema
>["starter_questions"][number];
export interface StarterQuestions {
  questions: readonly StarterQuestion[];
  /** Core generates starter questions, so a refresh can run. */
  enabled: boolean;
}
const readStarterQuestions = (text: string): StarterQuestions => {
  const body = parse(starterQuestionsSchema)(text);
  return { questions: body.starter_questions, enabled: body.enabled };
};
/** Reads Core's generated starter questions; empty until Core has generated some. */
export const listStarterQuestions = (signal?: AbortSignal) =>
  apiFetch("/v1/agent/starter-questions", { signal }, readStarterQuestions);
/** Asks Core to generate starter questions now; joins a pass already running. */
export const refreshStarterQuestions = (signal?: AbortSignal) =>
  apiFetch(
    "/v1/agent/starter-questions/refresh",
    { method: "POST", signal },
    readStarterQuestions
  );
export const createConversation = async (signal?: AbortSignal) => {
  const response = await apiFetch(
    "/v1/agent/conversations",
    { method: "POST", signal },
    parse(z.object({ id: z.string() }))
  );
  return response.id;
};
const turnStateSchema = z.discriminatedUnion("state", [
  z.object({ state: z.literal("running"), started_at: z.string() }),
  z.object({
    state: z.literal("failed"),
    started_at: z.string(),
    error: z.string(),
    /** The failed turn's own message; it may have failed before saving it. */
    prompt: z.string().optional(),
  }),
]);
export type TurnState = z.infer<typeof turnStateSchema>;
export interface ConversationHistory {
  messages: z.infer<typeof messageSchema>[];
  /** The latest turn while it runs or after it failed; null once it finished. */
  turn: TurnState | null;
}
export const loadHistory = async (
  id: string,
  signal?: AbortSignal
): Promise<ConversationHistory> => {
  const response = await apiFetch(
    `/v1/agent/conversations/${encodeURIComponent(id)}/messages`,
    { signal },
    parse(
      z.object({
        messages: z.array(messageSchema),
        turn: turnStateSchema.optional(),
      })
    )
  );
  return { messages: response.messages, turn: response.turn ?? null };
};
export const sendMessage = async (
  id: string,
  text: string,
  context?: SelectedContext,
  signal?: AbortSignal
) => {
  const response = await apiFetch(
    `/v1/agent/conversations/${encodeURIComponent(id)}/messages`,
    {
      method: "POST",
      body: JSON.stringify({ text, ...contextBody(context) }),
      signal,
    },
    parse(turnSchema)
  );
  return response;
};
export const streamTurn = async (
  id: string,
  text: string,
  context: SelectedContext | undefined,
  onEvent: (event: TurnEvent) => void,
  signal: AbortSignal
) => {
  const response = await fetch(
    `${getBaseUrl()}/v1/agent/conversations/${encodeURIComponent(id)}/messages/stream`,
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ text, ...contextBody(context) }),
      signal,
    }
  );
  if (!response.ok || !response.body) {
    throw new ApiError(
      response.status,
      `${response.status} ${response.statusText}`
    );
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  const pumpFrames = async (): Promise<void> => {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value, { stream: !done });
    const frames = buffer.split(/\r?\n\r?\n/u);
    buffer = frames.pop() ?? "";
    for (const frame of frames) {
      const data = frame
        .split(/\r?\n/u)
        .filter((line) => line.startsWith("data:"))
        .map((line) => line.slice(5).trimStart())
        .join("\n");
      if (data) {
        onEvent(eventSchema.parse(JSON.parse(data)));
      }
    }
    if (!done) {
      return pumpFrames();
    }
  };
  await pumpFrames();
};
