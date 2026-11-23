import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { StrictMode } from "react";
import { createMemoryRouter, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { setBaseUrl } from "../api/client";
import { ConversationHistory } from "../features/conversation/conversation-history";
import { ConversationsProvider } from "../features/conversation/conversations-provider";
import { WorkspaceProvider } from "../features/dashboards/workspace";
import { ViewDataProvider } from "../features/views/data/provider";
import AgentPage from "./agent-page";

const artifact = {
  kind: "hearth.view",
  artifact_id: "view-1",
  definition: {
    schema_version: 1,
    catalog_version: "hearth.dashboard/v1",
    title: "Warm house",
    spec: {
      root: "heading",
      elements: { heading: { type: "Heading", props: { text: "Hello home" } } },
    },
  },
};

const historyWithView = {
  messages: [
    {
      message_id: 1,
      role: "assistant",
      content: "",
      calls: [{ id: "call-1", name: "present_view", arguments: "{}" }],
      created_at: "",
    },
    {
      message_id: 2,
      role: "tool",
      content: "",
      tool_call_id: "call-1",
      tool_name: "present_view",
      artifacts: [artifact],
      created_at: "",
    },
  ],
};

/** The observer methods the component calls on a stubbed global. */
interface ObserverStub {
  observe: (target: Element) => void;
  unobserve: () => void;
  disconnect: () => void;
}

const streamController = <T,>(streams: Map<string, T>, id: string): T => {
  const controller = streams.get(id);
  if (controller === undefined) {
    throw new Error(`No stream was opened for ${id}`);
  }
  return controller;
};

const closestRequired = <T extends Element>(
  element: Element,
  selector: string
): T => {
  const match = element.closest<T>(selector);
  if (!match) {
    throw new Error(`Expected an element matching ${selector}`);
  }
  return match;
};

const pendingResolver = <T,>(read: () => T | undefined): T => {
  const resolver = read();
  if (resolver === undefined) {
    throw new Error("Expected a pending resolver");
  }
  return resolver;
};

const installNoopResizeObserver = () => {
  vi.stubGlobal(
    "ResizeObserver",
    function resizeObserverStub(this: ObserverStub) {
      this.observe = () => {};
      this.unobserve = () => {};
      this.disconnect = () => {};
    }
  );
};

const mount = (path: string) => {
  const router = createMemoryRouter(
    [
      {
        path: "/",
        element: (
          <ViewDataProvider>
            <ConversationsProvider>
              <WorkspaceProvider>
                <ConversationHistory id="recent" open={false} />
                <Outlet />
              </WorkspaceProvider>
            </ConversationsProvider>
          </ViewDataProvider>
        ),
        children: [
          { index: true, element: <AgentPage /> },
          { path: "conversations/:id", element: <AgentPage /> },
          { path: "dashboards", element: <h1>Dashboard library</h1> },
        ],
      },
    ],
    { initialEntries: [path] }
  );
  render(
    <StrictMode>
      <RouterProvider router={router} />
    </StrictMode>
  );
  return router;
};

/** Types a prompt once the composer accepts input and sends it. */
const sendMessage = async (text: string) => {
  const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(input).toHaveProperty("disabled", false));
  fireEvent.change(input, { target: { value: text } });
  fireEvent.click(screen.getByRole("button", { name: "Send message" }));
  return input;
};

interface ConversationStream {
  body: ReadableStream<Uint8Array>;
  controller: ReadableStreamDefaultController<Uint8Array>;
}

const conversationStream = (): ConversationStream => {
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  const body = new ReadableStream<Uint8Array>({
    start(value) {
      controller = value;
    },
  });
  return { body, controller };
};

/** Serves one live conversation stream and its running activity. */
const stubConversationStream = (reply: string) => {
  const stream = conversationStream();
  const state = {
    finished: false,
    signal: undefined as AbortSignal | undefined,
  };
  const fetcher = vi.fn<
    (input: RequestInfo | URL, init?: RequestInit) => Response
  >((input, init) => {
    const path = String(input);
    if (path.endsWith("/messages/stream")) {
      state.signal = init?.signal as AbortSignal;
      return new Response(stream.body);
    }
    return Response.json(
      path.endsWith("/messages")
        ? {
            messages: state.finished
              ? [
                  {
                    message_id: 1,
                    role: "user",
                    content: "Read the temperature",
                    created_at: "",
                  },
                  {
                    message_id: 2,
                    role: "assistant",
                    content: reply,
                    created_at: "",
                  },
                ]
              : [],
          }
        : { conversations: [] }
    );
  });
  vi.stubGlobal("fetch", fetcher);
  return { controller: stream.controller, fetcher, state };
};

/** Serves two conversations that each keep their own live stream. */
const stubTwoConversations = () => {
  const streams = new Map<
    string,
    ReadableStreamDefaultController<Uint8Array>
  >();
  const signals = new Map<string, AbortSignal>();
  const finished = new Set<string>();
  const fetcher = vi.fn<
    (input: RequestInfo | URL, init?: RequestInit) => Response
  >((input, init) => {
    const path = String(input);
    const id = path.includes("/c-a/") ? "c-a" : "c-b";
    if (path.endsWith("/messages/stream")) {
      signals.set(id, init?.signal as AbortSignal);
      return new Response(
        new ReadableStream({
          start(value) {
            streams.set(id, value);
          },
        })
      );
    }
    return Response.json(
      path.endsWith("/messages")
        ? {
            messages: finished.has(id)
              ? [
                  {
                    message_id: 1,
                    role: "assistant",
                    content: `Reply for ${id}`,
                    created_at: "",
                  },
                ]
              : [],
          }
        : { conversations: [] }
    );
  });
  vi.stubGlobal("fetch", fetcher);
  return { fetcher, finished, signals, streams };
};

/** Opens conversation A, leaves for B, and starts B's turn. */
const openTwoConversations = async () => {
  const conversation = stubTwoConversations();
  const router = mount("/conversations/c-a");
  await sendMessage("Question A");
  await waitFor(() => expect(conversation.streams.size).toBe(1));
  await act(async () => {
    await router.navigate("/conversations/c-b");
  });
  await sendMessage("Question B");
  await waitFor(() => expect(conversation.streams.size).toBe(2));
  return { ...conversation, router };
};

/** A delayed creation that resolves into an existing conversation. */
const stubCollisionConversation = () => {
  const create = {
    resolve: undefined as ((value: Response) => void) | undefined,
  };
  const list = {
    resolve: undefined as ((value: Response) => void) | undefined,
  };
  const streams: ReadableStreamDefaultController<Uint8Array>[] = [];
  const sent: string[] = [];
  const state = { completed: 0 };
  const fetcher = vi.fn<
    (
      input: RequestInfo | URL,
      init?: RequestInit
    ) => Response | Promise<Response>
  >((input, init) => {
    const path = String(input);
    if (path === "/v1/agent/conversations" && init?.method === "POST") {
      const deferred = Promise.withResolvers<Response>();
      create.resolve = deferred.resolve;
      return deferred.promise;
    }
    if (path === "/v1/agent/conversations") {
      const deferred = Promise.withResolvers<Response>();
      list.resolve = deferred.resolve;
      return deferred.promise;
    }
    if (path.endsWith("/messages/stream")) {
      sent.push(JSON.parse(String(init?.body)).text);
      return new Response(
        new ReadableStream({
          start(value) {
            streams.push(value);
          },
        })
      );
    }
    return Response.json({
      messages: [
        ...(state.completed >= 1
          ? [
              {
                message_id: 1,
                role: "user",
                content: "An already running prompt",
                created_at: "",
              },
              {
                message_id: 2,
                role: "assistant",
                content: "First reply",
                created_at: "",
              },
            ]
          : []),
        ...(state.completed >= 2
          ? [
              {
                message_id: 3,
                role: "user",
                content: "Show me the temperatures throughout the house.",
                created_at: "",
              },
              {
                message_id: 4,
                role: "assistant",
                content: "Original reply",
                created_at: "",
              },
            ]
          : []),
      ],
    });
  });
  vi.stubGlobal("fetch", fetcher);
  return { create, fetcher, list, sent, state, streams };
};

/** Opens the Home starter, reaches the empty conversation, and sends a prompt. */
const openCollisionConversation = async () => {
  const conversation = stubCollisionConversation();
  const router = mount("/");
  fireEvent.click(
    await screen.findByRole("button", { name: "Show me the temperatures" })
  );
  await waitFor(() => expect(conversation.create.resolve).toBeDefined());
  await act(() =>
    pendingResolver(() => conversation.list.resolve)(
      Response.json({
        conversations: [
          {
            id: "c-collision",
            created_at: "",
            message_count: 0,
            preview: "",
          },
        ],
      })
    )
  );
  fireEvent.click(
    await screen.findByRole("link", { name: "Empty conversation" })
  );
  await sendMessage("An already running prompt");
  return { ...conversation, router };
};

/** Serves a first turn the test fails and a queued turn that succeeds. */
const stubQueuedFailure = () => {
  const create = {
    resolve: undefined as ((value: Response) => void) | undefined,
  };
  const stream = conversationStream();
  const sent: string[] = [];
  const fetcher = vi.fn<
    (
      input: RequestInfo | URL,
      init?: RequestInit
    ) => Response | Promise<Response>
  >((input, init) => {
    const path = String(input);
    if (path === "/v1/agent/conversations" && init?.method === "POST") {
      const deferred = Promise.withResolvers<Response>();
      create.resolve = deferred.resolve;
      return deferred.promise;
    }
    if (path === "/v1/agent/conversations") {
      return Response.json({
        conversations: [
          {
            id: "c-collision",
            created_at: "",
            message_count: 0,
            preview: "",
          },
        ],
      });
    }
    if (path.endsWith("/messages/stream")) {
      sent.push(JSON.parse(String(init?.body)).text);
      if (sent.length === 1) {
        return new Response(stream.body);
      }
      return new Response(
        'data: {"type":"turn.finished","reply":"Queued reply"}\n\n'
      );
    }
    return Response.json({ messages: [] });
  });
  vi.stubGlobal("fetch", fetcher);
  return { create, fetcher, sent, stream: stream.controller };
};

/** Opens the queued-failure conversation and leaves its first turn running. */
const openQueuedFailure = async () => {
  const conversation = stubQueuedFailure();
  mount("/");
  fireEvent.click(
    await screen.findByRole("button", { name: "Show me the temperatures" })
  );
  await waitFor(() => expect(conversation.create.resolve).toBeDefined());
  fireEvent.click(
    await screen.findByRole("link", { name: "Empty conversation" })
  );
  await sendMessage("First prompt");
  await act(() =>
    pendingResolver(() => conversation.create.resolve)(
      Response.json({ id: "c-collision" })
    )
  );
  return conversation;
};

const historyWithViewReply = (reply: string) => ({
  messages: [
    { message_id: 1, role: "user", content: "Show a view", created_at: "" },
    {
      message_id: 2,
      role: "assistant",
      content: "Hidden tool narration",
      calls: [{ id: "call-1", name: "present_view", arguments: "{}" }],
      created_at: "",
    },
    {
      message_id: 3,
      role: "tool",
      content: JSON.stringify(artifact),
      tool_call_id: "call-1",
      tool_name: "present_view",
      artifacts: [artifact],
      created_at: "",
    },
    { message_id: 4, role: "assistant", content: reply, created_at: "" },
  ],
});

const presentViewEvents = (reply: string) => [
  {
    type: "tool.started",
    call_id: "call-1",
    name: "present_view",
    arguments: "{}",
  },
  {
    type: "tool.finished",
    call_id: "call-1",
    result: JSON.stringify(artifact),
  },
  { type: "view.presented", call_id: "call-1", artifact },
  { type: "turn.finished", reply },
];

const enqueueStreamEvents = async (
  stream: ReadableStreamDefaultController<Uint8Array>,
  events: unknown[]
) => {
  await act(() => {
    for (const event of events) {
      stream.enqueue(
        new TextEncoder().encode(`data: ${JSON.stringify(event)}\n\n`)
      );
    }
  });
};

const streamEvent =
  (stream: ReadableStreamDefaultController<Uint8Array>) =>
  async (value: unknown) => {
    await enqueueStreamEvents(stream, [value]);
  };

/** Sends a prompt and leaves the history reconciliation pending. */
const openCommittedView = async (reply: string) => {
  let stream!: ReadableStreamDefaultController<Uint8Array>;
  let streamOpened = false;
  let reconcile: ((response: Response) => void) | undefined;
  let recovered = false;
  const history = historyWithViewReply(reply);
  const fetcher = vi.fn<
    (input: RequestInfo | URL) => Response | Promise<Response>
  >((input) => {
    const path = String(input);
    if (path.endsWith("/messages/stream")) {
      streamOpened = true;
      return new Response(
        new ReadableStream({
          start(value) {
            stream = value;
          },
        })
      );
    }
    if (path.endsWith("/messages")) {
      if (recovered) {
        return Response.json(history);
      }
      if (streamOpened) {
        const deferred = Promise.withResolvers<Response>();
        reconcile = deferred.resolve;
        return deferred.promise;
      }
      return Response.json({ messages: [] });
    }
    return Response.json({ conversations: [] });
  });
  vi.stubGlobal("fetch", fetcher);
  mount("/conversations/committed-reply");
  const input = await sendMessage("Show a view");
  await waitFor(() => expect(stream).toBeDefined());
  return {
    fetcher,
    history,
    input,
    log: within(screen.getByRole("log")),
    reconcile: () => reconcile,
    recover: () => {
      recovered = true;
    },
    stream,
  };
};

const stubViewHistory = () => {
  vi.stubGlobal(
    "fetch",
    vi.fn<(input: RequestInfo | URL) => Response>((input) =>
      Response.json(
        String(input).endsWith("/messages")
          ? historyWithView
          : { conversations: [] }
      )
    )
  );
};

const stubStreamingConversation = () => {
  const stream = conversationStream();
  vi.stubGlobal(
    "fetch",
    vi.fn<(input: RequestInfo | URL) => Response>((input) => {
      const path = String(input);
      if (path.endsWith("/messages/stream")) {
        return new Response(stream.body);
      }
      return Response.json(
        path.endsWith("/messages") ? { messages: [] } : { conversations: [] }
      );
    })
  );
  return { controller: stream.controller };
};

const markdownReply =
  '**Room readings**\n\n- Kitchen: 21°C\n- Bedroom: 20°C\n\n| Room | Temperature |\n| --- | --- |\n| Kitchen | 21°C |\n\n```json\n{"temperature":21}\n```\n\n[Details](https://example.com)\n\n<script>unsafe()</script>';

const stubMarkdownReply = () => {
  vi.stubGlobal(
    "fetch",
    vi.fn<(input: RequestInfo | URL) => Response>((input) =>
      Response.json(
        String(input).endsWith("/messages")
          ? {
              messages: [
                {
                  message_id: 1,
                  role: "assistant",
                  content: "",
                  calls: [{ id: "c1", name: "get_device", arguments: "{}" }],
                  created_at: "",
                },
                {
                  message_id: 2,
                  role: "tool",
                  content: "Raw tool result is not reply text",
                  tool_call_id: "c1",
                  created_at: "",
                },
                {
                  message_id: 3,
                  role: "assistant",
                  content: markdownReply,
                  created_at: "",
                },
              ],
            }
          : { conversations: [] }
      )
    )
  );
};

describe("agent page", () => {
  beforeEach(installNoopResizeObserver);
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("keeps a running conversation alive across route changes and restores its live activity on return", async () => {
    const { controller, state } = stubConversationStream("A finished reply.");
    const router = mount("/conversations/c-running");
    const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
    await waitFor(() => expect(input).toHaveProperty("disabled", false));
    fireEvent.change(input, { target: { value: "Read the temperature" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => expect(state.signal).toBeDefined());
    await act(async () => {
      await router.navigate("/dashboards");
    });
    expect(state.signal?.aborted).toBeFalsy();
    await act(async () => {
      controller.enqueue(
        new TextEncoder().encode(
          'data: {"type":"tool.started","call_id":"lookup","name":"list_entities","arguments":"{}"}\n\n'
        )
      );
      await router.navigate("/conversations/c-running");
    });
    await expect(
      screen.findByText("Used 1 tool · 1 pending or unknown")
    ).resolves.toBeTruthy();
    expect(screen.getByRole("textbox", { name: "Ask Hearth" })).toHaveProperty(
      "disabled",
      true
    );
  });

  it("finishes a running conversation's reply after leaving and returning to its route", async () => {
    const { controller, fetcher, state } =
      stubConversationStream("A finished reply.");
    const router = mount("/conversations/c-running");
    const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
    await waitFor(() => expect(input).toHaveProperty("disabled", false));
    fireEvent.change(input, { target: { value: "Read the temperature" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => expect(state.signal).toBeDefined());
    await act(async () => {
      await router.navigate("/dashboards");
    });
    await act(async () => {
      await router.navigate("/conversations/c-running");
    });
    await act(() => {
      state.finished = true;
      controller.enqueue(
        new TextEncoder().encode(
          'data: {"type":"tool.finished","call_id":"lookup","result":"[]"}\n\ndata: {"type":"turn.finished","reply":"A finished reply."}\n\n'
        )
      );
      controller.close();
    });
    await expect(screen.findByText("A finished reply.")).resolves.toBeTruthy();
    expect(
      fetcher.mock.calls.filter(([path]) =>
        String(path).endsWith("/messages/stream")
      )
    ).toHaveLength(1);
  });

  it("stays on the active conversation when a background turn finishes", async () => {
    const { finished, router, streams } = await openTwoConversations();
    await act(() => {
      finished.add("c-a");
      streamController(streams, "c-a").enqueue(
        new TextEncoder().encode('data: {"type":"turn.finished"}\n\n')
      );
      streamController(streams, "c-a").close();
    });
    expect(router.state.location.pathname).toBe("/conversations/c-b");
    expect(screen.getByRole("textbox", { name: "Ask Hearth" })).toHaveProperty(
      "disabled",
      true
    );
  });

  it("does not show a background conversation's reply in the active conversation", async () => {
    const { finished, streams } = await openTwoConversations();
    await act(() => {
      finished.add("c-a");
      streamController(streams, "c-a").enqueue(
        new TextEncoder().encode('data: {"type":"turn.finished"}\n\n')
      );
      streamController(streams, "c-a").close();
    });
    expect(
      within(screen.getByRole("log")).queryByText("Reply for c-a")
    ).toBeNull();
  });

  it("restores each conversation's reply when its route is revisited", async () => {
    const { finished, router, signals, streams } = await openTwoConversations();
    await act(() => {
      finished.add("c-a");
      streamController(streams, "c-a").enqueue(
        new TextEncoder().encode('data: {"type":"turn.finished"}\n\n')
      );
      streamController(streams, "c-a").close();
    });
    await act(async () => {
      await router.navigate("/conversations/c-a");
    });
    await expect(screen.findByText("Reply for c-a")).resolves.toBeTruthy();
    expect(signals.get("c-b")?.aborted).toBeFalsy();
    await act(() => {
      finished.add("c-b");
      streamController(streams, "c-b").enqueue(
        new TextEncoder().encode('data: {"type":"turn.finished"}\n\n')
      );
      streamController(streams, "c-b").close();
    });
    await act(async () => {
      await router.navigate("/conversations/c-b");
    });
    await expect(screen.findByText("Reply for c-b")).resolves.toBeTruthy();
  });

  it("finishes creating a conversation after leaving Home without stealing navigation", async () => {
    let create!: (value: Response) => void;
    let stream!: ReadableStreamDefaultController<Uint8Array>;
    const body = new ReadableStream<Uint8Array>({
      start(value) {
        stream = value;
      },
    });
    vi.stubGlobal(
      "fetch",
      vi.fn<
        (
          input: RequestInfo | URL,
          init?: RequestInit
        ) => Response | Promise<Response>
      >((input, init) => {
        const path = String(input);
        if (path === "/v1/agent/conversations" && init?.method === "POST") {
          const deferred = Promise.withResolvers<Response>();
          create = deferred.resolve;
          return deferred.promise;
        }
        if (path.endsWith("/messages/stream")) {
          return new Response(body);
        }
        return Response.json(
          path.endsWith("/messages") ? { messages: [] } : { conversations: [] }
        );
      })
    );
    const router = mount("/");
    fireEvent.click(
      await screen.findByRole("button", { name: "Show me the temperatures" })
    );
    await waitFor(() => expect(create).toBeDefined());
    await act(async () => {
      await router.navigate("/dashboards");
      create(Response.json({ id: "c-late" }));
    });
    const recent = within(screen.getByRole("region", { name: "Recent" }));
    await expect(
      recent.findByRole("link", {
        name: "Show me the temperatures throughout the house.",
      })
    ).resolves.toBeTruthy();
    expect(router.state.location.pathname).toBe("/dashboards");
    await act(() => {
      stream.enqueue(
        new TextEncoder().encode('data: {"type":"turn.finished"}\n\n')
      );
      stream.close();
    });
    expect(router.state.location.pathname).toBe("/dashboards");
  });

  it("restores a failed creation's prompt when returning Home", async () => {
    let rejectCreation!: (error: Error) => void;
    vi.stubGlobal(
      "fetch",
      vi.fn<
        (
          input: RequestInfo | URL,
          init?: RequestInit
        ) => Response | Promise<Response>
      >((_input, init) => {
        if (init?.method === "POST") {
          const deferred = Promise.withResolvers<Response>();
          rejectCreation = deferred.reject;
          return deferred.promise;
        }
        return Response.json({ conversations: [] });
      })
    );
    const router = mount("/");
    fireEvent.click(
      await screen.findByRole("button", { name: "Show me the temperatures" })
    );
    await waitFor(() => expect(rejectCreation).toBeDefined());
    await act(async () => {
      await router.navigate("/dashboards");
      rejectCreation(new Error("Could not create conversation"));
    });
    expect(router.state.location.pathname).toBe("/dashboards");
    await act(async () => {
      await router.navigate("/");
    });
    await expect(screen.findByRole("alert")).resolves.toHaveProperty(
      "textContent",
      expect.stringContaining("Could not create conversation")
    );
    expect(screen.getByRole("textbox", { name: "Ask Hearth" })).toHaveProperty(
      "value",
      "Show me the temperatures throughout the house."
    );
    expect(
      screen.getByRole("button", { name: "Retry message" })
    ).toHaveProperty("disabled", false);
  });

  it("adopts a delayed creation that collides with an existing conversation", async () => {
    const conversation = await openCollisionConversation();
    await act(() =>
      pendingResolver(() => conversation.create.resolve)(
        Response.json({ id: "c-collision" })
      )
    );
    expect(conversation.router.state.location.pathname).toBe(
      "/conversations/c-collision"
    );
    expect(conversation.sent).toStrictEqual(["An already running prompt"]);
  });

  it("queues the new prompt until the active reply finishes", async () => {
    const conversation = await openCollisionConversation();
    await act(() =>
      pendingResolver(() => conversation.create.resolve)(
        Response.json({ id: "c-collision" })
      )
    );
    await expect(
      screen.findByText("Message queued until the current reply finishes.")
    ).resolves.toBeTruthy();
    await act(() => {
      conversation.state.completed = 1;
      conversation.streams[0].enqueue(
        new TextEncoder().encode('data: {"type":"turn.finished"}\n\n')
      );
      conversation.streams[0].close();
    });
    await waitFor(() =>
      expect(conversation.sent).toStrictEqual([
        "An already running prompt",
        "Show me the temperatures throughout the house.",
      ])
    );
    await expect(screen.findByText(/First reply/u)).resolves.toBeTruthy();
    await act(() => {
      conversation.streams[1].close();
    });
  });

  it("keeps both replies after the queued turn finishes", async () => {
    const conversation = await openCollisionConversation();
    await act(() =>
      pendingResolver(() => conversation.create.resolve)(
        Response.json({ id: "c-collision" })
      )
    );
    await screen.findByText("Message queued until the current reply finishes.");
    await act(() => {
      conversation.state.completed = 1;
      conversation.streams[0].enqueue(
        new TextEncoder().encode('data: {"type":"turn.finished"}\n\n')
      );
      conversation.streams[0].close();
    });
    await waitFor(() =>
      expect(conversation.sent).toStrictEqual([
        "An already running prompt",
        "Show me the temperatures throughout the house.",
      ])
    );
    await act(() => {
      conversation.state.completed = 2;
      conversation.streams[1].enqueue(
        new TextEncoder().encode('data: {"type":"turn.finished"}\n\n')
      );
      conversation.streams[1].close();
    });
    await expect(screen.findByText(/Original reply/u)).resolves.toBeTruthy();
  });

  it("keeps a queued prompt after the current reply fails", async () => {
    const { stream } = await openQueuedFailure();
    await expect(
      screen.findByText("Message queued until the current reply finishes.")
    ).resolves.toBeTruthy();
    await act(() => {
      stream.enqueue(
        new TextEncoder().encode(
          'data: {"type":"turn.failed","error":"Model failed"}\n\n'
        )
      );
      stream.close();
    });
    await expect(
      screen.findByText("Queued message has not been sent.")
    ).resolves.toBeTruthy();
  });

  it("keeps the failed prompt and blocks sending it until it is discarded", async () => {
    const { stream } = await openQueuedFailure();
    await screen.findByText("Message queued until the current reply finishes.");
    await act(() => {
      stream.enqueue(
        new TextEncoder().encode(
          'data: {"type":"turn.failed","error":"Model failed"}\n\n'
        )
      );
      stream.close();
    });
    await screen.findByText("Queued message has not been sent.");
    expect(
      screen.getByRole("button", { name: "Send queued message" })
    ).toHaveProperty("disabled", true);
    expect(screen.getByRole("textbox", { name: "Ask Hearth" })).toHaveProperty(
      "value",
      "First prompt"
    );
  });

  it("discards the failed prompt and sends the queued message", async () => {
    const { sent, stream } = await openQueuedFailure();
    await screen.findByText("Message queued until the current reply finishes.");
    await act(() => {
      stream.enqueue(
        new TextEncoder().encode(
          'data: {"type":"turn.failed","error":"Model failed"}\n\n'
        )
      );
      stream.close();
    });
    await screen.findByText("Queued message has not been sent.");
    fireEvent.click(
      screen.getByRole("button", { name: "Discard failed prompt" })
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Send queued message" })
      ).toHaveProperty("disabled", false)
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Send queued message" })
    );
    await waitFor(() =>
      expect(sent).toStrictEqual([
        "First prompt",
        "Show me the temperatures throughout the house.",
      ])
    );
    await waitFor(() =>
      expect(screen.queryByText("Queued message has not been sent.")).toBeNull()
    );
  });

  it("retains failed background activity and the prompt until history recovery succeeds", async () => {
    let stream!: ReadableStreamDefaultController<Uint8Array>;
    let historyUnavailable = false;
    let recover = false;
    const body = new ReadableStream<Uint8Array>({
      start(value) {
        stream = value;
      },
    });
    vi.stubGlobal(
      "fetch",
      vi.fn<
        (
          input: RequestInfo | URL,
          init?: RequestInit
        ) => Response | Promise<Response>
      >((input) => {
        const path = String(input);
        if (path.endsWith("/messages/stream")) {
          return new Response(body);
        }
        if (path.endsWith("/messages")) {
          if (historyUnavailable && !recover) {
            return new Response('{"detail":"History unavailable"}', {
              status: 503,
            });
          }
          return Response.json({ messages: [] });
        }
        return Response.json({ conversations: [] });
      })
    );
    const router = mount("/conversations/c-failed");
    await sendMessage("Keep this prompt");
    await act(async () => {
      await router.navigate("/dashboards");
      historyUnavailable = true;
      stream.error(new Error("Connection lost"));
    });
    await act(async () => {
      await router.navigate("/conversations/c-failed");
    });
    await expect(
      screen.findByRole("button", { name: "Retry loading history" })
    ).resolves.toBeTruthy();
    expect(screen.getByRole("textbox", { name: "Ask Hearth" })).toHaveProperty(
      "value",
      "Keep this prompt"
    );
    expect(
      screen.getByRole("button", { name: "Retry message" })
    ).toHaveProperty("disabled", true);
    recover = true;
    fireEvent.click(
      screen.getByRole("button", { name: "Retry loading history" })
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Retry message" })
      ).toHaveProperty("disabled", false)
    );
  });

  it("cancels owned streams when the backend changes and ignores their late results", async () => {
    let signal: AbortSignal | undefined;
    let stream!: ReadableStreamDefaultController<Uint8Array>;
    const body = new ReadableStream<Uint8Array>({
      start(value) {
        stream = value;
      },
    });
    vi.stubGlobal(
      "fetch",
      vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Response>(
        (input, init) => {
          const path = String(input);
          if (path.endsWith("/messages/stream")) {
            signal = init?.signal as AbortSignal;
            return new Response(body);
          }
          return Response.json(
            path.endsWith("/messages")
              ? { messages: [] }
              : { conversations: [] }
          );
        }
      )
    );
    mount("/conversations/c-backend");
    await sendMessage("Old server prompt");
    await waitFor(() => expect(signal).toBeDefined());
    await act(() => {
      setBaseUrl("http://other-core");
    });
    expect(signal?.aborted).toBeTruthy();
    await act(() => {
      stream.enqueue(
        new TextEncoder().encode(
          'data: {"type":"turn.finished","reply":"Old server reply"}\n\n'
        )
      );
      stream.close();
    });
    expect(
      within(screen.getByRole("log")).queryByText("Old server prompt")
    ).toBeNull();
    expect(screen.queryByText("Old server reply")).toBeNull();
  });

  it("does not create empty conversations during StrictMode initialization", async () => {
    const fetcher = vi.fn<
      (input: RequestInfo | URL, init?: RequestInit) => Response
    >(() => Response.json({ conversations: [] }));
    vi.stubGlobal("fetch", fetcher);
    mount("/");
    await expect(
      screen.findByText("What would make", { exact: false })
    ).resolves.toBeTruthy();
    expect(
      fetcher.mock.calls.every(([, init]) => !init || init.method !== "POST")
    ).toBeTruthy();
  });

  it("opens the conversation selected by its route, not a stale browser-local ID", async () => {
    localStorage.setItem("hearth.agentConversationId", "stale");
    const fetcher = vi.fn<(input: RequestInfo | URL) => Response>((input) =>
      Response.json(
        String(input).endsWith("/messages")
          ? {
              messages: [
                {
                  message_id: 1,
                  role: "user",
                  content: "Kitchen lights",
                  tool_calls: null,
                  created_at: "",
                },
              ],
            }
          : {
              conversations: [
                {
                  id: "selected",
                  created_at: "",
                  message_count: 1,
                  preview: "Kitchen lights",
                },
              ],
            }
      )
    );
    vi.stubGlobal("fetch", fetcher);
    mount("/conversations/selected");
    await expect(
      within(screen.getByRole("log")).findByText("Kitchen lights")
    ).resolves.toBeTruthy();
    expect(
      fetcher.mock.calls.some(([url]) =>
        String(url).includes("/conversations/selected/messages")
      )
    ).toBeTruthy();
    expect(
      fetcher.mock.calls.some(([url]) =>
        String(url).includes("/conversations/stale/messages")
      )
    ).toBeFalsy();
  });

  it("sends a starter immediately and lists the new conversation once the turn completes", async () => {
    let streamed: { text: string } | undefined;
    const fetcher = vi.fn<
      (input: RequestInfo | URL, init?: RequestInit) => Response
    >((input, init) => {
      const path = String(input);
      if (path === "/v1/agent/conversations" && init?.method === "POST") {
        return Response.json({ id: "conv-new" });
      }
      if (path === "/v1/agent/conversations") {
        return Response.json({
          conversations: streamed
            ? [
                {
                  id: "conv-new",
                  created_at: "",
                  message_count: 2,
                  preview: "Temperatures",
                },
              ]
            : [],
        });
      }
      if (path.endsWith("/messages/stream")) {
        streamed = JSON.parse(String(init?.body));
        return new Response('data: {"type":"turn.finished"}\n\n');
      }
      if (path.endsWith("/messages")) {
        return Response.json({ messages: [] });
      }
      throw new Error(`Unexpected request ${path}`);
    });
    vi.stubGlobal("fetch", fetcher);
    mount("/");
    fireEvent.click(
      await screen.findByRole("button", { name: "Show me the temperatures" })
    );
    await waitFor(() =>
      expect(streamed).toStrictEqual({
        text: "Show me the temperatures throughout the house.",
      })
    );
    await expect(
      within(screen.getByRole("region", { name: "Recent" })).findByRole(
        "link",
        {
          name: "Temperatures",
        }
      )
    ).resolves.toBeTruthy();
  });

  it("shows a committed reply before the stream closes", async () => {
    const reply = "The view is ready for your decision.";
    const run = await openCommittedView(reply);
    await enqueueStreamEvents(run.stream, presentViewEvents(reply));
    await expect(run.log.findByText(reply)).resolves.toHaveProperty(
      "textContent",
      reply
    );
    expect(run.log.getAllByText("Hello home")).toHaveLength(1);
    expect(run.input).toHaveProperty("disabled", true);
    expect(run.log.queryByText("Hidden tool narration")).toBeNull();
    expect(
      run.fetcher.mock.calls.filter(([path]) =>
        String(path).endsWith("/messages/stream")
      )
    ).toHaveLength(1);
  });

  it("shows a committed view before the stream closes when the turn has no reply", async () => {
    const run = await openCommittedView("");
    await enqueueStreamEvents(run.stream, presentViewEvents(""));
    expect(run.log.getAllByText("Hello home")).toHaveLength(1);
    expect(run.input).toHaveProperty("disabled", true);
    expect(run.log.queryByText("Hidden tool narration")).toBeNull();
    expect(
      screen.getByRole("log").querySelectorAll('[data-slot="message-markdown"]')
    ).toHaveLength(0);
    expect(
      run.fetcher.mock.calls.filter(([path]) =>
        String(path).endsWith("/messages/stream")
      )
    ).toHaveLength(1);
  });

  it("keeps the composer disabled while history reconciliation is pending", async () => {
    const reply = "The view is ready for your decision.";
    const run = await openCommittedView(reply);
    await enqueueStreamEvents(run.stream, presentViewEvents(reply));
    await act(() => run.stream.close());
    await waitFor(() => expect(run.reconcile()).toBeDefined());
    expect(screen.getByRole("button", { name: "Send message" })).toHaveProperty(
      "disabled",
      true
    );
    expect(run.input).toHaveProperty("disabled", true);
  });

  it("keeps the composer disabled after a failed reconciliation", async () => {
    const reply = "The view is ready for your decision.";
    const run = await openCommittedView(reply);
    await enqueueStreamEvents(run.stream, presentViewEvents(reply));
    await act(() => run.stream.close());
    await waitFor(() => expect(run.reconcile()).toBeDefined());
    await act(() =>
      pendingResolver(run.reconcile)(
        new Response("Unavailable", { status: 503 })
      )
    );
    await expect(screen.findByRole("alert")).resolves.toHaveProperty(
      "textContent",
      expect.stringContaining("Could not reconcile history")
    );
    expect(run.log.getAllByText(reply)).toHaveLength(1);
    expect(run.log.getAllByText("Hello home")).toHaveLength(1);
    expect(screen.getByRole("button", { name: "Send message" })).toHaveProperty(
      "disabled",
      true
    );
  });

  it("reconciles a committed reply without duplicating it", async () => {
    const reply = "The view is ready for your decision.";
    const run = await openCommittedView(reply);
    await enqueueStreamEvents(run.stream, presentViewEvents(reply));
    await act(() => run.stream.close());
    await waitFor(() => expect(run.reconcile()).toBeDefined());
    await act(() => pendingResolver(run.reconcile)(Response.json(run.history)));
    await waitFor(() => expect(run.input).toHaveProperty("disabled", false));
    expect(run.log.getAllByText(reply)).toHaveLength(1);
    expect(run.log.getAllByText("Hello home")).toHaveLength(1);
    expect(run.log.getAllByText("Hearth")).toHaveLength(1);
  });

  it.each([
    {
      name: "with a committed reply",
      reply: "The view is ready for your decision.",
    },
    { name: "without reply text", reply: "" },
  ])(
    "reconciles a committed view without retaining hidden tool narration ($name)",
    async ({ reply }) => {
      const run = await openCommittedView(reply);
      await enqueueStreamEvents(run.stream, presentViewEvents(reply));
      await act(() => run.stream.close());
      await waitFor(() => expect(run.reconcile()).toBeDefined());
      await act(() =>
        pendingResolver(run.reconcile)(Response.json(run.history))
      );
      await waitFor(() => expect(run.input).toHaveProperty("disabled", false));
      expect(run.log.queryByText("Hidden tool narration")).toBeNull();
      expect(
        run.fetcher.mock.calls.filter(([path]) =>
          String(path).endsWith("/messages/stream")
        )
      ).toHaveLength(1);
    }
  );

  it("reconciles a committed view without reply text", async () => {
    const run = await openCommittedView("");
    await enqueueStreamEvents(run.stream, presentViewEvents(""));
    await act(() => run.stream.close());
    await waitFor(() => expect(run.reconcile()).toBeDefined());
    await act(() => pendingResolver(run.reconcile)(Response.json(run.history)));
    await waitFor(() => expect(run.input).toHaveProperty("disabled", false));
    expect(run.log.getAllByText("Hello home")).toHaveLength(1);
    expect(run.log.getAllByText("Hearth")).toHaveLength(1);
    expect(run.log.queryByText("Hidden tool narration")).toBeNull();
    expect(
      screen.getByRole("log").querySelectorAll('[data-slot="message-markdown"]')
    ).toHaveLength(0);
  });

  it("recovers history after a failed reconciliation with a committed reply", async () => {
    const reply = "The view is ready for your decision.";
    const run = await openCommittedView(reply);
    await enqueueStreamEvents(run.stream, presentViewEvents(reply));
    await act(() => run.stream.close());
    await waitFor(() => expect(run.reconcile()).toBeDefined());
    await act(() =>
      pendingResolver(run.reconcile)(
        new Response("Unavailable", { status: 503 })
      )
    );
    await expect(screen.findByRole("alert")).resolves.toHaveProperty(
      "textContent",
      expect.stringContaining("Could not reconcile history")
    );
    run.recover();
    fireEvent.click(
      screen.getByRole("button", { name: "Retry loading history" })
    );
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    await waitFor(() => expect(run.input).toHaveProperty("disabled", false));
    expect(run.log.getAllByText(reply)).toHaveLength(1);
  });

  it("recovers history after a failed reconciliation without reply text", async () => {
    const run = await openCommittedView("");
    await enqueueStreamEvents(run.stream, presentViewEvents(""));
    await act(() => run.stream.close());
    await waitFor(() => expect(run.reconcile()).toBeDefined());
    await act(() =>
      pendingResolver(run.reconcile)(
        new Response("Unavailable", { status: 503 })
      )
    );
    await expect(screen.findByRole("alert")).resolves.toHaveProperty(
      "textContent",
      expect.stringContaining("Could not reconcile history")
    );
    run.recover();
    fireEvent.click(
      screen.getByRole("button", { name: "Retry loading history" })
    );
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    await waitFor(() => expect(run.input).toHaveProperty("disabled", false));
    expect(
      screen.getByRole("log").querySelectorAll('[data-slot="message-markdown"]')
    ).toHaveLength(0);
  });

  it.each([
    {
      name: "with a committed reply",
      reply: "The view is ready for your decision.",
    },
    { name: "without reply text", reply: "" },
  ])(
    "reconciles history on retry without losing the committed view ($name)",
    async ({ reply }) => {
      const run = await openCommittedView(reply);
      await enqueueStreamEvents(run.stream, presentViewEvents(reply));
      await act(() => run.stream.close());
      await waitFor(() => {
        if (run.reconcile() === undefined) {
          throw new Error("History reconciliation is still pending");
        }
      });
      await act(() =>
        pendingResolver(run.reconcile)(
          new Response("Unavailable", { status: 503 })
        )
      );
      await screen.findByRole("alert");
      run.recover();
      fireEvent.click(
        screen.getByRole("button", { name: "Retry loading history" })
      );
      await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
      expect(run.log.getAllByText("Hello home")).toHaveLength(1);
      expect(run.log.getAllByText("Hearth")).toHaveLength(1);
      expect(run.log.queryByText("Hidden tool narration")).toBeNull();
      expect(
        run.fetcher.mock.calls.filter(([path]) =>
          String(path).endsWith("/messages/stream")
        )
      ).toHaveLength(1);
    }
  );

  it("renders a view preview in the reply without opening the canvas", async () => {
    stubViewHistory();
    mount("/conversations/conv-1");
    const open = await screen.findByRole("button", { name: "Open in canvas" });
    expect(
      closestRequired<HTMLElement>(open, '[data-slot="artifact-preview"]')
        .dataset.kind
    ).toBe("view");
    expect(
      open.querySelector('[data-slot="artifact-open-detail"]')?.textContent
    ).toBe(" in canvas");
    expect(screen.queryByRole("region", { name: "View canvas" })).toBeNull();
    expect(
      screen.queryByRole("group", { name: "Workspace panels" })
    ).toBeNull();
    expect(
      within(screen.getByRole("log")).getByText("Hello home")
    ).toBeTruthy();
  });

  it("opens a view in the canvas on demand", async () => {
    stubViewHistory();
    mount("/conversations/conv-1");
    fireEvent.click(
      await screen.findByRole("button", { name: "Open in canvas" })
    );
    const canvas = await screen.findByRole("region", { name: "View canvas" });
    expect(
      screen.getByRole("button", { name: "Show selected view: Warm house" })
        .dataset.size
    ).toBe("touch");
    expect(
      screen.getByRole("button", { name: "Close selected view" }).dataset.size
    ).toBe("icon-touch");
    expect(within(canvas).getByText("Hello home")).toBeTruthy();
    // The open view is drawn once, in the canvas.
    expect(
      within(screen.getByRole("log")).queryByText("Hello home")
    ).toBeNull();
  });

  it("closes an unedited generated view without asking", async () => {
    stubViewHistory();
    mount("/conversations/conv-1");
    fireEvent.click(
      await screen.findByRole("button", { name: "Open in canvas" })
    );
    const canvas = await screen.findByRole("region", { name: "View canvas" });
    expect(
      screen.getByRole("group", { name: "Workspace panels" })
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Showing in canvas" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.click(within(canvas).getByRole("button", { name: "Close view" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "View canvas" })).toBeNull()
    );
  });

  it("returns focus to the composer after closing an unedited generated view", async () => {
    stubViewHistory();
    mount("/conversations/conv-1");
    fireEvent.click(
      await screen.findByRole("button", { name: "Open in canvas" })
    );
    const canvas = await screen.findByRole("region", { name: "View canvas" });
    fireEvent.click(screen.getByRole("button", { name: "Showing in canvas" }));
    fireEvent.click(within(canvas).getByRole("button", { name: "Close view" }));
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole("textbox", { name: "Ask Hearth" })
      )
    );
    expect(screen.getByRole("button", { name: "Open in canvas" })).toBeTruthy();
  });

  it("switches a view card's readings a screen away from view and keeps its height when they stop", async () => {
    const observers: {
      root?: Element | Document | null;
      rootMargin?: string;
      notify: (isIntersecting: boolean) => void;
    }[] = [];
    vi.stubGlobal(
      "IntersectionObserver",
      function intersectionObserverStub(
        this: ObserverStub,
        onIntersect: IntersectionObserverCallback,
        options?: IntersectionObserverInit
      ) {
        this.observe = (target) => {
          observers.push({
            root: options?.root,
            rootMargin: options?.rootMargin,
            notify: (isIntersecting) =>
              onIntersect(
                [{ isIntersecting, target } as IntersectionObserverEntry],
                this as unknown as IntersectionObserver
              ),
          });
        };
        this.unobserve = () => {};
        this.disconnect = () => {};
      }
    );
    const latest = () => {
      const observer = observers.at(-1);
      if (!observer) {
        throw new Error("No intersection observer has been created");
      }
      return observer;
    };
    const resizes: ResizeObserverCallback[] = [];
    vi.stubGlobal(
      "ResizeObserver",
      function resizeObserverStub(
        this: ObserverStub,
        onResize: ResizeObserverCallback
      ) {
        resizes.push(onResize);
        this.observe = () => {};
        this.unobserve = () => {};
        this.disconnect = () => {};
      }
    );
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(868);
    stubViewHistory();
    mount("/conversations/conv-1");
    const log = await screen.findByRole("log");
    await waitFor(() => expect(latest()?.root).toBe(log));
    // Observing within the conversation, with margin, switches cards before they reach the visible area.
    expect(latest()?.rootMargin).toMatch(/[1-9]/u);
    act(() => latest().notify(true));
    expect(within(log).getByText("Hello home")).toBeTruthy();
    act(() => {
      for (const notifyResize of resizes) {
        notifyResize([], {} as ResizeObserver);
      }
    });
    act(() => latest().notify(false));
    const summary = within(log).getByText("Open view to load live readings");
    expect(
      summary.closest<HTMLElement>('[data-slot="artifact-body"]')?.style
        .minHeight
    ).toBe("868px");
  });

  it("asks before closing a view with manual edits", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<(input: RequestInfo | URL) => Response>((input) => {
        const path = String(input);
        if (path.endsWith("/messages")) {
          return Response.json(historyWithView);
        }
        if (path.startsWith("/v1/entities")) {
          return Response.json({ items: [] });
        }
        return Response.json({ conversations: [] });
      })
    );
    mount("/conversations/conv-1");
    fireEvent.click(
      await screen.findByRole("button", { name: "Open in canvas" })
    );
    const canvas = await screen.findByRole("region", { name: "View canvas" });
    fireEvent.click(within(canvas).getByRole("button", { name: "Edit" }));
    fireEvent.change(within(canvas).getByLabelText("Dashboard title"), {
      target: { value: "Edited" },
    });
    fireEvent.click(within(canvas).getByRole("button", { name: "Close view" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Keep your changes?")).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "Stay" }));
    expect(screen.getByRole("region", { name: "View canvas" })).toBeTruthy();
    fireEvent.click(within(canvas).getByRole("button", { name: "Close view" }));
    fireEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: "Discard",
      })
    );
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "View canvas" })).toBeNull()
    );
  });

  it("shows one reply per turn with its tool calls collapsed into a summary", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<(input: RequestInfo | URL) => Response>((input) =>
        Response.json(
          String(input).endsWith("/messages")
            ? {
                messages: [
                  {
                    message_id: 1,
                    role: "user",
                    content: "Temperatures?",
                    created_at: "",
                  },
                  {
                    message_id: 2,
                    role: "assistant",
                    content: "",
                    calls: [
                      { id: "c1", name: "list_devices", arguments: "{}" },
                    ],
                    created_at: "",
                  },
                  {
                    message_id: 3,
                    role: "tool",
                    content: "[]",
                    tool_call_id: "c1",
                    created_at: "",
                  },
                  {
                    message_id: 4,
                    role: "assistant",
                    content: "",
                    calls: [{ id: "c2", name: "get_device", arguments: "{}" }],
                    created_at: "",
                  },
                  {
                    message_id: 5,
                    role: "tool",
                    content: "{}",
                    tool_call_id: "c2",
                    created_at: "",
                  },
                  {
                    message_id: 6,
                    role: "assistant",
                    content: "It is 21 degrees.",
                    created_at: "",
                  },
                ],
              }
            : { conversations: [] }
        )
      )
    );
    mount("/conversations/conv-1");
    const log = within(await screen.findByRole("log"));
    await expect(log.findByText("It is 21 degrees.")).resolves.toBeTruthy();
    expect(log.getAllByText("Hearth")).toHaveLength(1);
    const summary = log.getByText("Used 2 tools");
    expect(summary.closest("details")?.open).toBeFalsy();
    fireEvent.click(summary);
    expect(log.getByText(/list_devices/u)).toBeTruthy();
    expect(log.getByText(/get_device/u)).toBeTruthy();
  });

  it("keeps an expanded tool list open while streamed results update its pending count", async () => {
    const { controller } = stubStreamingConversation();
    mount("/conversations/conv-1");
    await sendMessage("Read the temperature");
    const event = streamEvent(controller);
    await event({
      type: "tool.started",
      call_id: "c1",
      name: "get_device",
      arguments: "{}",
    });
    const summary = await screen.findByText(
      "Used 1 tool · 1 pending or unknown"
    );
    fireEvent.click(summary);
    const list = closestRequired<HTMLDetailsElement>(summary, "details");
    expect(list.open).toBeTruthy();
    await event({
      type: "tool.started",
      call_id: "c2",
      name: "list_entities",
      arguments: "{}",
    });
    await expect(
      screen.findByText("Used 2 tools · 2 pending or unknown")
    ).resolves.toBeTruthy();
    await event({ type: "tool.finished", call_id: "c1", result: "21 degrees" });
    await expect(
      screen.findByText("Used 2 tools · 1 pending or unknown")
    ).resolves.toBeTruthy();
    expect(list.open).toBeTruthy();
    await act(() => controller.close());
  });

  it("reveals a finished tool's result inside the expanded list", async () => {
    const { controller } = stubStreamingConversation();
    mount("/conversations/conv-1");
    await sendMessage("Read the temperature");
    const event = streamEvent(controller);
    await event({
      type: "tool.started",
      call_id: "c1",
      name: "get_device",
      arguments: "{}",
    });
    const summary = await screen.findByText(
      "Used 1 tool · 1 pending or unknown"
    );
    fireEvent.click(summary);
    const list = closestRequired<HTMLDetailsElement>(summary, "details");
    await event({
      type: "tool.started",
      call_id: "c2",
      name: "list_entities",
      arguments: "{}",
    });
    await event({ type: "tool.finished", call_id: "c1", result: "21 degrees" });
    const tool = closestRequired<HTMLDetailsElement>(
      within(list).getByText(/get_device/u),
      "details"
    );
    fireEvent.click(within(tool).getByText(/get_device/u));
    expect(within(tool).getByText("21 degrees")).toBeTruthy();
    await event({ type: "tool.finished", call_id: "c2", result: "[]" });
    await expect(
      screen.findByText("Used 2 tools", { exact: true })
    ).resolves.toBeTruthy();
    expect(list.open).toBeTruthy();
    await act(() => controller.close());
  });

  it("holds a view's place while the model writes it and releases it when writing is rejected", async () => {
    const { controller } = stubStreamingConversation();
    mount("/conversations/conv-1");
    await sendMessage("Show the temperatures");
    const event = streamEvent(controller);
    const log = within(screen.getByRole("log"));
    // The model announces the call while it is still writing the definition.
    await event({
      type: "tool.preparing",
      call_id: "rejected",
      name: "compose_view",
    });
    await expect(log.findByText("Preparing a view")).resolves.toBeTruthy();
    await event({
      type: "tool.started",
      call_id: "rejected",
      name: "compose_view",
      arguments: "{}",
    });
    expect(log.getAllByText("Preparing a view")).toHaveLength(1);
    await event({
      type: "tool.finished",
      call_id: "rejected",
      result: "invalid view definition",
    });
    await waitFor(() => expect(log.queryByText("Preparing a view")).toBeNull());
    await event({
      type: "tool.preparing",
      call_id: "lookup",
      name: "list_entities",
    });
    expect(log.queryByText("Preparing a view")).toBeNull();
    await act(() => controller.close());
  });

  it("holds a view's place until Core accepts the presented view", async () => {
    const { controller } = stubStreamingConversation();
    mount("/conversations/conv-1");
    await sendMessage("Show the temperatures");
    const event = streamEvent(controller);
    const log = within(screen.getByRole("log"));
    await event({
      type: "tool.preparing",
      call_id: "accepted",
      name: "compose_view",
    });
    await expect(log.findByText("Preparing a view")).resolves.toBeTruthy();
    await event({
      type: "tool.started",
      call_id: "accepted",
      name: "compose_view",
      arguments: "{}",
    });
    await event({
      type: "tool.finished",
      call_id: "accepted",
      result: JSON.stringify(artifact),
    });
    expect(log.getByText("Preparing a view")).toBeTruthy();
    await event({ type: "view.presented", call_id: "accepted", artifact });
    await expect(
      log.findByRole("heading", { name: "Warm house" })
    ).resolves.toBeTruthy();
    expect(log.queryByText("Preparing a view")).toBeNull();
    await act(() => controller.close());
  });

  it("renders assistant Markdown and hides raw tool and script text", async () => {
    stubMarkdownReply();
    mount("/conversations/conv-1");
    const log = within(screen.getByRole("log"));
    const readings = await log.findByText("Room readings");
    expect(readings.tagName).toBe("STRONG");
    const markdown = within(
      closestRequired<HTMLElement>(readings, '[data-slot="message-markdown"]')
    );
    expect(markdown.getAllByRole("listitem")).toHaveLength(2);
    expect(log.getByRole("table")).toBeTruthy();
    expect(log.queryByText("unsafe()")).toBeNull();
  });

  it("renders Markdown links and code blocks", async () => {
    stubMarkdownReply();
    mount("/conversations/conv-1");
    const log = within(screen.getByRole("log"));
    await expect(log.findByText("Room readings")).resolves.toBeTruthy();
    expect(
      log.getByRole("link", { name: "Details" }).getAttribute("href")
    ).toBe("https://example.com");
    expect(log.getByText('{"temperature":21}').closest("pre")).toBeTruthy();
  });

  it("copies the original response text with native copied feedback", async () => {
    const writeText = vi.fn<() => Promise<void>>(() => Promise.resolve());
    vi.stubGlobal("navigator", {
      userAgent: "jsdom",
      clipboard: { writeText },
    });
    stubMarkdownReply();
    mount("/conversations/conv-1");
    const log = within(screen.getByRole("log"));
    await expect(log.findByText("Room readings")).resolves.toBeTruthy();
    const copy = log.getByRole("button", { name: "Copy response" });
    expect(copy.dataset.size).toBe("touch");
    fireEvent.click(copy);
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(markdownReply));
    await expect(log.findByText("Copied")).resolves.toBeTruthy();
    expect(log.getByRole("button", { name: "Response copied" })).toBeTruthy();
  });

  it("does not submit an IME commit or Shift+Enter, and sends ordinary Enter once", async () => {
    let sent: string | undefined;
    const fetcher = vi.fn<
      (input: RequestInfo | URL, init?: RequestInit) => Response
    >((input, init) => {
      const path = String(input);
      if (path === "/v1/agent/conversations" && init?.method === "POST") {
        return Response.json({ id: "conv-new" });
      }
      if (path.endsWith("/messages/stream")) {
        sent = JSON.parse(String(init?.body)).text;
        return new Response('data: {"type":"turn.finished"}\n\n');
      }
      return Response.json(
        path.endsWith("/messages") ? { messages: [] } : { conversations: [] }
      );
    });
    vi.stubGlobal("fetch", fetcher);
    mount("/");
    const input = await screen.findByRole("textbox", { name: "Ask Hearth" });
    fireEvent.change(input, { target: { value: "Read the temperature" } });
    fireEvent.keyDown(input, { key: "Enter", isComposing: true, keyCode: 229 });
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    expect(
      fetcher.mock.calls.some(([, init]) => init?.method === "POST")
    ).toBeFalsy();
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(sent).toBe("Read the temperature"));
    expect(
      fetcher.mock.calls.filter(([url]) =>
        String(url).endsWith("/messages/stream")
      )
    ).toHaveLength(1);
  });

  it("does not present a retained tool call without a result as complete", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<(input: RequestInfo | URL) => Response>((input) =>
        Response.json(
          String(input).endsWith("/messages")
            ? {
                messages: [
                  {
                    message_id: 1,
                    role: "assistant",
                    content: "",
                    calls: [{ id: "c1", name: "get_device", arguments: "{}" }],
                    created_at: "",
                  },
                ],
              }
            : { conversations: [] }
        )
      )
    );
    mount("/conversations/conv-1");
    const summary = await screen.findByText(
      "Used 1 tool · 1 pending or unknown"
    );
    fireEvent.click(summary);
    expect(
      screen.getByText(/get_device · outcome unknown or pending/u)
    ).toBeTruthy();
    expect(screen.queryByText(/get_device · complete/u)).toBeNull();
  });

  it("blocks native composer submission while view edits are invalid and keeps the prompt on discard", async () => {
    const fetcher = vi.fn<
      (input: RequestInfo | URL, init?: RequestInit) => Response
    >((input) => {
      const path = String(input);
      if (path.endsWith("/messages")) {
        return Response.json(historyWithView);
      }
      if (path.startsWith("/v1/entities")) {
        return Response.json({ items: [] });
      }
      return Response.json({ conversations: [] });
    });
    vi.stubGlobal("fetch", fetcher);
    mount("/conversations/conv-1");
    fireEvent.click(
      await screen.findByRole("button", { name: "Open in canvas" })
    );
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Dashboard title"), {
      target: { value: "" },
    });
    const input = screen.getByRole("textbox", { name: "Ask Hearth" });
    fireEvent.change(input, { target: { value: "Adjust this view" } });
    expect(screen.getByRole("button", { name: "Send message" })).toHaveProperty(
      "disabled",
      true
    );
    fireEvent.keyDown(input, { key: "Enter" });
    expect(
      fetcher.mock.calls.some(([, init]) => init?.method === "POST")
    ).toBeFalsy();
    fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "View canvas" })).toBeNull()
    );
    expect(input).toHaveProperty("value", "Adjust this view");
    expect(screen.getByRole("button", { name: "Send message" })).toHaveProperty(
      "disabled",
      false
    );
  });
});
