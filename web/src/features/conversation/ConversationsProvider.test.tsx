import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { ConversationsProvider, useConversations } from "./ConversationsProvider";
import { ConversationHistory } from "./ConversationHistory";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

function Controls() {
  const { session, refresh } = useConversations();
  return <><button onClick={() => void session("new").send("Temporary preview", { context: undefined, draftVersion: 0, onCreated: () => {} })}>Create conversation</button><button onClick={refresh}>Refresh recent</button><ConversationHistory id="recent" open /></>;
}

it("removes acknowledged creation previews so pruned conversations do not reappear", async () => {
  let created = false;
  let pruned = false;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/v1/agent/conversations" && init?.method === "POST") { created = true; return new Response('{"id":"c-created"}'); }
    if (path.endsWith("/messages/stream")) return new Response('data: {"type":"turn.finished"}\n\n');
    if (path.endsWith("/messages")) return new Response('{"messages":[]}');
    return new Response(JSON.stringify({ conversations: created && !pruned ? [{ id: "c-created", preview: "Server preview", created_at: "", message_count: 1 }] : [] }));
  }));
  const router = createMemoryRouter([{ path: "/", element: <ConversationsProvider><Controls /></ConversationsProvider> }]);
  render(<RouterProvider router={router} />);
  fireEvent.click(screen.getByRole("button", { name: "Create conversation" }));
  expect(await screen.findByRole("link", { name: "Server preview" })).toBeTruthy();
  pruned = true;
  fireEvent.click(screen.getByRole("button", { name: "Refresh recent" }));
  await waitFor(() => expect(screen.queryByRole("link")).toBeNull());
  expect(screen.queryByText("Temporary preview")).toBeNull();
});
