import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FLUE_AGENT_PATH, FLUE_INSTANCE_KEY } from "../api/flue.ts";
import FlueAgentPage from "./FlueAgentPage.tsx";

/**
 * FlueAgentPage uses `useFlueAgent()` from @flue/react, which reaches the Flue
 * runtime only through the global `fetch` (history reads plus the live updates
 * stream). Stubbing that global is the seam that lets each test answer those
 * requests without a Flue process: history 404s model a not-yet-created
 * instance, history snapshots drive transcript/tool rendering, POST admission
 * receipts model sends, and `hang` stands in for the open-ended SSE stream.
 */

interface FlueRequest {
  method: string;
  path: string;
  view: string | null;
  body: unknown;
}

type FlueResponder = (request: FlueRequest) => Response | "hang";

function installFlueFetch(respond: FlueResponder): FlueRequest[] {
  const requests: FlueRequest[] = [];
  vi.stubGlobal(
    "fetch",
    (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const url = new URL(String(input), "http://hearth.test");
      const request: FlueRequest = {
        method: (init?.method ?? "GET").toUpperCase(),
        path: url.pathname,
        view: url.searchParams.get("view"),
        body: typeof init?.body === "string" ? JSON.parse(init.body) : null,
      };
      requests.push(request);
      const response = respond(request);
      // An open updates stream never resolves on its own; aborting it on
      // unmount keeps the fake aligned with the SDK's own cancellation.
      if (response === "hang") return hangUntilAborted(init?.signal);
      return Promise.resolve(response);
    },
  );
  return requests;
}

function hangUntilAborted(signal: AbortSignal | null | undefined): Promise<Response> {
  return new Promise<Response>((_resolve, reject) => {
    const abort = () => reject(new DOMException("Aborted", "AbortError"));
    if (signal?.aborted) {
      abort();
      return;
    }
    signal?.addEventListener("abort", abort, { once: true });
  });
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function notFound(): Response {
  return json({ title: "Not Found", detail: "agent instance not found" }, 404);
}

/** Materialized history snapshot, matching the `?view=history` response. */
function historySnapshot(conversationId: string, messages: unknown[] = []): Response {
  return json({
    v: 1,
    conversationId,
    incarnation: conversationId,
    offset: "0",
    messages,
    settlements: [],
  });
}

/** Let pending fetch handlers and their React state updates run to completion. */
async function flushMacrotasks(turns = 8): Promise<void> {
  for (let index = 0; index < turns; index += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("FlueAgentPage", () => {
  it("persists a fresh instance id and shows the empty transcript before it exists", async () => {
    const requests = installFlueFetch((request) => {
      if (request.method === "GET" && request.view === "history") return notFound();
      return "hang";
    });

    render(<FlueAgentPage />);

    await screen.findByText(/No messages yet/);
    await flushMacrotasks();

    const instanceId = localStorage.getItem(FLUE_INSTANCE_KEY);
    expect(instanceId).toMatch(/^web-/);
    // The page addresses the persisted instance over the same-origin /flue path.
    expect(
      requests.some(
        (request) =>
          request.method === "GET" &&
          request.view === "history" &&
          request.path === `${FLUE_AGENT_PATH}/${instanceId}`,
      ),
    ).toBe(true);
  });

  it("shows the optimistic user message and posts the prompt to the instance", async () => {
    const instanceId = "web-send-test";
    localStorage.setItem(FLUE_INSTANCE_KEY, instanceId);
    const requests = installFlueFetch((request) => {
      if (request.method === "POST") {
        return json(
          {
            streamUrl: `${FLUE_AGENT_PATH}/${instanceId}`,
            offset: "0",
            submissionId: "sub-1",
            uid: "uid-1",
          },
          202,
        );
      }
      if (request.method === "GET" && request.view === "history") return notFound();
      return "hang";
    });

    render(
      <StrictMode>
        <FlueAgentPage />
      </StrictMode>,
    );

    await screen.findByText(/No messages yet/);
    fireEvent.change(screen.getByLabelText("Flue message"), {
      target: { value: "List the lights." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    await screen.findByText("List the lights.");
    const posted = requests.find((request) => request.method === "POST");
    expect(posted?.path).toBe(`${FLUE_AGENT_PATH}/${instanceId}`);
    expect(posted?.body).toMatchObject({ kind: "user", body: "List the lights." });
  });

  it("renders assistant text and the tool call lifecycle from history", async () => {
    const instanceId = "web-tools-test";
    localStorage.setItem(FLUE_INSTANCE_KEY, instanceId);
    installFlueFetch((request) => {
      if (request.method === "GET" && request.view === "history") {
        return historySnapshot(instanceId, [
          {
            id: "m-user",
            role: "user",
            purpose: "user",
            display: "visible",
            parts: [{ type: "text", text: "Turn on the kitchen lights", state: "done" }],
          },
          {
            id: "m-assistant",
            role: "assistant",
            purpose: "assistant",
            display: "visible",
            submissionId: "sub-9",
            parts: [
              { type: "text", text: "Checking the kitchen lights.", state: "done" },
              {
                type: "dynamic-tool",
                toolName: "list_entities",
                toolCallId: "call-1",
                state: "output-available",
                input: { area: "kitchen" },
                output: [{ id: "ent-1" }],
                durationMs: 12,
              },
              {
                type: "dynamic-tool",
                toolName: "get_entity_state",
                toolCallId: "call-2",
                state: "input-available",
                input: { entity_id: "ent-1" },
              },
            ],
          },
        ]);
      }
      return "hang";
    });

    render(<FlueAgentPage />);

    await screen.findByText("Turn on the kitchen lights");
    expect(screen.getByText("Checking the kitchen lights.")).toBeTruthy();
    expect(screen.getByText("list_entities")).toBeTruthy();
    expect(screen.getByText("tool: done")).toBeTruthy();
    expect(screen.getByText("get_entity_state")).toBeTruthy();
    expect(screen.getByText("tool: running")).toBeTruthy();
  });

  it("starts a finished tool call collapsed and a running one expanded", async () => {
    const instanceId = "web-tools-collapse";
    localStorage.setItem(FLUE_INSTANCE_KEY, instanceId);
    installFlueFetch((request) => {
      if (request.method === "GET" && request.view === "history") {
        return historySnapshot(instanceId, [
          {
            id: "m-assistant",
            role: "assistant",
            purpose: "assistant",
            display: "visible",
            submissionId: "sub-1",
            parts: [
              {
                type: "dynamic-tool",
                toolName: "list_entities",
                toolCallId: "call-1",
                state: "output-available",
                input: { area: "kitchen" },
                output: [{ id: "ent-1" }],
                durationMs: 12,
              },
              {
                type: "dynamic-tool",
                toolName: "get_entity_state",
                toolCallId: "call-2",
                state: "input-available",
                input: { entity_id: "ent-1" },
              },
            ],
          },
        ]);
      }
      return "hang";
    });

    render(<FlueAgentPage />);
    await screen.findByText("list_entities");

    // The finished call's payload is hidden behind its summary; the running
    // call stays expanded so its live input remains visible.
    const finished = screen.getByText("list_entities").closest("details");
    const running = screen.getByText("get_entity_state").closest("details");
    expect(finished?.open).toBe(false);
    expect(running?.open).toBe(true);

    // The summary click toggles the finished call open without a rerender.
    fireEvent.click(screen.getByText("list_entities"));
    expect(finished?.open).toBe(true);
    expect(screen.getByText(/"area": "kitchen"/)).toBeTruthy();
  });

  it("starts a new conversation with a fresh persisted instance id", async () => {
    const originalId = "web-original";
    localStorage.setItem(FLUE_INSTANCE_KEY, originalId);
    const requests = installFlueFetch((request) => {
      if (request.method === "GET" && request.view === "history") return notFound();
      return "hang";
    });

    render(<FlueAgentPage />);
    await screen.findByText(/No messages yet/);

    fireEvent.click(screen.getByRole("button", { name: "New conversation" }));

    let nextId = originalId;
    await waitFor(() => {
      nextId = localStorage.getItem(FLUE_INSTANCE_KEY) ?? "";
      expect(nextId).not.toBe(originalId);
      expect(nextId).toMatch(/^web-/);
    });
    await waitFor(() => {
      expect(
        requests.some(
          (request) =>
            request.method === "GET" &&
            request.view === "history" &&
            request.path === `${FLUE_AGENT_PATH}/${nextId}`,
        ),
      ).toBe(true);
    });
  });
});
