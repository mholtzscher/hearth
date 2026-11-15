import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ConversationHistory } from "./conversation-history";
import {
  ConversationsProvider,
  useConversations,
} from "./conversations-provider";

const Controls = () => {
  const { session, refresh } = useConversations();
  return (
    <>
      <button
        type="button"
        onClick={() => {
          session("new").send("Temporary preview", {
            context: undefined,
            draftVersion: 0,
            onCreated: () => {},
          });
        }}
      >
        Create conversation
      </button>
      <button type="button" onClick={refresh}>
        Refresh recent
      </button>
      <ConversationHistory id="recent" open />
    </>
  );
};

describe(ConversationsProvider, () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("removes acknowledged creation previews so pruned conversations do not reappear", async () => {
    let created = false;
    let pruned = false;
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>((input, init) => {
        const path = String(input);
        if (path === "/v1/agent/conversations" && init?.method === "POST") {
          created = true;
          return Promise.resolve(new Response('{"id":"c-created"}'));
        }
        if (path.endsWith("/messages/stream")) {
          return Promise.resolve(
            new Response('data: {"type":"turn.finished"}\n\n')
          );
        }
        if (path.endsWith("/messages")) {
          return Promise.resolve(new Response('{"messages":[]}'));
        }
        return Promise.resolve(
          Response.json({
            conversations:
              created && !pruned
                ? [
                    {
                      id: "c-created",
                      preview: "Server preview",
                      created_at: "",
                      message_count: 1,
                    },
                  ]
                : [],
          })
        );
      })
    );
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConversationsProvider>
            <Controls />
          </ConversationsProvider>
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    fireEvent.click(
      screen.getByRole("button", { name: "Create conversation" })
    );
    await expect(
      screen.findByRole("link", { name: "Server preview" })
    ).resolves.toBeTruthy();
    pruned = true;
    fireEvent.click(screen.getByRole("button", { name: "Refresh recent" }));
    await waitFor(() => expect(screen.queryByRole("link")).toBeNull());
    expect(screen.queryByText("Temporary preview")).toBeNull();
  });
});
