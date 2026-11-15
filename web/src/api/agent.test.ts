import { afterEach, describe, expect, it, vi } from "vitest";

import { loadHistory, streamTurn } from "./agent";

describe("Agent transport", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("parses split SSE frames and preserves the complete artifact definition", async () => {
    const artifact = {
      kind: "hearth.view",
      artifact_id: "view-1",
      definition: { future: { preserved: true } },
    };
    const text = `data: ${JSON.stringify({ type: "view.presented", call_id: "c", artifact })}\r\n\r\ndata: {"type":"turn.finished"}\r\n\r\n`;
    const encoder = new TextEncoder();
    const bytes = encoder.encode(text);
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(bytes.slice(0, 23));
        controller.enqueue(bytes.slice(23, 75));
        controller.enqueue(bytes.slice(75));
        controller.close();
      },
    });
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(new Response(stream));
    vi.stubGlobal("fetch", fetcher);
    const events: unknown[] = [];
    await streamTurn(
      "conv-1",
      "hello",
      undefined,
      (event) => events.push(event),
      new AbortController().signal
    );
    expect(events).toStrictEqual([
      { type: "view.presented", call_id: "c", artifact },
      { type: "turn.finished" },
    ]);
    const [[, init]] = fetcher.mock.calls;
    expect(JSON.parse(String(init?.body))).toStrictEqual({ text: "hello" });
  });

  it("rejects malformed history at the transport boundary", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>().mockResolvedValue(
        Response.json({
          messages: [
            {
              message_id: "bad",
              role: "assistant",
              content: "",
              created_at: "",
            },
          ],
        })
      )
    );
    await expect(loadHistory("conv-1")).rejects.toThrow(/messages/u);
  });
});
