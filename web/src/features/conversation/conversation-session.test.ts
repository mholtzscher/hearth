import { afterEach, describe, expect, it, vi } from "vitest";

import { ConversationSession } from "./conversation-session";

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
});
