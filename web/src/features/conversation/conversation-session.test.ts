import { afterEach, describe, expect, it, vi } from "vitest";

import {
  ConversationSession,
  FOLLOW_INTERVAL_MS,
} from "./conversation-session";

const options = { context: undefined, draftVersion: 0, onCreated: () => {} };
const json = (value: unknown) => Response.json(value);

const failedTurn = async (historyFailure: boolean) => {
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  let failHistory = false;
  const sent: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/v1/agent/conversations") {
        return json({ id: "canonical" });
      }
      if (path.endsWith("/messages/stream")) {
        sent.push(JSON.parse(String(init?.body)).text);
        if (sent.length > 1) {
          return new Response('data: {"type":"turn.finished"}\n\n');
        }
        return new Response(
          new ReadableStream({
            start(value) {
              controller = value;
            },
          })
        );
      }
      return failHistory
        ? new Response('{"detail":"History unavailable"}', { status: 503 })
        : json({ messages: [] });
    })
  );
  const owner: ConversationSession = new ConversationSession(
    "canonical",
    "canonical",
    () => owner,
    () => {},
    () => {}
  );
  await owner.load();
  const first = owner.send("First prompt", options);
  const creating = new ConversationSession(
    "new",
    undefined,
    () => owner,
    () => {},
    () => {}
  );
  const handedOff = creating.send("Original queued prompt", options);
  const fail = async () => {
    failHistory = historyFailure;
    controller.enqueue(
      new TextEncoder().encode(
        'data: {"type":"turn.failed","error":"Model failed"}\n\n'
      )
    );
    controller.close();
    await first;
    await handedOff;
  };
  return {
    owner,
    sent,
    fail,
    setHistoryFailure: (value: boolean) => {
      failHistory = value;
    },
  };
};

// Rows and fetch scripts for following a turn Core runs without this page.
const userRow = (text: string) => ({
  message_id: 1,
  role: "user",
  content: text,
  created_at: "",
});
const replyRow = (text: string) => ({
  message_id: 2,
  role: "assistant",
  content: text,
  created_at: "",
});
const running = { state: "running", started_at: "2026-11-25T02:28:58Z" };

// Serves each history read from the next scripted body, repeating the last.
const scriptHistory = (bodies: unknown[]) => {
  const reads: number[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = String(input);
      if (!path.endsWith("/messages")) {
        throw new Error(`Unexpected request ${path}`);
      }
      reads.push(Date.now());
      return json(bodies[Math.min(reads.length, bodies.length) - 1]);
    })
  );
  return reads;
};
const session = (settled = vi.fn<() => void>()) => ({
  settled,
  conversation: new ConversationSession(
    "conv",
    "conv",
    () => {
      throw new Error("no creation expected");
    },
    settled,
    () => {}
  ),
});

describe(ConversationSession, () => {
  afterEach(() => vi.unstubAllGlobals());

  it("loads an existing canonical owner before handing it a newly created prompt", async () => {
    let resolveHistory!: (value: Response) => void;
    const sent: string[] = [];
    let reads = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path === "/v1/agent/conversations") {
          return json({ id: "canonical" });
        }
        if (path.endsWith("/messages/stream")) {
          sent.push(JSON.parse(String(init?.body)).text);
          return new Response('data: {"type":"turn.finished"}\n\n');
        }
        reads += 1;
        if (reads === 1) {
          const pending = Promise.withResolvers<Response>();
          resolveHistory = pending.resolve;
          return pending.promise;
        }
        return json({ messages: [] });
      })
    );
    const owner: ConversationSession = new ConversationSession(
      "canonical",
      "canonical",
      () => owner,
      () => {},
      () => {}
    );
    const creating = new ConversationSession(
      "new",
      undefined,
      () => owner,
      () => {},
      () => {}
    );
    const sending = creating.send("Original prompt", options);
    await vi.waitFor(() => expect(resolveHistory).toBeDefined());
    expect(owner.getSnapshot().queued?.text).toBe("Original prompt");
    expect(sent).toStrictEqual([]);
    resolveHistory(json({ messages: [] }));
    await sending;
    expect(sent).toStrictEqual(["Original prompt"]);
    expect(owner.getSnapshot().queued).toBeNull();
  });

  it.each([false, true])(
    "preserves both prompts after a failed canonical turn (history failure: %s)",
    async (historyFailure) => {
      const { owner, sent, fail } = await failedTurn(historyFailure);
      await vi.waitFor(() =>
        expect(owner.getSnapshot().queued?.text).toBe("Original queued prompt")
      );
      await fail();
      expect(owner.getSnapshot().failedPrompt).toBe("First prompt");
      expect(owner.getSnapshot().queued?.text).toBe("Original queued prompt");
      expect(sent).toStrictEqual(["First prompt"]);
    }
  );

  it.each([false, true])(
    "keeps a failed prompt until discarded, then sends the queued prompt (history failure: %s)",
    async (historyFailure) => {
      const { owner, sent, fail, setHistoryFailure } =
        await failedTurn(historyFailure);
      await vi.waitFor(() =>
        expect(owner.getSnapshot().queued?.text).toBe("Original queued prompt")
      );
      await fail();
      if (historyFailure) {
        setHistoryFailure(false);
        await owner.load();
      }
      await owner.sendQueued();
      expect(sent).toStrictEqual(["First prompt"]);
      expect(owner.getSnapshot().failedPrompt).toBe("First prompt");
      owner.discardFailedPrompt();
      await owner.sendQueued();
      expect(sent).toStrictEqual(["First prompt", "Original queued prompt"]);
      expect(owner.getSnapshot().queued).toBeNull();
    }
  );

  describe("following a turn Core runs without this page", () => {
    afterEach(() => vi.useRealTimers());

    it("stays busy while Core reports the turn running and shows the reply once it ends", async () => {
      vi.useFakeTimers();
      const reads = scriptHistory([
        { messages: [userRow("Which sensors are offline?")], turn: running },
        { messages: [userRow("Which sensors are offline?")], turn: running },
        {
          messages: [
            userRow("Which sensors are offline?"),
            replyRow("Two are offline."),
          ],
        },
      ]);
      const { conversation, settled } = session();
      const loading = conversation.load();
      await vi.advanceTimersByTimeAsync(0);
      expect(conversation.getSnapshot()).toMatchObject({
        busy: true,
        following: true,
      });
      await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS);
      expect([reads.length, conversation.getSnapshot().busy]).toStrictEqual([
        2,
        true,
      ]);
      await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS);
      await loading;
      const state = conversation.getSnapshot();
      expect(state).toMatchObject({ busy: false, following: false, error: "" });
      expect(state.items.at(-1)?.text).toBe("Two are offline.");
      expect(settled).toHaveBeenCalledOnce();
    });

    it("reads again as soon as the page becomes visible", async () => {
      vi.useFakeTimers();
      const reads = scriptHistory([
        { messages: [userRow("Status?")], turn: running },
        { messages: [userRow("Status?"), replyRow("All good.")] },
      ]);
      const { conversation } = session();
      const loading = conversation.load();
      await vi.advanceTimersByTimeAsync(0);
      expect(reads).toHaveLength(1);
      document.dispatchEvent(new Event("visibilitychange"));
      await loading;
      expect(reads).toHaveLength(2);
      expect(conversation.getSnapshot().busy).toBeFalsy();
    });

    it("offers a failed turn's own prompt once and not again after it is discarded", async () => {
      // The failed turn never saved its prompt, so history still ends with an
      // earlier, answered one; only the turn's own prompt may be offered.
      const failed = {
        state: "failed",
        started_at: "2026-11-25T02:28:58Z",
        error: "agent turn timed out",
        prompt: "Chart the garage",
      };
      scriptHistory([
        {
          messages: [userRow("Earlier question"), replyRow("Earlier answer")],
          turn: failed,
        },
      ]);
      const { conversation } = session();
      await conversation.load();
      expect(conversation.getSnapshot()).toMatchObject({
        error: "agent turn timed out",
        failedPrompt: "Chart the garage",
      });
      conversation.discardFailedPrompt();
      await conversation.load();
      expect(conversation.getSnapshot().failedPrompt).toBe("");
    });

    it("follows a turn whose stream dropped instead of failing it", async () => {
      vi.useFakeTimers();
      let historyReads = 0;
      vi.stubGlobal(
        "fetch",
        vi.fn((input: RequestInfo | URL) => {
          const path = String(input);
          if (path.endsWith("/messages/stream")) {
            return new Response('data: {"type":"turn.started"}\n\n');
          }
          historyReads += 1;
          if (historyReads === 1) {
            return json({ messages: [] });
          }
          return json(
            historyReads < 4
              ? { messages: [userRow("Which sensors?")], turn: running }
              : { messages: [userRow("Which sensors?"), replyRow("Two.")] }
          );
        })
      );
      const { conversation } = session();
      await conversation.load();
      const sending = conversation.send("Which sensors?", options);
      await vi.advanceTimersByTimeAsync(0);
      expect(conversation.getSnapshot()).toMatchObject({
        busy: true,
        following: true,
        error: "",
        failedPrompt: "",
      });
      await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS * 2);
      await sending;
      expect(conversation.getSnapshot()).toMatchObject({
        busy: false,
        error: "",
        failedPrompt: "",
      });
      expect(conversation.getSnapshot().items.at(-1)?.text).toBe("Two.");
    });

    it("stops following when disposed without settling", async () => {
      vi.useFakeTimers();
      const reads = scriptHistory([
        { messages: [userRow("Status?")], turn: running },
      ]);
      const { conversation, settled } = session();
      const loading = conversation.load();
      await vi.advanceTimersByTimeAsync(0);
      conversation.dispose();
      await loading;
      await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS * 3);
      expect(reads).toHaveLength(1);
      expect(settled).not.toHaveBeenCalled();
    });

    it("stops following on a client error such as a pruned conversation", async () => {
      vi.useFakeTimers();
      let reads = 0;
      vi.stubGlobal(
        "fetch",
        vi.fn(() => {
          reads += 1;
          return reads === 1
            ? json({ messages: [userRow("Status?")], turn: running })
            : new Response('{"detail":"agent conversation not found"}', {
                status: 404,
              });
        })
      );
      const { conversation } = session();
      const loading = conversation.load();
      await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS);
      await loading;
      expect(conversation.getSnapshot()).toMatchObject({
        busy: false,
        needsRecovery: true,
        error: "agent conversation not found",
      });
    });

    it("offers a dropped prompt Core never recorded even when an older turn failed", async () => {
      const olderFailure = {
        state: "failed",
        started_at: "2026-11-25T01:00:00Z",
        error: "agent turn failed",
      };
      let historyReads = 0;
      vi.stubGlobal(
        "fetch",
        vi.fn((input: RequestInfo | URL) => {
          if (String(input).endsWith("/messages/stream")) {
            return new Response("");
          }
          historyReads += 1;
          return json({
            messages: [userRow("Older prompt")],
            turn: olderFailure,
          });
        })
      );
      const { conversation } = session();
      await conversation.load();
      conversation.discardFailedPrompt();
      await conversation.send("New prompt", options);
      expect(historyReads).toBe(2);
      expect(conversation.getSnapshot()).toMatchObject({
        busy: false,
        error: "The connection ended before the turn started.",
        failedPrompt: "New prompt",
      });
    });

    it("does not mistake an earlier identical prompt for a dropped one", async () => {
      let historyReads = 0;
      vi.stubGlobal(
        "fetch",
        vi.fn((input: RequestInfo | URL) => {
          if (String(input).endsWith("/messages/stream")) {
            return new Response("");
          }
          historyReads += 1;
          return json({ messages: [userRow("yes"), replyRow("Done.")] });
        })
      );
      const { conversation } = session();
      await conversation.load();
      await conversation.send("yes", options);
      expect(historyReads).toBe(2);
      expect(conversation.getSnapshot()).toMatchObject({
        error: "The connection ended before the turn started.",
        failedPrompt: "yes",
      });
    });

    it("hands a draft completed while following to the open editor", async () => {
      vi.useFakeTimers();
      const artifact = {
        kind: "hearth.view",
        artifact_id: "view-1",
        definition: { title: "Garage" },
      };
      const finished = [
        userRow("Show the garage"),
        {
          message_id: 2,
          role: "assistant",
          content: "",
          calls: [{ id: "c", name: "compose_view", arguments: "{}" }],
          created_at: "",
        },
        {
          message_id: 3,
          role: "tool",
          content: "ok",
          tool_call_id: "c",
          tool_name: "compose_view",
          artifacts: [artifact],
          created_at: "",
        },
      ];
      let historyReads = 0;
      vi.stubGlobal(
        "fetch",
        vi.fn((input: RequestInfo | URL) => {
          if (String(input).endsWith("/messages/stream")) {
            return new Response("");
          }
          historyReads += 1;
          if (historyReads === 1) {
            return json({ messages: [] });
          }
          return json(
            historyReads === 2
              ? { messages: [userRow("Show the garage")], turn: running }
              : { messages: finished }
          );
        })
      );
      const { conversation } = session();
      const presented = vi.fn<(id: string, draftVersion: number) => void>();
      conversation.subscribeArtifacts((value, draftVersion) =>
        presented(value.artifact.artifact_id, draftVersion)
      );
      await conversation.load();
      const sending = conversation.send("Show the garage", {
        ...options,
        context: {
          definition: {
            schema_version: 1,
            catalog_version: "hearth.dashboard/v1",
            title: "Home",
            spec: {
              root: "heading",
              elements: {
                heading: { type: "Heading", props: { text: "Home" } },
              },
            },
          },
        },
        draftVersion: 3,
      });
      await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS);
      await sending;
      expect(presented).toHaveBeenCalledExactlyOnceWith("view-1", 3);
    });
  });
});
