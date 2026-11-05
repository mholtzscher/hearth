import { afterEach, expect, it, vi } from "vitest";
import { ConversationSession } from "./ConversationSession";

afterEach(() => vi.unstubAllGlobals());
const options = { context: undefined, draftVersion: 0, onCreated: () => {} };
const json = (value: unknown) => new Response(JSON.stringify(value));

it("loads an existing canonical owner before handing it a newly created prompt", async () => {
  let resolveHistory!: (value: Response) => void;
  const sent: string[] = [];
  let reads = 0;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/agent/conversations") return json({ id: "canonical" });
    if (path.endsWith("/messages/stream")) { sent.push(JSON.parse(String(init?.body)).text); return new Response('data: {"type":"turn.finished"}\n\n'); }
    if (++reads === 1) return new Promise<Response>(resolve => { resolveHistory = resolve; });
    return json({ messages: [] });
  }));
  const owner: ConversationSession = new ConversationSession("canonical", "canonical", () => owner, () => {}, () => {});
  const creating = new ConversationSession("new", undefined, () => owner, () => {}, () => {});
  const sending = creating.send("Original prompt", options);
  await vi.waitFor(() => expect(resolveHistory).toBeDefined());
  expect(owner.getSnapshot().queued?.text).toBe("Original prompt");
  expect(sent).toEqual([]);
  resolveHistory(json({ messages: [] }));
  await sending;
  expect(sent).toEqual(["Original prompt"]);
  expect(owner.getSnapshot().queued).toBeNull();
});

it.each([false, true])("preserves both prompts after a failed canonical turn (history failure: %s)", async historyFailure => {
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  let failHistory = false;
  const sent: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/agent/conversations") return json({ id: "canonical" });
    if (path.endsWith("/messages/stream")) {
      sent.push(JSON.parse(String(init?.body)).text);
      if (sent.length > 1) return new Response('data: {"type":"turn.finished"}\n\n');
      return new Response(new ReadableStream({ start(value) { controller = value; } }));
    }
    return failHistory ? new Response('{"detail":"History unavailable"}', { status: 503 }) : json({ messages: [] });
  }));
  const owner: ConversationSession = new ConversationSession("canonical", "canonical", () => owner, () => {}, () => {});
  await owner.load();
  const first = owner.send("First prompt", options);
  const creating = new ConversationSession("new", undefined, () => owner, () => {}, () => {});
  const handedOff = creating.send("Original queued prompt", options);
  await vi.waitFor(() => expect(owner.getSnapshot().queued?.text).toBe("Original queued prompt"));
  failHistory = historyFailure;
  controller.enqueue(new TextEncoder().encode('data: {"type":"turn.failed","error":"Model failed"}\n\n')); controller.close();
  await first; await handedOff;
  expect(owner.getSnapshot().failedPrompt).toBe("First prompt");
  expect(owner.getSnapshot().queued?.text).toBe("Original queued prompt");
  expect(sent).toEqual(["First prompt"]);
  if (historyFailure) { failHistory = false; await owner.load(); }
  await owner.sendQueued();
  expect(sent).toEqual(["First prompt"]);
  expect(owner.getSnapshot().failedPrompt).toBe("First prompt");
  owner.discardFailedPrompt();
  await owner.sendQueued();
  expect(sent).toEqual(["First prompt", "Original queued prompt"]);
  expect(owner.getSnapshot().queued).toBeNull();
});
