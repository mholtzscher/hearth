import { http, HttpResponse } from "msw";
import { z } from "zod";

import type {
  AgentMessage,
  AutomationArtifact,
  TurnEvent,
} from "../src/api/agent";
import { viewContextSchema } from "../src/api/agent";
import type { Dashboard } from "../src/api/dashboards";
import type { ViewDefinition } from "../src/features/views/catalog";
import {
  automation,
  automationHandlers,
  automationId,
  definition as automationDefinition,
} from "./automation-fixtures";
import {
  catalogExamples,
  dashboardDefinition,
  deviceId,
  entityId,
  fixtureNow,
} from "./fixtures";

export const conversationId = "conversation_story_morning";
export const queuedConversationId = "aconv_01920000000070008000000000000001";
export const dashboardId = "dashboard_story_morning";
export const conversationPath = `/conversations/${conversationId}`;
export const dashboardPath = `/dashboards/${dashboardId}`;
export const conversationPrompt = "Show me a morning overview.";
export const conversationReply =
  "Here is your morning overview. The hallway lamp is on.";
export const sentReply = "The living room is 21.5 °C.";

const artifact = {
  kind: "hearth.view" as const,
  artifact_id: "view_story_morning",
  definition: dashboardDefinition,
};
const compactArtifact = {
  ...artifact,
  definition: {
    ...dashboardDefinition,
    spec: {
      root: "root",
      elements: {
        root: {
          type: "Stack",
          props: { gap: "small" },
          children: ["heading", "readings"],
        },
        heading: catalogExamples.Heading,
        readings: {
          type: "Grid",
          props: { columns: 2 },
          children: ["humidity", "lamp"],
        },
        humidity: catalogExamples.Metric,
        lamp: catalogExamples.Device,
      },
    },
  } satisfies ViewDefinition,
};
const secondView = {
  ...compactArtifact,
  artifact_id: "view_story_followup",
  definition: { ...compactArtifact.definition, title: "Evening overview" },
};
const revisedArtifact = {
  ...artifact,
  artifact_id: "view_story_revised",
  definition: { ...dashboardDefinition, title: "Morning at home" },
};
type FixtureView = typeof artifact;

const fixtureToolResult = (
  started: { call_id: string; name: string; arguments: string },
  messageId: number,
  view?: FixtureView
) => {
  const { call_id: id, name, arguments: args } = started;
  const result = view ? JSON.stringify(view) : '{"value":21.5}';
  const rows: AgentMessage[] = [
    {
      message_id: messageId,
      role: "assistant",
      content: "",
      created_at: fixtureNow,
      calls: [{ id, name, arguments: args }],
    },
    {
      message_id: messageId + 1,
      role: "tool",
      content: result,
      tool_call_id: id,
      tool_name: name,
      ...(view && { artifacts: [view] }),
      created_at: fixtureNow,
    },
  ];
  const events: TurnEvent[] = [{ type: "tool.finished", call_id: id, result }];
  if (view) {
    events.push({ type: "view.presented", call_id: id, artifact: view });
  }
  return { rows, events };
};

const fixtureTurnOutcome = (
  messages: AgentMessage[],
  prompt: { text: string; view_context?: AgentMessage["view_context"] },
  started: Parameters<typeof fixtureToolResult>[0] & { type: "tool.started" },
  view: FixtureView | undefined,
  reusedViewCall: boolean,
  editing: boolean
) => {
  const next = messages.length + 1;
  let reply = sentReply;
  if (view) {
    reply = conversationReply;
  }
  if (editing) {
    reply = "Updated the title.";
  }
  const first = fixtureToolResult(started, next + 1, view);
  const second = reusedViewCall
    ? fixtureToolResult(started, next + 3, secondView)
    : undefined;
  const history: AgentMessage[] = [
    ...messages,
    {
      message_id: next,
      role: "user",
      content: prompt.text,
      view_context: prompt.view_context,
      created_at: fixtureNow,
    },
    ...first.rows,
    ...(second?.rows ?? []),
    {
      message_id: next + (reusedViewCall ? 5 : 3),
      role: "assistant",
      content: reply,
      created_at: fixtureNow,
    },
  ];
  const finished = { type: "turn.finished", reply } as const;
  return {
    history,
    results: second ? [...first.events, started] : [...first.events, finished],
    followup: second ? [...second.events, finished] : undefined,
  };
};
const automationHeader = {
  kind: "hearth.automation",
  schema_version: 1,
  artifact_id: "aart_01920000-0000-7000-8000-000000000001",
} as const;
export const automationArtifacts = {
  create: {
    ...automationHeader,
    mode: "create",
    definition_json: JSON.stringify(automationDefinition),
  },
  replace: {
    ...automationHeader,
    mode: "replace",
    automation_id: automationId,
    expected_revision: 3,
    definition_json: JSON.stringify(automationDefinition),
  },
  reference: {
    ...automationHeader,
    mode: "reference",
    automation_id: automationId,
  },
} satisfies Record<string, AutomationArtifact>;
const automationArguments = {
  create: { mode: "create", definition: automationDefinition },
  replace: {
    mode: "replace",
    automation_id: automationId,
    expected_revision: 3,
    definition: automationDefinition,
  },
  reference: { mode: "reference", automation_id: automationId },
} as const;
const loadedMessages: AgentMessage[] = [
  {
    message_id: 1,
    role: "user",
    content: conversationPrompt,
    created_at: fixtureNow,
  },
  {
    message_id: 2,
    role: "assistant",
    content: "",
    created_at: fixtureNow,
    calls: [
      {
        id: "read-home",
        name: "compose_view",
        arguments: JSON.stringify({
          mode: "create",
          title: dashboardDefinition.title,
          layout: {
            type: "Stack",
            gap: "large",
            children: [
              { type: "Heading", text: "House at a glance" },
              {
                type: "Note",
                text: "Synthetic readings. Controls affect only this story's household.",
              },
              {
                type: "Grid",
                columns: 3,
                children: [
                  {
                    type: "OutsideNow",
                    label: "Outside",
                    temperature: entityId(101),
                    humidity: entityId(102),
                    wind: entityId(103),
                    wind_direction: entityId(104),
                    rain_rate: entityId(105),
                    rain_today: entityId(106),
                  },
                  {
                    type: "Metric",
                    label: "Outdoor humidity",
                    source: { entity_id: entityId(102) },
                    trend: "24h",
                    range: true,
                  },
                  {
                    type: "Device",
                    label: "Hallway lamp",
                    device_id: deviceId(2),
                  },
                ],
              },
              {
                type: "TimeSeries",
                title: "Temperatures",
                window: "24h",
                series: [
                  { label: "Outdoor", entity_id: entityId(101) },
                  { label: "Bedroom", entity_id: entityId(114) },
                ],
              },
            ],
          },
        }),
      },
    ],
  },
  {
    message_id: 3,
    role: "tool",
    content: "View prepared from synthetic household readings.",
    tool_call_id: "read-home",
    tool_name: "compose_view",
    artifacts: [artifact],
    created_at: fixtureNow,
  },
  {
    message_id: 4,
    role: "assistant",
    content: conversationReply,
    created_at: fixtureNow,
  },
];

export interface ApplicationScenario {
  server?: "ready" | "not-ready" | "unavailable";
  turn?:
    | "reply"
    | "pending"
    | "pending-view"
    | "view-ready"
    | "reused-view-call"
    | "tool-complete"
    | "view-edit"
    | "fail-once"
    | "queue-collision";
  /** The queued collision stays pending until this story-local barrier releases a failed turn. */
  queuedTurnFailure?: Promise<void>;
  historyFailure?: boolean;
  saveConflict?: boolean;
  dashboards?: "empty";
  conversations?: "searchable" | "empty" | "error";
  messages?:
    | "compact-view"
    | "legacy-view"
    | "long"
    | "rich"
    | "automation-create"
    | "automation-replace"
    | "automation-reference"
    | "automation-missing"
    | "automation-loading";
}

const automationModeFor = (
  messages: ApplicationScenario["messages"]
): "create" | "replace" | "reference" | null => {
  if (messages === "automation-create") {
    return "create";
  }
  if (messages === "automation-replace") {
    return "replace";
  }
  return messages?.startsWith("automation-") ? "reference" : null;
};

/** The stored history of the story conversation, by message scenario. Each call returns a fresh copy. */
export const conversationHistory = (
  messages?: ApplicationScenario["messages"]
): AgentMessage[] => {
  const initialMessages =
    messages === "compact-view"
      ? loadedMessages.map((message) =>
          message.role === "tool"
            ? { ...message, artifacts: [compactArtifact] }
            : message
        )
      : loadedMessages;
  let history = structuredClone(initialMessages);
  if (messages === "legacy-view" && history[1].calls?.[0] && history[2]) {
    history[1].calls[0].name = "present_view";
    history[1].calls[0].arguments = JSON.stringify({
      definition: dashboardDefinition,
    });
    history[2].tool_name = "present_view";
  }
  if (messages === "long") {
    history = Array.from({ length: 30 }, (_, index) => ({
      message_id: index + 1,
      role: index % 2 ? "assistant" : "user",
      content:
        index % 2
          ? `Room ${Math.ceil(index / 2)} is comfortable. The temperature is 21.5 °C and the lamp is on.`
          : `Check room ${index / 2 + 1}.`,
      created_at: fixtureNow,
    }));
  }
  if (messages === "rich") {
    // Synthetic compatibility fixture: a future artifact is retained beside a Markdown reply.
    history = [
      {
        message_id: 1,
        role: "user",
        content: "Summarize the rooms.",
        created_at: fixtureNow,
      },
      {
        message_id: 2,
        role: "assistant",
        content: "",
        created_at: fixtureNow,
        calls: [{ id: "future", name: "present_future", arguments: "{}" }],
      },
      {
        message_id: 3,
        role: "tool",
        content: "Future artifact",
        tool_call_id: "future",
        tool_name: "present_future",
        artifacts: [
          {
            kind: "unsupported",
            artifact_id: "future-story",
            original: { kind: "hearth.future", artifact_id: "future-story" },
          },
        ],
        created_at: fixtureNow,
      },
      {
        message_id: 4,
        role: "assistant",
        content:
          '**Room readings**\n\n- Kitchen: 21 °C\n- Bedroom: 20 °C\n\n| Room | Temperature |\n| --- | --- |\n| Kitchen | 21 °C |\n\n```json\n{"temperature":21}\n```',
        created_at: fixtureNow,
      },
    ];
  }
  const automationMode = automationModeFor(messages);
  if (automationMode) {
    // Synthetic tool result using the same definition and saved record as the Automation stories.
    history = [
      {
        message_id: 1,
        role: "user",
        content: "Show the hallway Automation.",
        created_at: fixtureNow,
      },
      {
        message_id: 2,
        role: "assistant",
        content: "",
        created_at: fixtureNow,
        calls: [
          {
            id: "automation",
            name: "present_automation",
            arguments: JSON.stringify(automationArguments[automationMode]),
          },
        ],
      },
      {
        message_id: 3,
        role: "tool",
        content: JSON.stringify(automationArtifacts[automationMode]),
        tool_call_id: "automation",
        tool_name: "present_automation",
        artifacts: [automationArtifacts[automationMode]],
        created_at: fixtureNow,
      },
      {
        message_id: 4,
        role: "assistant",
        content: "Here is the hallway Automation.",
        created_at: fixtureNow,
      },
    ];
  }
  return history;
};

const startedTool = (turn: ApplicationScenario["turn"]) => {
  let name = "get_entity";
  let args = "{}";
  if (
    turn === "pending-view" ||
    turn === "view-ready" ||
    turn === "reused-view-call" ||
    turn === "view-edit"
  ) {
    name = "compose_view";
    args = JSON.stringify({
      mode: "create",
      title: compactArtifact.definition.title,
      layout: {
        type: "Stack",
        gap: "small",
        children: [
          { type: "Heading", text: "House at a glance" },
          {
            type: "Grid",
            columns: 2,
            children: [
              {
                type: "Metric",
                label: "Outdoor humidity",
                source: { entity_id: entityId(102) },
                trend: "24h",
                range: true,
              },
              {
                type: "Device",
                label: "Hallway lamp",
                device_id: deviceId(2),
              },
            ],
          },
        ],
      },
    });
  }
  if (turn === "queue-collision") {
    args = JSON.stringify({ entity_id: entityId(4) });
  }
  if (turn === "view-edit") {
    args = JSON.stringify({
      mode: "edit",
      changes: [{ op: "set_title", title: "Morning at home" }],
    });
  }
  return {
    type: "tool.started",
    call_id: "read-temperature",
    name,
    arguments: args,
  } as const satisfies TurnEvent;
};

const selectedFixtureView = (turn: ApplicationScenario["turn"]) => {
  if (turn === "view-edit") {
    return revisedArtifact;
  }
  if (turn === "view-ready" || turn === "reused-view-call") {
    return compactArtifact;
  }
};

/**
 * One synthetic turn: the events the stream sends first and, unless the turn
 * stays pending, the stored history and the events that complete it.
 */
export const fixtureTurn = (
  turn: ApplicationScenario["turn"],
  history: AgentMessage[],
  prompt: Parameters<typeof fixtureTurnOutcome>[1]
) => {
  const pending =
    turn === "pending" || turn === "pending-view" || turn === "queue-collision";
  const reusedViewCall = turn === "reused-view-call";
  const viewReady = turn === "view-ready" || reusedViewCall;
  const toolStarted = startedTool(turn);
  const events: TurnEvent[] = [
    ...(turn === "queue-collision" ? [{ type: "turn.started" } as const] : []),
    toolStarted,
  ];
  const outcome = pending
    ? undefined
    : fixtureTurnOutcome(
        history,
        prompt,
        toolStarted,
        selectedFixtureView(turn),
        reusedViewCall,
        turn === "view-edit"
      );
  return { toolStarted, events, outcome, viewReady, reusedViewCall };
};

/** Each factory owns its history, stream attempts, and saved revision; stories never share writes. */
export const createApplicationHandlers = (
  scenario: ApplicationScenario = {}
) => {
  const histories = new Map<string, AgentMessage[]>([
    [conversationId, conversationHistory(scenario.messages)],
  ]);
  if (scenario.conversations === "empty") {
    histories.clear();
  }
  if (scenario.conversations === "searchable") {
    for (let index = 1; index <= 12; index += 1) {
      histories.set(`conversation_story_${index}`, [
        {
          message_id: 1,
          role: "user",
          content: `Check room ${index}.`,
          created_at: fixtureNow,
        },
      ]);
    }
  }
  const automationMode = automationModeFor(scenario.messages);
  const initial: Dashboard = {
    id: dashboardId,
    revision: 1,
    definition: structuredClone(dashboardDefinition),
    created_at: fixtureNow,
    updated_at: fixtureNow,
  };
  const dashboards = new Map([[dashboardId, initial]]);
  if (scenario.dashboards === "empty") {
    dashboards.clear();
  }
  let conflicted = false;
  let attempts = 0;
  let created = 0;
  const canonicalStarted = Promise.withResolvers<undefined>();
  const creationCommitted = Promise.withResolvers<undefined>();
  return [
    http.get("/healthz", () => HttpResponse.json({ status: "ok" })),
    http.get("/readyz", () => {
      if (scenario.server === "unavailable") {
        return HttpResponse.error();
      }
      return HttpResponse.json(
        { status: scenario.server === "not-ready" ? "not_ready" : "ready" },
        { status: scenario.server === "not-ready" ? 503 : 200 }
      );
    }),
    http.get("/v1/agent/conversations", async () => {
      if (scenario.conversations === "error") {
        return HttpResponse.json(
          { detail: "Synthetic conversation list unavailable" },
          { status: 503 }
        );
      }
      // A list read already in flight observes the newly committed row before its create response arrives.
      if (scenario.turn === "queue-collision") {
        await creationCommitted.promise;
      }
      return HttpResponse.json({
        conversations: [...histories].map(([id, messages]) => ({
          id,
          created_at: fixtureNow,
          ...(messages.length ? { last_message_at: fixtureNow } : {}),
          message_count: messages.length,
          preview:
            messages.find((message) => message.role === "user")?.content ?? "",
        })),
      });
    }),
    http.post("/v1/agent/conversations", async () => {
      if (scenario.turn === "queue-collision") {
        histories.set(queuedConversationId, []);
        creationCommitted.resolve(undefined);
        await canonicalStarted.promise;
        return HttpResponse.json({
          id: queuedConversationId,
          created_at: fixtureNow,
        });
      }
      created += 1;
      const id = `conversation_story_new_${created}`;
      histories.set(id, []);
      return HttpResponse.json({ id });
    }),
    http.get("/v1/agent/conversations/:id/messages", ({ params }) => {
      if (scenario.historyFailure) {
        return HttpResponse.json(
          { detail: "Conversation history unavailable" },
          { status: 503 }
        );
      }
      if (
        scenario.turn === "queue-collision" &&
        !histories.has(String(params.id))
      ) {
        return HttpResponse.json(
          { detail: "Conversation not found" },
          { status: 404 }
        );
      }
      const messages = histories.get(String(params.id)) ?? [];
      return HttpResponse.json({
        messages: messages.map((message) => ({
          ...message,
          artifacts: message.artifacts?.map((item) =>
            item.kind === "unsupported" ? item.original : item
          ),
        })),
      });
    }),
    http.post(
      "/v1/agent/conversations/:id/messages/stream",
      async ({ params, request }) => {
        if (
          scenario.turn === "queue-collision" &&
          !histories.has(String(params.id))
        ) {
          return HttpResponse.json(
            { detail: "Conversation not found" },
            { status: 404 }
          );
        }
        const { text, view_context } = z
          .object({
            text: z.string(),
            view_context: viewContextSchema.optional(),
          })
          .parse(await request.json());
        if (scenario.turn === "view-edit" && !view_context?.definition) {
          return HttpResponse.json(
            { detail: "Expected the selected synthetic view" },
            { status: 400 }
          );
        }
        if (scenario.turn === "fail-once") {
          attempts += 1;
          if (attempts === 1) {
            return HttpResponse.json(
              { detail: "Synthetic provider unavailable" },
              { status: 503 }
            );
          }
        }
        const { toolStarted, events, outcome, viewReady, reusedViewCall } =
          fixtureTurn(scenario.turn, histories.get(String(params.id)) ?? [], {
            text,
            view_context,
          });
        if (
          scenario.turn === "queue-collision" &&
          params.id === queuedConversationId
        ) {
          histories.set(queuedConversationId, [
            {
              message_id: 1,
              role: "user",
              content: text,
              created_at: fixtureNow,
            },
            {
              message_id: 2,
              role: "assistant",
              content: "",
              created_at: fixtureNow,
              calls: [
                {
                  id: toolStarted.call_id,
                  name: toolStarted.name,
                  arguments: toolStarted.arguments,
                },
              ],
            },
          ]);
          canonicalStarted.resolve(undefined);
        }
        if (outcome) {
          histories.set(String(params.id), outcome.history);
        }
        const encoder = new TextEncoder();
        let canceled = false;
        const stream = new ReadableStream<Uint8Array>({
          cancel() {
            canceled = true;
          },
          start(controller) {
            const emit = (batch: TurnEvent[]) => {
              for (const event of batch) {
                controller.enqueue(
                  encoder.encode(`data: ${JSON.stringify(event)}\n\n`)
                );
              }
            };
            emit(events);
            if (
              scenario.turn === "queue-collision" &&
              scenario.queuedTurnFailure
            ) {
              void (async () => {
                await scenario.queuedTurnFailure;
                if (canceled || request.signal.aborted) {
                  return;
                }
                controller.enqueue(
                  encoder.encode(
                    'data: {"type":"turn.failed","error":"agent turn failed"}\n\n'
                  )
                );
                controller.close();
              })();
            }
            if (outcome && (viewReady || scenario.turn === "tool-complete")) {
              // Leave time to see each placeholder, without putting the first
              // view's commit at the edge of Storybook's assertion window.
              const delay = reusedViewCall ? 600 : 900;
              setTimeout(() => {
                if (canceled || request.signal.aborted) {
                  return;
                }
                emit(outcome.results);
                const { followup } = outcome;
                if (followup) {
                  setTimeout(() => {
                    if (canceled || request.signal.aborted) {
                      return;
                    }
                    emit(followup);
                    controller.close();
                  }, delay);
                } else {
                  controller.close();
                }
              }, delay);
            } else if (outcome) {
              emit(outcome.results);
              controller.close();
            }
          },
        });
        return new HttpResponse(stream, {
          headers: { "Content-Type": "text/event-stream" },
        });
      }
    ),
    ...(automationMode
      ? [
          http.get(`/v1/automations/${automationId}`, async ({ request }) => {
            if (scenario.messages === "automation-missing") {
              return HttpResponse.json(
                { detail: "Automation not found" },
                { status: 404 }
              );
            }
            if (scenario.messages === "automation-loading") {
              const aborted = Promise.withResolvers<undefined>();
              request.signal.addEventListener(
                "abort",
                () => aborted.resolve(undefined),
                { once: true }
              );
              await aborted.promise;
              return HttpResponse.error();
            }
            return HttpResponse.json(automation);
          }),
          ...automationHandlers(),
        ]
      : []),
    http.get("/v1/dashboards", () =>
      HttpResponse.json({
        items: [...dashboards.values()].map((saved) => ({
          id: saved.id,
          revision: saved.revision,
          title: z.object({ title: z.string() }).parse(saved.definition).title,
          created_at: saved.created_at,
          updated_at: saved.updated_at,
        })),
      })
    ),
    http.delete("/v1/dashboards/:id", ({ params, request }) => {
      const saved = dashboards.get(String(params.id));
      if (!saved) {
        return HttpResponse.json(
          { detail: "Dashboard not found" },
          { status: 404 }
        );
      }
      if (
        new URL(request.url).searchParams.get("expected_revision") !==
        String(saved.revision)
      ) {
        return HttpResponse.json(
          { detail: "Revision conflict" },
          { status: 409 }
        );
      }
      dashboards.delete(saved.id);
      return new HttpResponse(null, { status: 204 });
    }),
    http.get("/v1/dashboards/:id", ({ params }) => {
      const saved = dashboards.get(String(params.id));
      return saved
        ? HttpResponse.json(saved)
        : HttpResponse.json({ detail: "Dashboard not found" }, { status: 404 });
    }),
    http.put("/v1/dashboards/:id", async ({ params, request }) => {
      const body = z
        .object({ expected_revision: z.number(), definition: z.json() })
        .parse(await request.json());
      const saved = dashboards.get(String(params.id));
      if (!saved) {
        return HttpResponse.json(
          { detail: "Dashboard not found" },
          { status: 404 }
        );
      }
      if (scenario.saveConflict && !conflicted) {
        conflicted = true;
        dashboards.set(saved.id, {
          ...saved,
          revision: saved.revision + 1,
          definition: {
            ...structuredClone(dashboardDefinition),
            title: "Updated elsewhere",
          },
        });
        return HttpResponse.json(
          { detail: "Revision conflict" },
          { status: 409 }
        );
      }
      if (body.expected_revision !== saved.revision) {
        return HttpResponse.json(
          { detail: "Revision conflict" },
          { status: 409 }
        );
      }
      const updated = {
        ...saved,
        revision: saved.revision + 1,
        definition: body.definition,
        updated_at: fixtureNow,
      };
      dashboards.set(saved.id, updated);
      return HttpResponse.json(updated);
    }),
    http.post("/v1/dashboards", async ({ request }) => {
      const { definition } = z
        .object({ definition: z.json() })
        .parse(await request.json());
      created += 1;
      const copy = {
        ...initial,
        id: `dashboard_story_copy_${created}`,
        revision: 1,
        definition,
      };
      dashboards.set(copy.id, copy);
      return HttpResponse.json(copy);
    }),
  ];
};
