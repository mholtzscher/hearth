import { cleanup, render, screen } from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AgentPage from "./AgentPage.tsx";

/**
 * AgentPage initialization tests.
 *
 * The page reaches hearthd only through the global `fetch`, so stubbing that
 * global is the seam that lets a test answer `/v1/agent` requests without a
 * server. Each test asserts on the requests the page made and the conversation
 * it settled on, so the StrictMode and stale-stored-ID behavior stays observable
 * without reaching into component internals.
 */

/** localStorage key holding the active conversation ID. */
const CONV_KEY = "hearth.agentConversationId";
const NEW_CONVERSATION_ID = "cnv_01920000-0000-7000-8000-000000000001";
const LISTED_CONVERSATION_ID = "cnv_01920000-0000-7000-8000-000000000002";
const STALE_CONVERSATION_ID = "cnv_01920000-0000-7000-8000-000000000003";
const CREATED_AT = "2026-02-01T09:00:00.000Z";
const LISTED_PREVIEW = "Kitchen lights";
const LISTED_MESSAGE = "turn on the kitchen lights";

interface AgentRequest {
  method: string;
  path: string;
}

interface AgentResponse {
  status?: number;
  body?: unknown;
}

/** Routes keyed by `"<METHOD> <path>"`; an unmatched request fails the test. */
type AgentHandlers = Record<string, () => AgentResponse>;

function installAgentFetch(handlers: AgentHandlers): AgentRequest[] {
  const requests: AgentRequest[] = [];
  vi.stubGlobal(
    "fetch",
    async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const method = (init?.method ?? "GET").toUpperCase();
      const path = new URL(String(input), "http://hearthd.test").pathname;
      requests.push({ method, path });
      // Yield to the macrotask queue like a real round trip does, so the
      // StrictMode setup/cleanup/setup sequence interleaves with responses.
      await new Promise((resolve) => setTimeout(resolve, 0));
      const handler = handlers[`${method} ${path}`];
      if (!handler) throw new Error(`agent fetch fake has no route for ${method} ${path}`);
      const response = handler();
      return new Response(response.body === undefined ? "" : JSON.stringify(response.body), {
        status: response.status ?? 200,
        headers: { "content-type": "application/json" },
      });
    },
  );
  return requests;
}

/** Let every request the fake is holding run to completion. */
async function flushMacrotasks(turns = 6): Promise<void> {
  for (let index = 0; index < turns; index += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
}

function postRequests(requests: AgentRequest[]): AgentRequest[] {
  return requests.filter((request) => request.method === "POST");
}

/** One listed conversation with a rendered preview, matching the API shape. */
function listedConversation() {
  return {
    id: LISTED_CONVERSATION_ID,
    created_at: CREATED_AT,
    message_count: 1,
    last_message_at: CREATED_AT,
    preview: LISTED_PREVIEW,
  };
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("AgentPage initialization", () => {
  it("creates one conversation when StrictMode double-invokes initialization", async () => {
    let created = false;
    const requests = installAgentFetch({
      "GET /v1/agent/conversations": () => ({
        body: { conversations: created ? [listedConversation()] : [] },
      }),
      "POST /v1/agent/conversations": () => {
        created = true;
        return { body: { id: NEW_CONVERSATION_ID } };
      },
      [`GET /v1/agent/conversations/${NEW_CONVERSATION_ID}/messages`]: () => ({
        body: { messages: [] },
      }),
    });

    render(
      <StrictMode>
        <AgentPage />
      </StrictMode>,
    );

    // The created conversation reaches the sidebar, so initialization finished.
    await screen.findByRole("button", { name: /Kitchen lights/ }, { timeout: 5_000 });
    await flushMacrotasks();

    // The discarded StrictMode run must not create a second durable conversation.
    expect(postRequests(requests)).toHaveLength(1);
    expect(localStorage.getItem(CONV_KEY)).toBe(NEW_CONVERSATION_ID);
  });

  it("reuses a listed conversation when the stored ID is gone", async () => {
    localStorage.setItem(CONV_KEY, STALE_CONVERSATION_ID);
    const requests = installAgentFetch({
      "GET /v1/agent/conversations": () => ({ body: { conversations: [listedConversation()] } }),
      [`GET /v1/agent/conversations/${STALE_CONVERSATION_ID}/messages`]: () => ({
        status: 404,
        body: { title: "Not Found", detail: "agent conversation not found" },
      }),
      [`GET /v1/agent/conversations/${LISTED_CONVERSATION_ID}/messages`]: () => ({
        body: {
          messages: [
            {
              message_id: 1,
              role: "user",
              content: LISTED_MESSAGE,
              tool_calls: null,
              created_at: CREATED_AT,
            },
          ],
        },
      }),
    });

    render(<AgentPage />);

    // The listed conversation's history is shown, so the fallback was selected.
    await screen.findByText(LISTED_MESSAGE);
    await flushMacrotasks();

    expect(postRequests(requests)).toHaveLength(0);
    expect(localStorage.getItem(CONV_KEY)).toBe(LISTED_CONVERSATION_ID);
    const listed = screen.getByRole("button", { name: /Kitchen lights/ });
    expect(listed.getAttribute("aria-current")).toBe("true");
  });
});
